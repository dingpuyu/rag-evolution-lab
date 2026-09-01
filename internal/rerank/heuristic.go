package rerank

import (
	"context"
	"sort"
	"strings"
	"unicode"

	"github.com/dingpuyu/rag-evolution-lab/internal/domain"
	"github.com/dingpuyu/rag-evolution-lab/internal/textutil"
)

// Reranker separates broad candidate recall from final evidence ordering.
// Production implementations can replace Heuristic with a cross-encoder or
// hosted rerank model without changing the pipeline contract.
type Reranker interface {
	Name() string
	Rerank(ctx context.Context, request domain.QueryRequest, candidates []domain.RetrievedChunk) ([]domain.RetrievedChunk, error)
}

// Heuristic is deterministic and intentionally lightweight. It is not meant
// to imitate a learned cross-encoder; it provides a reproducible baseline that
// makes rerank regressions testable in CI without an external model.
type Heuristic struct{}

func (Heuristic) Name() string { return "heuristic-evidence-reranker" }

func (Heuristic) Rerank(_ context.Context, request domain.QueryRequest, candidates []domain.RetrievedChunk) ([]domain.RetrievedChunk, error) {
	queryTokens := unique(textutil.Tokens(textutil.ExpandSemantic(request.Query)))
	queryIdentifiers := identifiers(queryTokens)
	results := append([]domain.RetrievedChunk(nil), candidates...)
	for index := range results {
		candidateTokens := tokenSet(textutil.Tokens(textutil.ExpandSemantic(
			results[index].Chunk.DocumentTitle + " " + strings.Join(results[index].Chunk.HeadingPath, " ") + " " + results[index].Chunk.Content,
		)))
		titleTokens := tokenSet(textutil.Tokens(textutil.ExpandSemantic(
			results[index].Chunk.DocumentTitle + " " + strings.Join(results[index].Chunk.HeadingPath, " "),
		)))
		coverage := overlap(queryTokens, candidateTokens)
		titleCoverage := overlap(queryTokens, titleTokens)
		identifierCoverage := overlap(queryIdentifiers, candidateTokens)
		originalRank := results[index].Rank
		if originalRank <= 0 {
			originalRank = index + 1
		}
		// Coverage dominates while original rank remains a stable tie-breaker.
		results[index].Score = 0.55*coverage + 0.20*titleCoverage + 0.20*identifierCoverage + 0.05/float64(originalRank)
		results[index].Stage = "rerank:" + (Heuristic{}).Name()
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			return results[i].Chunk.ID < results[j].Chunk.ID
		}
		return results[i].Score > results[j].Score
	})
	for index := range results {
		results[index].Rank = index + 1
	}
	return results, nil
}

func unique(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func tokenSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func identifiers(tokens []string) []string {
	var result []string
	for _, token := range tokens {
		hasDigit := false
		for _, value := range token {
			if unicode.IsDigit(value) {
				hasDigit = true
				break
			}
		}
		if hasDigit || (len(token) >= 3 && isASCII(token)) {
			result = append(result, token)
		}
	}
	return result
}

func isASCII(value string) bool {
	for _, current := range value {
		if current > unicode.MaxASCII {
			return false
		}
	}
	return true
}

func overlap(values []string, candidate map[string]struct{}) float64 {
	if len(values) == 0 {
		return 0
	}
	hits := 0
	for _, value := range values {
		if _, ok := candidate[value]; ok {
			hits++
		}
	}
	return float64(hits) / float64(len(values))
}
