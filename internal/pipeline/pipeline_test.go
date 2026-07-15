package pipeline

import (
	"context"
	"testing"

	"github.com/dingpuyu/rag-evolution-lab/internal/domain"
	"github.com/dingpuyu/rag-evolution-lab/internal/retrieval"
)

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
	if response.Trace.Pipeline != "v0-keyword" || len(response.Trace.Events) != 2 {
		t.Fatalf("unexpected trace: %#v", response.Trace)
	}
}
