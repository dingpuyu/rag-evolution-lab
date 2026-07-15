package routing

import (
	"testing"

	"github.com/dingpuyu/rag-evolution-lab/internal/domain"
)

func TestHeuristicClassifierUsesObservableQueryFeatures(t *testing.T) {
	tests := []struct {
		name    string
		request domain.QueryRequest
		want    Intent
	}{
		{name: "tenant operation", request: domain.QueryRequest{Query: "Tenant A 的专用队列叫什么？"}, want: IntentAccessSensitive},
		{name: "tenant paraphrase", request: domain.QueryRequest{Query: "租户 A 的专属加速队列叫什么？"}, want: IntentAccessSensitive},
		{name: "verification risk", request: domain.QueryRequest{Query: "是否已经通过 ISO-X9 认证？"}, want: IntentUnanswerableRisk},
		{name: "error code", request: domain.QueryRequest{Query: "E1027 是什么错误？"}, want: IntentExact},
		{name: "header", request: domain.QueryRequest{Query: "X-RateLimit-Reset 是什么？"}, want: IntentExact},
		{name: "explicit version", request: domain.QueryRequest{Query: "2.3 版本如何配置？", Version: "2.3"}, want: IntentExact},
		{name: "context is not intent", request: domain.QueryRequest{Query: "怎么让员工只登录一次？", Version: "2.3"}, want: IntentSemantic},
		{name: "numeric lookup", request: domain.QueryRequest{Query: "最多保留多少天？"}, want: IntentExact},
		{name: "paraphrase", request: domain.QueryRequest{Query: "怎么让员工只登录一次？"}, want: IntentSemantic},
	}
	classifier := HeuristicClassifier{}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision := classifier.Classify(test.request)
			if decision.Intent != test.want || decision.Reason == "" {
				t.Fatalf("unexpected decision: %#v", decision)
			}
		})
	}
}

func TestAccessRiskTakesPriorityOverExactIdentifier(t *testing.T) {
	decision := (HeuristicClassifier{}).Classify(domain.QueryRequest{
		Query: "Tenant B 可以使用 reports-priority-a 吗？",
	})
	if decision.Intent != IntentAccessSensitive {
		t.Fatalf("tenant boundary should take priority, got %#v", decision)
	}
}
