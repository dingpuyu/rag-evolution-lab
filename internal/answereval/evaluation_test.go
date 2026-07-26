package answereval

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRunnerScoresGroundedAnswerAndCrossTenantContract(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/auth/login", func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]string{"access_token": "token"})
	})
	mux.HandleFunc("POST /api/v1/datasets/{dataset_id}/answer", func(writer http.ResponseWriter, request *http.Request) {
		if request.PathValue("dataset_id") == "tenant-b" {
			writer.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"error": map[string]string{"code": "dataset_not_found"},
			})
			return
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"result": map[string]any{
			"answerable": true, "answer": "queue-a", "citations": []map[string]string{{
				"chunk_id": "a#1", "document_id": "doc-a",
			}},
			"search": map[string]any{"hits": []map[string]any{{
				"tenant_id": "tenant_a", "visibility": "tenant",
			}}},
			"generation": map[string]any{
				"latency_ms": 20, "prompt_tokens": 10, "output_tokens": 4,
			},
		}})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	suite := Suite{
		Version: "1", Name: "test", Identities: map[string]Identity{"alice": {Email: "alice@test.local"}},
		Cases: []Case{
			{
				ID: "grounded", Identity: "alice", DatasetID: "tenant-a", Query: "queue", ExpectedStatus: 200,
				ExpectedAnswerable: true, RequiredFacts: []string{"queue-a"},
				RequiredCitationDocuments: []string{"doc-a"}, ExpectedTenant: "tenant_a", ExpectedVisibility: "tenant",
			},
			{
				ID: "cross", Identity: "alice", DatasetID: "tenant-b", Query: "queue", ExpectedStatus: 404,
				ExpectedErrorCode: "dataset_not_found",
			},
		},
	}
	report, err := (Runner{
		BaseURL: server.URL, HTTPClient: server.Client(), Passwords: map[string]string{"alice": "password"},
	}).Run(context.Background(), suite)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed || report.PassedCases != 2 || report.AnswerabilityAccuracy != 1 ||
		report.RequiredFactCoverage != 1 || report.CitationViolations != 0 ||
		report.UnauthorizedRetrievals != 0 || report.PromptTokens != 10 || report.OutputTokens != 4 {
		t.Fatalf("unexpected report %#v", report)
	}
}

func TestRunnerDetectsForbiddenFactAndUnauthorizedHit(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/auth/login", func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]string{"access_token": "token"})
	})
	mux.HandleFunc("POST /api/v1/datasets/tenant-a/answer", func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]any{"result": map[string]any{
			"answerable": true, "answer": "tenant-b-secret", "citations": []map[string]string{{
				"chunk_id": "b#1", "document_id": "doc-b",
			}},
			"search": map[string]any{"hits": []map[string]any{{
				"tenant_id": "tenant_b", "visibility": "tenant",
			}}},
			"generation": map[string]any{},
		}})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	suite := Suite{
		Version: "1", Name: "leak", Identities: map[string]Identity{"alice": {Email: "alice@test.local"}},
		Cases: []Case{{
			ID: "leak", Identity: "alice", DatasetID: "tenant-a", Query: "secret", ExpectedStatus: 200,
			ExpectedAnswerable: true, ForbiddenFacts: []string{"tenant-b-secret"},
			ForbiddenCitationDocuments: []string{"doc-b"}, ExpectedTenant: "tenant_a", ExpectedVisibility: "tenant",
		}},
	}
	report, err := (Runner{
		BaseURL: server.URL, HTTPClient: server.Client(), Passwords: map[string]string{"alice": "password"},
	}).Run(context.Background(), suite)
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed || report.ForbiddenFactHits == 0 || report.CitationViolations == 0 ||
		report.UnauthorizedRetrievals == 0 {
		t.Fatalf("security regression must fail: %#v", report)
	}
}
