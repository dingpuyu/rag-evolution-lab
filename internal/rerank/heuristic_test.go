package rerank

import (
	"context"
	"testing"

	"github.com/dingpuyu/rag-evolution-lab/internal/domain"
)

func TestHeuristicPromotesCandidateWithExactEvidence(t *testing.T) {
	candidates := []domain.RetrievedChunk{
		{Chunk: domain.Chunk{ID: "noise", Content: "API 请求失败的一般排查方法"}, Rank: 1},
		{Chunk: domain.Chunk{ID: "evidence", Content: "E1027 表示 API 配额已用尽"}, Rank: 2},
	}
	results, err := (Heuristic{}).Rerank(context.Background(), domain.QueryRequest{Query: "E1027 是什么错误？"}, candidates)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Chunk.ID != "evidence" || results[0].Rank != 1 {
		t.Fatalf("expected exact evidence first, got %#v", results)
	}
}

func TestHeuristicDoesNotMutateCandidateSlice(t *testing.T) {
	candidates := []domain.RetrievedChunk{{Chunk: domain.Chunk{ID: "a", Content: "alpha"}, Score: 42, Rank: 1}}
	_, err := (Heuristic{}).Rerank(context.Background(), domain.QueryRequest{Query: "alpha"}, candidates)
	if err != nil {
		t.Fatal(err)
	}
	if candidates[0].Score != 42 || candidates[0].Stage != "" {
		t.Fatalf("input candidates were mutated: %#v", candidates)
	}
}
