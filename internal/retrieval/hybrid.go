package retrieval

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/dingpuyu/rag-evolution-lab/internal/domain"
)

const (
	defaultRRFConstant         = 60
	defaultCandidateMultiplier = 4
	defaultMinCandidates       = 20
)

// RRF fuses independently ranked candidate lists without comparing their raw
// scores. That makes keyword and vector retrieval composable even though their
// score distributions are unrelated.
type RRF struct {
	retrievers          []Retriever
	rankConstant        int
	candidateMultiplier int
	minCandidates       int
	minSourceMatches    int
	allowPartialResults bool
	searchTimeout       time.Duration
}

func NewRRF(retrievers ...Retriever) *RRF {
	return NewRRFWithOptions(RRFOptions{}, retrievers...)
}

type RRFOptions struct {
	// MinSourceMatches drops evidence returned by fewer than this many
	// retrievers. The default of one preserves union-style candidate fusion.
	MinSourceMatches int
	// AllowPartialResults keeps healthy sources available when one retriever
	// times out or fails. It must remain disabled for consensus/security gates.
	AllowPartialResults bool
	// SearchTimeout bounds the shared retrieval budget. Zero uses the caller's
	// context without adding a deadline.
	SearchTimeout time.Duration
}

func NewRRFWithOptions(options RRFOptions, retrievers ...Retriever) *RRF {
	minSourceMatches := options.MinSourceMatches
	if minSourceMatches <= 0 {
		minSourceMatches = 1
	}
	return &RRF{
		retrievers:          append([]Retriever(nil), retrievers...),
		rankConstant:        defaultRRFConstant,
		candidateMultiplier: defaultCandidateMultiplier,
		minCandidates:       defaultMinCandidates,
		minSourceMatches:    minSourceMatches,
		allowPartialResults: options.AllowPartialResults,
		searchTimeout:       options.SearchTimeout,
	}
}

func (r *RRF) Name() string { return "hybrid-rrf" }

func (r *RRF) TraceAttributes(request domain.QueryRequest) map[string]any {
	sources := make([]string, 0, len(r.retrievers))
	attributes := map[string]any{
		"fusion":                "rrf",
		"min_source_matches":    r.minSourceMatches,
		"allow_partial_results": r.allowPartialResults,
	}
	if r.searchTimeout > 0 {
		attributes["search_timeout_ms"] = float64(r.searchTimeout.Microseconds()) / 1000
	}
	for _, target := range r.retrievers {
		sources = append(sources, target.Name())
		if provider, ok := target.(TraceAttributesProvider); ok {
			for key, value := range provider.TraceAttributes(request) {
				attributes[key] = value
			}
		}
	}
	attributes["retrieval_sources"] = sources
	return attributes
}

func (r *RRF) Search(ctx context.Context, request domain.QueryRequest) ([]domain.RetrievedChunk, error) {
	if len(r.retrievers) == 0 {
		return nil, fmt.Errorf("hybrid retrieval requires at least one retriever")
	}
	topK := request.TopK
	if topK <= 0 {
		topK = 5
	}
	candidateK := max(topK*r.candidateMultiplier, r.minCandidates)
	candidateRequest := request
	candidateRequest.TopK = candidateK

	type searchResult struct {
		chunks []domain.RetrievedChunk
		err    error
	}
	searches := make([]searchResult, len(r.retrievers))
	searchContext, cancel := context.WithCancel(ctx)
	if r.searchTimeout > 0 {
		searchContext, cancel = context.WithTimeout(searchContext, r.searchTimeout)
	}
	defer cancel()
	var group sync.WaitGroup
	for index, retriever := range r.retrievers {
		group.Add(1)
		go func(index int, retriever Retriever) {
			defer group.Done()
			searches[index].chunks, searches[index].err = retriever.Search(searchContext, candidateRequest)
			if searches[index].err != nil && !r.allowPartialResults {
				cancel()
			}
		}(index, retriever)
	}
	group.Wait()
	successfulSources := 0
	var firstError error
	partial := false
	for index, search := range searches {
		if search.err != nil {
			if firstError == nil {
				firstError = fmt.Errorf("search with %s: %w", r.retrievers[index].Name(), search.err)
			}
			if !r.allowPartialResults {
				return nil, firstError
			}
			partial = true
			continue
		}
		successfulSources++
	}
	if successfulSources == 0 {
		return nil, firstError
	}

	type fusedCandidate struct {
		result  domain.RetrievedChunk
		sources int
	}
	fused := make(map[string]fusedCandidate)
	stage := r.Name()
	if partial {
		stage += "-partial"
	}
	for _, search := range searches {
		seen := make(map[string]struct{}, len(search.chunks))
		for rankIndex, candidate := range search.chunks {
			if _, exists := seen[candidate.Chunk.ID]; exists {
				continue
			}
			seen[candidate.Chunk.ID] = struct{}{}
			value := fused[candidate.Chunk.ID]
			if value.result.Chunk.ID == "" {
				value.result.Chunk = candidate.Chunk
				value.result.Stage = stage
			}
			value.result.Score += 1 / float64(r.rankConstant+rankIndex+1)
			value.sources++
			fused[candidate.Chunk.ID] = value
		}
	}
	results := make([]domain.RetrievedChunk, 0, len(fused))
	for _, candidate := range fused {
		if candidate.sources >= r.minSourceMatches {
			results = append(results, candidate.result)
		}
	}
	return rank(results, topK), nil
}
