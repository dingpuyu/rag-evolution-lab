package retrieval

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/dingpuyu/rag-evolution-lab/internal/domain"
)

type stubRetriever struct {
	name      string
	results   []domain.RetrievedChunk
	err       error
	observedK int
}

func (s *stubRetriever) Name() string { return s.name }

func (s *stubRetriever) Search(_ context.Context, request domain.QueryRequest) ([]domain.RetrievedChunk, error) {
	s.observedK = request.TopK
	if s.err != nil {
		return nil, s.err
	}
	limit := min(request.TopK, len(s.results))
	return append([]domain.RetrievedChunk(nil), s.results[:limit]...), nil
}

func TestRRFFusesRanksAndDeduplicatesChunks(t *testing.T) {
	keyword := &stubRetriever{name: "keyword", results: []domain.RetrievedChunk{
		{Chunk: domain.Chunk{ID: "exact"}},
		{Chunk: domain.Chunk{ID: "shared"}},
	}}
	vector := &stubRetriever{name: "vector", results: []domain.RetrievedChunk{
		{Chunk: domain.Chunk{ID: "semantic"}},
		{Chunk: domain.Chunk{ID: "shared"}},
	}}
	results, err := NewRRF(keyword, vector).Search(context.Background(), domain.QueryRequest{Query: "query", TopK: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 || results[0].Chunk.ID != "shared" {
		t.Fatalf("shared evidence should win two-list fusion: %#v", results)
	}
	expected := 2.0 / 62.0
	if math.Abs(results[0].Score-expected) > 1e-12 {
		t.Fatalf("unexpected fused score: got %.12f want %.12f", results[0].Score, expected)
	}
	if results[0].Stage != "hybrid-rrf" || results[0].Rank != 1 {
		t.Fatalf("unexpected result metadata: %#v", results[0])
	}
}

func TestRRFRequestsDeeperCandidatePools(t *testing.T) {
	first := &stubRetriever{name: "first"}
	second := &stubRetriever{name: "second"}
	_, err := NewRRF(first, second).Search(context.Background(), domain.QueryRequest{Query: "query", TopK: 3})
	if err != nil {
		t.Fatal(err)
	}
	if first.observedK != 20 || second.observedK != 20 {
		t.Fatalf("expected minimum candidate depth 20, got %d and %d", first.observedK, second.observedK)
	}
}

func TestRRFCanRequireCrossRetrieverConsensus(t *testing.T) {
	keyword := &stubRetriever{name: "keyword", results: []domain.RetrievedChunk{
		{Chunk: domain.Chunk{ID: "shared"}},
		{Chunk: domain.Chunk{ID: "keyword-only"}},
	}}
	vector := &stubRetriever{name: "vector", results: []domain.RetrievedChunk{
		{Chunk: domain.Chunk{ID: "vector-only"}},
		{Chunk: domain.Chunk{ID: "shared"}},
	}}
	results, err := NewRRFWithOptions(RRFOptions{MinSourceMatches: 2}, keyword, vector).Search(
		context.Background(), domain.QueryRequest{Query: "query", TopK: 5},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Chunk.ID != "shared" {
		t.Fatalf("consensus gate should retain only shared evidence: %#v", results)
	}
}

func TestRRFIdentifiesFailingRetriever(t *testing.T) {
	failing := &stubRetriever{name: "vector", err: errors.New("model unavailable")}
	_, err := NewRRF(&stubRetriever{name: "keyword"}, failing).Search(
		context.Background(), domain.QueryRequest{Query: "query", TopK: 5},
	)
	if err == nil || err.Error() != "search with vector: model unavailable" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRRFRejectsEmptyConfiguration(t *testing.T) {
	_, err := NewRRF().Search(context.Background(), domain.QueryRequest{Query: "query"})
	if err == nil {
		t.Fatal("expected empty hybrid configuration to fail")
	}
}
