package knowledgegateway

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/dingpuyu/rag-evolution-lab/internal/auth"
	"github.com/dingpuyu/rag-evolution-lab/internal/datasetaccess"
	"github.com/dingpuyu/rag-evolution-lab/internal/generation"
	"github.com/dingpuyu/rag-evolution-lab/internal/milvus"
	"github.com/dingpuyu/rag-evolution-lab/internal/querytrace"
)

type fakeApps struct {
	bindings []datasetaccess.KnowledgeBinding
	err      error
}

func (store fakeApps) VisibleApplications(context.Context, auth.Identity) ([]datasetaccess.Application, error) {
	return nil, nil
}
func (store fakeApps) CreateApplication(context.Context, auth.Identity, datasetaccess.CreateApplication) (datasetaccess.Application, error) {
	return datasetaccess.Application{}, nil
}
func (store fakeApps) Environments(context.Context, auth.Identity, string) ([]datasetaccess.Environment, error) {
	return nil, nil
}
func (store fakeApps) CreateEnvironment(context.Context, auth.Identity, string, datasetaccess.CreateEnvironment) (datasetaccess.Environment, error) {
	return datasetaccess.Environment{}, nil
}
func (store fakeApps) Bindings(context.Context, auth.Identity, string, string) ([]datasetaccess.KnowledgeBinding, error) {
	return store.bindings, store.err
}
func (store fakeApps) CreateBinding(context.Context, auth.Identity, string, datasetaccess.CreateBinding) (datasetaccess.KnowledgeBinding, error) {
	return datasetaccess.KnowledgeBinding{}, nil
}

type fakeDatasetStore struct{ catalog *datasetaccess.Catalog }

func (store fakeDatasetStore) EnsureIdentity(context.Context, auth.Identity) error { return nil }
func (store fakeDatasetStore) Visible(ctx context.Context, identity auth.Identity) ([]datasetaccess.Dataset, error) {
	return store.catalog.Visible(ctx, identity)
}
func (store fakeDatasetStore) Authorize(ctx context.Context, id string, identity auth.Identity) (datasetaccess.Dataset, error) {
	return store.catalog.Authorize(ctx, id, identity)
}
func (store fakeDatasetStore) Create(context.Context, auth.Identity, datasetaccess.CreateDataset) (datasetaccess.Dataset, error) {
	return datasetaccess.Dataset{}, errors.New("not supported")
}
func (store fakeDatasetStore) Members(ctx context.Context, identity auth.Identity) ([]datasetaccess.Membership, error) {
	return store.catalog.Members(ctx, identity)
}
func (store fakeDatasetStore) Status(ctx context.Context, identity auth.Identity) (datasetaccess.Status, error) {
	return store.catalog.Status(ctx, identity)
}

type fakeSearcher struct{ queries []milvus.Query }

type fakeTraceStore struct{ records []querytrace.Record }

func (store *fakeTraceStore) UpsertQueryTrace(_ context.Context, record querytrace.Record) error {
	for index := range store.records {
		if store.records[index].TraceID == record.TraceID {
			store.records[index] = record
			return nil
		}
	}
	store.records = append(store.records, record)
	return nil
}

func (store *fakeTraceStore) GetQueryTrace(context.Context, auth.Identity, string, string) (querytrace.Record, error) {
	if len(store.records) == 0 {
		return querytrace.Record{}, querytrace.ErrNotFound
	}
	return store.records[len(store.records)-1], nil
}

func (searcher *fakeSearcher) Search(_ context.Context, query milvus.Query) (milvus.SearchResult, error) {
	searcher.queries = append(searcher.queries, query)
	return milvus.SearchResult{
		Query: query.Text, Collection: "lifecycle", Embedder: "test", Dimensions: 3, Metric: "COSINE",
		EmbeddingLatencyMS: 10, SearchLatencyMS: 2,
		Hits: []milvus.SearchHit{
			{ChunkID: query.Product + "-chunk", DocumentID: query.Product + "-doc", Title: query.Product, Content: "verified evidence for " + query.Product, Distance: float64(len(searcher.queries))},
			{ChunkID: query.Product + "-chunk-2", DocumentID: query.Product + "-doc", Title: query.Product, Content: "secondary evidence for " + query.Product, Distance: float64(len(searcher.queries)) + 0.2},
		},
	}, nil
}

func TestSearchResolvesBindingsAndMergesResults(t *testing.T) {
	searcher := &fakeSearcher{}
	service, err := New(searcher, fakeDatasetStore{catalog: datasetaccess.Defaults()}, fakeApps{bindings: []datasetaccess.KnowledgeBinding{
		{DatasetID: "public-identity", DatasetName: "Identity", Status: "active", Priority: 2, Policy: datasetaccess.RetrievalPolicy{TopK: 1, CandidateK: 4}},
		{DatasetID: "public-reports", DatasetName: "Reports", Status: "active", Priority: 1, Policy: datasetaccess.RetrievalPolicy{TopK: 1, CandidateK: 4}},
	}}, generation.ExtractiveGenerator{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Search(context.Background(), auth.Identity{Subject: "alice", TenantID: "tenant_a", Roles: []string{"viewer"}}, Request{AppID: "tenant_a-support", EnvironmentID: "tenant_a-support-dev", Query: "export", TopK: 4})
	if err != nil {
		t.Fatal(err)
	}
	if len(searcher.queries) != 2 || len(result.Bindings) != 2 || len(result.Result.Hits) != 2 {
		t.Fatalf("unexpected gateway result: queries=%#v bindings=%#v result=%#v", searcher.queries, result.Bindings, result.Result)
	}
	for _, query := range searcher.queries {
		if query.Tenant != "public" || query.AccessScope != "public_only" || query.Product == "" {
			t.Fatalf("gateway did not build trusted public query: %#v", query)
		}
	}
	if result.Result.Collection != "knowledge-gateway" || result.Result.Filter == "" {
		t.Fatalf("missing gateway trace: %#v", result.Result)
	}
	if result.Result.EmbeddingLatencyMS != 20 || result.Result.SearchLatencyMS != 4 {
		t.Fatalf("gateway latency should sum each binding exactly once: %#v", result.Result)
	}
}

func TestSearchFailsClosedWhenBindingIsNotVisible(t *testing.T) {
	service, err := New(&fakeSearcher{}, fakeDatasetStore{catalog: datasetaccess.Defaults()}, fakeApps{bindings: []datasetaccess.KnowledgeBinding{
		{DatasetID: "tenant-a-operations", Status: "active", Policy: datasetaccess.RetrievalPolicy{TopK: 5}},
	}}, generation.ExtractiveGenerator{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Search(context.Background(), auth.Identity{Subject: "bob", TenantID: "tenant_b", Roles: []string{"viewer"}}, Request{AppID: "tenant_a-support", Query: "secret"})
	if !errors.Is(err, datasetaccess.ErrDatasetDenied) {
		t.Fatalf("expected binding to fail closed with dataset denial, got %v", err)
	}
}

func TestAnswerUsesMergedEvidence(t *testing.T) {
	service, err := New(&fakeSearcher{}, fakeDatasetStore{catalog: datasetaccess.Defaults()}, fakeApps{bindings: []datasetaccess.KnowledgeBinding{
		{DatasetID: "public-identity", Status: "active", Policy: datasetaccess.RetrievalPolicy{TopK: 1, CandidateK: 1}},
	}}, generation.ExtractiveGenerator{})
	if err != nil {
		t.Fatal(err)
	}
	answer, err := service.Answer(context.Background(), auth.Identity{Subject: "viewer", TenantID: "tenant_a", Roles: []string{"viewer"}}, Request{AppID: "tenant_a-support", Query: "sso"})
	if err != nil {
		t.Fatal(err)
	}
	if !answer.Result.Answerable || len(answer.Result.Citations) != 1 || answer.Result.Search.Collection != "knowledge-gateway" {
		t.Fatalf("unexpected gateway answer: %#v", answer)
	}
}

func TestGatewayAppliesRewriteRerankAndPersistsTraceLifecycle(t *testing.T) {
	searcher := &fakeSearcher{}
	traces := &fakeTraceStore{}
	service, err := NewWithOptions(searcher, fakeDatasetStore{catalog: datasetaccess.Defaults()}, fakeApps{bindings: []datasetaccess.KnowledgeBinding{
		{DatasetID: "public-identity", Status: "active", Policy: datasetaccess.RetrievalPolicy{TopK: 1, CandidateK: 2, QueryRewrite: true, Rerank: true}},
	}}, generation.ExtractiveGenerator{}, Options{TraceStore: traces})
	if err != nil {
		t.Fatal(err)
	}
	identity := auth.Identity{Subject: "viewer", TenantID: "tenant_a", Roles: []string{"viewer"}}
	result, err := service.Search(context.Background(), identity, Request{AppID: "tenant_a-support", Query: "单点登录"})
	if err != nil {
		t.Fatal(err)
	}
	if len(traces.records) != 1 || result.TraceID == "" || !result.Bindings[0].Rewrite.Applied || !result.Bindings[0].Rerank.Applied {
		t.Fatalf("strategy/trace not applied: result=%#v traces=%#v", result, traces.records)
	}
	if len(searcher.queries) != 1 || !strings.Contains(searcher.queries[0].Text, "sso") {
		t.Fatalf("rewritten query was not sent to retriever: %#v", searcher.queries)
	}
	answer, err := service.Answer(context.Background(), identity, Request{AppID: "tenant_a-support", Query: "单点登录"})
	if err != nil {
		t.Fatal(err)
	}
	if answer.TraceID == "" || len(traces.records) != 2 || traces.records[1].Status != "completed" || traces.records[1].Generator == "" {
		t.Fatalf("answer trace was not completed: answer=%#v traces=%#v", answer, traces.records)
	}
}

type crossBindingSearcher struct{}

func (crossBindingSearcher) Search(_ context.Context, query milvus.Query) (milvus.SearchResult, error) {
	content := "通用知识说明"
	if query.Product == "reports" {
		content = "安全审计字段包括操作人、时间戳和资源标识"
	}
	return milvus.SearchResult{Query: query.Text, Collection: "lifecycle", Embedder: "test", Dimensions: 3, Metric: "COSINE", Hits: []milvus.SearchHit{
		{ChunkID: query.Product + "-noise", DocumentID: query.Product + "-noise-doc", Title: query.Product, Content: "通用知识说明", Distance: 0.2},
		{ChunkID: query.Product + "-evidence", DocumentID: query.Product + "-doc", Title: query.Product, Content: content, Distance: 0.4},
	}}, nil
}

func TestSearchGloballyMergesRerankedBindingsBeforeTopK(t *testing.T) {
	service, err := New(crossBindingSearcher{}, fakeDatasetStore{catalog: datasetaccess.Defaults()}, fakeApps{bindings: []datasetaccess.KnowledgeBinding{
		{DatasetID: "public-identity", Status: "active", Priority: 1, Policy: datasetaccess.RetrievalPolicy{TopK: 1, CandidateK: 2, Rerank: true}},
		{DatasetID: "public-reports", Status: "active", Priority: 2, Policy: datasetaccess.RetrievalPolicy{TopK: 1, CandidateK: 2, Rerank: true}},
	}}, generation.ExtractiveGenerator{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Search(context.Background(), auth.Identity{Subject: "viewer", TenantID: "tenant_a", Roles: []string{"viewer"}}, Request{
		AppID: "tenant_a-support", Query: "安全审计字段", TopK: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Result.Hits) != 1 || result.Result.Hits[0].DocumentID != "reports-doc" {
		t.Fatalf("global rerank was not applied before TopK: bindings=%#v hits=%#v", result.Bindings, result.Result.Hits)
	}
}
