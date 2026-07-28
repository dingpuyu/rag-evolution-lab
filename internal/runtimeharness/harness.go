// Package runtimeharness exercises the enterprise runtime through its public
// HTTP contract.  It intentionally does not import the server implementation:
// the report is useful against a locally running stack, a container, or a
// staging deployment and catches wiring errors between control and data plane.
package runtimeharness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	BuildQueued    = "queued"
	BuildRunning   = "running"
	BuildCompleted = "completed"
	BuildFailed    = "failed"
)

// Config describes the application boundary to verify. Build and Publish are
// explicit because they create control-plane records; a read-only run is safe
// to execute in CI or against a shared environment.
type Config struct {
	BaseURL           string
	Email             string
	Password          string
	ApplicationID     string
	EnvironmentID     string
	Collection        string
	BuildVersion      string
	EmbeddingModel    string
	EmbeddingVer      string
	ChunkerVersion    string
	SourceRevision    int64
	CrossAppID        string
	Build             bool
	Publish           bool
	RateLimitRequests int
	HTTPClient        *http.Client
	PollInterval      time.Duration
	Timeout           time.Duration
}

type Report struct {
	Suite       string       `json:"suite"`
	StartedAt   time.Time    `json:"started_at"`
	CompletedAt time.Time    `json:"completed_at"`
	Passed      bool         `json:"passed"`
	Cases       int          `json:"cases"`
	PassedCases int          `json:"passed_cases"`
	FailedCases int          `json:"failed_cases"`
	Application Application  `json:"application"`
	Build       *Build       `json:"build,omitempty"`
	Release     *Release     `json:"release,omitempty"`
	Results     []CaseResult `json:"results"`
}

type Application struct {
	ID          string `json:"app_id"`
	Environment string `json:"environment_id"`
	Collection  string `json:"collection,omitempty"`
	TraceID     string `json:"trace_id,omitempty"`
}

type CaseResult struct {
	ID        string   `json:"id"`
	Passed    bool     `json:"passed"`
	Expected  int      `json:"expected_status"`
	Actual    int      `json:"actual_status"`
	LatencyMS float64  `json:"latency_ms"`
	Details   string   `json:"details,omitempty"`
	Failures  []string `json:"failures,omitempty"`
}

type Build struct {
	BuildID        string    `json:"build_id"`
	IdempotencyKey string    `json:"idempotency_key"`
	ApplicationID  string    `json:"app_id"`
	EnvironmentID  string    `json:"environment_id"`
	Version        string    `json:"version"`
	Collection     string    `json:"collection"`
	Status         string    `json:"status"`
	Stage          string    `json:"stage"`
	Attempts       int       `json:"attempts"`
	LastError      string    `json:"last_error,omitempty"`
	Manifest       *Manifest `json:"manifest,omitempty"`
}

type Manifest struct {
	BuildID        string    `json:"build_id"`
	Version        string    `json:"version"`
	Collection     string    `json:"collection"`
	EmbeddingModel string    `json:"embedding_model"`
	EmbeddingVer   string    `json:"embedding_version"`
	RowCount       int64     `json:"row_count"`
	Dimensions     int       `json:"dimensions"`
	SchemaHash     string    `json:"schema_hash"`
	ManifestHash   string    `json:"manifest_hash"`
	ValidatedAt    time.Time `json:"validated_at"`
}

type Release struct {
	ReleaseID      string `json:"release_id"`
	Version        string `json:"version"`
	Collection     string `json:"collection"`
	State          string `json:"state"`
	Channel        string `json:"channel"`
	RolloutPercent int    `json:"rollout_percent"`
}

type gatewayResponse struct {
	AppID         string `json:"app_id"`
	EnvironmentID string `json:"environment_id"`
	TraceID       string `json:"trace_id"`
	Result        struct {
		Hits []json.RawMessage `json:"hits"`
	} `json:"result"`
}

type traceResponse struct {
	TraceID       string  `json:"trace_id"`
	AppID         string  `json:"app_id"`
	EnvironmentID string  `json:"environment_id"`
	Status        string  `json:"status"`
	IndexVersion  string  `json:"index_version"`
	TotalCostUSD  float64 `json:"total_cost_usd"`
}

type credentialResponse struct {
	Credential struct {
		CredentialID string   `json:"credential_id"`
		Scopes       []string `json:"scopes"`
		Status       string   `json:"status"`
	} `json:"credential"`
	Secret string `json:"secret"`
}

type errorEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type client struct {
	base string
	http *http.Client
}

type response struct {
	status int
	body   []byte
}

// Run executes the contract checks and returns a report even when an
// assertion fails. Transport/configuration errors are returned separately.
func Run(ctx context.Context, config Config) (Report, error) {
	started := time.Now().UTC()
	config.normalize()
	report := Report{Suite: "enterprise-runtime-v1", StartedAt: started, Application: Application{ID: config.ApplicationID, Environment: config.EnvironmentID, Collection: config.Collection}}
	api := client{base: strings.TrimRight(config.BaseURL, "/"), http: config.HTTPClient}
	if api.http == nil {
		api.http = &http.Client{Timeout: config.Timeout}
	}
	ctx, cancel := context.WithTimeout(ctx, config.Timeout)
	defer cancel()
	token, err := api.login(ctx, config.Email, config.Password)
	if err != nil {
		return Report{}, fmt.Errorf("runtime harness login: %w", err)
	}

	if config.Build {
		build, result, buildErr := api.build(ctx, token, config)
		report.Results = append(report.Results, result)
		if buildErr != nil {
			return finish(report), buildErr
		}
		report.Build = &build
		if build.Manifest != nil {
			report.Application.Collection = build.Manifest.Collection
		}
		if config.Publish {
			release, publishResults, publishErr := api.publishAndRollback(ctx, token, config, build)
			report.Results = append(report.Results, publishResults...)
			if publishErr != nil {
				return finish(report), publishErr
			}
			report.Release = &release
		}
	}

	query, queryCase, err := api.query(ctx, token, config)
	if err != nil {
		return Report{}, err
	}
	report.Results = append(report.Results, queryCase)
	report.Application.TraceID = query.TraceID
	traceCase := api.trace(ctx, token, config, query.TraceID)
	report.Results = append(report.Results, traceCase)

	credential, createCase, err := api.createCredential(ctx, token, config)
	if err != nil {
		return Report{}, err
	}
	report.Results = append(report.Results, createCase)
	credentialQuery := api.credentialRequest(ctx, credential.Secret, http.MethodPost, "/api/v1/apps/"+config.ApplicationID+"/query", map[string]any{"environment_id": config.EnvironmentID, "query": "验证应用凭证查询权限", "top_k": 1}, http.StatusOK, "credential_query_allowed")
	report.Results = append(report.Results, credentialQuery)
	credentialAnswer := api.credentialRequest(ctx, credential.Secret, http.MethodPost, "/api/v1/apps/"+config.ApplicationID+"/answer", map[string]any{"environment_id": config.EnvironmentID, "query": "验证 answer scope", "top_k": 1}, http.StatusUnprocessableEntity, "credential_answer_scope_denied")
	report.Results = append(report.Results, credentialAnswer)
	if config.CrossAppID != "" {
		cross := api.credentialRequest(ctx, credential.Secret, http.MethodPost, "/api/v1/apps/"+config.CrossAppID+"/query", map[string]any{"environment_id": config.CrossAppID + "-dev", "query": "验证跨应用隔离", "top_k": 1}, http.StatusForbidden, "credential_cross_app_denied")
		report.Results = append(report.Results, cross)
	}
	if config.RateLimitRequests > 0 {
		rateLimit := api.rateLimitProbe(ctx, credential.Secret, config, config.RateLimitRequests)
		report.Results = append(report.Results, rateLimit)
	}
	revoke := api.revokeCredential(ctx, token, config, credential.Credential.CredentialID)
	report.Results = append(report.Results, revoke)
	revoked := api.credentialRequest(ctx, credential.Secret, http.MethodPost, "/api/v1/apps/"+config.ApplicationID+"/query", map[string]any{"environment_id": config.EnvironmentID, "query": "验证已撤销凭证", "top_k": 1}, http.StatusUnauthorized, "revoked_credential_denied")
	report.Results = append(report.Results, revoked)

	return finish(report), nil
}

func (config *Config) normalize() {
	if strings.TrimSpace(config.BaseURL) == "" {
		config.BaseURL = "http://127.0.0.1:8080"
	}
	if strings.TrimSpace(config.ApplicationID) == "" {
		config.ApplicationID = "tenant_a-support-agent"
	}
	if strings.TrimSpace(config.EnvironmentID) == "" {
		config.EnvironmentID = config.ApplicationID + "-dev"
	}
	if strings.TrimSpace(config.Collection) == "" {
		config.Collection = "raglab_lifecycle_v1"
	}
	if strings.TrimSpace(config.CrossAppID) == "" && strings.HasPrefix(config.ApplicationID, "tenant_a-") {
		config.CrossAppID = "tenant_b-support-agent"
	}
	if strings.TrimSpace(config.BuildVersion) == "" {
		config.BuildVersion = "harness-" + time.Now().UTC().Format("20060102-150405")
	}
	if config.PollInterval <= 0 {
		config.PollInterval = 500 * time.Millisecond
	}
	if config.Timeout <= 0 {
		config.Timeout = 5 * time.Minute
	}
}

func finish(report Report) Report {
	report.CompletedAt = time.Now().UTC()
	report.Cases = len(report.Results)
	for _, result := range report.Results {
		if result.Passed {
			report.PassedCases++
		}
	}
	report.FailedCases = report.Cases - report.PassedCases
	report.Passed = report.FailedCases == 0
	return report
}

func (api client) login(ctx context.Context, email, password string) (string, error) {
	res, err := api.do(ctx, http.MethodPost, "/api/v1/auth/login", "", map[string]string{"email": email, "password": password})
	if err != nil {
		return "", err
	}
	if res.status != http.StatusOK {
		return "", fmt.Errorf("HTTP %d: %s", res.status, describe(res.body))
	}
	var body struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(res.body, &body); err != nil || body.AccessToken == "" {
		return "", fmt.Errorf("login response does not contain access_token")
	}
	return body.AccessToken, nil
}

func (api client) build(ctx context.Context, token string, config Config) (Build, CaseResult, error) {
	started := time.Now()
	path := "/api/v1/apps/" + config.ApplicationID + "/environments/" + config.EnvironmentID + "/index-builds"
	body := map[string]any{
		"idempotency_key": "runtime-harness-" + config.BuildVersion,
		"version":         config.BuildVersion, "collection": config.Collection,
		"embedding_model": config.EmbeddingModel, "embedding_version": config.EmbeddingVer,
		"chunker_version": config.ChunkerVersion, "source_revision": config.SourceRevision,
	}
	res, err := api.do(ctx, http.MethodPost, path, "Bearer "+token, body)
	result := CaseResult{ID: "index_build_completed", Expected: http.StatusOK, LatencyMS: elapsed(started)}
	if err != nil {
		result.Failures = []string{err.Error()}
		return Build{}, result, err
	}
	if res.status != http.StatusAccepted && res.status != http.StatusOK {
		result.Actual, result.Failures = res.status, []string{describe(res.body)}
		return Build{}, result, fmt.Errorf("index build submit HTTP %d: %s", res.status, describe(res.body))
	}
	var envelope struct {
		Build Build `json:"build"`
	}
	if err := json.Unmarshal(res.body, &envelope); err != nil {
		return Build{}, result, fmt.Errorf("decode index build: %w", err)
	}
	build := envelope.Build
	deadline := time.Now().Add(config.Timeout)
	for build.Status == BuildQueued || build.Status == BuildRunning {
		if time.Now().After(deadline) {
			result.Failures = []string{"timed out waiting for index build"}
			return build, result, fmt.Errorf("index build %s timed out", build.BuildID)
		}
		select {
		case <-ctx.Done():
			return build, result, ctx.Err()
		case <-time.After(config.PollInterval):
		}
		res, err = api.do(ctx, http.MethodGet, "/api/v1/apps/"+config.ApplicationID+"/index-builds/"+build.BuildID, "Bearer "+token, nil)
		if err != nil {
			return build, result, err
		}
		if res.status != http.StatusOK {
			return build, result, fmt.Errorf("index build detail HTTP %d: %s", res.status, describe(res.body))
		}
		if err := json.Unmarshal(res.body, &build); err != nil {
			return build, result, fmt.Errorf("decode index build detail: %w", err)
		}
	}
	result.Actual = statusCodeForBuild(build.Status)
	result.Details = fmt.Sprintf("status=%s stage=%s attempts=%d rows=%d dimensions=%d manifest=%s", build.Status, build.Stage, build.Attempts, manifestRows(build.Manifest), manifestDimensions(build.Manifest), manifestHash(build.Manifest))
	if build.Status != BuildCompleted || build.Manifest == nil || build.Manifest.ManifestHash == "" || build.Manifest.SchemaHash == "" {
		result.Failures = []string{fmt.Sprintf("expected completed build with manifest, got status=%s error=%s", build.Status, build.LastError)}
	}
	result.Passed = len(result.Failures) == 0
	if !result.Passed {
		return build, result, fmt.Errorf("index build failed: %s", strings.Join(result.Failures, "; "))
	}
	return build, result, nil
}

func (api client) publishAndRollback(ctx context.Context, token string, config Config, build Build) (Release, []CaseResult, error) {
	if build.Manifest == nil {
		return Release{}, nil, fmt.Errorf("cannot publish an index without a manifest")
	}
	path := "/api/v1/apps/" + config.ApplicationID + "/environments/" + config.EnvironmentID + "/indexes/publish"
	started := time.Now()
	res, err := api.do(ctx, http.MethodPost, path, "Bearer "+token, map[string]any{"environment_id": config.EnvironmentID, "version": build.Version, "collection": build.Collection, "channel": "stable", "rollout_percent": 100})
	first := CaseResult{ID: "stable_release_published", Expected: http.StatusCreated, LatencyMS: elapsed(started)}
	if err != nil {
		first.Failures = []string{err.Error()}
		return Release{}, []CaseResult{first}, err
	}
	first.Actual = res.status
	var stable Release
	if res.status == http.StatusCreated {
		_ = json.Unmarshal(res.body, &stable)
		first.Details = fmt.Sprintf("release=%s state=%s channel=%s rollout=%d%%", stable.ReleaseID, stable.State, stable.Channel, stable.RolloutPercent)
	} else {
		first.Failures = []string{describe(res.body)}
	}
	first.Passed = len(first.Failures) == 0
	if !first.Passed {
		return stable, []CaseResult{first}, fmt.Errorf("stable release failed: %s", strings.Join(first.Failures, "; "))
	}
	// A second stable publish makes the first release a real rollback target.
	secondVersion := build.Version + "-next"
	res, err = api.do(ctx, http.MethodPost, path, "Bearer "+token, map[string]any{"environment_id": config.EnvironmentID, "version": secondVersion, "collection": build.Collection, "channel": "stable", "rollout_percent": 100})
	second := CaseResult{ID: "stable_release_superseded", Expected: http.StatusCreated}
	if err != nil {
		second.Failures = []string{err.Error()}
	} else {
		second.Actual = res.status
		second.Passed = res.status == http.StatusCreated
		if !second.Passed {
			second.Failures = []string{describe(res.body)}
		}
	}
	rollbackStarted := time.Now()
	rollbackPath := "/api/v1/apps/" + config.ApplicationID + "/environments/" + config.EnvironmentID + "/indexes/rollback"
	res, err = api.do(ctx, http.MethodPost, rollbackPath, "Bearer "+token, map[string]string{"release_id": stable.ReleaseID})
	rollback := CaseResult{ID: "stable_release_rollback", Expected: http.StatusOK, LatencyMS: elapsed(rollbackStarted)}
	if err != nil {
		rollback.Failures = []string{err.Error()}
	} else {
		rollback.Actual = res.status
		rollback.Passed = res.status == http.StatusOK
		if !rollback.Passed {
			rollback.Failures = []string{describe(res.body)}
		}
	}
	if !second.Passed || !rollback.Passed {
		return stable, []CaseResult{first, second, rollback}, fmt.Errorf("release lifecycle assertion failed")
	}
	return stable, []CaseResult{first, second, rollback}, nil
}

func (api client) query(ctx context.Context, token string, config Config) (gatewayResponse, CaseResult, error) {
	started := time.Now()
	path := "/api/v1/apps/" + config.ApplicationID + "/query"
	res, err := api.do(ctx, http.MethodPost, path, "Bearer "+token, map[string]any{"environment_id": config.EnvironmentID, "query": "验证企业知识库查询链路", "top_k": 3})
	result := CaseResult{ID: "gateway_query_and_trace_id", Expected: http.StatusOK, LatencyMS: elapsed(started)}
	if err != nil {
		result.Failures = []string{err.Error()}
		return gatewayResponse{}, result, err
	}
	result.Actual = res.status
	var body gatewayResponse
	if res.status == http.StatusOK {
		if err := json.Unmarshal(res.body, &body); err != nil {
			result.Failures = []string{fmt.Sprintf("decode gateway response: %v", err)}
		} else if body.TraceID == "" || body.AppID != config.ApplicationID || body.EnvironmentID != config.EnvironmentID {
			result.Failures = []string{"gateway response did not contain the expected app/environment/trace boundary"}
		}
	} else {
		result.Failures = []string{describe(res.body)}
	}
	result.Passed = len(result.Failures) == 0
	return body, result, nil
}

func (api client) trace(ctx context.Context, token string, config Config, traceID string) CaseResult {
	started := time.Now()
	result := CaseResult{ID: "query_trace_persisted", Expected: http.StatusOK, LatencyMS: elapsed(started)}
	if traceID == "" {
		result.Failures = []string{"query did not return trace_id"}
		return result
	}
	res, err := api.do(ctx, http.MethodGet, "/api/v1/apps/"+config.ApplicationID+"/traces/"+traceID, "Bearer "+token, nil)
	result.LatencyMS = elapsed(started)
	if err != nil {
		result.Failures = []string{err.Error()}
		return result
	}
	result.Actual = res.status
	var body traceResponse
	if res.status == http.StatusOK {
		if err := json.Unmarshal(res.body, &body); err != nil {
			result.Failures = []string{fmt.Sprintf("decode trace: %v", err)}
		} else if body.TraceID != traceID || body.AppID != config.ApplicationID || body.EnvironmentID != config.EnvironmentID || body.Status == "" {
			result.Failures = []string{"persisted trace does not match query boundary or status"}
		}
	} else {
		result.Failures = []string{describe(res.body)}
	}
	result.Passed = len(result.Failures) == 0
	return result
}

func (api client) createCredential(ctx context.Context, token string, config Config) (credentialResponse, CaseResult, error) {
	started := time.Now()
	res, err := api.do(ctx, http.MethodPost, "/api/v1/apps/"+config.ApplicationID+"/credentials", "Bearer "+token, map[string]any{"name": "runtime-harness-query", "scopes": []string{"rag:query"}})
	result := CaseResult{ID: "application_credential_created", Expected: http.StatusCreated, LatencyMS: elapsed(started)}
	if err != nil {
		result.Failures = []string{err.Error()}
		return credentialResponse{}, result, err
	}
	result.Actual = res.status
	var body credentialResponse
	if res.status == http.StatusCreated {
		if err := json.Unmarshal(res.body, &body); err != nil {
			result.Failures = []string{fmt.Sprintf("decode credential: %v", err)}
		} else if body.Credential.CredentialID == "" || body.Secret == "" || !contains(body.Credential.Scopes, "rag:query") {
			result.Failures = []string{"credential response did not contain one-time secret and rag:query scope"}
		}
	} else {
		result.Failures = []string{describe(res.body)}
	}
	result.Passed = len(result.Failures) == 0
	if !result.Passed {
		return body, result, fmt.Errorf("credential creation failed: %s", strings.Join(result.Failures, "; "))
	}
	return body, result, nil
}

func (api client) revokeCredential(ctx context.Context, token string, config Config, credentialID string) CaseResult {
	started := time.Now()
	res, err := api.do(ctx, http.MethodPost, "/api/v1/apps/"+config.ApplicationID+"/credentials/"+credentialID+"/revoke", "Bearer "+token, nil)
	result := CaseResult{ID: "application_credential_revoked", Expected: http.StatusOK, LatencyMS: elapsed(started)}
	if err != nil {
		result.Failures = []string{err.Error()}
		return result
	}
	result.Actual = res.status
	result.Passed = res.status == http.StatusOK
	if !result.Passed {
		result.Failures = []string{describe(res.body)}
	}
	return result
}

func (api client) rateLimitProbe(ctx context.Context, secret string, config Config, requests int) CaseResult {
	started := time.Now()
	result := CaseResult{ID: "rate_limit_429_contract", Expected: http.StatusTooManyRequests}
	if requests < 1 {
		requests = 1
	}
	path := "/api/v1/apps/" + config.ApplicationID + "/query"
	for attempt := 1; attempt <= requests; attempt++ {
		res, err := api.do(ctx, http.MethodPost, path, "AppCredential "+secret, map[string]any{"environment_id": config.EnvironmentID, "query": "验证限流", "top_k": 1})
		if err != nil {
			result.Failures = []string{err.Error()}
			break
		}
		if res.status == http.StatusTooManyRequests {
			result.Actual, result.Passed = res.status, true
			result.Details = fmt.Sprintf("received 429 at request %d/%d", attempt, requests)
			break
		}
		result.Actual = res.status
	}
	result.LatencyMS = elapsed(started)
	if !result.Passed && len(result.Failures) == 0 {
		result.Failures = []string{fmt.Sprintf("no 429 received within %d requests; start the server with a low burst for this probe", requests)}
	}
	return result
}

func (api client) credentialRequest(ctx context.Context, secret, method, path string, payload any, expected int, id string) CaseResult {
	started := time.Now()
	res, err := api.do(ctx, method, path, "AppCredential "+secret, payload)
	result := CaseResult{ID: id, Expected: expected, LatencyMS: elapsed(started)}
	if err != nil {
		result.Failures = []string{err.Error()}
		return result
	}
	result.Actual = res.status
	result.Passed = res.status == expected
	if !result.Passed {
		result.Failures = []string{describe(res.body)}
	} else if expected >= 400 {
		var envelope errorEnvelope
		if json.Unmarshal(res.body, &envelope) == nil && envelope.Error.Code != "" {
			result.Details = envelope.Error.Code
		}
	}
	return result
}

func (api client) do(ctx context.Context, method, path, authorization string, payload any) (response, error) {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return response{}, err
		}
		body = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, api.base+path, body)
	if err != nil {
		return response{}, err
	}
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	res, err := api.http.Do(request)
	if err != nil {
		return response{}, err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	return response{status: res.StatusCode, body: data}, err
}

func statusCodeForBuild(status string) int {
	if status == BuildCompleted {
		return http.StatusOK
	}
	return http.StatusUnprocessableEntity
}

func manifestRows(manifest *Manifest) int64 {
	if manifest == nil {
		return 0
	}
	return manifest.RowCount
}

func manifestDimensions(manifest *Manifest) int {
	if manifest == nil {
		return 0
	}
	return manifest.Dimensions
}

func manifestHash(manifest *Manifest) string {
	if manifest == nil {
		return ""
	}
	return manifest.ManifestHash
}

func elapsed(start time.Time) float64 { return float64(time.Since(start).Microseconds()) / 1000 }

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func describe(data []byte) string {
	var envelope errorEnvelope
	if json.Unmarshal(data, &envelope) == nil && envelope.Error.Code != "" {
		if envelope.Error.Message != "" {
			return envelope.Error.Code + ": " + envelope.Error.Message
		}
		return envelope.Error.Code
	}
	text := strings.TrimSpace(strings.ReplaceAll(string(data), "\n", " "))
	if len(text) > 240 {
		text = text[:240] + "…"
	}
	return text
}

func MarshalReport(report Report) ([]byte, error) { return json.MarshalIndent(report, "", "  ") }

func Markdown(report Report) string {
	status := "PASS"
	if !report.Passed {
		status = "FAIL"
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "# Enterprise Runtime Harness 报告\n\n- 结果：**%s**\n- 应用：`%s`\n- 环境：`%s`\n- 用例：%d（通过 %d / 失败 %d）\n\n", status, report.Application.ID, report.Application.Environment, report.Cases, report.PassedCases, report.FailedCases)
	fmt.Fprintln(&builder, "| 用例 | 结果 | HTTP | 延迟 | 说明 |")
	fmt.Fprintln(&builder, "|---|---|---:|---:|---|")
	for _, result := range report.Results {
		state := "PASS"
		if !result.Passed {
			state = "FAIL: " + strings.Join(result.Failures, "; ")
		}
		fmt.Fprintf(&builder, "| `%s` | %s | %d (expect %d) | %.1f ms | %s |\n", result.ID, state, result.Actual, result.Expected, result.LatencyMS, result.Details)
	}
	builder.WriteString("\n## 证明点\n\n")
	fmt.Fprintln(&builder, "- Index Build 以 idempotency key 提交，并等待 durable Manifest（row count、维度、schema hash、manifest hash）。")
	fmt.Fprintln(&builder, "- Gateway 查询返回 trace_id，并通过应用边界读取持久化 Query Trace。")
	fmt.Fprintln(&builder, "- App Credential 只授予 `rag:query`，验证查询允许、回答拒绝、跨应用拒绝和撤销后拒绝。")
	if report.Release != nil {
		fmt.Fprintf(&builder, "- 发布控制面验证 stable supersede + rollback，最终回滚版本 `%s`。\n", report.Release.Version)
	}
	return builder.String()
}
