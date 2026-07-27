package evaluation

import "testing"

func TestCheckGateAcceptsImprovementAndThresholds(t *testing.T) {
	baseline := Report{HitRate: 0.80, MRR: 0.70, Recall: 0.75, NDCG: 0.72, AnswerabilityAccuracy: 0.80}
	candidate := Report{HitRate: 0.90, MRR: 0.78, Recall: 0.82, NDCG: 0.80, AnswerabilityAccuracy: 0.90, LatencyP95MS: 120}
	violations := CheckGate(baseline, candidate, GatePolicy{
		FailOnRegression: true, MinHitRate: 0.90, MinMRR: 0.77, MinRecall: 0.80,
		MinNDCG: 0.79, MinAnswerability: 0.90, MaxLatencyP95MS: 150,
	})
	if len(violations) != 0 {
		t.Fatalf("expected gate to pass, got %#v", violations)
	}
}

func TestCheckGateReportsQualityAndSecurityRegressions(t *testing.T) {
	baseline := Report{
		HitRate: 0.90, MRR: 0.80, Recall: 0.85, NDCG: 0.82, AnswerabilityAccuracy: 0.90,
		UnauthorizedRetrievals: 0, CitationViolations: 0,
	}
	candidate := Report{
		HitRate: 0.85, MRR: 0.70, Recall: 0.80, NDCG: 0.75, AnswerabilityAccuracy: 0.80,
		UnauthorizedRetrievals: 1, CitationViolations: 2, LatencyP95MS: 301,
	}
	violations := CheckGate(baseline, candidate, GatePolicy{
		FailOnRegression: true, MinHitRate: 0.88, MaxLatencyP95MS: 300,
	})
	if len(violations) != 9 {
		t.Fatalf("expected nine gate violations, got %d: %#v", len(violations), violations)
	}
}

func TestCheckGateDisabledByDefault(t *testing.T) {
	violations := CheckGate(Report{HitRate: 1}, Report{HitRate: 0}, GatePolicy{})
	if len(violations) != 0 {
		t.Fatalf("default compare must remain informational, got %#v", violations)
	}
}
