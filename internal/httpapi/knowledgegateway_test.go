package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dingpuyu/rag-evolution-lab/internal/auth"
	"github.com/dingpuyu/rag-evolution-lab/internal/datasetaccess"
	"github.com/dingpuyu/rag-evolution-lab/internal/generation"
	"github.com/dingpuyu/rag-evolution-lab/internal/knowledgegateway"
	"github.com/dingpuyu/rag-evolution-lab/internal/milvus"
)

type gatewayTestSearcher struct{}

func (gatewayTestSearcher) Search(_ context.Context, query milvus.Query) (milvus.SearchResult, error) {
	return milvus.SearchResult{Query: query.Text, Hits: []milvus.SearchHit{{ChunkID: "c1", DocumentID: "d1", Title: "SSO", Content: "SSO 入口在身份中心。", Distance: 0.1}}}, nil
}

func TestKnowledgeGatewayAPIQueryUsesApplicationBoundary(t *testing.T) {
	apps := &fakeApplicationStore{bindings: []datasetaccess.KnowledgeBinding{{
		ApplicationID: "tenant_a-support", EnvironmentID: "tenant_a-support-dev", DatasetID: "public-identity", Status: "active",
		Policy: datasetaccess.RetrievalPolicy{TopK: 5, CandidateK: 10},
	}}}
	gateway, err := knowledgegateway.New(gatewayTestSearcher{}, datasetaccess.Defaults(), apps, generation.ExtractiveGenerator{})
	if err != nil {
		t.Fatal(err)
	}
	api := &KnowledgeGatewayAPI{service: gateway}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/apps/tenant_a-support/query", strings.NewReader(`{"query":"SSO","top_k":3}`))
	request.SetPathValue("app_id", "tenant_a-support")
	request = request.WithContext(context.WithValue(request.Context(), identityContextKey{}, auth.Identity{Subject: "alice", TenantID: "tenant_a", Roles: []string{"viewer"}}))
	response := httptest.NewRecorder()
	api.query(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"app_id":"tenant_a-support"`) || !strings.Contains(response.Body.String(), `"knowledge-gateway"`) {
		t.Fatalf("gateway query status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestKnowledgeGatewayAPIHidesCrossTenantApplication(t *testing.T) {
	apps := &fakeApplicationStore{bindings: []datasetaccess.KnowledgeBinding{{
		ApplicationID: "tenant_a-support", EnvironmentID: "tenant_a-support-dev", DatasetID: "tenant-a-operations", Status: "active",
		Policy: datasetaccess.RetrievalPolicy{TopK: 5},
	}}}
	gateway, err := knowledgegateway.New(gatewayTestSearcher{}, datasetaccess.Defaults(), apps, generation.ExtractiveGenerator{})
	if err != nil {
		t.Fatal(err)
	}
	api := &KnowledgeGatewayAPI{service: gateway}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/apps/tenant_a-support/query", strings.NewReader(`{"query":"secret"}`))
	request.SetPathValue("app_id", "tenant_a-support")
	request = request.WithContext(context.WithValue(request.Context(), identityContextKey{}, auth.Identity{Subject: "bob", TenantID: "tenant_b", Roles: []string{"viewer"}}))
	response := httptest.NewRecorder()
	api.query(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant gateway status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestKnowledgeGatewayAPIAnswerStreamEmitsGatewayCompletion(t *testing.T) {
	apps := &fakeApplicationStore{bindings: []datasetaccess.KnowledgeBinding{{
		ApplicationID: "tenant_a-support", EnvironmentID: "tenant_a-support-dev", DatasetID: "public-identity", Status: "active",
		Policy: datasetaccess.RetrievalPolicy{TopK: 1, CandidateK: 1},
	}}}
	gateway, err := knowledgegateway.New(gatewayTestSearcher{}, datasetaccess.Defaults(), apps, generation.ExtractiveGenerator{})
	if err != nil {
		t.Fatal(err)
	}
	api := &KnowledgeGatewayAPI{service: gateway}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/apps/tenant_a-support/answer/stream", strings.NewReader(`{"query":"SSO"}`))
	request.SetPathValue("app_id", "tenant_a-support")
	request = request.WithContext(context.WithValue(request.Context(), identityContextKey{}, auth.Identity{Subject: "alice", TenantID: "tenant_a", Roles: []string{"viewer"}}))
	response := httptest.NewRecorder()
	api.answerStream(response, request)
	body := response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(response.Header().Get("Content-Type"), "text/event-stream") || !strings.Contains(body, "event: gateway_completed") || !strings.Contains(body, "knowledge-gateway") {
		t.Fatalf("gateway SSE status=%d headers=%v body=%s", response.Code, response.Header(), body)
	}
}
