package answerability

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/dingpuyu/rag-evolution-lab/internal/domain"
)

// Decision is intentionally deterministic. Safety, privacy and evidence
// presence are release gates and must not depend on an LLM judge.
type Decision struct {
	Answerable bool
	Reason     string
	TopScore   float64
}

type Gate interface {
	Assess(request domain.QueryRequest, candidates []domain.RetrievedChunk) Decision
}

// SelectiveGate turns a ranker into a selective retriever: it may abstain when
// the question is outside the indexed evidence or crosses a medical safety,
// privacy or dynamic-commercial boundary.
type SelectiveGate struct {
	MinTopScore float64
}

var (
	strongIdentifier = regexp.MustCompile(`(?i)[a-z0-9]+(?:[-_.][a-z0-9]+)+|[a-z]+[0-9]{2,}[a-z]*`)
	identityNumber   = regexp.MustCompile(`(?:^|[^0-9])[1-9][0-9]{16}[0-9xX](?:[^0-9]|$)`)
	futureYear       = regexp.MustCompile(`20(?:2[7-9]|[3-9][0-9])`)
)

func (g SelectiveGate) Assess(request domain.QueryRequest, candidates []domain.RetrievedChunk) Decision {
	query := strings.ToLower(strings.TrimSpace(request.Query))
	if query == "" {
		return Decision{Reason: "empty_query"}
	}
	if identityNumber.MatchString(query) || containsAny(query, "患者监护记录", "病人监护记录", "身份证") {
		return Decision{Reason: "private_patient_data"}
	}
	if isUnsafeBypass(query) {
		return Decision{Reason: "unsafe_operation"}
	}
	if isClinicalDecision(query) {
		return Decision{Reason: "clinical_boundary"}
	}
	if isDynamicCommercialQuery(query) {
		return Decision{Reason: "dynamic_commercial_data_missing"}
	}
	if containsAny(query, "未来是否", "明年是否", "是否仍然持有有效注册证") ||
		(futureYear.MatchString(query) && containsAny(query, "注册", "供货", "可售", "上市", "保证")) {
		return Decision{Reason: "future_or_external_status"}
	}
	if (containsAny(query, "医院", "科室") && containsAny(query, "多少台", "授权数量", "现有数量", "资产数量")) ||
		containsAny(query, "医院目前有多少台", "医院现在有多少台", "科室目前有多少台") {
		return Decision{Reason: "private_external_data_missing"}
	}
	if len(candidates) == 0 {
		return Decision{Reason: "no_evidence"}
	}

	topScore := candidates[0].Score
	if g.MinTopScore > 0 && topScore < g.MinTopScore {
		return Decision{Reason: "low_evidence_score", TopScore: topScore}
	}
	anchors := strongIdentifier.FindAllString(query, -1)
	if len(anchors) > 0 && !anchorsCovered(anchors, candidates) {
		return Decision{Reason: "unknown_identifier", TopScore: topScore}
	}
	return Decision{Answerable: true, Reason: "evidence_accepted", TopScore: topScore}
}

func isClinicalDecision(query string) bool {
	if containsAny(query, "直接诊断", "给出诊断", "判断病因", "告诉我怎么治疗", "制定治疗方案") {
		return true
	}
	clinicalAction := containsAny(query, "诊断", "病因", "治疗", "用药", "剂量", "潮气量", "报警阈值", "多少焦耳", "何时放电", "应该设多少", "应该设置多少", "设成多少", "参数怎么设置", "参数如何设置", "氧浓度", "麻药浓度")
	if !clinicalAction && containsAny(query, "怎么设", "如何设", "应该选", "如何调", "怎么调") {
		clinicalAction = containsAny(query, "压力", "浓度", "阈值", "焦耳", "通气", "麻醉")
	}
	patientContext := containsAny(query, "患者", "病人", "新生儿", "早产儿", "肺炎", "具体患者")
	return clinicalAction && patientContext
}

func isUnsafeBypass(query string) bool {
	bypass := containsAny(query, "绕过", "跳过", "关闭", "禁用", "屏蔽")
	safeguard := containsAny(query, "安全", "自检", "设备检查", "报警", "联锁")
	return (bypass && safeguard) || containsAny(query, "强制启动", "直接开始通气")
}

func isDynamicCommercialQuery(query string) bool {
	// Questions about how to verify a product before quoting are grounded
	// workflow questions, not requests for live price data.
	if containsAny(query, "报价前", "报价流程", "报价核验") {
		return false
	}
	priceTerm := containsAny(query, "成交价", "报价多少", "多少钱", "底价", "含税价", "价格多少")
	liveQualifier := containsAny(query, "今天", "当前", "实时", "最低", "给我", "是多少", "多少")
	stockTerm := containsAny(query, "库存数量", "实时库存", "今天库存", "现货库存")
	return (priceTerm && liveQualifier) || stockTerm
}

func anchorsCovered(anchors []string, candidates []domain.RetrievedChunk) bool {
	limit := len(candidates)
	if limit > 5 {
		limit = 5
	}
	for _, anchor := range anchors {
		if isWeakAnchor(anchor) {
			continue
		}
		covered := false
		for _, candidate := range candidates[:limit] {
			evidence := strings.ToLower(candidate.Chunk.DocumentTitle + " " + candidate.Chunk.Content)
			if strings.Contains(evidence, strings.ToLower(anchor)) {
				covered = true
				break
			}
		}
		if !covered {
			return false
		}
	}
	return true
}

// Years and generic measurements are not product identifiers. Treating them
// as anchors would incorrectly reject legitimate version/freshness questions.
func isWeakAnchor(value string) bool {
	if len(value) == 4 && value[0] == '2' {
		allDigits := true
		for _, current := range value {
			allDigits = allDigits && unicode.IsDigit(current)
		}
		return allDigits
	}
	return false
}

func containsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}
