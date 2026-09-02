package retrievallab

import (
	"context"
	"strings"
	"testing"

	"github.com/dingpuyu/rag-evolution-lab/internal/milvus"
	"github.com/dingpuyu/rag-evolution-lab/internal/retrieval"
)

type fakeMilvus struct {
	created string
	dropped []string
	records []milvus.Record
	filter  string
}

func (client *fakeMilvus) CreateCollectionWithOptions(_ context.Context, name string, _ milvus.CollectionOptions) error {
	client.created = name
	return nil
}
func (client *fakeMilvus) DropCollection(_ context.Context, name string) error {
	client.dropped = append(client.dropped, name)
	return nil
}
func (client *fakeMilvus) Upsert(_ context.Context, _ string, records []milvus.Record) (int64, error) {
	client.records = records
	return int64(len(records)), nil
}
func (*fakeMilvus) FlushCollection(context.Context, string) error { return nil }
func (client *fakeMilvus) HybridSearch(_ context.Context, request milvus.HybridSearchRequest) ([]milvus.SearchHit, error) {
	client.filter = request.Filter
	return []milvus.SearchHit{
		{ChunkID: client.records[0].ChunkID, DocumentID: client.records[0].DocumentID, Content: client.records[0].Content, Distance: 0, FusionScore: .02},
		{ChunkID: client.records[1].ChunkID, DocumentID: client.records[1].DocumentID, Content: client.records[1].Content, Distance: 1, FusionScore: .01},
	}, nil
}

type reverseReranker struct{}

func (reverseReranker) Name() string { return "strict-test-reranker" }
func (reverseReranker) Rerank(_ context.Context, _ string, hits []milvus.SearchHit) ([]milvus.SearchHit, error) {
	result := append([]milvus.SearchHit(nil), hits...)
	for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
		result[left], result[right] = result[right], result[left]
	}
	for index := range result {
		result[index].RerankScore = 1 - float64(index)*.1
		result[index].RerankScoreSet = true
	}
	return result, nil
}

func TestRunUsesTemporaryCollectionTrustedACLAndCleanup(t *testing.T) {
	client := &fakeMilvus{}
	service, err := New(client, retrieval.HashEmbedder{Dimensions: 8}, reverseReranker{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Run(context.Background(), Identity{TenantID: "tenant_a", Role: "admin"}, RunInput{
		RunID: "run-1", Variant: "candidate",
		Chunks: []Chunk{
			{ChunkID: "doc-a#1", DocumentID: "doc-a", Content: "first answer"},
			{ChunkID: "doc-b#1", DocumentID: "doc-b", Content: "second answer"},
		},
		Queries: []Query{{QueryID: "q1", Text: "answer", TopK: 2}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(client.created, "raglab_eval_") || client.created == "raglab_chunks_qwen3" {
		t.Fatalf("unsafe collection name %q", client.created)
	}
	if len(client.dropped) != 1 || client.dropped[0] != client.created || !result.CleanupCompleted || result.ProductionMutation {
		t.Fatalf("sandbox cleanup contract failed: %#v %#v", client.dropped, result)
	}
	if !strings.Contains(client.filter, `tenant_id == "tenant_a"`) || !strings.Contains(client.filter, `allowed_roles, "admin"`) {
		t.Fatalf("trusted ACL missing from pre-ANN filter: %s", client.filter)
	}
	if got := result.Queries[0].Hits[0]; got.DocumentID != "doc-b" || got.PreRerankRank != 2 || got.PostRerankRank != 1 || got.RerankScore == nil {
		t.Fatalf("rerank trace is incomplete: %#v", got)
	}
}

func TestRunRejectsUnboundedOrIncompleteInput(t *testing.T) {
	service, _ := New(&fakeMilvus{}, retrieval.HashEmbedder{Dimensions: 8}, reverseReranker{})
	_, err := service.Run(context.Background(), Identity{TenantID: "tenant_a", Role: "admin"}, RunInput{RunID: "run-1", Variant: "baseline"})
	if err == nil || !strings.Contains(err.Error(), "chunks") {
		t.Fatalf("expected bounded input validation, got %v", err)
	}
}

func TestExactScopeFilterPrefersLongestModelAndExplicitVersionAndLot(t *testing.T) {
	filters, applied := exactScopeFilter(
		"VSM-410 Pro 软件 4.2 批次 LOT-K2608 怎么处理？",
		[]Chunk{
			{ModelCodes: []string{"VSM-410"}, SoftwareVersionFrom: "3.8", SoftwareVersionTo: "3.8", AffectedLots: []string{"LOT-K2501"}},
			{ModelCodes: []string{"VSM-410 Pro"}, SoftwareVersionFrom: "4.2", SoftwareVersionTo: "4.2", AffectedLots: []string{"LOT-K2608"}},
		},
	)
	joined := strings.Join(filters, " and ")
	if !strings.Contains(joined, `array_contains(model_codes, "VSM-410 Pro")`) || strings.Contains(joined, `array_contains(model_codes, "VSM-410")`) {
		t.Fatalf("model suffix scope is unsafe: %s", joined)
	}
	if !strings.Contains(joined, `software_version_from == "4.2"`) || !strings.Contains(joined, `array_contains(affected_lots, "LOT-K2608")`) {
		t.Fatalf("version/lot scope missing: %s", joined)
	}
	if strings.Join(applied, ",") != "model_code=VSM-410 Pro,software_version=4.2,affected_lot=LOT-K2608" {
		t.Fatalf("unexpected scope trace: %#v", applied)
	}
}

func TestExactScopeFilterRejectsPartialASCIIIdentifiers(t *testing.T) {
	filters, applied := exactScopeFilter("VSM-420 软件 14.2，批次 LOT-K26081", []Chunk{
		{ModelCodes: []string{"VSM-42"}, SoftwareVersionFrom: "4.2", AffectedLots: []string{"LOT-K2608"}},
	})
	if len(filters) != 0 || len(applied) != 0 {
		t.Fatalf("partial identifiers must not become trusted scope filters: filters=%v applied=%v", filters, applied)
	}
}
