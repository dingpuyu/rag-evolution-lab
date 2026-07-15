package retrieval

import (
	"context"
	"math"

	"github.com/dingpuyu/rag-evolution-lab/internal/domain"
	"github.com/dingpuyu/rag-evolution-lab/internal/textutil"
)

type BM25 struct {
	chunks    []domain.Chunk
	termFreqs []map[string]float64
	docFreq   map[string]int
	avgLength float64
	k1        float64
	b         float64
	options   Options
}

func NewBM25(chunks []domain.Chunk) *BM25 {
	return NewBM25WithOptions(chunks, Options{})
}

func NewBM25WithOptions(chunks []domain.Chunk, options Options) *BM25 {
	index := &BM25{
		chunks:    append([]domain.Chunk(nil), chunks...),
		termFreqs: make([]map[string]float64, len(chunks)),
		docFreq:   make(map[string]int),
		k1:        1.5,
		b:         0.75,
		options:   options,
	}
	var totalLength int
	for i, chunk := range chunks {
		tokens := textutil.Tokens(chunk.DocumentTitle + " " + chunk.Content)
		totalLength += len(tokens)
		frequencies := textutil.TermFrequency(tokens)
		index.termFreqs[i] = frequencies
		for token := range frequencies {
			index.docFreq[token]++
		}
	}
	if len(chunks) > 0 {
		index.avgLength = float64(totalLength) / float64(len(chunks))
	}
	return index
}

func (b *BM25) Name() string {
	if b.options.UseMetadata {
		return "keyword+metadata"
	}
	return "keyword"
}

func (b *BM25) Search(_ context.Context, request domain.QueryRequest) ([]domain.RetrievedChunk, error) {
	queryTerms := textutil.TermFrequency(textutil.Tokens(request.Query))
	results := make([]domain.RetrievedChunk, 0, len(b.chunks))
	for index, chunk := range b.chunks {
		if !allowed(chunk, request, b.options) {
			continue
		}
		frequency := b.termFreqs[index]
		documentLength := 0.0
		for _, count := range frequency {
			documentLength += count
		}
		var score float64
		for term, queryCount := range queryTerms {
			tf := frequency[term]
			if tf == 0 {
				continue
			}
			df := float64(b.docFreq[term])
			idf := math.Log(1 + (float64(len(b.chunks))-df+0.5)/(df+0.5))
			denominator := tf + b.k1*(1-b.b+b.b*documentLength/max(b.avgLength, 1))
			score += queryCount * idf * (tf * (b.k1 + 1) / denominator)
		}
		if score > 0 {
			results = append(results, domain.RetrievedChunk{Chunk: chunk, Score: score, Stage: b.Name()})
		}
	}
	return rank(results, request.TopK), nil
}
