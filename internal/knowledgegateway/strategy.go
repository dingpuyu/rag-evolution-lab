package knowledgegateway

import (
	"context"
	"strings"

	"github.com/dingpuyu/rag-evolution-lab/internal/domain"
	"github.com/dingpuyu/rag-evolution-lab/internal/milvus"
	"github.com/dingpuyu/rag-evolution-lab/internal/rerank"
	"github.com/dingpuyu/rag-evolution-lab/internal/textutil"
)

type RewriteResult struct {
	Query    string `json:"query"`
	Applied  bool   `json:"applied"`
	Rewriter string `json:"rewriter"`
	Reason   string `json:"reason,omitempty"`
}

type QueryRewriter interface {
	Rewrite(context.Context, string) (RewriteResult, error)
}

// SemanticRewriter is deterministic and preserves the original query while
// adding known semantic aliases. It is safe for CI and can later be replaced
// with an LLM rewriter without changing the Gateway contract.
type SemanticRewriter struct{}

func (SemanticRewriter) Rewrite(_ context.Context, query string) (RewriteResult, error) {
	original := strings.TrimSpace(query)
	normalized := strings.TrimSpace(textutil.NormalizeSemantic(original))
	if normalized == "" || normalized == strings.ToLower(original) {
		return RewriteResult{Query: original, Rewriter: "semantic-alias-v1", Reason: "no_alias_change"}, nil
	}
	return RewriteResult{
		Query: original + "\n" + normalized, Applied: true, Rewriter: "semantic-alias-v1", Reason: "preserve_original_plus_aliases",
	}, nil
}

type HitReranker interface {
	Name() string
	Rerank(context.Context, string, []milvus.SearchHit) ([]milvus.SearchHit, error)
}

// HeuristicReranker adapts the already-tested deterministic reranker to the
// Milvus hit contract. It is explicit about being a baseline, not a learned
// cross encoder.
type HeuristicReranker struct{ implementation rerank.Reranker }

func NewHeuristicReranker() HeuristicReranker {
	return HeuristicReranker{implementation: rerank.Heuristic{}}
}

func (reranker HeuristicReranker) Name() string {
	if reranker.implementation == nil {
		return "heuristic-evidence-reranker"
	}
	return reranker.implementation.Name()
}

func (reranker HeuristicReranker) Rerank(ctx context.Context, query string, hits []milvus.SearchHit) ([]milvus.SearchHit, error) {
	implementation := reranker.implementation
	if implementation == nil {
		implementation = rerank.Heuristic{}
	}
	candidates := make([]domain.RetrievedChunk, 0, len(hits))
	for index, hit := range hits {
		candidates = append(candidates, domain.RetrievedChunk{
			Chunk: domain.Chunk{ID: hit.ChunkID, DocumentID: hit.DocumentID, DocumentTitle: hit.Title, Content: hit.Content},
			Score: 1 / (1 + hit.Distance), Rank: index + 1, Stage: "candidate",
		})
	}
	ranked, err := implementation.Rerank(ctx, domain.QueryRequest{Query: query, TopK: len(hits)}, candidates)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]milvus.SearchHit, len(hits))
	for _, hit := range hits {
		byID[hit.ChunkID] = hit
	}
	result := make([]milvus.SearchHit, 0, len(ranked))
	for _, candidate := range ranked {
		if hit, ok := byID[candidate.Chunk.ID]; ok {
			hit.RerankScore = candidate.Score
			hit.RerankScoreSet = true
			result = append(result, hit)
		}
	}
	return result, nil
}
