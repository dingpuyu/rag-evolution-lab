package runtimeharness

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRunReadOnlyCredentialAndTraceContract(t *testing.T) {
	const appID = "tenant_a-support-agent"
	const environmentID = appID + "-dev"
	revoked := false
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/api/v1/auth/login":
			_ = json.NewEncoder(writer).Encode(map[string]string{"access_token": "admin-token"})
		case request.Method == http.MethodPost && request.URL.Path == "/api/v1/apps/"+appID+"/query":
			if request.Header.Get("Authorization") == "AppCredential ragc_test" {
				if revoked {
					writer.WriteHeader(http.StatusUnauthorized)
					_ = json.NewEncoder(writer).Encode(map[string]any{"error": map[string]string{"code": "invalid_credentials", "message": "revoked"}})
					return
				}
			} else if request.Header.Get("Authorization") != "Bearer admin-token" {
				writer.WriteHeader(http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"app_id": appID, "environment_id": environmentID, "trace_id": "trace-1", "result": map[string]any{"hits": []any{}}})
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/apps/"+appID+"/traces/trace-1":
			_ = json.NewEncoder(writer).Encode(map[string]any{"trace_id": "trace-1", "app_id": appID, "environment_id": environmentID, "status": "retrieved"})
		case request.Method == http.MethodPost && request.URL.Path == "/api/v1/apps/"+appID+"/credentials":
			writer.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(writer).Encode(map[string]any{"credential": map[string]any{"credential_id": "cred-1", "scopes": []string{"rag:query"}, "status": "active"}, "secret": "ragc_test"})
		case request.Method == http.MethodPost && request.URL.Path == "/api/v1/apps/"+appID+"/answer":
			writer.WriteHeader(http.StatusUnprocessableEntity)
			_ = json.NewEncoder(writer).Encode(map[string]any{"error": map[string]string{"code": "credential_request_failed", "message": "answer scope required"}})
		case request.Method == http.MethodPost && strings.HasPrefix(request.URL.Path, "/api/v1/apps/tenant_b-support-agent/"):
			writer.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(writer).Encode(map[string]any{"error": map[string]string{"code": "credential_scope_violation", "message": "different app"}})
		case request.Method == http.MethodPost && request.URL.Path == "/api/v1/apps/"+appID+"/credentials/cred-1/revoke":
			revoked = true
			_ = json.NewEncoder(writer).Encode(map[string]string{"status": "revoked"})
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	report, err := Run(context.Background(), Config{BaseURL: server.URL, Email: "alice@example.com", Password: "password", ApplicationID: appID, EnvironmentID: environmentID, CrossAppID: "tenant_b-support-agent", HTTPClient: server.Client(), Timeout: time.Second})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !report.Passed || report.Cases != 8 || report.FailedCases != 0 {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestDescribeErrorEnvelope(t *testing.T) {
	message := describe([]byte(`{"error":{"code":"credential_scope_violation","message":"different app"}}`))
	if message != "credential_scope_violation: different app" {
		t.Fatalf("message=%q", message)
	}
}
