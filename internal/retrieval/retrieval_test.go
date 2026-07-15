package retrieval

import (
	"context"
	"testing"

	"github.com/dingpuyu/rag-evolution-lab/internal/domain"
)

func TestBM25RanksExactErrorCodeFirst(t *testing.T) {
	chunks := []domain.Chunk{
		{ID: "errors#1", DocumentID: "errors", Content: "E1027 表示请求超过配额", Status: "active", Visibility: "public"},
		{ID: "other#1", DocumentID: "other", Content: "E1021 表示 API Key 无效", Status: "active", Visibility: "public"},
	}
	results, err := NewBM25(chunks).Search(context.Background(), domain.QueryRequest{Query: "E1027", TopK: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 || results[0].Chunk.DocumentID != "errors" {
		t.Fatalf("unexpected results: %#v", results)
	}
}

func TestVectorUsesSemanticAliases(t *testing.T) {
	chunks := []domain.Chunk{
		{ID: "sso#1", DocumentID: "sso", Content: "企业 SSO 单点登录配置", Status: "active", Visibility: "public"},
		{ID: "upload#1", DocumentID: "upload", Content: "上传文件大小限制", Status: "active", Visibility: "public"},
	}
	index, err := NewVector(context.Background(), chunks, HashEmbedder{Dimensions: 256})
	if err != nil {
		t.Fatal(err)
	}
	results, err := index.Search(context.Background(), domain.QueryRequest{Query: "员工只登录一次", TopK: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 || results[0].Chunk.DocumentID != "sso" {
		t.Fatalf("unexpected results: %#v", results)
	}
}

func TestRetrieverFailsClosedWithoutTenantContext(t *testing.T) {
	chunks := []domain.Chunk{{
		ID:             "secret#1",
		DocumentID:     "secret",
		Content:        "reports-priority-a",
		Status:         "active",
		Visibility:     "tenant",
		AllowedTenants: []string{"tenant_a"},
		AllowedRoles:   []string{"admin"},
	}}
	index := NewBM25(chunks)
	results, err := index.Search(context.Background(), domain.QueryRequest{Query: "reports-priority-a", TopK: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("expected no results without tenant context, got %#v", results)
	}
}

func TestRetrieverAllowsMatchingTenantAndRole(t *testing.T) {
	chunks := []domain.Chunk{{
		ID:             "secret#1",
		DocumentID:     "secret",
		Content:        "reports-priority-a",
		Status:         "active",
		Visibility:     "tenant",
		AllowedTenants: []string{"tenant_a"},
		AllowedRoles:   []string{"admin"},
	}}
	results, err := NewBM25(chunks).Search(context.Background(), domain.QueryRequest{
		Query: "reports-priority-a", TenantID: "tenant_a", UserRole: "admin", TopK: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected one authorized result, got %#v", results)
	}
}

func TestMetadataFilterIsOptInForBaselineComparability(t *testing.T) {
	chunks := []domain.Chunk{
		{ID: "old#1", DocumentID: "old", Content: "SSO 旧入口", Product: "identity", Version: "2.1", Status: "deprecated", Visibility: "public"},
		{ID: "new#1", DocumentID: "new", Content: "SSO 新入口", Product: "identity", Version: "2.3", Status: "active", Visibility: "public"},
	}
	request := domain.QueryRequest{Query: "SSO 入口", Product: "identity", Version: "2.3", TopK: 5}
	baseline, err := NewBM25(chunks).Search(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(baseline) != 2 {
		t.Fatalf("baseline should expose version-conflict failure, got %#v", baseline)
	}
	structured, err := NewBM25WithOptions(chunks, Options{UseMetadata: true}).Search(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(structured) != 1 || structured[0].Chunk.DocumentID != "new" {
		t.Fatalf("metadata filter should keep requested version, got %#v", structured)
	}
}

func TestMetadataFilterAllowsExplicitDeprecatedVersion(t *testing.T) {
	chunks := []domain.Chunk{
		{ID: "old#1", DocumentID: "old", Content: "SSO 安全设置入口", Product: "identity", Version: "2.1", Status: "deprecated", Visibility: "public"},
		{ID: "new#1", DocumentID: "new", Content: "SSO 身份中心入口", Product: "identity", Version: "2.3", Status: "active", Visibility: "public"},
	}
	request := domain.QueryRequest{Query: "SSO 入口", Product: "identity", Version: "2.1", TopK: 5}
	results, err := NewBM25WithOptions(chunks, Options{UseMetadata: true}).Search(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Chunk.DocumentID != "old" {
		t.Fatalf("explicit old version should remain retrievable, got %#v", results)
	}
}

func TestMetadataFilterRejectsDifferentProduct(t *testing.T) {
	chunks := []domain.Chunk{
		{ID: "identity#1", DocumentID: "identity", Content: "配置入口", Product: "identity", Version: "2.3", Status: "active", Visibility: "public"},
		{ID: "storage#1", DocumentID: "storage", Content: "配置入口", Product: "storage", Version: "2.3", Status: "active", Visibility: "public"},
	}
	request := domain.QueryRequest{Query: "配置入口", Product: "identity", Version: "2.3", TopK: 5}
	results, err := NewBM25WithOptions(chunks, Options{UseMetadata: true}).Search(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Chunk.DocumentID != "identity" {
		t.Fatalf("product filter returned unexpected chunks: %#v", results)
	}
}
