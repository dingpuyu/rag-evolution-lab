package searchharness

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRunnerValidatesRelevanceVisibilityAndIsolation(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/auth/login", func(writer http.ResponseWriter, request *http.Request) {
		var input map[string]string
		_ = json.NewDecoder(request.Body).Decode(&input)
		_ = json.NewEncoder(writer).Encode(map[string]string{"access_token": strings.Split(input["email"], "@")[0]})
	})
	mux.HandleFunc("GET /api/v1/datasets", func(writer http.ResponseWriter, request *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]any{"datasets": []map[string]string{
			{"id": "public-identity"}, {"id": "tenant-a-operations"},
		}})
	})
	mux.HandleFunc("POST /api/v1/datasets/{dataset_id}/search", func(writer http.ResponseWriter, request *http.Request) {
		if request.PathValue("dataset_id") == "tenant-b-operations" {
			writer.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(writer).Encode(map[string]any{"error": map[string]string{"code": "dataset_not_found"}})
			return
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"result": map[string]any{
			"collection": "knowledge", "embedder": "test", "dimensions": 3,
			"filter": `array_contains(allowed_tenants, "tenant_a") and product == "tenant-operations"`,
			"hits": []map[string]any{{
				"document_id": "golden-a", "content": "queue-a", "tenant_id": "tenant_a",
				"visibility": "tenant", "distance": 0.91,
			}},
		}})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	suite := Suite{
		Version: "1", Name: "test",
		Identities: map[string]Identity{"alice": {Email: "alice@example.test"}},
		VisibilityCases: []VisibilityCase{{
			ID: "visible", Identity: "alice", RequiredDatasets: []string{"tenant-a-operations"},
			ForbiddenDatasets: []string{"tenant-b-operations"},
		}},
		SearchCases: []SearchCase{
			{
				ID: "own", Identity: "alice", DatasetID: "tenant-a-operations", Query: "queue", TopK: 3,
				ExpectedStatus: http.StatusOK, RelevantDocumentIDs: []string{"golden-a"},
				MaxFirstRelevantRank: 1, RequiredFacts: []string{"queue-a"}, ExpectedVisibility: "tenant",
				ExpectedTenant: "tenant_a", RequiredFilterFragments: []string{`allowed_tenants, "tenant_a"`},
			},
			{
				ID: "cross", Identity: "alice", DatasetID: "tenant-b-operations", Query: "queue", TopK: 3,
				ExpectedStatus: http.StatusNotFound, ExpectedErrorCode: "dataset_not_found",
			},
		},
	}
	report, err := (Runner{
		BaseURL: server.URL, HTTPClient: server.Client(), Passwords: map[string]string{"alice": "test"},
	}).Run(context.Background(), suite, false)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed || report.PassedCases != 3 || report.HitRateAtK != 1 || report.MRR != 1 {
		t.Fatalf("unexpected report: %#v", report)
	}
	if report.UnauthorizedRetrievals != 0 || report.FilterViolations != 0 || report.ContractViolations != 0 {
		t.Fatalf("unexpected violations: %#v", report)
	}
}

func TestRunnerFailsOnForbiddenTenantHit(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/auth/login", func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]string{"access_token": "token"})
	})
	mux.HandleFunc("POST /api/v1/datasets/tenant-a-operations/search", func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]any{"result": map[string]any{
			"filter": `visibility == "public"`,
			"hits": []map[string]any{{
				"document_id": "tenant-b-secret", "content": "queue-b", "tenant_id": "tenant_b", "visibility": "tenant",
			}},
		}})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	suite := Suite{
		Version: "1", Name: "leak",
		Identities: map[string]Identity{"alice": {Email: "alice@example.test"}},
		SearchCases: []SearchCase{{
			ID: "leak", Identity: "alice", DatasetID: "tenant-a-operations", Query: "queue", ExpectedStatus: 200,
			ForbiddenDocumentIDs: []string{"tenant-b-secret"}, ForbiddenFacts: []string{"queue-b"},
			ExpectedTenant: "tenant_a", ExpectedVisibility: "tenant",
			RequiredFilterFragments: []string{`allowed_tenants, "tenant_a"`},
		}},
	}
	report, err := (Runner{
		BaseURL: server.URL, HTTPClient: server.Client(), Passwords: map[string]string{"alice": "test"},
	}).Run(context.Background(), suite, false)
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed || report.UnauthorizedRetrievals == 0 || report.ForbiddenFactHits == 0 || report.FilterViolations == 0 {
		t.Fatalf("leak must fail closed: %#v", report)
	}
}

func TestSuiteValidationRejectsDuplicateCaseID(t *testing.T) {
	suite := Suite{
		Version: "1", Name: "bad", Identities: map[string]Identity{"alice": {}},
		VisibilityCases: []VisibilityCase{{ID: "duplicate", Identity: "alice"}},
		SearchCases: []SearchCase{{
			ID: "duplicate", Identity: "alice", DatasetID: "x", Query: "q", ExpectedStatus: 200,
		}},
	}
	if err := suite.Validate(); err == nil {
		t.Fatal("expected duplicate case validation error")
	}
}
