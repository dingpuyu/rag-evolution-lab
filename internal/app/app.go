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
	OllamaModel string
	OllamaURL   string
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
	hashIndex, err := retrieval.NewVector(ctx, chunks, retrieval.HashEmbedder{Dimensions: 512})
	if err != nil {
		return nil, err
	}
	vector := pipeline.New("v1-vector", hashIndex)
	pipelines := map[string]*pipeline.Pipeline{
		keyword.Name(): keyword,
		vector.Name():  vector,
	}
	if options.OllamaModel != "" {
		ollamaIndex, err := retrieval.NewVector(ctx, chunks, retrieval.OllamaEmbedder{
			BaseURL: options.OllamaURL,
			Model:   options.OllamaModel,
		})
		if err != nil {
			return nil, err
		}
		ollama := pipeline.New("v1-ollama", ollamaIndex)
		pipelines[ollama.Name()] = ollama
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
