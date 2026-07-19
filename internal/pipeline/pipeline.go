package pipeline

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/dingpuyu/rag-evolution-lab/internal/contextbuilder"
	"github.com/dingpuyu/rag-evolution-lab/internal/domain"
	"github.com/dingpuyu/rag-evolution-lab/internal/rerank"
	"github.com/dingpuyu/rag-evolution-lab/internal/retrieval"
	"github.com/dingpuyu/rag-evolution-lab/internal/trace"
	"github.com/dingpuyu/rag-evolution-lab/internal/verification"
)

type Pipeline struct {
	name           string
	retriever      retrieval.Retriever
	reranker       rerank.Reranker
	candidateTopN  int
	contextBuilder contextbuilder.Packer
}

func New(name string, retriever retrieval.Retriever) *Pipeline {
	return NewWithOptions(name, retriever, Options{})
}

type Options struct {
	Reranker           rerank.Reranker
	CandidateTopN      int
	ContextMaxChunks   int
	ContextTokenBudget int
}

func NewWithOptions(name string, retriever retrieval.Retriever, options Options) *Pipeline {
	return &Pipeline{
		name:          name,
		retriever:     retriever,
		reranker:      options.Reranker,
		candidateTopN: options.CandidateTopN,
		contextBuilder: contextbuilder.Packer{
			MaxChunks:   options.ContextMaxChunks,
			TokenBudget: options.ContextTokenBudget,
		},
	}
}

func (p *Pipeline) Name() string { return p.name }

func (p *Pipeline) Query(ctx context.Context, request domain.QueryRequest) (*domain.QueryResponse, error) {
	if strings.TrimSpace(request.Query) == "" {
		return nil, fmt.Errorf("query must not be empty")
	}
	if request.TopK <= 0 {
		request.TopK = 5
	}
	recorder := trace.New(p.name, request.Query)

	candidateRequest := request
	if p.reranker != nil && p.candidateTopN > candidateRequest.TopK {
		candidateRequest.TopK = p.candidateTopN
	}
	started := time.Now()
	results, err := p.retriever.Search(ctx, candidateRequest)
	attributes := map[string]any{
		"retriever":       p.retriever.Name(),
		"result_count":    len(results),
		"requested_top_k": request.TopK,
		"candidate_top_n": candidateRequest.TopK,
	}
	if provider, ok := p.retriever.(retrieval.TraceAttributesProvider); ok {
		for key, value := range provider.TraceAttributes(request) {
			attributes[key] = value
		}
	}
	recorder.Add("retrieval", started, attributes)
	if err != nil {
		return nil, err
	}
	if p.reranker != nil {
		started = time.Now()
		results, err = p.reranker.Rerank(ctx, request, results)
		recorder.Add("results_reranked", started, map[string]any{
			"reranker":        p.reranker.Name(),
			"candidate_count": len(results),
		})
		if err != nil {
			return nil, fmt.Errorf("rerank results: %w", err)
		}
	}
	if len(results) > request.TopK {
		results = results[:request.TopK]
	}
	for index := range results {
		results[index].Rank = index + 1
	}
	started = time.Now()
	selectedContext, contextStats := p.contextBuilder.Pack(results)
	recorder.Add("context_packed", started, map[string]any{
		"candidate_chunks": contextStats.CandidateChunks,
		"selected_chunks":  contextStats.SelectedChunks,
		"estimated_tokens": contextStats.EstimatedTokens,
		"truncated":        contextStats.Truncated,
	})

	response := &domain.QueryResponse{
		Answerable: len(results) > 0,
		Retrieval:  results,
		Context:    selectedContext,
	}
	started = time.Now()
	if len(selectedContext) == 0 {
		response.Answer = "知识库中没有找到足够证据。"
		response.Answerable = false
	} else {
		best := selectedContext[0].Chunk
		response.Answer = best.Content
		response.Citations = []domain.Citation{{
			ChunkID:    best.ID,
			DocumentID: best.DocumentID,
			Document:   best.DocumentTitle,
			Excerpt:    best.Content,
		}}
	}
	recorder.Add("extractive_generation", started, map[string]any{
		"citation_count": len(response.Citations),
	})
	started = time.Now()
	if err := verification.VerifyCitations(response.Citations, selectedContext); err != nil {
		return nil, fmt.Errorf("verify citations: %w", err)
	}
	recorder.Add("citations_verified", started, map[string]any{
		"citation_count": len(response.Citations),
		"valid":          true,
	})
	response.Trace = recorder.Finish()
	return response, nil
}
