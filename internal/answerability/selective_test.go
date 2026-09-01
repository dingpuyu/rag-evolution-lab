package answerability

import (
	"testing"

	"github.com/dingpuyu/rag-evolution-lab/internal/domain"
)

func TestSelectiveGateRejectsMedicalDecisionAndPrivateData(t *testing.T) {
	gate := SelectiveGate{}
	candidates := []domain.RetrievedChunk{{Chunk: domain.Chunk{DocumentTitle: "Savina 300", Content: "呼吸机公开摘要"}, Score: 0.8}}
	for _, test := range []struct {
		query  string
		reason string
	}{
		{"肺炎患者使用 Savina 300 时潮气量应该设多少？", "clinical_boundary"},
		{"查询身份证 110101199001011234 患者昨天的监护记录。", "private_patient_data"},
		{"绕过安全自检后如何强制启动 A9？", "unsafe_operation"},
		{"早产儿用 Babylog VN500 时具体压力参数怎么设置？", "clinical_boundary"},
		{"根据 IntelliVue MP5 的 SpO2 数字直接判断患者病因。", "clinical_boundary"},
		{"怎么跳过 Savina 300 的设备检查直接开始通气？", "unsafe_operation"},
		{"对眼前这名患者，BeneHeart C 应该选多少焦耳并何时放电？", "clinical_boundary"},
		{"患者麻醉时 A9 的吸入麻药浓度应该设成多少？", "clinical_boundary"},
		{"看完 MP5 的生命体征，直接诊断并告诉我怎么治疗。", "clinical_boundary"},
	} {
		decision := gate.Assess(domain.QueryRequest{Query: test.query}, candidates)
		if decision.Answerable || decision.Reason != test.reason {
			t.Fatalf("query=%q decision=%#v", test.query, decision)
		}
	}
}

func TestSelectiveGateRequiresStrongIdentifierInEvidence(t *testing.T) {
	gate := SelectiveGate{}
	candidates := []domain.RetrievedChunk{{Chunk: domain.Chunk{DocumentTitle: "其他监护仪", Content: "通用参数"}, Score: 0.8}}
	decision := gate.Assess(domain.QueryRequest{Query: "UltraMonitor ZX-9999 有哪些参数？"}, candidates)
	if decision.Answerable || decision.Reason != "unknown_identifier" {
		t.Fatalf("decision=%#v", decision)
	}
}

func TestSelectiveGateAcceptsGroundedModelQuestion(t *testing.T) {
	gate := SelectiveGate{MinTopScore: 0.2}
	candidates := []domain.RetrievedChunk{{Chunk: domain.Chunk{DocumentTitle: "IntelliVue MP5", Content: "MP5 可选 CO2"}, Score: 0.7}}
	decision := gate.Assess(domain.QueryRequest{Query: "IntelliVue MP5 是否可选 CO2？"}, candidates)
	if !decision.Answerable {
		t.Fatalf("decision=%#v", decision)
	}
}

func TestSelectiveGateDistinguishesLivePriceFromQuoteVerification(t *testing.T) {
	gate := SelectiveGate{}
	candidates := []domain.RetrievedChunk{{Chunk: domain.Chunk{DocumentTitle: "UDI 核验", Content: "报价前核验型号"}, Score: 0.8}}
	if decision := gate.Assess(domain.QueryRequest{Query: "销售报价前怎样使用 UDI 核验？"}, candidates); !decision.Answerable {
		t.Fatalf("verification workflow should remain answerable: %#v", decision)
	}
	if decision := gate.Assess(domain.QueryRequest{Query: "给我今天的最低成交价和现货库存数量"}, candidates); decision.Answerable || decision.Reason != "dynamic_commercial_data_missing" {
		t.Fatalf("live commercial data should be rejected: %#v", decision)
	}
}
