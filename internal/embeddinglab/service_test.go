package embeddinglab

import (
	"context"
	"math"
	"testing"

	"github.com/dingpuyu/rag-evolution-lab/internal/retrieval"
)

type asymmetricEmbedder struct {
	queryCalls    int
	documentCalls int
}

func (a *asymmetricEmbedder) Name() string { return "asymmetric-test" }
func (a *asymmetricEmbedder) EmbedQuery(_ context.Context, _ string) ([]float64, error) {
	a.queryCalls++
	return []float64{1, 0}, nil
}
func (a *asymmetricEmbedder) EmbedDocuments(_ context.Context, texts []string) ([][]float64, error) {
	a.documentCalls++
	result := make([][]float64, len(texts))
	for index := range result {
		result[index] = []float64{0, 1}
	}
	return result, nil
}

func TestSimilarityReturnsVectorEvidenceAndMetrics(t *testing.T) {
	service, err := New(retrieval.HashEmbedder{Dimensions: 32})
	if err != nil {
		t.Fatal(err)
	}
	response, err := service.Similarity(context.Background(), SimilarityRequest{
		TextA: "企业单点登录配置",
		TextB: "员工只登录一次",
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Dimensions != 32 || len(response.VectorA.Preview) != 12 || response.VectorA.Norm == 0 {
		t.Fatalf("unexpected response: %#v", response)
	}
	if math.IsNaN(response.Metrics.CosineSimilarity) {
		t.Fatal("cosine similarity must be finite")
	}
}

func TestSimilarityCanReturnFullVectors(t *testing.T) {
	service, _ := New(retrieval.HashEmbedder{Dimensions: 8})
	response, err := service.Similarity(context.Background(), SimilarityRequest{
		TextA: "alpha", TextB: "beta", IncludeVectors: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.VectorA.Vector) != 8 || len(response.VectorB.Vector) != 8 {
		t.Fatalf("expected full vectors: %#v", response)
	}
}

func TestQueryDocumentModeUsesAsymmetricEncodingPaths(t *testing.T) {
	embedder := &asymmetricEmbedder{}
	service, _ := New(embedder)
	response, err := service.Similarity(context.Background(), SimilarityRequest{
		TextA: "query", TextB: "document", Mode: ModeQueryDocument,
	})
	if err != nil {
		t.Fatal(err)
	}
	if embedder.queryCalls != 1 || embedder.documentCalls != 1 {
		t.Fatalf("unexpected encoding calls: query=%d document=%d", embedder.queryCalls, embedder.documentCalls)
	}
	if response.Metrics.CosineSimilarity != 0 {
		t.Fatalf("unexpected cosine: %#v", response.Metrics)
	}
}

func TestSimilarityRejectsInvalidInput(t *testing.T) {
	service, _ := New(retrieval.HashEmbedder{Dimensions: 8})
	for _, request := range []SimilarityRequest{
		{TextA: "", TextB: "text"},
		{TextA: "a", TextB: "b", Mode: "invalid"},
		{TextA: "a", TextB: "b", PreviewDimensions: 65},
	} {
		if _, err := service.Similarity(context.Background(), request); err == nil {
			t.Fatalf("expected request to fail: %#v", request)
		}
	}
}
