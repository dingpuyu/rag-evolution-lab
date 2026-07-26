package milvus

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dingpuyu/rag-evolution-lab/internal/domain"
	"github.com/dingpuyu/rag-evolution-lab/internal/retrieval"
)

func TestRetrieverPushesACLAndMetadataIntoMilvus(t *testing.T) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v2/vectordb/entities/search" {
			t.Fatalf("unexpected path %s", request.URL.Path)
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"code": 0,
			"data": []map[string]any{{
				"chunk_id": "sso#c001", "document_id": "sso", "title": "SSO", "content": "在身份中心配置。",
				"allowed_tenants": []string{"tenant_a"}, "allowed_roles": []string{"admin"},
				"product": "identity", "version": "2.3", "status": "active", "visibility": "internal", "distance": 0.93,
			}},
		})
	}))
	defer server.Close()

	target, err := NewRetriever(
		NewClient(Config{BaseURL: server.URL}),
		retrieval.HashEmbedder{Dimensions: 8},
		"chunks",
		RetrieverOptions{UseMetadata: true, SearchEF: 96},
	)
	if err != nil {
		t.Fatal(err)
	}
	request := domain.QueryRequest{
		Query: "如何配置 SSO", TenantID: "tenant_a", UserRole: "admin", Product: "identity", TopK: 5,
	}
	results, err := target.Search(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	wantFilter := `(visibility == "public" or (array_contains(allowed_tenants, "tenant_a") and array_contains(allowed_roles, "admin"))) and product == "identity" and status == "active"`
	if payload["filter"] != wantFilter {
		t.Fatalf("filter=%q want=%q", payload["filter"], wantFilter)
	}
	searchParams := payload["searchParams"].(map[string]any)
	params := searchParams["params"].(map[string]any)
	if params["ef"] != float64(96) {
		t.Fatalf("unexpected search params: %#v", params)
	}
	if len(results) != 1 || results[0].Chunk.ID != "sso#c001" || results[0].Rank != 1 || results[0].Score != 0.93 {
		t.Fatalf("unexpected results: %#v", results)
	}
	if results[0].Stage != "milvus-hnsw-metadata" || len(results[0].Chunk.AllowedRoles) != 1 {
		t.Fatalf("metadata was not preserved: %#v", results[0])
	}
}

func TestRetrieverFilterFailsClosedAndSupportsExplicitVersion(t *testing.T) {
	filter := buildRetrieverFilter(domain.QueryRequest{
		TenantID: "tenant_a", Product: "identity", Version: "2.1",
	}, true)
	want := `visibility == "public" and product == "identity" and version == "2.1"`
	if filter != want {
		t.Fatalf("filter=%q want=%q", filter, want)
	}
}

func TestRetrieverExposesProductionTraceAttributes(t *testing.T) {
	target, err := NewRetriever(NewClient(Config{}), retrieval.HashEmbedder{Dimensions: 8}, "chunks", RetrieverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	attributes := target.TraceAttributes(domain.QueryRequest{})
	if attributes["vector_backend"] != "milvus" || attributes["filter_stage"] != "pre_ann" || attributes["index_type"] != "HNSW" {
		t.Fatalf("unexpected trace attributes: %#v", attributes)
	}
}

func TestServiceDatasetScopesFailClosed(t *testing.T) {
	publicOnly := buildFilter(Query{
		Tenant: "tenant_a", Role: "admin", Product: "identity", AccessScope: "public_only",
	})
	if strings.Contains(publicOnly, "allowed_tenants") || !strings.Contains(publicOnly, `visibility == "public"`) {
		t.Fatalf("public dataset filter=%q", publicOnly)
	}
	tenantOnly := buildFilter(Query{
		Tenant: "tenant_a", Role: "admin", Product: "tenant-operations", AccessScope: "tenant_only",
	})
	if strings.Contains(tenantOnly, `visibility == "public"`) ||
		!strings.Contains(tenantOnly, `allowed_tenants, "tenant_a"`) {
		t.Fatalf("tenant dataset filter=%q", tenantOnly)
	}
	failClosed := buildFilter(Query{Product: "tenant-operations", AccessScope: "tenant_only"})
	if !strings.Contains(failClosed, "false") {
		t.Fatalf("missing trusted tenant must fail closed: %q", failClosed)
	}
}
