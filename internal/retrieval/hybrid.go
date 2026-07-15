package retrieval

import (
	"context"
	"fmt"
	"sync"

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
}

func NewRRF(retrievers ...Retriever) *RRF {
	return NewRRFWithOptions(RRFOptions{}, retrievers...)
}

type RRFOptions struct {
	// MinSourceMatches drops evidence returned by fewer than this many
	// retrievers. The default of one preserves union-style candidate fusion.
	MinSourceMatches int
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
	}
}

func (r *RRF) Name() string { return "hybrid-rrf" }

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
	defer cancel()
	var group sync.WaitGroup
	for index, retriever := range r.retrievers {
		group.Add(1)
		go func(index int, retriever Retriever) {
			defer group.Done()
			searches[index].chunks, searches[index].err = retriever.Search(searchContext, candidateRequest)
			if searches[index].err != nil {
				cancel()
			}
		}(index, retriever)
	}
	group.Wait()
	for index, search := range searches {
		if search.err != nil {
			return nil, fmt.Errorf("search with %s: %w", r.retrievers[index].Name(), search.err)
		}
	}

	type fusedCandidate struct {
		result  domain.RetrievedChunk
		sources int
	}
	fused := make(map[string]fusedCandidate)
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
				value.result.Stage = r.Name()
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
