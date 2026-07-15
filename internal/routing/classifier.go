package routing

import (
	"regexp"
	"strings"

	"github.com/dingpuyu/rag-evolution-lab/internal/domain"
)

type Intent string

const (
	IntentExact            Intent = "exact"
	IntentSemantic         Intent = "semantic"
	IntentAccessSensitive  Intent = "access_sensitive"
	IntentUnanswerableRisk Intent = "unanswerable_risk"
)

type Decision struct {
	Intent Intent
	Reason string
}

type Classifier interface {
	Classify(request domain.QueryRequest) Decision
}

type HeuristicClassifier struct{}

var (
	strongIdentifierPattern = regexp.MustCompile(`(?i)[a-z0-9]+(?:[-_.][a-z0-9]+)+|[a-z]+[0-9]{3,}`)
	versionPattern          = regexp.MustCompile(`(?i)(?:^|[^0-9])[v]?[0-9]+\.[0-9]+(?:[^0-9]|$)`)
)

func (HeuristicClassifier) Classify(request domain.QueryRequest) Decision {
	query := strings.ToLower(strings.TrimSpace(request.Query))
	if containsAny(query, "tenant ", "tenant-a", "tenant_b", "租户", "专用", "专属", "跨租户", "权限字段", "私有队列") {
		return Decision{Intent: IntentAccessSensitive, Reason: "tenant or privileged-operation language"}
	}
	if containsAny(query, "是否已经", "是否通过", "有没有通过", "是否获得", "认证") {
		return Decision{Intent: IntentUnanswerableRisk, Reason: "external-status verification language"}
	}
	if strongIdentifierPattern.MatchString(query) {
		return Decision{Intent: IntentExact, Reason: "structured identifier"}
	}
	if versionPattern.MatchString(query) || containsAny(query, "当前稳定版", "历史版本", "旧版本") {
		return Decision{Intent: IntentExact, Reason: "explicit version constraint"}
	}
	if containsAny(query, "多少", "多大", "最大", "最高", "配额", "保留几天", "保留多少天") {
		return Decision{Intent: IntentExact, Reason: "numeric or table lookup"}
	}
	return Decision{Intent: IntentSemantic, Reason: "natural-language semantic query"}
}

func containsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}
