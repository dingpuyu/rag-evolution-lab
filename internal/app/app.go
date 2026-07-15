package app

import (
	"context"
	"fmt"

	"github.com/dingpuyu/rag-evolution-lab/internal/dataset"
	"github.com/dingpuyu/rag-evolution-lab/internal/domain"
	"github.com/dingpuyu/rag-evolution-lab/internal/ingest"
	"github.com/dingpuyu/rag-evolution-lab/internal/pipeline"
	"github.com/dingpuyu/rag-evolution-lab/internal/retrieval"
)

type Runtime struct {
	Documents []domain.Document
	Chunks    []domain.Chunk
	Pipelines map[string]*pipeline.Pipeline
}

type Options struct {
	OllamaModel       string
	OllamaURL         string
	QueryInstruction  string
	EmbeddingCacheDir string
}

func Build(corpusRoot string) (*Runtime, error) {
	return BuildWithOptions(context.Background(), corpusRoot, Options{})
}

func BuildWithOptions(ctx context.Context, corpusRoot string, options Options) (*Runtime, error) {
	documents, err := dataset.LoadCorpus(corpusRoot)
	if err != nil {
		return nil, err
	}
	chunker := ingest.Chunker{MaxRunes: 700}
	var chunks []domain.Chunk
	for _, document := range documents {
		chunks = append(chunks, chunker.Chunk(document)...)
	}
	keyword := pipeline.New("v0-keyword", retrieval.NewBM25(chunks))
	metadata := pipeline.New("v2-metadata", retrieval.NewBM25WithOptions(chunks, retrieval.Options{UseMetadata: true}))
	hashIndex, err := retrieval.NewVector(ctx, chunks, retrieval.HashEmbedder{Dimensions: 512})
	if err != nil {
		return nil, err
	}
	vector := pipeline.New("v1-vector", hashIndex)
	hybrid := pipeline.New("v3-hybrid", retrieval.NewRRF(retrieval.NewBM25(chunks), hashIndex))
	hybridMetadata := pipeline.New("v3-hybrid-metadata", retrieval.NewRRF(
		retrieval.NewBM25WithOptions(chunks, retrieval.Options{UseMetadata: true}),
		hashIndex.WithOptions(retrieval.Options{UseMetadata: true}),
	))
	hybridConsensus := pipeline.New("v3-hybrid-metadata-consensus", retrieval.NewRRFWithOptions(
		retrieval.RRFOptions{MinSourceMatches: 2},
		retrieval.NewBM25WithOptions(chunks, retrieval.Options{UseMetadata: true}),
		hashIndex.WithOptions(retrieval.Options{UseMetadata: true}),
	))
	pipelines := map[string]*pipeline.Pipeline{
		keyword.Name():         keyword,
		vector.Name():          vector,
		metadata.Name():        metadata,
		hybrid.Name():          hybrid,
		hybridMetadata.Name():  hybridMetadata,
		hybridConsensus.Name(): hybridConsensus,
	}
	if options.OllamaModel != "" {
		var embedder retrieval.Embedder = retrieval.OllamaEmbedder{
			BaseURL:          options.OllamaURL,
			Model:            options.OllamaModel,
			QueryInstruction: options.QueryInstruction,
		}
		if options.EmbeddingCacheDir != "" {
			embedder = retrieval.CachedEmbedder{Inner: embedder, Dir: options.EmbeddingCacheDir}
		}
		ollamaIndex, err := retrieval.NewVector(ctx, chunks, embedder)
		if err != nil {
			return nil, err
		}
		ollama := pipeline.New("v1-ollama", ollamaIndex)
		pipelines[ollama.Name()] = ollama
		ollamaHybrid := pipeline.New("v3-ollama-hybrid", retrieval.NewRRF(retrieval.NewBM25(chunks), ollamaIndex))
		pipelines[ollamaHybrid.Name()] = ollamaHybrid
		ollamaHybridMetadata := pipeline.New("v3-ollama-hybrid-metadata", retrieval.NewRRF(
			retrieval.NewBM25WithOptions(chunks, retrieval.Options{UseMetadata: true}),
			ollamaIndex.WithOptions(retrieval.Options{UseMetadata: true}),
		))
		pipelines[ollamaHybridMetadata.Name()] = ollamaHybridMetadata
		ollamaHybridConsensus := pipeline.New("v3-ollama-hybrid-metadata-consensus", retrieval.NewRRFWithOptions(
			retrieval.RRFOptions{MinSourceMatches: 2},
			retrieval.NewBM25WithOptions(chunks, retrieval.Options{UseMetadata: true}),
			ollamaIndex.WithOptions(retrieval.Options{UseMetadata: true}),
		))
		pipelines[ollamaHybridConsensus.Name()] = ollamaHybridConsensus
	}
	return &Runtime{
		Documents: documents,
		Chunks:    chunks,
		Pipelines: pipelines,
	}, nil
}

func (r *Runtime) Pipeline(name string) (*pipeline.Pipeline, error) {
	target, ok := r.Pipelines[name]
	if !ok {
		return nil, fmt.Errorf("unknown pipeline %q", name)
	}
	return target, nil
}
