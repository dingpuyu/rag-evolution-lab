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
	"github.com/dingpuyu/rag-evolution-lab/internal/ingestionjob"
	"github.com/dingpuyu/rag-evolution-lab/internal/milvus"
	"github.com/dingpuyu/rag-evolution-lab/internal/retrieval"
	"github.com/dingpuyu/rag-evolution-lab/internal/scalebench"
)

func newEnterpriseTestHandler(t *testing.T, searchFilter *string) http.Handler {
	return newEnterpriseTestHandlerWithDevIssuer(t, searchFilter, true)
}

func newEnterpriseTestHandlerWithDevIssuer(t *testing.T, searchFilter *string, enableDevIssuer bool, ingestionJobs ...*ingestionjob.Service) http.Handler {
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
	lifecycleService, err := milvus.NewLifecycleService(client, embedder, milvus.LifecycleConfig{
		Collection: "lifecycle", EmbeddingVersion: "hash-v1", StatePath: t.TempDir() + "/lifecycle.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := auth.NewManager(auth.Config{
		Secret: []byte("01234567890123456789012345678901"), Issuer: "raglab", Audience: "raglab-api", TTL: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	options := EnterpriseOptions{Verifier: manager, Audit: auth.NewAuditLog(20)}
	if enableDevIssuer {
		options.DevIssuer = manager
		options.LocalAccounts, err = auth.NewAccountStore(t.TempDir() + "/accounts.json")
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(ingestionJobs) > 0 {
		options.IngestionJobs = ingestionJobs[0]
	}
	handler, err := NewEnterpriseLabHandler(embeddingService, vectorService, scaleService, options, lifecycleService)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestLocalAccountRegistrationAndLoginUseServerAssignedTenant(t *testing.T) {
	var filter string
	handler := newEnterpriseTestHandler(t, &filter)
	register := httptest.NewRecorder()
	handler.ServeHTTP(register, httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", strings.NewReader(`{
		"email":"new@example.com",
		"password":"a strong local password",
		"organization":"New Organization"
	}`)))
	if register.Code != http.StatusCreated || strings.Contains(register.Body.String(), `"tenant_id":"tenant_a"`) {
		t.Fatalf("registration status=%d body=%s", register.Code, register.Body.String())
	}

	login := httptest.NewRecorder()
	handler.ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{
		"email":"new@example.com",
		"password":"a strong local password"
	}`)))
	if login.Code != http.StatusOK || !strings.Contains(login.Body.String(), `"roles":["admin"]`) {
		t.Fatalf("login status=%d body=%s", login.Code, login.Body.String())
	}

	bad := httptest.NewRecorder()
	handler.ServeHTTP(bad, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{
		"email":"new@example.com",
		"password":"wrong password"
	}`)))
	if bad.Code != http.StatusUnauthorized || strings.Contains(bad.Body.String(), "new@example.com") {
		t.Fatalf("bad login status=%d body=%s", bad.Code, bad.Body.String())
	}
}

func TestDatasetAuthorizationStopsCrossTenantRequestBeforeMilvus(t *testing.T) {
	var filter string
	handler := newEnterpriseTestHandler(t, &filter)
	tenantA := issueTestPersona(t, handler, "tenant_a_admin")

	listRequest := httptest.NewRequest(http.MethodGet, "/api/v1/datasets", nil)
	listRequest.Header.Set("Authorization", "Bearer "+tenantA)
	list := httptest.NewRecorder()
	handler.ServeHTTP(list, listRequest)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"tenant-a-operations"`) ||
		strings.Contains(list.Body.String(), `"tenant-b-operations"`) {
		t.Fatalf("dataset list status=%d body=%s", list.Code, list.Body.String())
	}

	deniedRequest := httptest.NewRequest(http.MethodPost, "/api/v1/datasets/tenant-b-operations/search", strings.NewReader(`{
		"query":"private queue", "top_k":5
	}`))
	deniedRequest.Header.Set("Authorization", "Bearer "+tenantA)
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, deniedRequest)
	if denied.Code != http.StatusNotFound || filter != "" {
		t.Fatalf("cross-tenant request status=%d filter=%q body=%s", denied.Code, filter, denied.Body.String())
	}

	allowedRequest := httptest.NewRequest(http.MethodPost, "/api/v1/datasets/tenant-a-operations/search", strings.NewReader(`{
		"query":"private queue", "top_k":5
	}`))
	allowedRequest.Header.Set("Authorization", "Bearer "+tenantA)
	allowed := httptest.NewRecorder()
	handler.ServeHTTP(allowed, allowedRequest)
	if allowed.Code != http.StatusOK ||
		!strings.Contains(filter, `allowed_tenants, "tenant_a"`) ||
		!strings.Contains(filter, `product == "tenant-operations"`) ||
		strings.Contains(filter, `visibility == "public"`) {
		t.Fatalf("tenant dataset status=%d filter=%q body=%s", allowed.Code, filter, allowed.Body.String())
	}
}

func TestDatasetAnswerReusesAuthorizationAndFailsClosedWithoutEvidence(t *testing.T) {
	var filter string
	handler := newEnterpriseTestHandler(t, &filter)
	tenantA := issueTestPersona(t, handler, "tenant_a_admin")

	allowedRequest := httptest.NewRequest(http.MethodPost, "/api/v1/datasets/tenant-a-operations/answer", strings.NewReader(`{
		"query":"private queue", "top_k":5
	}`))
	allowedRequest.Header.Set("Authorization", "Bearer "+tenantA)
	allowed := httptest.NewRecorder()
	handler.ServeHTTP(allowed, allowedRequest)
	if allowed.Code != http.StatusOK ||
		!strings.Contains(filter, `allowed_tenants, "tenant_a"`) ||
		!strings.Contains(allowed.Body.String(), `"answerable":false`) ||
		!strings.Contains(allowed.Body.String(), `"refusal_reason":"no_retrieval_evidence"`) {
		t.Fatalf("tenant answer status=%d filter=%q body=%s", allowed.Code, filter, allowed.Body.String())
	}

	filter = ""
	deniedRequest := httptest.NewRequest(http.MethodPost, "/api/v1/datasets/tenant-b-operations/answer", strings.NewReader(`{
		"query":"private queue", "top_k":5
	}`))
	deniedRequest.Header.Set("Authorization", "Bearer "+tenantA)
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, deniedRequest)
	if denied.Code != http.StatusNotFound || filter != "" {
		t.Fatalf("cross-tenant answer status=%d filter=%q body=%s", denied.Code, filter, denied.Body.String())
	}
}

func TestDatasetAnswerStreamEmitsGroundedLifecycle(t *testing.T) {
	var filter string
	handler := newEnterpriseTestHandler(t, &filter)
	token := issueTestPersona(t, handler, "tenant_a_admin")
	request := httptest.NewRequest(http.MethodPost, "/api/v1/datasets/tenant-a-operations/answer/stream", strings.NewReader(`{
		"query":"private queue", "top_k":5
	}`))
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("stream status=%d body=%s", response.Code, response.Body.String())
	}
	if !strings.HasPrefix(response.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("expected SSE content type, got %q", response.Header().Get("Content-Type"))
	}
	body := response.Body.String()
	for _, event := range []string{"event: started", "event: retrieved", "event: completed", "event: done"} {
		if !strings.Contains(body, event) {
			t.Fatalf("missing %s in stream: %s", event, body)
		}
	}
	if !strings.Contains(body, `"refusal_reason":"no_retrieval_evidence"`) {
		t.Fatalf("stream did not carry deterministic refusal: %s", body)
	}
}

func TestDatasetAnswerStreamPreservesResourceNonEnumeration(t *testing.T) {
	var filter string
	handler := newEnterpriseTestHandler(t, &filter)
	token := issueTestPersona(t, handler, "tenant_a_admin")
	request := httptest.NewRequest(http.MethodPost, "/api/v1/datasets/tenant-b-operations/answer/stream", strings.NewReader(`{
		"query":"private queue", "top_k":5
	}`))
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound || filter != "" || strings.Contains(response.Body.String(), "text/event-stream") {
		t.Fatalf("cross-tenant stream leaked resource or reached Milvus: status=%d filter=%q body=%s", response.Code, filter, response.Body.String())
	}
}

func TestLifecycleAdministrationRequiresPlatformRole(t *testing.T) {
	var filter string
	handler := newEnterpriseTestHandler(t, &filter)
	viewer := issueTestPersona(t, handler, "tenant037_admin")
	denied := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/milvus/lifecycle/status", nil)
	request.Header.Set("Authorization", "Bearer "+viewer)
	handler.ServeHTTP(denied, request)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("tenant admin accessed lifecycle administration: status=%d body=%s", denied.Code, denied.Body.String())
	}

	platform := issueTestPersona(t, handler, "platform_admin")
	allowed := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/api/v1/milvus/lifecycle/status", nil)
	request.Header.Set("Authorization", "Bearer "+platform)
	handler.ServeHTTP(allowed, request)
	if allowed.Code != http.StatusOK || !strings.Contains(allowed.Body.String(), `"embedding_version":"hash-v1"`) {
		t.Fatalf("platform lifecycle status failed: status=%d body=%s", allowed.Code, allowed.Body.String())
	}
}

func TestEnterpriseProductionModeDoesNotExposeDevIssuer(t *testing.T) {
	var filter string
	handler := newEnterpriseTestHandlerWithDevIssuer(t, &filter, false)
	for _, path := range []string{"/api/v1/auth/dev-token", "/api/v1/auth/register", "/api/v1/auth/login"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`)))
		if response.Code != http.StatusNotFound {
			t.Fatalf("production mode exposed local auth path %s: status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
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
