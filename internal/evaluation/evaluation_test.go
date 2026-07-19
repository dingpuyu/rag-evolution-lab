package evaluation

import (
	"testing"

	"github.com/dingpuyu/rag-evolution-lab/internal/domain"
)

func TestScoreCaseComputesReciprocalRankAndRecall(t *testing.T) {
	golden := domain.GoldenCase{
		ID: "case",
		Expected: domain.GoldenExpected{
			RelevantDocumentIDs: []string{"a", "b"},
		},
	}
	results := []domain.RetrievedChunk{
		{Chunk: domain.Chunk{DocumentID: "x"}},
		{Chunk: domain.Chunk{DocumentID: "a"}},
		{Chunk: domain.Chunk{DocumentID: "b"}},
	}
	score := scoreCase(golden, results, 3)
	if !score.Hit || score.ReciprocalRank != 0.5 || score.DocumentRecall != 1 || score.Precision != 2.0/3.0 {
		t.Fatalf("unexpected score: %#v", score)
	}
	if score.NDCG <= 0 || score.NDCG >= 1 {
		t.Fatalf("expected discounted ranking below ideal, got %#v", score)
	}
}

func TestScoreCaseTreatsEmptyExpectedAsSuccessfulOnlyWithNoResults(t *testing.T) {
	golden := domain.GoldenCase{Expected: domain.GoldenExpected{RelevantDocumentIDs: nil}}
	empty := scoreCase(golden, nil, 5)
	if !empty.Hit || empty.DocumentRecall != 1 || empty.ReciprocalRank != 1 {
		t.Fatalf("empty retrieval should satisfy unanswerable case: %#v", empty)
	}
	unexpected := scoreCase(golden, []domain.RetrievedChunk{{Chunk: domain.Chunk{DocumentID: "noise"}}}, 5)
	if unexpected.Hit {
		t.Fatalf("unanswerable case should fail with retrieved noise: %#v", unexpected)
	}
}

func TestScoreCaseCountsDuplicateDocumentOnce(t *testing.T) {
	golden := domain.GoldenCase{Expected: domain.GoldenExpected{RelevantDocumentIDs: []string{"a"}}}
	results := []domain.RetrievedChunk{
		{Chunk: domain.Chunk{DocumentID: "a"}},
		{Chunk: domain.Chunk{DocumentID: "a"}},
	}
	score := scoreCase(golden, results, 2)
	if score.Precision != 0.5 || score.DocumentRecall != 1 || score.NDCG != 1 {
		t.Fatalf("duplicate document inflated metrics: %#v", score)
	}
}

func TestPercentileUsesNearestRank(t *testing.T) {
	values := []float64{5, 1, 4, 2, 3}
	if got := percentile(values, 0.50); got != 3 {
		t.Fatalf("unexpected p50: %v", got)
	}
	if got := percentile(values, 0.95); got != 5 {
		t.Fatalf("unexpected p95: %v", got)
	}
}

func TestIsAuthorizedRejectsCrossTenantChunk(t *testing.T) {
	chunk := domain.Chunk{
		Visibility: "tenant", AllowedTenants: []string{"tenant_a"}, AllowedRoles: []string{"admin"},
	}
	if isAuthorized(chunk, domain.GoldenContext{TenantID: "tenant_b", UserRole: "admin"}) {
		t.Fatal("cross-tenant chunk must be unauthorized")
	}
}

func TestIsMetadataCompatibleChecksProductVersionAndLifecycle(t *testing.T) {
	product := "identity"
	version := "2.3"
	matching := domain.Chunk{Product: "identity", Version: "2.3", Status: "active"}
	if !isMetadataCompatible(matching, domain.GoldenContext{Product: &product, Version: &version}) {
		t.Fatal("matching product and version should be compatible")
	}
	wrongVersion := domain.Chunk{Product: "identity", Version: "2.1", Status: "deprecated"}
	if isMetadataCompatible(wrongVersion, domain.GoldenContext{Product: &product, Version: &version}) {
		t.Fatal("wrong version should be a violation")
	}
	if isMetadataCompatible(wrongVersion, domain.GoldenContext{Product: &product}) {
		t.Fatal("deprecated chunk should be a violation when no version is requested")
	}
	if !isMetadataCompatible(wrongVersion, domain.GoldenContext{Product: &product, Version: stringPointer("2.1")}) {
		t.Fatal("explicit deprecated version should remain compatible")
	}
}

func TestRouteFromTraceReadsRoutingDecision(t *testing.T) {
	value := domain.QueryTrace{Events: []domain.TraceEvent{{
		Name:       "retrieval",
		Attributes: map[string]any{"route": "access_sensitive", "strategy": "hybrid-rrf"},
	}}}
	if got := routeFromTrace(value); got != "access_sensitive" {
		t.Fatalf("unexpected route: %q", got)
	}
}

func stringPointer(value string) *string { return &value }
