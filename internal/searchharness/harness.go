package searchharness

import (
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

	"github.com/dingpuyu/rag-evolution-lab/internal/milvus"
)

type Suite struct {
	Version         string                   `json:"version"`
	Name            string                   `json:"name"`
	Seeds           []milvus.LifecycleChange `json:"seeds"`
	Identities      map[string]Identity      `json:"identities"`
	VisibilityCases []VisibilityCase         `json:"visibility_cases"`
	SearchCases     []SearchCase             `json:"search_cases"`
}

type Identity struct {
	Email       string `json:"email"`
	PasswordEnv string `json:"password_env"`
}

type VisibilityCase struct {
	ID                string   `json:"id"`
	Identity          string   `json:"identity"`
	RequiredDatasets  []string `json:"required_datasets"`
	ForbiddenDatasets []string `json:"forbidden_datasets"`
}

type SearchCase struct {
	ID                       string   `json:"id"`
	Identity                 string   `json:"identity"`
	DatasetID                string   `json:"dataset_id"`
	Query                    string   `json:"query"`
	TopK                     int      `json:"top_k"`
	ExpectedStatus           int      `json:"expected_status"`
	ExpectedErrorCode        string   `json:"expected_error_code,omitempty"`
	RelevantDocumentIDs      []string `json:"relevant_document_ids,omitempty"`
	MaxFirstRelevantRank     int      `json:"max_first_relevant_rank,omitempty"`
	RequiredFacts            []string `json:"required_facts,omitempty"`
	ForbiddenDocumentIDs     []string `json:"forbidden_document_ids,omitempty"`
	ForbiddenFacts           []string `json:"forbidden_facts,omitempty"`
	ExpectedVisibility       string   `json:"expected_visibility,omitempty"`
	ExpectedTenant           string   `json:"expected_tenant,omitempty"`
	RequiredFilterFragments  []string `json:"required_filter_fragments,omitempty"`
	ForbiddenFilterFragments []string `json:"forbidden_filter_fragments,omitempty"`
}

type Runner struct {
	BaseURL    string
	HTTPClient *http.Client
	Passwords  map[string]string
}

type Report struct {
	Suite                  string            `json:"suite"`
	Version                string            `json:"version"`
	StartedAt              time.Time         `json:"started_at"`
	CompletedAt            time.Time         `json:"completed_at"`
	Passed                 bool              `json:"passed"`
	Cases                  int               `json:"cases"`
	PassedCases            int               `json:"passed_cases"`
	FailedCases            int               `json:"failed_cases"`
	SearchCases            int               `json:"search_cases"`
	HitRateAtK             float64           `json:"hit_rate_at_k"`
	MRR                    float64           `json:"mrr"`
	LatencyP50MS           float64           `json:"latency_p50_ms"`
	LatencyP95MS           float64           `json:"latency_p95_ms"`
	UnauthorizedRetrievals int               `json:"unauthorized_retrievals"`
	ForbiddenFactHits      int               `json:"forbidden_fact_hits"`
	FilterViolations       int               `json:"filter_violations"`
	ContractViolations     int               `json:"contract_violations"`
	Results                []CaseResult      `json:"results"`
	Environment            ReportEnvironment `json:"environment"`
}

type ReportEnvironment struct {
	BaseURL    string `json:"base_url"`
	Collection string `json:"collection,omitempty"`
	Embedder   string `json:"embedder,omitempty"`
	Dimensions int    `json:"dimensions,omitempty"`
}

type CaseResult struct {
	ID                string           `json:"id"`
	Type              string           `json:"type"`
	Passed            bool             `json:"passed"`
	HTTPStatus        int              `json:"http_status"`
	LatencyMS         float64          `json:"latency_ms"`
	FirstRelevantRank int              `json:"first_relevant_rank,omitempty"`
	ReciprocalRank    float64          `json:"reciprocal_rank"`
	RetrievedDocIDs   []string         `json:"retrieved_doc_ids,omitempty"`
	RetrievedHits     []HitObservation `json:"retrieved_hits,omitempty"`
	Filter            string           `json:"filter,omitempty"`
	Failures          []string         `json:"failures,omitempty"`
}

type HitObservation struct {
	Rank       int     `json:"rank"`
	DocumentID string  `json:"document_id"`
	TenantID   string  `json:"tenant_id"`
	Visibility string  `json:"visibility"`
	Score      float64 `json:"score"`
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
}

type datasetListResponse struct {
	Datasets []struct {
		ID string `json:"id"`
	} `json:"datasets"`
}

type datasetSearchResponse struct {
	Result struct {
		Collection string             `json:"collection"`
		Embedder   string             `json:"embedder"`
		Dimensions int                `json:"dimensions"`
		Filter     string             `json:"filter"`
		Hits       []milvus.SearchHit `json:"hits"`
	} `json:"result"`
}

type errorResponse struct {
	Error struct {
		Code string `json:"code"`
	} `json:"error"`
}

func Load(path string) (Suite, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Suite{}, fmt.Errorf("read search suite: %w", err)
	}
	var suite Suite
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&suite); err != nil {
		return Suite{}, fmt.Errorf("decode search suite: %w", err)
	}
	if err := suite.Validate(); err != nil {
		return Suite{}, err
	}
	return suite, nil
}

func (suite Suite) Validate() error {
	if strings.TrimSpace(suite.Version) == "" || strings.TrimSpace(suite.Name) == "" {
		return fmt.Errorf("search suite version and name are required")
	}
	if len(suite.Identities) == 0 || len(suite.SearchCases) == 0 {
		return fmt.Errorf("search suite requires identities and search cases")
	}
	seen := make(map[string]struct{})
	checkID := func(id, identity string) error {
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("case id is required")
		}
		if _, duplicate := seen[id]; duplicate {
			return fmt.Errorf("duplicate case id %q", id)
		}
		seen[id] = struct{}{}
		if _, ok := suite.Identities[identity]; !ok {
			return fmt.Errorf("case %q references unknown identity %q", id, identity)
		}
		return nil
	}
	for _, test := range suite.VisibilityCases {
		if err := checkID(test.ID, test.Identity); err != nil {
			return err
		}
	}
	for _, test := range suite.SearchCases {
		if err := checkID(test.ID, test.Identity); err != nil {
			return err
		}
		if test.DatasetID == "" || test.Query == "" || test.ExpectedStatus == 0 {
			return fmt.Errorf("search case %q requires dataset_id, query and expected_status", test.ID)
		}
	}
	return nil
}

func (runner Runner) Run(ctx context.Context, suite Suite, seed bool) (Report, error) {
	report := Report{
		Suite: suite.Name, Version: suite.Version, StartedAt: time.Now().UTC(),
		Environment: ReportEnvironment{BaseURL: strings.TrimRight(runner.BaseURL, "/")},
	}
	if runner.HTTPClient == nil {
		runner.HTTPClient = &http.Client{Timeout: 2 * time.Minute}
	}
	if report.Environment.BaseURL == "" {
		report.Environment.BaseURL = "http://127.0.0.1:8080"
	}
	tokens := make(map[string]string, len(suite.Identities))
	for name, identity := range suite.Identities {
		password := runner.Passwords[name]
		if password == "" && identity.PasswordEnv != "" {
			password = os.Getenv(identity.PasswordEnv)
		}
		if password == "" {
			return Report{}, fmt.Errorf("password for identity %q is required via %s", name, identity.PasswordEnv)
		}
		token, err := runner.login(ctx, report.Environment.BaseURL, identity.Email, password)
		if err != nil {
			return Report{}, fmt.Errorf("login identity %q: %w", name, err)
		}
		tokens[name] = token
	}
	if seed {
		if err := runner.seed(ctx, report.Environment.BaseURL, suite.Seeds); err != nil {
			return Report{}, err
		}
	}
	for _, test := range suite.VisibilityCases {
		result, err := runner.runVisibility(ctx, report.Environment.BaseURL, tokens[test.Identity], test)
		if err != nil {
			return Report{}, err
		}
		report.Results = append(report.Results, result)
	}
	var relevanceCases, hits int
	var reciprocalRank float64
	var latencies []float64
	for _, test := range suite.SearchCases {
		result, counters, environment, err := runner.runSearch(ctx, report.Environment.BaseURL, tokens[test.Identity], test)
		if err != nil {
			return Report{}, err
		}
		report.Results = append(report.Results, result)
		report.UnauthorizedRetrievals += counters.unauthorized
		report.ForbiddenFactHits += counters.forbiddenFacts
		report.FilterViolations += counters.filter
		report.ContractViolations += counters.contract
		if len(test.RelevantDocumentIDs) > 0 && test.ExpectedStatus == http.StatusOK {
			relevanceCases++
			if result.FirstRelevantRank > 0 {
				hits++
			}
			reciprocalRank += result.ReciprocalRank
		}
		latencies = append(latencies, result.LatencyMS)
		if environment.Collection != "" {
			report.Environment.Collection = environment.Collection
			report.Environment.Embedder = environment.Embedder
			report.Environment.Dimensions = environment.Dimensions
		}
	}
	report.Cases = len(report.Results)
	report.SearchCases = len(suite.SearchCases)
	for _, result := range report.Results {
		if result.Passed {
			report.PassedCases++
		}
	}
	report.FailedCases = report.Cases - report.PassedCases
	report.Passed = report.FailedCases == 0 && report.UnauthorizedRetrievals == 0 &&
		report.ForbiddenFactHits == 0 && report.FilterViolations == 0 && report.ContractViolations == 0
	if relevanceCases > 0 {
		report.HitRateAtK = float64(hits) / float64(relevanceCases)
		report.MRR = reciprocalRank / float64(relevanceCases)
	}
	report.LatencyP50MS = percentile(latencies, 0.50)
	report.LatencyP95MS = percentile(latencies, 0.95)
	report.CompletedAt = time.Now().UTC()
	return report, nil
}

func (runner Runner) login(ctx context.Context, baseURL, email, password string) (string, error) {
	status, body, _, err := runner.request(ctx, http.MethodPost, baseURL+"/api/v1/auth/login", "", map[string]string{
		"email": email, "password": password,
	})
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("HTTP %d: %s", status, compact(body))
	}
	var response tokenResponse
	if err := json.Unmarshal(body, &response); err != nil || response.AccessToken == "" {
		return "", fmt.Errorf("invalid login response")
	}
	return response.AccessToken, nil
}

func (runner Runner) seed(ctx context.Context, baseURL string, seeds []milvus.LifecycleChange) error {
	if len(seeds) == 0 {
		return nil
	}
	status, body, _, err := runner.request(ctx, http.MethodPost, baseURL+"/api/v1/auth/dev-token", "", map[string]string{"persona": "platform_admin"})
	if err != nil {
		return fmt.Errorf("request platform seed token: %w", err)
	}
	if status != http.StatusOK {
		return fmt.Errorf("platform seed token HTTP %d: %s", status, compact(body))
	}
	var token tokenResponse
	if err := json.Unmarshal(body, &token); err != nil || token.AccessToken == "" {
		return fmt.Errorf("invalid platform seed token response")
	}
	for _, change := range seeds {
		for attempt := 1; attempt <= 4; attempt++ {
			status, body, _, err = runner.request(ctx, http.MethodPost, baseURL+"/api/v1/milvus/lifecycle/apply", token.AccessToken, change)
			if err != nil {
				return fmt.Errorf("seed event %q: %w", change.EventID, err)
			}
			if status == http.StatusOK {
				break
			}
			if !retryableMilvusRateLimit(status, body) || attempt == 4 {
				return fmt.Errorf("seed event %q HTTP %d: %s", change.EventID, status, compact(body))
			}
			timer := time.NewTimer(time.Duration(attempt) * 11 * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return fmt.Errorf("seed event %q retry: %w", change.EventID, ctx.Err())
			case <-timer.C:
			}
		}
	}
	return nil
}

func retryableMilvusRateLimit(status int, body []byte) bool {
	if status != http.StatusTooManyRequests && status != http.StatusUnprocessableEntity &&
		status != http.StatusServiceUnavailable {
		return false
	}
	message := strings.ToLower(string(body))
	return strings.Contains(message, "rate limit") || strings.Contains(message, "ratelimiter")
}

func (runner Runner) runVisibility(ctx context.Context, baseURL, token string, test VisibilityCase) (CaseResult, error) {
	started := time.Now()
	status, body, _, err := runner.request(ctx, http.MethodGet, baseURL+"/api/v1/datasets", token, nil)
	if err != nil {
		return CaseResult{}, fmt.Errorf("visibility case %q: %w", test.ID, err)
	}
	result := CaseResult{ID: test.ID, Type: "dataset_visibility", HTTPStatus: status, LatencyMS: elapsed(started)}
	if status != http.StatusOK {
		result.Failures = append(result.Failures, fmt.Sprintf("expected HTTP 200, got %d", status))
		result.Passed = false
		return result, nil
	}
	var response datasetListResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return CaseResult{}, fmt.Errorf("visibility case %q response: %w", test.ID, err)
	}
	visible := make(map[string]struct{}, len(response.Datasets))
	for _, dataset := range response.Datasets {
		visible[dataset.ID] = struct{}{}
	}
	for _, id := range test.RequiredDatasets {
		if _, ok := visible[id]; !ok {
			result.Failures = append(result.Failures, "required dataset not visible: "+id)
		}
	}
	for _, id := range test.ForbiddenDatasets {
		if _, ok := visible[id]; ok {
			result.Failures = append(result.Failures, "forbidden dataset visible: "+id)
		}
	}
	result.Passed = len(result.Failures) == 0
	return result, nil
}

type violationCounters struct {
	unauthorized, forbiddenFacts, filter, contract int
}

func (runner Runner) runSearch(ctx context.Context, baseURL, token string, test SearchCase) (CaseResult, violationCounters, ReportEnvironment, error) {
	started := time.Now()
	status, body, _, err := runner.request(ctx, http.MethodPost, baseURL+"/api/v1/datasets/"+test.DatasetID+"/search", token, map[string]any{
		"query": test.Query, "top_k": test.TopK,
	})
	if err != nil {
		return CaseResult{}, violationCounters{}, ReportEnvironment{}, fmt.Errorf("search case %q: %w", test.ID, err)
	}
	result := CaseResult{ID: test.ID, Type: "dataset_search", HTTPStatus: status, LatencyMS: elapsed(started)}
	var counters violationCounters
	if status != test.ExpectedStatus {
		result.Failures = append(result.Failures, fmt.Sprintf("expected HTTP %d, got %d", test.ExpectedStatus, status))
		counters.contract++
	}
	if status != http.StatusOK {
		var response errorResponse
		if err := json.Unmarshal(body, &response); err != nil {
			result.Failures = append(result.Failures, "error response is not valid JSON")
			counters.contract++
		} else if test.ExpectedErrorCode != "" && response.Error.Code != test.ExpectedErrorCode {
			result.Failures = append(result.Failures, fmt.Sprintf("expected error code %q, got %q", test.ExpectedErrorCode, response.Error.Code))
			counters.contract++
		}
		result.Passed = len(result.Failures) == 0
		return result, counters, ReportEnvironment{}, nil
	}
	var response datasetSearchResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return CaseResult{}, counters, ReportEnvironment{}, fmt.Errorf("search case %q response: %w", test.ID, err)
	}
	result.Filter = response.Result.Filter
	relevant := stringSet(test.RelevantDocumentIDs)
	for index, hit := range response.Result.Hits {
		result.RetrievedDocIDs = appendUnique(result.RetrievedDocIDs, hit.DocumentID)
		result.RetrievedHits = append(result.RetrievedHits, HitObservation{
			Rank: index + 1, DocumentID: hit.DocumentID, TenantID: hit.TenantID,
			Visibility: hit.Visibility, Score: hit.Distance,
		})
		if _, ok := relevant[hit.DocumentID]; ok && result.FirstRelevantRank == 0 {
			result.FirstRelevantRank = index + 1
			result.ReciprocalRank = 1 / float64(index+1)
		}
		if contains(test.ForbiddenDocumentIDs, hit.DocumentID) {
			result.Failures = append(result.Failures, "forbidden document retrieved: "+hit.DocumentID)
			counters.unauthorized++
		}
		content := strings.ToLower(hit.Content)
		for _, fact := range test.ForbiddenFacts {
			if strings.Contains(content, strings.ToLower(fact)) {
				result.Failures = append(result.Failures, "forbidden fact retrieved: "+fact)
				counters.forbiddenFacts++
			}
		}
		if test.ExpectedVisibility != "" && hit.Visibility != test.ExpectedVisibility {
			result.Failures = append(result.Failures, fmt.Sprintf("hit %s visibility=%q", hit.DocumentID, hit.Visibility))
			counters.unauthorized++
		}
		if test.ExpectedTenant != "" && hit.TenantID != test.ExpectedTenant {
			result.Failures = append(result.Failures, fmt.Sprintf("hit %s tenant=%q", hit.DocumentID, hit.TenantID))
			counters.unauthorized++
		}
	}
	if len(relevant) > 0 && result.FirstRelevantRank == 0 {
		result.Failures = append(result.Failures, "no relevant document in top-k")
	}
	if test.MaxFirstRelevantRank > 0 && (result.FirstRelevantRank == 0 || result.FirstRelevantRank > test.MaxFirstRelevantRank) {
		result.Failures = append(result.Failures, fmt.Sprintf("first relevant rank=%d exceeds %d", result.FirstRelevantRank, test.MaxFirstRelevantRank))
	}
	allContent := strings.ToLower(string(body))
	for _, fact := range test.RequiredFacts {
		if !strings.Contains(allContent, strings.ToLower(fact)) {
			result.Failures = append(result.Failures, "required fact missing: "+fact)
		}
	}
	for _, fragment := range test.RequiredFilterFragments {
		if !strings.Contains(result.Filter, fragment) {
			result.Failures = append(result.Failures, "required filter fragment missing: "+fragment)
			counters.filter++
		}
	}
	for _, fragment := range test.ForbiddenFilterFragments {
		if strings.Contains(result.Filter, fragment) {
			result.Failures = append(result.Failures, "forbidden filter fragment present: "+fragment)
			counters.filter++
		}
	}
	result.Passed = len(result.Failures) == 0
	return result, counters, ReportEnvironment{
		Collection: response.Result.Collection, Embedder: response.Result.Embedder, Dimensions: response.Result.Dimensions,
	}, nil
}

func (runner Runner) request(ctx context.Context, method, url, token string, payload any) (int, []byte, http.Header, error) {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, nil, err
		}
		body = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return 0, nil, nil, err
	}
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := runner.HTTPClient.Do(request)
	if err != nil {
		return 0, nil, nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	return response.StatusCode, data, response.Header, err
}

func MarshalReport(report Report) ([]byte, error) {
	return json.MarshalIndent(report, "", "  ")
}

func Markdown(report Report) string {
	status := "PASS"
	if !report.Passed {
		status = "FAIL"
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "# Dataset Search Harness 报告\n\n")
	fmt.Fprintf(&builder, "- 结果：**%s**\n- Suite：`%s` (`%s`)\n- API：`%s`\n", status, report.Suite, report.Version, report.Environment.BaseURL)
	fmt.Fprintf(&builder, "- Milvus：`%s`，Embedding：`%s`，维度：`%d`\n", report.Environment.Collection, report.Environment.Embedder, report.Environment.Dimensions)
	fmt.Fprintf(&builder, "- 用例：%d（通过 %d / 失败 %d）\n\n", report.Cases, report.PassedCases, report.FailedCases)
	fmt.Fprintf(&builder, "## 核心指标\n\n| Hit@K | MRR | P50 | P95 | 越权召回 | 禁止事实 | Filter 违规 | API 契约违规 |\n|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	fmt.Fprintf(&builder, "| %.3f | %.3f | %.1f ms | %.1f ms | %d | %d | %d | %d |\n\n",
		report.HitRateAtK, report.MRR, report.LatencyP50MS, report.LatencyP95MS,
		report.UnauthorizedRetrievals, report.ForbiddenFactHits, report.FilterViolations, report.ContractViolations)
	fmt.Fprintf(&builder, "## 用例明细\n\n| 用例 | 类型 | 结果 | HTTP | 首个相关排名 | 延迟 | 召回文档（Milvus score） |\n|---|---|---|---:|---:|---:|---|\n")
	for _, result := range report.Results {
		caseStatus := "PASS"
		if !result.Passed {
			caseStatus = "FAIL: " + strings.Join(result.Failures, "; ")
		}
		hits := make([]string, 0, len(result.RetrievedHits))
		for _, hit := range result.RetrievedHits {
			hits = append(hits, fmt.Sprintf("%s=%.4f", hit.DocumentID, hit.Score))
		}
		fmt.Fprintf(&builder, "| `%s` | %s | %s | %d | %d | %.1f ms | `%s` |\n",
			result.ID, result.Type, caseStatus, result.HTTPStatus, result.FirstRelevantRank, result.LatencyMS, strings.Join(hits, ", "))
	}
	fmt.Fprintf(&builder, "\n## 判定说明\n\n")
	fmt.Fprintf(&builder, "- 相关性使用文档 ID 计算 Hit@K 与 MRR；Milvus 相似度只记录，不设脆弱的固定阈值。\n")
	fmt.Fprintf(&builder, "- 权限门禁同时检查数据集可见性、跨租户 HTTP 404、结果 tenant/visibility、禁止文档与禁止事实。\n")
	fmt.Fprintf(&builder, "- Filter 检查的是服务端最终生成表达式，客户端不能提交或放宽 `AccessScope`。\n")
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

func compact(value []byte) string {
	return strings.TrimSpace(strings.ReplaceAll(string(value), "\n", " "))
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func contains(values []string, target string) bool {
	_, ok := stringSet(values)[target]
	return ok
}

func appendUnique(values []string, value string) []string {
	if contains(values, value) {
		return values
	}
	return append(values, value)
}
