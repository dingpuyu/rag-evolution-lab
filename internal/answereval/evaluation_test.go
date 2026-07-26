package answereval

import (
	"context"
	"encoding/json"
	"fmt"
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
				"generator": "openai-compatible-deepseek", "model": "deepseek-v4-pro",
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
		Cost: CostConfig{PromptPer1MUSD: 1, CompletionPer1MUSD: 2},
	}).Run(context.Background(), suite)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed || report.PassedCases != 2 || report.AnswerabilityAccuracy != 1 ||
		report.RequiredFactCoverage != 1 || report.CitationViolations != 0 ||
		report.UnauthorizedRetrievals != 0 || report.PromptTokens != 10 || report.OutputTokens != 4 ||
		!report.CostConfigured || report.EstimatedCostUSD != 0.000018 ||
		len(report.Providers) != 1 || report.Providers[0] != "openai-compatible-deepseek" ||
		len(report.Models) != 1 || report.Models[0] != "deepseek-v4-pro" {
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

func TestRunnerScoresSSEAnswerAndTTFT(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/auth/login", func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]string{"access_token": "token"})
	})
	mux.HandleFunc("POST /api/v1/datasets/public/answer/stream", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		response := map[string]any{
			"answerable": true,
			"answer":     "Use HTTPS",
			"citations":  []map[string]string{{"chunk_id": "doc#1", "document_id": "doc"}},
			"search":     map[string]any{"hits": []map[string]any{{"tenant_id": "public", "visibility": "public"}}},
			"generation": map[string]any{
				"generator": "openai-compatible-deepseek", "model": "deepseek-v4-pro",
				"latency_ms": 120, "ttft_ms": 12.3, "token_rate_tps": 44.5,
				"prompt_tokens": 50, "output_tokens": 10,
			},
		}
		event, _ := json.Marshal(map[string]any{"event": map[string]any{"type": "completed", "response": response}})
		_, _ = fmt.Fprintf(writer, "event: completed\ndata: %s\n\n", event)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	suite := Suite{
		Version: "1", Name: "stream", Identities: map[string]Identity{"alice": {Email: "alice@test.local"}},
		Cases: []Case{{
			ID: "streamed", Identity: "alice", DatasetID: "public", Query: "protocol", ExpectedStatus: 200,
			ExpectedAnswerable: true, RequiredFacts: []string{"HTTPS"}, RequiredCitationDocuments: []string{"doc"},
			ExpectedTenant: "public", ExpectedVisibility: "public",
		}},
	}
	report, err := (Runner{BaseURL: server.URL, HTTPClient: server.Client(), Passwords: map[string]string{"alice": "password"}, Stream: true}).Run(context.Background(), suite)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed || !report.Streaming || report.TTFTP50MS != 12.3 || report.TokenRateP50TPS != 44.5 ||
		len(report.Providers) != 1 || report.Providers[0] != "openai-compatible-deepseek" {
		t.Fatalf("unexpected stream report %#v", report)
	}
}
