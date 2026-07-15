package routing

import (
	"context"
	"errors"
	"testing"

	"github.com/dingpuyu/rag-evolution-lab/internal/domain"
	"github.com/dingpuyu/rag-evolution-lab/internal/retrieval"
)

type fixedClassifier struct{ decision Decision }

func (f fixedClassifier) Classify(domain.QueryRequest) Decision { return f.decision }

type stubRetriever struct {
	name    string
	results []domain.RetrievedChunk
	err     error
}

func (s stubRetriever) Name() string { return s.name }

func (s stubRetriever) Search(context.Context, domain.QueryRequest) ([]domain.RetrievedChunk, error) {
	return append([]domain.RetrievedChunk(nil), s.results...), s.err
}

func TestRouterSelectsStrategyAndAnnotatesStage(t *testing.T) {
	target := stubRetriever{name: "keyword", results: []domain.RetrievedChunk{{
		Chunk: domain.Chunk{ID: "doc#1"}, Stage: "keyword",
	}}}
	router := NewRouter(
		fixedClassifier{decision: Decision{Intent: IntentExact, Reason: "identifier"}},
		map[Intent]retrieval.Retriever{IntentExact: target},
		nil,
	)
	results, err := router.Search(context.Background(), domain.QueryRequest{Query: "E1027"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Stage != "route:exact/keyword" {
		t.Fatalf("unexpected routed results: %#v", results)
	}
	attributes := router.TraceAttributes(domain.QueryRequest{Query: "E1027"})
	if attributes["route"] != "exact" || attributes["strategy"] != "keyword" {
		t.Fatalf("unexpected trace attributes: %#v", attributes)
	}
}

func TestRouterUsesFallbackAndWrapsErrors(t *testing.T) {
	router := NewRouter(
		fixedClassifier{decision: Decision{Intent: IntentSemantic, Reason: "default"}},
		nil,
		stubRetriever{name: "fallback", err: errors.New("offline")},
	)
	_, err := router.Search(context.Background(), domain.QueryRequest{Query: "query"})
	if err == nil || err.Error() != "route semantic to fallback: offline" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAnchorGateRejectsEvidenceMissingQueryIdentifier(t *testing.T) {
	inner := stubRetriever{name: "consensus", results: []domain.RetrievedChunk{
		{Chunk: domain.Chunk{ID: "noise", Content: "支持常见数据主权认证"}, Stage: "hybrid-rrf"},
		{Chunk: domain.Chunk{ID: "proof", Content: "已通过 ISO-X9 认证"}, Stage: "hybrid-rrf"},
	}}
	results, err := NewAnchorGate(inner).Search(context.Background(), domain.QueryRequest{Query: "是否通过 ISO-X9 认证？"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Chunk.ID != "proof" || results[0].Stage != "anchor-gate/hybrid-rrf" {
		t.Fatalf("unexpected gated results: %#v", results)
	}
}

func TestAnchorGateLeavesQueriesWithoutIdentifiersUnchanged(t *testing.T) {
	inner := stubRetriever{name: "consensus", results: []domain.RetrievedChunk{{Chunk: domain.Chunk{ID: "doc"}}}}
	results, err := NewAnchorGate(inner).Search(context.Background(), domain.QueryRequest{Query: "是否支持跨区域备份？"})
	if err != nil || len(results) != 1 {
		t.Fatalf("expected passthrough, results=%#v err=%v", results, err)
	}
}

func TestTenantScopeGateRejectsConflictingTenantReference(t *testing.T) {
	inner := stubRetriever{name: "consensus", results: []domain.RetrievedChunk{{Chunk: domain.Chunk{ID: "public-noise"}}}}
	gate := NewTenantScopeGate(inner)
	for _, query := range []string{"Tenant A 的专用队列", "租户 A 的专属队列"} {
		results, err := gate.Search(context.Background(), domain.QueryRequest{Query: query, TenantID: "tenant_b"})
		if err != nil || len(results) != 0 {
			t.Fatalf("conflicting tenant should fail closed, query=%q results=%#v err=%v", query, results, err)
		}
	}
}

func TestTenantScopeGateAllowsMatchingTenantReference(t *testing.T) {
	inner := stubRetriever{name: "consensus", results: []domain.RetrievedChunk{{Chunk: domain.Chunk{ID: "private"}, Stage: "hybrid-rrf"}}}
	results, err := NewTenantScopeGate(inner).Search(context.Background(), domain.QueryRequest{
		Query: "租户 A 的专属队列", TenantID: "tenant_a",
	})
	if err != nil || len(results) != 1 || results[0].Stage != "tenant-scope-gate/hybrid-rrf" {
		t.Fatalf("matching tenant should pass, results=%#v err=%v", results, err)
	}
}
