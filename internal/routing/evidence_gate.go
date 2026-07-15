package routing

import (
	"context"
	"regexp"
	"strings"

	"github.com/dingpuyu/rag-evolution-lab/internal/domain"
	"github.com/dingpuyu/rag-evolution-lab/internal/retrieval"
)

var tenantReferencePattern = regexp.MustCompile(`(?i)(?:tenant|租户)\s*[-_]?([a-z0-9]+)`)

// AnchorGate requires structured identifiers from a verification query to be
// present in the evidence. It prevents a high-level semantic neighbor from
// being treated as proof for an unknown certification, code, or version.
type AnchorGate struct {
	inner retrieval.Retriever
}

func NewAnchorGate(inner retrieval.Retriever) *AnchorGate { return &AnchorGate{inner: inner} }

func (g *AnchorGate) Name() string { return "anchor-gate+" + g.inner.Name() }

func (g *AnchorGate) Search(ctx context.Context, request domain.QueryRequest) ([]domain.RetrievedChunk, error) {
	results, err := g.inner.Search(ctx, request)
	if err != nil {
		return nil, err
	}
	anchors := strongIdentifierPattern.FindAllString(strings.ToLower(request.Query), -1)
	if len(anchors) == 0 {
		return results, nil
	}
	filtered := results[:0]
	for _, result := range results {
		evidence := strings.ToLower(result.Chunk.DocumentTitle + " " + result.Chunk.Content)
		if containsAll(evidence, anchors) {
			filtered = append(filtered, result)
		}
	}
	for index := range filtered {
		filtered[index].Rank = index + 1
		filtered[index].Stage = "anchor-gate/" + filtered[index].Stage
	}
	return filtered, nil
}

func containsAll(value string, candidates []string) bool {
	for _, candidate := range candidates {
		if !strings.Contains(value, candidate) {
			return false
		}
	}
	return true
}

// TenantScopeGate rejects an explicit tenant reference that conflicts with the
// authenticated request context before any candidate retrieval occurs.
type TenantScopeGate struct {
	inner retrieval.Retriever
}

func NewTenantScopeGate(inner retrieval.Retriever) *TenantScopeGate {
	return &TenantScopeGate{inner: inner}
}

func (g *TenantScopeGate) Name() string { return "tenant-scope-gate+" + g.inner.Name() }

func (g *TenantScopeGate) Search(ctx context.Context, request domain.QueryRequest) ([]domain.RetrievedChunk, error) {
	match := tenantReferencePattern.FindStringSubmatch(request.Query)
	if len(match) == 2 && normalizeTenant(match[1]) != normalizeTenant(request.TenantID) {
		return []domain.RetrievedChunk{}, nil
	}
	results, err := g.inner.Search(ctx, request)
	if err != nil {
		return nil, err
	}
	for index := range results {
		results[index].Stage = "tenant-scope-gate/" + results[index].Stage
	}
	return results, nil
}

func normalizeTenant(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "tenant")
	return strings.TrimLeft(value, "-_")
}
