package app

import (
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

func Build(corpusRoot string) (*Runtime, error) {
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
	vector := pipeline.New("v1-vector", retrieval.NewVector(chunks, retrieval.HashEmbedder{Dimensions: 512}))
	return &Runtime{
		Documents: documents,
		Chunks:    chunks,
		Pipelines: map[string]*pipeline.Pipeline{
			keyword.Name(): keyword,
			vector.Name():  vector,
		},
	}, nil
}

func (r *Runtime) Pipeline(name string) (*pipeline.Pipeline, error) {
	target, ok := r.Pipelines[name]
	if !ok {
		return nil, fmt.Errorf("unknown pipeline %q", name)
	}
	return target, nil
}
