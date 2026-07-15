package retrieval

import (
	"context"
	"testing"
)

type countingEmbedder struct {
	documentCalls int
	queryCalls    int
}

func (c *countingEmbedder) Name() string { return "counting" }

func (c *countingEmbedder) EmbedDocuments(_ context.Context, texts []string) ([][]float64, error) {
	c.documentCalls++
	vectors := make([][]float64, len(texts))
	for index := range texts {
		vectors[index] = []float64{float64(index + 1), 1}
	}
	return vectors, nil
}

func (c *countingEmbedder) EmbedQuery(_ context.Context, _ string) ([]float64, error) {
	c.queryCalls++
	return []float64{1, 1}, nil
}

func TestCachedEmbedderReusesDocumentVectors(t *testing.T) {
	inner := &countingEmbedder{}
	cached := CachedEmbedder{Inner: inner, Dir: t.TempDir()}
	texts := []string{"first", "second"}

	first, err := cached.EmbedDocuments(context.Background(), texts)
	if err != nil {
		t.Fatal(err)
	}
	second, err := cached.EmbedDocuments(context.Background(), texts)
	if err != nil {
		t.Fatal(err)
	}
	if inner.documentCalls != 1 {
		t.Fatalf("expected one document embedding call, got %d", inner.documentCalls)
	}
	if len(first) != len(second) || first[1][0] != second[1][0] {
		t.Fatalf("cached vectors differ: %#v %#v", first, second)
	}
}

func TestCachedEmbedderInvalidatesWhenCorpusChanges(t *testing.T) {
	inner := &countingEmbedder{}
	cached := CachedEmbedder{Inner: inner, Dir: t.TempDir()}
	if _, err := cached.EmbedDocuments(context.Background(), []string{"first"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cached.EmbedDocuments(context.Background(), []string{"changed"}); err != nil {
		t.Fatal(err)
	}
	if inner.documentCalls != 2 {
		t.Fatalf("expected cache invalidation, got %d calls", inner.documentCalls)
	}
}

func TestCachedEmbedderDoesNotCacheQueries(t *testing.T) {
	inner := &countingEmbedder{}
	cached := CachedEmbedder{Inner: inner, Dir: t.TempDir()}
	if _, err := cached.EmbedQuery(context.Background(), "same query"); err != nil {
		t.Fatal(err)
	}
	if _, err := cached.EmbedQuery(context.Background(), "same query"); err != nil {
		t.Fatal(err)
	}
	if inner.queryCalls != 2 {
		t.Fatalf("expected live query embedding calls, got %d", inner.queryCalls)
	}
}
