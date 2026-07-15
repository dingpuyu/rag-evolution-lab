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
	score := scoreCase(golden, results)
	if !score.Hit || score.ReciprocalRank != 0.5 || score.DocumentRecall != 1 {
		t.Fatalf("unexpected score: %#v", score)
	}
}

func TestScoreCaseTreatsEmptyExpectedAsSuccessfulOnlyWithNoResults(t *testing.T) {
	golden := domain.GoldenCase{Expected: domain.GoldenExpected{RelevantDocumentIDs: nil}}
	empty := scoreCase(golden, nil)
	if !empty.Hit || empty.DocumentRecall != 1 || empty.ReciprocalRank != 1 {
		t.Fatalf("empty retrieval should satisfy unanswerable case: %#v", empty)
	}
	unexpected := scoreCase(golden, []domain.RetrievedChunk{{Chunk: domain.Chunk{DocumentID: "noise"}}})
	if unexpected.Hit {
		t.Fatalf("unanswerable case should fail with retrieved noise: %#v", unexpected)
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
