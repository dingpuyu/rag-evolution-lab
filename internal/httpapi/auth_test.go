package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dingpuyu/rag-evolution-lab/internal/auth"
	"github.com/dingpuyu/rag-evolution-lab/internal/embeddinglab"
	"github.com/dingpuyu/rag-evolution-lab/internal/milvus"
	"github.com/dingpuyu/rag-evolution-lab/internal/retrieval"
	"github.com/dingpuyu/rag-evolution-lab/internal/scalebench"
)

func newEnterpriseTestHandler(t *testing.T, searchFilter *string) http.Handler {
	t.Helper()
	milvusServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v2/vectordb/entities/search" {
			t.Fatalf("unexpected Milvus path %s", request.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		*searchFilter = payload["filter"].(string)
		_ = json.NewEncoder(writer).Encode(map[string]any{"code": 0, "data": []any{}})
	}))
	t.Cleanup(milvusServer.Close)

	embedder := retrieval.HashEmbedder{Dimensions: 8}
	embeddingService, err := embeddinglab.New(embedder)
	if err != nil {
		t.Fatal(err)
	}
	client := milvus.NewClient(milvus.Config{BaseURL: milvusServer.URL})
	vectorService, err := milvus.NewService(client, embedder, "chunks")
	if err != nil {
		t.Fatal(err)
	}
	generator, err := scalebench.NewGenerator(scalebench.DatasetConfig{
		Chunks: 100, Dimensions: 8, Topics: 10, Tenants: 10, Seed: 1, Profile: scalebench.ProfileHardV2,
	})
	if err != nil {
		t.Fatal(err)
	}
	scaleService, err := scalebench.NewDemoService(client, generator, scalebench.Collections{Flat: "flat", HNSW: "hnsw"})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := auth.NewManager(auth.Config{
		Secret: []byte("01234567890123456789012345678901"), Issuer: "raglab", Audience: "raglab-api", TTL: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewEnterpriseLabHandler(embeddingService, vectorService, scaleService, EnterpriseOptions{
		Manager: manager, Audit: auth.NewAuditLog(20),
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func issueTestPersona(t *testing.T, handler http.Handler, persona string) string {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/auth/dev-token", strings.NewReader(`{"persona":"`+persona+`"}`)))
	if response.Code != http.StatusOK {
		t.Fatalf("issue token status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body.AccessToken
}

func TestEnterpriseSearchRequiresJWTAndIgnoresClientSuppliedIdentity(t *testing.T) {
	var filter string
	handler := newEnterpriseTestHandler(t, &filter)

	unauthenticated := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodPost, "/api/v1/milvus/search", strings.NewReader(`{"query":"SSO"}`)))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status=%d", unauthenticated.Code)
	}

	token := issueTestPersona(t, handler, "tenant037_admin")
	request := httptest.NewRequest(http.MethodPost, "/api/v1/milvus/search", strings.NewReader(`{
		"query":"SSO", "tenant_id":"tenant_evil", "user_role":"platform_admin", "top_k":5
	}`))
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("search status=%d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(filter, "tenant_evil") || !strings.Contains(filter, `allowed_tenants, "tenant_037"`) || !strings.Contains(filter, `allowed_roles, "admin"`) {
		t.Fatalf("client identity was trusted or JWT claims were not applied: %s", filter)
	}
	if response.Header().Get("X-Request-ID") == "" {
		t.Fatal("authenticated response must include request ID")
	}
}

func TestEnterpriseScaleAdminScenarioRejectsViewer(t *testing.T) {
	var filter string
	handler := newEnterpriseTestHandler(t, &filter)
	token := issueTestPersona(t, handler, "tenant037_viewer")
	request := httptest.NewRequest(http.MethodPost, "/api/v1/milvus/scale/search", strings.NewReader(`{
		"topic":3, "scenario":"tenant_admin_active", "top_k":10, "ef":64
	}`))
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "requires a trusted tenant and admin role") {
		t.Fatalf("viewer must be denied: status=%d body=%s", response.Code, response.Body.String())
	}
	if filter != "" {
		t.Fatalf("denied request reached Milvus with filter %q", filter)
	}
}
