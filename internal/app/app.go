package app

import (
	"context"
	"fmt"

	"github.com/dingpuyu/rag-evolution-lab/internal/dataset"
	"github.com/dingpuyu/rag-evolution-lab/internal/domain"
	"github.com/dingpuyu/rag-evolution-lab/internal/ingest"
	"github.com/dingpuyu/rag-evolution-lab/internal/milvus"
	"github.com/dingpuyu/rag-evolution-lab/internal/pipeline"
	"github.com/dingpuyu/rag-evolution-lab/internal/rerank"
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
	MilvusURL         string
	MilvusToken       string
	MilvusCollection  string
	MilvusSearchEF    int
	SkipOllamaMemory  bool
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
	rerankPipeline := pipeline.NewWithOptions("v5-rerank", newQueryRouter(metadataIndex, hybridMetadataIndex, hybridConsensusIndex), pipeline.Options{
		Reranker:           rerank.Heuristic{},
		CandidateTopN:      20,
		ContextMaxChunks:   6,
		ContextTokenBudget: 4000,
	})
	pipelines := map[string]*pipeline.Pipeline{
		keyword.Name():         keyword,
		vector.Name():          vector,
		metadata.Name():        metadata,
		hybrid.Name():          hybrid,
		hybridMetadata.Name():  hybridMetadata,
		hybridConsensus.Name(): hybridConsensus,
		router.Name():          router,
		rerankPipeline.Name():  rerankPipeline,
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
		if !options.SkipOllamaMemory {
			if err := registerOllamaMemoryPipelines(ctx, chunks, keywordIndex, metadataIndex, embedder, pipelines); err != nil {
				return nil, err
			}
		}
		if options.MilvusURL != "" {
			if err := registerMilvusPipelines(keywordIndex, metadataIndex, embedder, options, pipelines); err != nil {
				return nil, err
			}
		}
	} else if options.MilvusURL != "" {
		return nil, fmt.Errorf("Milvus retrieval requires OllamaModel for query embeddings")
	}
	return &Runtime{
		Documents: documents,
		Chunks:    chunks,
		Pipelines: pipelines,
	}, nil
}

func registerOllamaMemoryPipelines(ctx context.Context, chunks []domain.Chunk, keywordIndex, metadataIndex retrieval.Retriever, embedder retrieval.Embedder, pipelines map[string]*pipeline.Pipeline) error {
	ollamaIndex, err := retrieval.NewVector(ctx, chunks, embedder)
	if err != nil {
		return err
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
	ollamaHybridConsensusIndex := retrieval.NewRRFWithOptions(retrieval.RRFOptions{MinSourceMatches: 2}, metadataIndex, ollamaMetadata)
	ollamaHybridConsensus := pipeline.New("v3-ollama-hybrid-metadata-consensus", ollamaHybridConsensusIndex)
	pipelines[ollamaHybridConsensus.Name()] = ollamaHybridConsensus
	ollamaRouter := pipeline.New("v4-ollama-router", newQueryRouter(metadataIndex, ollamaHybridMetadataIndex, ollamaHybridConsensusIndex))
	pipelines[ollamaRouter.Name()] = ollamaRouter
	ollamaRerank := pipeline.NewWithOptions("v5-ollama-rerank", newQueryRouter(metadataIndex, ollamaHybridMetadataIndex, ollamaHybridConsensusIndex), productionPipelineOptions())
	pipelines[ollamaRerank.Name()] = ollamaRerank
	return nil
}

func registerMilvusPipelines(keywordIndex, metadataIndex retrieval.Retriever, embedder retrieval.Embedder, options Options, pipelines map[string]*pipeline.Pipeline) error {
	client := milvus.NewClient(milvus.Config{BaseURL: options.MilvusURL, Token: options.MilvusToken})
	vectorIndex, err := milvus.NewRetriever(client, embedder, options.MilvusCollection, milvus.RetrieverOptions{SearchEF: options.MilvusSearchEF})
	if err != nil {
		return err
	}
	metadataVectorIndex, err := milvus.NewRetriever(client, embedder, options.MilvusCollection, milvus.RetrieverOptions{UseMetadata: true, SearchEF: options.MilvusSearchEF})
	if err != nil {
		return err
	}
	v1 := pipeline.New("v1-milvus", vectorIndex)
	pipelines[v1.Name()] = v1
	hybridIndex := retrieval.NewRRF(keywordIndex, vectorIndex)
	v3 := pipeline.New("v3-milvus-hybrid", hybridIndex)
	pipelines[v3.Name()] = v3
	hybridMetadataIndex := retrieval.NewRRF(metadataIndex, metadataVectorIndex)
	v3Metadata := pipeline.New("v3-milvus-hybrid-metadata", hybridMetadataIndex)
	pipelines[v3Metadata.Name()] = v3Metadata
	hybridConsensusIndex := retrieval.NewRRFWithOptions(retrieval.RRFOptions{MinSourceMatches: 2}, metadataIndex, metadataVectorIndex)
	v3Consensus := pipeline.New("v3-milvus-hybrid-metadata-consensus", hybridConsensusIndex)
	pipelines[v3Consensus.Name()] = v3Consensus
	v4 := pipeline.New("v4-milvus-router", newQueryRouter(metadataIndex, hybridMetadataIndex, hybridConsensusIndex))
	pipelines[v4.Name()] = v4
	v5 := pipeline.NewWithOptions("v5-milvus-rerank", newQueryRouter(metadataIndex, hybridMetadataIndex, hybridConsensusIndex), productionPipelineOptions())
	pipelines[v5.Name()] = v5
	return nil
}

func productionPipelineOptions() pipeline.Options {
	return pipeline.Options{Reranker: rerank.Heuristic{}, CandidateTopN: 20, ContextMaxChunks: 6, ContextTokenBudget: 4000}
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
