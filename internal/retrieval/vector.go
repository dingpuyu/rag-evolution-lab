package retrieval

import (
	"context"

	"github.com/dingpuyu/rag-evolution-lab/internal/domain"
	"github.com/dingpuyu/rag-evolution-lab/internal/textutil"
)

type Embedder interface {
	Embed(text string) []float64
	Name() string
}

type HashEmbedder struct {
	Dimensions int
}

func (h HashEmbedder) Name() string { return "semantic-hash-v1" }

func (h HashEmbedder) Embed(text string) []float64 {
	return textutil.HashVector(text, h.Dimensions)
}

type Vector struct {
	chunks   []domain.Chunk
	vectors  [][]float64
	embedder Embedder
	options  Options
}

func NewVector(chunks []domain.Chunk, embedder Embedder) *Vector {
	return NewVectorWithOptions(chunks, embedder, Options{})
}

func NewVectorWithOptions(chunks []domain.Chunk, embedder Embedder, options Options) *Vector {
	if embedder == nil {
		embedder = HashEmbedder{Dimensions: 512}
	}
	index := &Vector{
		chunks:   append([]domain.Chunk(nil), chunks...),
		vectors:  make([][]float64, len(chunks)),
		embedder: embedder,
		options:  options,
	}
	for i, chunk := range chunks {
		index.vectors[i] = embedder.Embed(chunk.DocumentTitle + " " + chunk.Content)
	}
	return index
}

func (v *Vector) Name() string { return "vector" }

func (v *Vector) Search(_ context.Context, request domain.QueryRequest) ([]domain.RetrievedChunk, error) {
	queryVector := v.embedder.Embed(request.Query)
	results := make([]domain.RetrievedChunk, 0, len(v.chunks))
	for index, chunk := range v.chunks {
		if !allowed(chunk, request, v.options) {
			continue
		}
		score := textutil.Cosine(queryVector, v.vectors[index])
		if score > 0 {
			results = append(results, domain.RetrievedChunk{Chunk: chunk, Score: score, Stage: v.Name()})
		}
	}
	return rank(results, request.TopK), nil
}
