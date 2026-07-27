package pipeline

import (
	"context"
	"testing"

	"github.com/dingpuyu/rag-evolution-lab/internal/domain"
	"github.com/dingpuyu/rag-evolution-lab/internal/rerank"
	"github.com/dingpuyu/rag-evolution-lab/internal/retrieval"
)

type degradedRetriever struct{}

func (degradedRetriever) Name() string { return "hybrid-rrf" }

func (degradedRetriever) Search(context.Context, domain.QueryRequest) ([]domain.RetrievedChunk, error) {
	return []domain.RetrievedChunk{{
		Chunk: domain.Chunk{ID: "partial#1", DocumentID: "partial", Content: "fallback evidence", Status: "active", Visibility: "public"},
		Stage: "hybrid-rrf-partial",
	}}, nil
}

func TestPipelineReturnsCitationAndTrace(t *testing.T) {
	index := retrieval.NewBM25([]domain.Chunk{{
		ID: "doc#1", DocumentID: "doc", DocumentTitle: "文档", Content: "E1027 超过配额", Status: "active", Visibility: "public",
	}})
	target := New("v0-keyword", index)
	response, err := target.Query(context.Background(), domain.QueryRequest{Query: "E1027", TopK: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Citations) != 1 || response.Citations[0].ChunkID != "doc#1" {
		t.Fatalf("unexpected citations: %#v", response.Citations)
	}
	if response.Trace.Pipeline != "v0-keyword" || len(response.Trace.Events) != 4 {
		t.Fatalf("unexpected trace: %#v", response.Trace)
	}
	if len(response.Context) != 1 || response.Context[0].Chunk.ID != "doc#1" {
		t.Fatalf("unexpected selected context: %#v", response.Context)
	}
}

func TestAdvancedPipelineReranksCandidatesAndPacksContext(t *testing.T) {
	index := retrieval.NewBM25([]domain.Chunk{
		{ID: "noise#1", DocumentID: "noise", Content: "API 错误的一般排查", Status: "active", Visibility: "public"},
		{ID: "exact#1", DocumentID: "exact", Content: "E1027 表示请求超过配额", Status: "active", Visibility: "public"},
	})
	target := NewWithOptions("v5-rerank", index, Options{
		Reranker:           rerank.Heuristic{},
		CandidateTopN:      10,
		ContextMaxChunks:   1,
		ContextTokenBudget: 100,
	})
	response, err := target.Query(context.Background(), domain.QueryRequest{Query: "E1027 是什么错误", TopK: 2})
	if err != nil {
		t.Fatal(err)
	}
	if response.Retrieval[0].Chunk.ID != "exact#1" || len(response.Context) != 1 {
		t.Fatalf("unexpected advanced response: %#v", response)
	}
	events := make(map[string]bool)
	for _, event := range response.Trace.Events {
		events[event.Name] = true
	}
	for _, expected := range []string{"results_reranked", "context_packed", "citations_verified"} {
		if !events[expected] {
			t.Fatalf("missing trace event %q: %#v", expected, response.Trace.Events)
		}
	}
}

func TestPipelineTraceMarksDegradedRetrieval(t *testing.T) {
	response, err := New("hybrid", degradedRetriever{}).Query(context.Background(), domain.QueryRequest{Query: "fallback", TopK: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range response.Trace.Events {
		if event.Name != "retrieval" {
			continue
		}
		if degraded, ok := event.Attributes["degraded"].(bool); !ok || !degraded {
			t.Fatalf("expected degraded retrieval trace, got %#v", event.Attributes)
		}
		stages, ok := event.Attributes["result_stages"].(map[string]int)
		if !ok || stages["hybrid-rrf-partial"] != 1 {
			t.Fatalf("expected partial stage counts, got %#v", event.Attributes["result_stages"])
		}
		return
	}
	t.Fatal("retrieval trace event not found")
}
