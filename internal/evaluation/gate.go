package evaluation

// GatePolicy describes the quality and safety constraints for a candidate
// pipeline. A zero threshold means that the corresponding constraint is not
// configured; this keeps the default compare command backwards compatible.
type GatePolicy struct {
	FailOnRegression bool
	MinHitRate       float64
	MinMRR           float64
	MinRecall        float64
	MinNDCG          float64
	MinAnswerability float64
	MaxLatencyP95MS  float64
}

type GateViolation struct {
	Metric    string  `json:"metric"`
	Baseline  float64 `json:"baseline,omitempty"`
	Candidate float64 `json:"candidate"`
	Limit     float64 `json:"limit,omitempty"`
	Reason    string  `json:"reason"`
}

func (policy GatePolicy) Enabled() bool {
	return policy.FailOnRegression || policy.MinHitRate > 0 || policy.MinMRR > 0 ||
		policy.MinRecall > 0 || policy.MinNDCG > 0 || policy.MinAnswerability > 0 || policy.MaxLatencyP95MS > 0
}

// CheckGate compares a candidate report with its baseline and returns every
// violation instead of stopping on the first one. Security violations are
// always monotonic: a candidate may not increase unauthorized retrievals or
// citation violations when regression gating is enabled.
func CheckGate(baseline, candidate Report, policy GatePolicy) []GateViolation {
	if !policy.Enabled() {
		return nil
	}
	violations := make([]GateViolation, 0)
	checkMin := func(metric string, value, limit float64) {
		if limit > 0 && value+1e-9 < limit {
			violations = append(violations, GateViolation{
				Metric: metric, Candidate: value, Limit: limit,
				Reason: "candidate is below the configured minimum",
			})
		}
	}
	checkMin("hit_rate_at_k", candidate.HitRate, policy.MinHitRate)
	checkMin("mrr", candidate.MRR, policy.MinMRR)
	checkMin("document_recall_at_k", candidate.Recall, policy.MinRecall)
	checkMin("ndcg_at_k", candidate.NDCG, policy.MinNDCG)
	checkMin("answerability_accuracy", candidate.AnswerabilityAccuracy, policy.MinAnswerability)
	if policy.MaxLatencyP95MS > 0 && candidate.LatencyP95MS > policy.MaxLatencyP95MS {
		violations = append(violations, GateViolation{
			Metric: "latency_p95_ms", Candidate: candidate.LatencyP95MS,
			Limit: policy.MaxLatencyP95MS, Reason: "candidate exceeds the configured maximum",
		})
	}

	if !policy.FailOnRegression {
		return violations
	}
	checkRegression := func(metric string, baselineValue, candidateValue float64) {
		if candidateValue+1e-9 < baselineValue {
			violations = append(violations, GateViolation{
				Metric: metric, Baseline: baselineValue, Candidate: candidateValue,
				Reason: "candidate regressed against baseline",
			})
		}
	}
	checkRegression("hit_rate_at_k", baseline.HitRate, candidate.HitRate)
	checkRegression("mrr", baseline.MRR, candidate.MRR)
	checkRegression("document_recall_at_k", baseline.Recall, candidate.Recall)
	checkRegression("ndcg_at_k", baseline.NDCG, candidate.NDCG)
	checkRegression("answerability_accuracy", baseline.AnswerabilityAccuracy, candidate.AnswerabilityAccuracy)
	if candidate.UnauthorizedRetrievals > baseline.UnauthorizedRetrievals {
		violations = append(violations, GateViolation{
			Metric: "unauthorized_retrievals", Baseline: float64(baseline.UnauthorizedRetrievals),
			Candidate: float64(candidate.UnauthorizedRetrievals), Reason: "candidate increased unauthorized retrievals",
		})
	}
	if candidate.CitationViolations > baseline.CitationViolations {
		violations = append(violations, GateViolation{
			Metric: "citation_violations", Baseline: float64(baseline.CitationViolations),
			Candidate: float64(candidate.CitationViolations), Reason: "candidate increased citation violations",
		})
	}
	return violations
}
