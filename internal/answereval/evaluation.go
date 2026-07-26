package answereval

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/dingpuyu/rag-evolution-lab/internal/generation"
)

type Suite struct {
	Version    string              `json:"version"`
	Name       string              `json:"name"`
	Identities map[string]Identity `json:"identities"`
	Cases      []Case              `json:"cases"`
}

type Identity struct {
	Email       string `json:"email"`
	PasswordEnv string `json:"password_env"`
}

type Case struct {
	ID                         string   `json:"id"`
	Identity                   string   `json:"identity"`
	DatasetID                  string   `json:"dataset_id"`
	Query                      string   `json:"query"`
	TopK                       int      `json:"top_k"`
	ExpectedStatus             int      `json:"expected_status"`
	ExpectedErrorCode          string   `json:"expected_error_code,omitempty"`
	ExpectedAnswerable         bool     `json:"expected_answerable"`
	RequiredFacts              []string `json:"required_facts"`
	ForbiddenFacts             []string `json:"forbidden_facts"`
	RequiredCitationDocuments  []string `json:"required_citation_documents"`
	ForbiddenCitationDocuments []string `json:"forbidden_citation_documents"`
	ExpectedRefusalReasons     []string `json:"expected_refusal_reasons,omitempty"`
	ExpectedTenant             string   `json:"expected_tenant,omitempty"`
	ExpectedVisibility         string   `json:"expected_visibility,omitempty"`
}

type Runner struct {
	BaseURL    string
	HTTPClient *http.Client
	Passwords  map[string]string
	Cost       CostConfig
	Stream     bool
}

// CostConfig is supplied by the caller instead of hard-coding vendor pricing.
// Provider prices and billing units change, so a report must expose the rates
// used for an estimate.
type CostConfig struct {
	PromptPer1MUSD     float64
	CompletionPer1MUSD float64
}

func (config CostConfig) configured() bool {
	return config.PromptPer1MUSD > 0 || config.CompletionPer1MUSD > 0
}

func (config CostConfig) estimate(promptTokens, completionTokens int) float64 {
	return float64(promptTokens)/1_000_000*config.PromptPer1MUSD +
		float64(completionTokens)/1_000_000*config.CompletionPer1MUSD
}

type Report struct {
	Suite                  string       `json:"suite"`
	Version                string       `json:"version"`
	StartedAt              time.Time    `json:"started_at"`
	CompletedAt            time.Time    `json:"completed_at"`
	Passed                 bool         `json:"passed"`
	Cases                  int          `json:"cases"`
	PassedCases            int          `json:"passed_cases"`
	FailedCases            int          `json:"failed_cases"`
	AnswerabilityAccuracy  float64      `json:"answerability_accuracy"`
	RequiredFactCoverage   float64      `json:"required_fact_coverage"`
	ForbiddenFactHits      int          `json:"forbidden_fact_hits"`
	CitationViolations     int          `json:"citation_violations"`
	UnauthorizedRetrievals int          `json:"unauthorized_retrievals"`
	ContractViolations     int          `json:"contract_violations"`
	LatencyP50MS           float64      `json:"latency_p50_ms"`
	LatencyP95MS           float64      `json:"latency_p95_ms"`
	TTFTP50MS              float64      `json:"ttft_p50_ms,omitempty"`
	TTFTP95MS              float64      `json:"ttft_p95_ms,omitempty"`
	TokenRateP50TPS        float64      `json:"token_rate_p50_tps,omitempty"`
	TokenRateP95TPS        float64      `json:"token_rate_p95_tps,omitempty"`
	Streaming              bool         `json:"streaming"`
	PromptTokens           int          `json:"prompt_tokens"`
	OutputTokens           int          `json:"output_tokens"`
	Providers              []string     `json:"providers,omitempty"`
	Models                 []string     `json:"models,omitempty"`
	PromptCostPer1MUSD     float64      `json:"prompt_cost_per_1m_usd"`
	CompletionCostPer1MUSD float64      `json:"completion_cost_per_1m_usd"`
	EstimatedCostUSD       float64      `json:"estimated_cost_usd"`
	CostConfigured         bool         `json:"cost_configured"`
	SafetyAdjustments      int          `json:"safety_adjustments"`
	Results                []CaseResult `json:"results"`
}

type CaseResult struct {
	ID                string   `json:"id"`
	Passed            bool     `json:"passed"`
	HTTPStatus        int      `json:"http_status"`
	Answerable        bool     `json:"answerable"`
	Answer            string   `json:"answer,omitempty"`
	RefusalReason     string   `json:"refusal_reason,omitempty"`
	CitationDocuments []string `json:"citation_documents,omitempty"`
	Generator         string   `json:"generator,omitempty"`
	Model             string   `json:"model,omitempty"`
	LatencyMS         float64  `json:"latency_ms"`
	GenerationMS      float64  `json:"generation_ms"`
	TTFTMS            float64  `json:"ttft_ms,omitempty"`
	TokenRateTPS      float64  `json:"token_rate_tps,omitempty"`
	PromptTokens      int      `json:"prompt_tokens"`
	OutputTokens      int      `json:"output_tokens"`
	EstimatedCostUSD  float64  `json:"estimated_cost_usd,omitempty"`
	SafetyAdjustments []string `json:"safety_adjustments,omitempty"`
	Failures          []string `json:"failures,omitempty"`
}

type answerResponse struct {
	Result generation.Response `json:"result"`
	Error  struct {
		Code string `json:"code"`
	} `json:"error"`
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
}

func Load(path string) (Suite, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Suite{}, fmt.Errorf("read answer suite: %w", err)
	}
	var suite Suite
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&suite); err != nil {
		return Suite{}, fmt.Errorf("decode answer suite: %w", err)
	}
	if suite.Name == "" || suite.Version == "" || len(suite.Identities) == 0 || len(suite.Cases) == 0 {
		return Suite{}, fmt.Errorf("answer suite requires name, version, identities and cases")
	}
	seen := make(map[string]struct{})
	for _, test := range suite.Cases {
		if test.ID == "" || test.DatasetID == "" || test.Query == "" || test.ExpectedStatus == 0 {
			return Suite{}, fmt.Errorf("answer case requires id, dataset_id, query and expected_status")
		}
		if _, duplicate := seen[test.ID]; duplicate {
			return Suite{}, fmt.Errorf("duplicate answer case %q", test.ID)
		}
		seen[test.ID] = struct{}{}
		if _, ok := suite.Identities[test.Identity]; !ok {
			return Suite{}, fmt.Errorf("case %q references unknown identity %q", test.ID, test.Identity)
		}
	}
	return suite, nil
}

func (runner Runner) Run(ctx context.Context, suite Suite) (Report, error) {
	if runner.HTTPClient == nil {
		runner.HTTPClient = &http.Client{Timeout: 2 * time.Minute}
	}
	baseURL := strings.TrimRight(strings.TrimSpace(runner.BaseURL), "/")
	if baseURL == "" {
		baseURL = "http://127.0.0.1:8080"
	}
	report := Report{Suite: suite.Name, Version: suite.Version, StartedAt: time.Now().UTC()}
	tokens := make(map[string]string, len(suite.Identities))
	for name, identity := range suite.Identities {
		password := runner.Passwords[name]
		if password == "" {
			password = os.Getenv(identity.PasswordEnv)
		}
		token, err := runner.login(ctx, baseURL, identity.Email, password)
		if err != nil {
			return Report{}, fmt.Errorf("login %q: %w", name, err)
		}
		tokens[name] = token
	}
	var answerableCases, answerableMatches, requiredFacts, requiredFactHits int
	var latencies, ttfts, tokenRates []float64
	providers := make(map[string]struct{})
	models := make(map[string]struct{})
	for _, test := range suite.Cases {
		result, counters, err := runner.runCase(ctx, baseURL, tokens[test.Identity], test)
		if err != nil {
			return Report{}, err
		}
		report.Results = append(report.Results, result)
		report.ForbiddenFactHits += counters.forbiddenFacts
		report.CitationViolations += counters.citations
		report.UnauthorizedRetrievals += counters.unauthorized
		report.ContractViolations += counters.contract
		if test.ExpectedStatus == http.StatusOK {
			answerableCases++
			if result.Answerable == test.ExpectedAnswerable {
				answerableMatches++
			}
		}
		requiredFacts += len(test.RequiredFacts)
		requiredFactHits += counters.requiredFactHits
		report.PromptTokens += result.PromptTokens
		report.OutputTokens += result.OutputTokens
		report.EstimatedCostUSD += result.EstimatedCostUSD
		if result.Generator != "" {
			providers[result.Generator] = struct{}{}
		}
		if result.Model != "" {
			models[result.Model] = struct{}{}
		}
		report.SafetyAdjustments += len(result.SafetyAdjustments)
		latencies = append(latencies, result.LatencyMS)
		if result.TTFTMS > 0 {
			ttfts = append(ttfts, result.TTFTMS)
		}
		if result.TokenRateTPS > 0 {
			tokenRates = append(tokenRates, result.TokenRateTPS)
		}
	}
	report.Cases = len(report.Results)
	for _, result := range report.Results {
		if result.Passed {
			report.PassedCases++
		}
	}
	report.FailedCases = report.Cases - report.PassedCases
	report.Passed = report.FailedCases == 0
	if answerableCases > 0 {
		report.AnswerabilityAccuracy = float64(answerableMatches) / float64(answerableCases)
	}
	if requiredFacts == 0 {
		report.RequiredFactCoverage = 1
	} else {
		report.RequiredFactCoverage = float64(requiredFactHits) / float64(requiredFacts)
	}
	report.LatencyP50MS = percentile(latencies, 0.5)
	report.LatencyP95MS = percentile(latencies, 0.95)
	report.TTFTP50MS = percentile(ttfts, 0.5)
	report.TTFTP95MS = percentile(ttfts, 0.95)
	report.TokenRateP50TPS = percentile(tokenRates, 0.5)
	report.TokenRateP95TPS = percentile(tokenRates, 0.95)
	report.Streaming = runner.Stream
	report.Providers = sortedSet(providers)
	report.Models = sortedSet(models)
	report.PromptCostPer1MUSD = runner.Cost.PromptPer1MUSD
	report.CompletionCostPer1MUSD = runner.Cost.CompletionPer1MUSD
	report.CostConfigured = runner.Cost.configured()
	report.CompletedAt = time.Now().UTC()
	return report, nil
}

type counters struct {
	requiredFactHits, forbiddenFacts, citations, unauthorized, contract int
}

func (runner Runner) runCase(ctx context.Context, baseURL, token string, test Case) (CaseResult, counters, error) {
	started := time.Now()
	var status int
	var response answerResponse
	var err error
	payload := map[string]any{"query": test.Query, "top_k": test.TopK}
	if runner.Stream && test.ExpectedStatus == http.StatusOK {
		status, response, err = runner.requestStream(ctx, baseURL+"/api/v1/datasets/"+test.DatasetID+"/answer/stream", token, payload)
	} else {
		var body []byte
		status, body, err = runner.request(ctx, http.MethodPost, baseURL+"/api/v1/datasets/"+test.DatasetID+"/answer", token, payload)
		if err == nil {
			err = json.Unmarshal(body, &response)
		}
	}
	if err != nil {
		return CaseResult{}, counters{}, fmt.Errorf("answer case %q: %w", test.ID, err)
	}
	result := CaseResult{ID: test.ID, HTTPStatus: status, LatencyMS: elapsed(started)}
	var count counters
	if status != test.ExpectedStatus {
		result.Failures = append(result.Failures, fmt.Sprintf("expected HTTP %d, got %d", test.ExpectedStatus, status))
		count.contract++
	}
	if status != http.StatusOK {
		if test.ExpectedErrorCode != "" && response.Error.Code != test.ExpectedErrorCode {
			result.Failures = append(result.Failures, fmt.Sprintf("expected error code %q, got %q", test.ExpectedErrorCode, response.Error.Code))
			count.contract++
		}
		result.Passed = len(result.Failures) == 0
		return result, count, nil
	}
	answer := response.Result
	result.Answerable, result.Answer, result.RefusalReason = answer.Answerable, answer.Answer, answer.RefusalReason
	result.Generator, result.Model = answer.Generation.Generator, answer.Generation.Model
	result.GenerationMS = answer.Generation.LatencyMS
	result.TTFTMS, result.TokenRateTPS = answer.Generation.TTFTMS, answer.Generation.TokenRateTPS
	result.PromptTokens, result.OutputTokens = answer.Generation.PromptTokens, answer.Generation.OutputTokens
	result.EstimatedCostUSD = runner.Cost.estimate(result.PromptTokens, result.OutputTokens)
	result.SafetyAdjustments = append([]string(nil), answer.Generation.SafetyAdjustments...)
	if answer.Answerable != test.ExpectedAnswerable {
		result.Failures = append(result.Failures, fmt.Sprintf("answerable=%t, expected %t", answer.Answerable, test.ExpectedAnswerable))
		count.contract++
	}
	lowerAnswer := strings.ToLower(answer.Answer)
	for _, fact := range test.RequiredFacts {
		if strings.Contains(lowerAnswer, strings.ToLower(fact)) {
			count.requiredFactHits++
		} else {
			result.Failures = append(result.Failures, "required fact missing: "+fact)
		}
	}
	for _, fact := range test.ForbiddenFacts {
		if strings.Contains(lowerAnswer, strings.ToLower(fact)) {
			result.Failures = append(result.Failures, "forbidden fact in answer: "+fact)
			count.forbiddenFacts++
		}
	}
	cited := make(map[string]struct{}, len(answer.Citations))
	for _, citation := range answer.Citations {
		result.CitationDocuments = appendUnique(result.CitationDocuments, citation.DocumentID)
		cited[citation.DocumentID] = struct{}{}
	}
	for _, documentID := range test.RequiredCitationDocuments {
		if _, ok := cited[documentID]; !ok {
			result.Failures = append(result.Failures, "required citation missing: "+documentID)
			count.citations++
		}
	}
	for _, documentID := range test.ForbiddenCitationDocuments {
		if _, ok := cited[documentID]; ok {
			result.Failures = append(result.Failures, "forbidden citation present: "+documentID)
			count.citations++
		}
	}
	if len(test.ExpectedRefusalReasons) > 0 && !contains(test.ExpectedRefusalReasons, answer.RefusalReason) {
		result.Failures = append(result.Failures, "unexpected refusal_reason: "+answer.RefusalReason)
		count.contract++
	}
	for _, hit := range answer.Search.Hits {
		if test.ExpectedTenant != "" && hit.TenantID != test.ExpectedTenant {
			result.Failures = append(result.Failures, fmt.Sprintf("retrieved tenant %q", hit.TenantID))
			count.unauthorized++
		}
		if test.ExpectedVisibility != "" && hit.Visibility != test.ExpectedVisibility {
			result.Failures = append(result.Failures, fmt.Sprintf("retrieved visibility %q", hit.Visibility))
			count.unauthorized++
		}
	}
	result.Passed = len(result.Failures) == 0
	return result, count, nil
}

func (runner Runner) login(ctx context.Context, baseURL, email, password string) (string, error) {
	if password == "" {
		return "", fmt.Errorf("password is required")
	}
	status, body, err := runner.request(ctx, http.MethodPost, baseURL+"/api/v1/auth/login", "", map[string]string{
		"email": email, "password": password,
	})
	if err != nil {
		return "", err
	}
	var response tokenResponse
	if status != http.StatusOK || json.Unmarshal(body, &response) != nil || response.AccessToken == "" {
		return "", fmt.Errorf("login HTTP %d", status)
	}
	return response.AccessToken, nil
}

func (runner Runner) request(ctx context.Context, method, url, token string, payload any) (int, []byte, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, err
	}
	request, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(data))
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := runner.HTTPClient.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	return response.StatusCode, body, err
}

func (runner Runner) requestStream(ctx context.Context, url, token string, payload any) (int, answerResponse, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return 0, answerResponse{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return 0, answerResponse{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := runner.HTTPClient.Do(request)
	if err != nil {
		return 0, answerResponse{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 4<<20))
		if readErr != nil {
			return response.StatusCode, answerResponse{}, readErr
		}
		var decoded answerResponse
		if decodeErr := json.Unmarshal(body, &decoded); decodeErr != nil {
			return response.StatusCode, answerResponse{}, decodeErr
		}
		return response.StatusCode, decoded, nil
	}
	type streamEnvelope struct {
		Event struct {
			Response *generation.Response `json:"response"`
			Error    string               `json:"error"`
		} `json:"event"`
	}
	var result *generation.Response
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var envelope streamEnvelope
		if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &envelope); err != nil {
			return response.StatusCode, answerResponse{}, fmt.Errorf("decode answer stream event: %w", err)
		}
		if envelope.Event.Error != "" {
			return response.StatusCode, answerResponse{}, fmt.Errorf("answer stream returned error: %s", envelope.Event.Error)
		}
		if envelope.Event.Response != nil {
			responseCopy := *envelope.Event.Response
			result = &responseCopy
		}
	}
	if err := scanner.Err(); err != nil {
		return response.StatusCode, answerResponse{}, fmt.Errorf("read answer stream: %w", err)
	}
	if result == nil {
		return response.StatusCode, answerResponse{}, fmt.Errorf("answer stream completed without a response")
	}
	return response.StatusCode, answerResponse{Result: *result}, nil
}

func MarshalReport(report Report) ([]byte, error) { return json.MarshalIndent(report, "", "  ") }

func Markdown(report Report) string {
	status := "PASS"
	if !report.Passed {
		status = "FAIL"
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "# Grounded Answer Harness 报告\n\n- 结果：**%s**\n- Suite：`%s` (`%s`)\n- 用例：%d（通过 %d / 失败 %d）\n\n",
		status, report.Suite, report.Version, report.Cases, report.PassedCases, report.FailedCases)
	transport := "JSON"
	if report.Streaming {
		transport = "SSE stream"
	}
	fmt.Fprintf(&builder, "## 运行配置\n\n- Transport：`%s`\n- Provider：`%s`\n- Model：`%s`\n- 成本估算：%s\n\n", transport, strings.Join(report.Providers, ", "), strings.Join(report.Models, ", "), costSummary(report))
	fmt.Fprintf(&builder, "## 核心指标\n\n| Answerability | Required Fact Coverage | 禁止事实 | 引用违规 | 越权召回 | 契约违规 | 安全纠偏 | P50 | P95 | TTFT P50/P95 | Token Rate P50/P95 | Prompt / Output Tokens | 估算成本 |\n")
	fmt.Fprintf(&builder, "|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n| %.3f | %.3f | %d | %d | %d | %d | %d | %.1f ms | %.1f ms | %.1f/%.1f ms | %.1f/%.1f tps | %d / %d | $%.6f |\n\n",
		report.AnswerabilityAccuracy, report.RequiredFactCoverage, report.ForbiddenFactHits,
		report.CitationViolations, report.UnauthorizedRetrievals, report.ContractViolations,
		report.SafetyAdjustments, report.LatencyP50MS, report.LatencyP95MS,
		report.TTFTP50MS, report.TTFTP95MS, report.TokenRateP50TPS, report.TokenRateP95TPS,
		report.PromptTokens, report.OutputTokens, report.EstimatedCostUSD)
	fmt.Fprintf(&builder, "## 用例\n\n| 用例 | 结果 | Answerable | Refusal | Provider / Model | 引用 | 总延迟 | 生成延迟 | 成本 |\n|---|---|---:|---|---|---|---:|---:|---:|\n")
	for _, result := range report.Results {
		caseStatus := "PASS"
		if !result.Passed {
			caseStatus = "FAIL: " + strings.Join(result.Failures, "; ")
		}
		providerModel := strings.TrimSpace(strings.TrimSpace(result.Generator) + " / " + strings.TrimSpace(result.Model))
		providerModel = strings.Trim(providerModel, " /")
		fmt.Fprintf(&builder, "| `%s` | %s | %t | `%s` | `%s` | `%s` | %.1f ms | %.1f ms | $%.6f |\n",
			result.ID, caseStatus, result.Answerable, result.RefusalReason,
			providerModel, strings.Join(result.CitationDocuments, ", "), result.LatencyMS, result.GenerationMS, result.EstimatedCostUSD)
	}
	fmt.Fprintf(&builder, "\n## 门禁\n\n- 模型引用只能来自服务端最终 Context，引用正文由服务端重建。\n")
	fmt.Fprintf(&builder, "- Required/Forbidden Fact、拒答枚举和跨租户结果使用确定性规则判断。\n")
	fmt.Fprintf(&builder, "- Prompt Injection 用例要求拒答，且输出不得包含知识正文中的伪造秘密。\n")
	return builder.String()
}

func percentile(values []float64, quantile float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	index := int(math.Ceil(quantile*float64(len(sorted)))) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}

func elapsed(started time.Time) float64 {
	return float64(time.Since(started).Microseconds()) / 1000
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func appendUnique(values []string, target string) []string {
	if contains(values, target) {
		return values
	}
	return append(values, target)
}

func sortedSet(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func costSummary(report Report) string {
	if !report.CostConfigured {
		return "未配置费率（仅统计 Token）"
	}
	return fmt.Sprintf("按输入 $%.6f/1M、输出 $%.6f/1M 估算，总计 $%.6f",
		report.PromptCostPer1MUSD, report.CompletionCostPer1MUSD, report.EstimatedCostUSD)
}
