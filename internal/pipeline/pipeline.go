package pipeline

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/dingpuyu/rag-evolution-lab/internal/answerability"
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
	evidenceGate   answerability.Gate
	diversifyDocs  bool
}

func New(name string, retriever retrieval.Retriever) *Pipeline {
	return NewWithOptions(name, retriever, Options{})
}

type Options struct {
	Reranker           rerank.Reranker
	CandidateTopN      int
	ContextMaxChunks   int
	ContextTokenBudget int
	EvidenceGate       answerability.Gate
	DiversifyDocuments bool
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
		evidenceGate:  options.EvidenceGate,
		diversifyDocs: options.DiversifyDocuments,
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
	stageCounts := make(map[string]int)
	degraded := false
	for _, result := range results {
		stageCounts[result.Stage]++
		if strings.HasSuffix(result.Stage, "-partial") {
			degraded = true
		}
	}
	attributes := map[string]any{
		"retriever":       p.retriever.Name(),
		"result_count":    len(results),
		"requested_top_k": request.TopK,
		"candidate_top_n": candidateRequest.TopK,
		"degraded":        degraded,
		"result_stages":   stageCounts,
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
	if p.diversifyDocs {
		started = time.Now()
		before := append([]domain.RetrievedChunk(nil), results...)
		results = diversifyByDocument(results)
		uniqueDocuments := make(map[string]struct{}, len(results))
		for _, result := range results {
			uniqueDocuments[result.Chunk.DocumentID] = struct{}{}
		}
		moved := false
		for index := range results {
			if index >= len(before) || results[index].Chunk.ID != before[index].Chunk.ID {
				moved = true
				break
			}
		}
		recorder.Add("document_diversified", started, map[string]any{
			"candidate_chunks": len(results),
			"unique_documents": len(uniqueDocuments),
			"reordered":        moved,
		})
	}
	refusalReason := ""
	if p.evidenceGate != nil {
		started = time.Now()
		decision := p.evidenceGate.Assess(request, results)
		recorder.Add("answerability_gate", started, map[string]any{
			"answerable": decision.Answerable,
			"reason":     decision.Reason,
			"top_score":  decision.TopScore,
		})
		if !decision.Answerable {
			results = nil
			refusalReason = decision.Reason
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
		Answerable:    len(results) > 0,
		RefusalReason: refusalReason,
		Retrieval:     results,
		Context:       selectedContext,
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

// diversifyByDocument performs stable round-robin interleaving after rerank.
// Cross-encoders score passages independently, so several passages from one
// superficially similar document can otherwise occupy the entire Top-K and
// hide a correct document that was successfully recalled. No candidate is
// dropped: the best passage from every document is emitted first, followed by
// each document's second passage, and so on.
func diversifyByDocument(results []domain.RetrievedChunk) []domain.RetrievedChunk {
	if len(results) < 2 {
		return append([]domain.RetrievedChunk(nil), results...)
	}
	order := make([]string, 0, len(results))
	groups := make(map[string][]domain.RetrievedChunk, len(results))
	for _, result := range results {
		key := strings.TrimSpace(result.Chunk.DocumentID)
		if key == "" {
			key = "chunk:" + result.Chunk.ID
		}
		if _, exists := groups[key]; !exists {
			order = append(order, key)
		}
		groups[key] = append(groups[key], result)
	}
	diversified := make([]domain.RetrievedChunk, 0, len(results))
	for depth := 0; len(diversified) < len(results); depth++ {
		for _, key := range order {
			if depth < len(groups[key]) {
				diversified = append(diversified, groups[key][depth])
			}
		}
	}
	for index := range diversified {
		diversified[index].Rank = index + 1
	}
	return diversified
}
