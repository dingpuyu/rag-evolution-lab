package contextbuilder

import (
	"strings"
	"unicode"

	"github.com/dingpuyu/rag-evolution-lab/internal/domain"
)

type Packer struct {
	MaxChunks   int
	TokenBudget int
}

type Stats struct {
	CandidateChunks int
	SelectedChunks  int
	EstimatedTokens int
	Truncated       bool
}

// Pack selects evidence independently from retrieval evaluation. Token counts
// are estimates for deterministic budgeting; provider-reported usage remains
// the source of truth for billing.
func (p Packer) Pack(candidates []domain.RetrievedChunk) ([]domain.RetrievedChunk, Stats) {
	maxChunks := p.MaxChunks
	if maxChunks <= 0 {
		maxChunks = 6
	}
	budget := p.TokenBudget
	if budget <= 0 {
		budget = 4000
	}
	stats := Stats{CandidateChunks: len(candidates)}
	selected := make([]domain.RetrievedChunk, 0, min(maxChunks, len(candidates)))
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if len(selected) >= maxChunks || stats.EstimatedTokens >= budget {
			stats.Truncated = true
			break
		}
		if _, ok := seen[candidate.Chunk.ID]; ok {
			continue
		}
		seen[candidate.Chunk.ID] = struct{}{}
		cost := EstimateTokens(candidate.Chunk.DocumentTitle + "\n" + candidate.Chunk.Content)
		remaining := budget - stats.EstimatedTokens
		if cost > remaining {
			titleCost := EstimateTokens(candidate.Chunk.DocumentTitle + "\n")
			contentBudget := remaining - titleCost
			if len(selected) == 0 && contentBudget > 0 {
				candidate.Chunk.Content = truncateToEstimatedTokens(candidate.Chunk.Content, contentBudget)
				selected = append(selected, candidate)
				stats.EstimatedTokens += EstimateTokens(candidate.Chunk.DocumentTitle + "\n" + candidate.Chunk.Content)
			}
			stats.Truncated = true
			break
		}
		selected = append(selected, candidate)
		stats.EstimatedTokens += cost
	}
	stats.SelectedChunks = len(selected)
	return selected, stats
}

// EstimateTokens uses a conservative mixed Chinese/ASCII approximation:
// one CJK rune per token and four non-space ASCII characters per token.
func EstimateTokens(value string) int {
	cjk, other := 0, 0
	for _, current := range value {
		switch {
		case unicode.Is(unicode.Han, current):
			cjk++
		case !unicode.IsSpace(current):
			other++
		}
	}
	return cjk + (other+3)/4
}

func truncateToEstimatedTokens(value string, budget int) string {
	if budget <= 0 {
		return ""
	}
	var builder strings.Builder
	cjk, other := 0, 0
	for _, current := range value {
		nextCJK, nextOther := cjk, other
		switch {
		case unicode.Is(unicode.Han, current):
			nextCJK++
		case !unicode.IsSpace(current):
			nextOther++
		}
		if nextCJK+(nextOther+3)/4 > budget {
			break
		}
		builder.WriteRune(current)
		cjk, other = nextCJK, nextOther
	}
	return strings.TrimSpace(builder.String())
}
