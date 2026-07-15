package app

import (
	"context"
	"fmt"

	"github.com/dingpuyu/rag-evolution-lab/internal/dataset"
	"github.com/dingpuyu/rag-evolution-lab/internal/domain"
	"github.com/dingpuyu/rag-evolution-lab/internal/ingest"
	"github.com/dingpuyu/rag-evolution-lab/internal/pipeline"
	"github.com/dingpuyu/rag-evolution-lab/internal/retrieval"
	"github.com/dingpuyu/rag-evolution-lab/internal/routing"
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
	keywordIndex := retrieval.NewBM25(chunks)
	metadataIndex := retrieval.NewBM25WithOptions(chunks, retrieval.Options{UseMetadata: true})
	keyword := pipeline.New("v0-keyword", keywordIndex)
	metadata := pipeline.New("v2-metadata", metadataIndex)
	hashIndex, err := retrieval.NewVector(ctx, chunks, retrieval.HashEmbedder{Dimensions: 512})
	if err != nil {
		return nil, err
	}
	vector := pipeline.New("v1-vector", hashIndex)
	hashMetadata := hashIndex.WithOptions(retrieval.Options{UseMetadata: true})
	hybridIndex := retrieval.NewRRF(keywordIndex, hashIndex)
	hybridMetadataIndex := retrieval.NewRRF(metadataIndex, hashMetadata)
	hybridConsensusIndex := retrieval.NewRRFWithOptions(
		retrieval.RRFOptions{MinSourceMatches: 2},
		metadataIndex,
		hashMetadata,
	)
	hybrid := pipeline.New("v3-hybrid", hybridIndex)
	hybridMetadata := pipeline.New("v3-hybrid-metadata", hybridMetadataIndex)
	hybridConsensus := pipeline.New("v3-hybrid-metadata-consensus", hybridConsensusIndex)
	router := pipeline.New("v4-router", newQueryRouter(metadataIndex, hybridMetadataIndex, hybridConsensusIndex))
	pipelines := map[string]*pipeline.Pipeline{
		keyword.Name():         keyword,
		vector.Name():          vector,
		metadata.Name():        metadata,
		hybrid.Name():          hybrid,
		hybridMetadata.Name():  hybridMetadata,
		hybridConsensus.Name(): hybridConsensus,
		router.Name():          router,
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
		ollamaMetadata := ollamaIndex.WithOptions(retrieval.Options{UseMetadata: true})
		ollamaHybridIndex := retrieval.NewRRF(keywordIndex, ollamaIndex)
		ollamaHybrid := pipeline.New("v3-ollama-hybrid", ollamaHybridIndex)
		pipelines[ollamaHybrid.Name()] = ollamaHybrid
		ollamaHybridMetadataIndex := retrieval.NewRRF(metadataIndex, ollamaMetadata)
		ollamaHybridMetadata := pipeline.New("v3-ollama-hybrid-metadata", ollamaHybridMetadataIndex)
		pipelines[ollamaHybridMetadata.Name()] = ollamaHybridMetadata
		ollamaHybridConsensusIndex := retrieval.NewRRFWithOptions(
			retrieval.RRFOptions{MinSourceMatches: 2},
			metadataIndex,
			ollamaMetadata,
		)
		ollamaHybridConsensus := pipeline.New("v3-ollama-hybrid-metadata-consensus", ollamaHybridConsensusIndex)
		pipelines[ollamaHybridConsensus.Name()] = ollamaHybridConsensus
		ollamaRouter := pipeline.New("v4-ollama-router", newQueryRouter(
			metadataIndex, ollamaHybridMetadataIndex, ollamaHybridConsensusIndex,
		))
		pipelines[ollamaRouter.Name()] = ollamaRouter
	}
	return &Runtime{
		Documents: documents,
		Chunks:    chunks,
		Pipelines: pipelines,
	}, nil
}

func newQueryRouter(exact, semantic, consensus retrieval.Retriever) *routing.Router {
	return routing.NewRouter(routing.HeuristicClassifier{}, map[routing.Intent]retrieval.Retriever{
		routing.IntentExact:            exact,
		routing.IntentSemantic:         semantic,
		routing.IntentAccessSensitive:  routing.NewTenantScopeGate(consensus),
		routing.IntentUnanswerableRisk: routing.NewAnchorGate(consensus),
	}, semantic)
}

func (r *Runtime) Pipeline(name string) (*pipeline.Pipeline, error) {
	target, ok := r.Pipelines[name]
	if !ok {
		return nil, fmt.Errorf("unknown pipeline %q", name)
	}
	return target, nil
}
