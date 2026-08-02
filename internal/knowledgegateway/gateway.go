// Package knowledgegateway exposes the application-facing retrieval boundary.
// It resolves an Agent application's environment and knowledge bindings before
// touching Milvus, so callers never need to pass tenant, product or ACL fields.
package knowledgegateway

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dingpuyu/rag-evolution-lab/internal/auth"
	"github.com/dingpuyu/rag-evolution-lab/internal/cost"
	"github.com/dingpuyu/rag-evolution-lab/internal/datasetaccess"
	"github.com/dingpuyu/rag-evolution-lab/internal/generation"
	"github.com/dingpuyu/rag-evolution-lab/internal/milvus"
	"github.com/dingpuyu/rag-evolution-lab/internal/querytrace"
	"github.com/dingpuyu/rag-evolution-lab/internal/ratelimit"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Searcher is the small part of the retrieval service needed by the gateway.
// Keeping it as an interface makes policy and multi-binding behavior testable
// without requiring a running Milvus instance.
type Searcher interface {
	Search(context.Context, milvus.Query) (milvus.SearchResult, error)
}

type Request struct {
	AppID         string `json:"app_id,omitempty"`
	EnvironmentID string `json:"environment_id,omitempty"`
	Query         string `json:"query"`
	TopK          int    `json:"top_k,omitempty"`
}

type BindingTrace struct {
	DatasetID       string                        `json:"dataset_id"`
	DatasetName     string                        `json:"dataset_name"`
	Purpose         string                        `json:"purpose,omitempty"`
	Priority        int                           `json:"priority"`
	Hits            int                           `json:"hits"`
	Policy          datasetaccess.RetrievalPolicy `json:"policy"`
	IndexVersion    string                        `json:"index_version,omitempty"`
	IndexCollection string                        `json:"index_collection,omitempty"`
	Rewrite         RewriteResult                 `json:"rewrite"`
	Rerank          RerankTrace                   `json:"rerank"`
}

type RerankTrace struct {
	Applied    bool   `json:"applied"`
	Model      string `json:"model,omitempty"`
	Candidates int    `json:"candidates"`
}

type SearchResponse struct {
	AppID          string              `json:"app_id"`
	EnvironmentID  string              `json:"environment_id"`
	TraceID        string              `json:"trace_id,omitempty"`
	RewrittenQuery string              `json:"rewritten_query,omitempty"`
	Bindings       []BindingTrace      `json:"bindings"`
	Result         milvus.SearchResult `json:"result"`
	TraceRecord    querytrace.Record   `json:"-"`
}

type AnswerResponse struct {
	AppID         string              `json:"app_id"`
	EnvironmentID string              `json:"environment_id"`
	TraceID       string              `json:"trace_id,omitempty"`
	Bindings      []BindingTrace      `json:"bindings"`
	Result        generation.Response `json:"result"`
}

type Service struct {
	searcher   Searcher
	datasets   datasetaccess.Store
	apps       datasetaccess.ApplicationStore
	generator  generation.Generator
	indexStore datasetaccess.IndexStore
	traceStore querytrace.Store
	rewriter   QueryRewriter
	reranker   HitReranker
	tracer     trace.Tracer
	cost       *cost.Calculator
	limiter    ratelimit.Gate
}

type Options struct {
	IndexStore datasetaccess.IndexStore
	TraceStore querytrace.Store
	Rewriter   QueryRewriter
	Reranker   HitReranker
	Tracer     trace.Tracer
	Cost       *cost.Calculator
	Limiter    ratelimit.Gate
}

func New(searcher Searcher, datasets datasetaccess.Store, apps datasetaccess.ApplicationStore, generator generation.Generator) (*Service, error) {
	return NewWithOptions(searcher, datasets, apps, generator, Options{})
}

func NewWithOptions(searcher Searcher, datasets datasetaccess.Store, apps datasetaccess.ApplicationStore, generator generation.Generator, options Options) (*Service, error) {
	if searcher == nil || datasets == nil || apps == nil || generator == nil {
		return nil, fmt.Errorf("knowledge gateway requires searcher, dataset store, application store and generator")
	}
	if options.Rewriter == nil {
		options.Rewriter = SemanticRewriter{}
	}
	if options.Reranker == nil {
		options.Reranker = NewHeuristicReranker()
	}
	if options.Tracer == nil {
		options.Tracer = otel.Tracer("raglab.knowledge-gateway")
	}
	return &Service{searcher: searcher, datasets: datasets, apps: apps, generator: generator,
		indexStore: options.IndexStore, traceStore: options.TraceStore, rewriter: options.Rewriter, reranker: options.Reranker,
		tracer: options.Tracer, cost: options.Cost, limiter: options.Limiter}, nil
}

func (service *Service) Search(ctx context.Context, identity auth.Identity, request Request) (SearchResponse, error) {
	appID := strings.TrimSpace(request.AppID)
	if appID == "" {
		return SearchResponse{}, fmt.Errorf("app_id is required")
	}
	if identity.ApplicationID != "" && identity.ApplicationID != appID {
		return SearchResponse{}, fmt.Errorf("application credential is scoped to a different app")
	}
	if len(identity.Scopes) > 0 && !identity.HasScope("rag:query") {
		return SearchResponse{}, fmt.Errorf("credential scope rag:query is required")
	}
	text := strings.TrimSpace(request.Query)
	if text == "" {
		return SearchResponse{}, fmt.Errorf("query must not be empty")
	}
	ctx, span := service.tracer.Start(ctx, "rag.search", trace.WithAttributes(
		attribute.String("rag.app_id", appID), attribute.String("rag.tenant_id", identity.TenantID),
		attribute.String("rag.subject", identity.Subject), attribute.String("rag.operation", "search")))
	defer span.End()
	limitKey := strings.Join([]string{"tenant", identity.TenantID, "app", appID, "subject", identity.Subject}, ":")
	if service.limiter != nil {
		// Admission is deliberately conservative: reserve a small prompt
		// budget before retrieval, then the trace records the exact LLM usage.
		quotaTokens := 1024
		if request.TopK > 0 {
			quotaTokens += request.TopK * 256
		}
		decision := service.limiter.Allow(limitKey, quotaTokens)
		span.SetAttributes(attribute.Int("rate.remaining", decision.Remaining))
		if !decision.Allowed {
			span.SetStatus(codes.Error, "rate limit exceeded")
			return SearchResponse{}, &RateLimitError{RetryAfter: decision.RetryAfter}
		}
		defer service.limiter.Release(limitKey)
	}
	environmentID := strings.TrimSpace(request.EnvironmentID)
	if environmentID == "" {
		environmentID = appID + "-dev"
	}
	bindings, err := service.apps.Bindings(ctx, identity, appID, environmentID)
	if err != nil {
		return SearchResponse{}, err
	}
	if len(bindings) == 0 {
		return SearchResponse{}, fmt.Errorf("no active knowledge bindings for application environment")
	}

	started := time.Now()
	traces := make([]BindingTrace, 0, len(bindings))
	results := make([]milvus.SearchResult, 0, len(bindings))
	globalTopK := request.TopK
	if globalTopK <= 0 || globalTopK > 20 {
		globalTopK = 5
	}
	for _, binding := range bindings {
		if binding.Status != "active" {
			continue
		}
		dataset, err := service.datasets.Authorize(ctx, binding.DatasetID, identity)
		if err != nil {
			// Fail closed if a binding was revoked after it was published. Do not
			// silently fall back to another dataset and change the app's contract.
			return SearchResponse{}, err
		}
		effectiveQuery := text
		rewrite := RewriteResult{Query: text, Rewriter: "disabled", Reason: "policy_disabled"}
		if binding.Policy.QueryRewrite {
			rewriteCtx, rewriteSpan := service.tracer.Start(ctx, "rag.query_rewrite", trace.WithAttributes(attribute.String("rag.dataset_id", binding.DatasetID)))
			var rewriteErr error
			rewrite, rewriteErr = service.rewriter.Rewrite(rewriteCtx, text)
			rewriteSpan.End()
			if rewriteErr != nil {
				if binding.Policy.AllowFallback {
					rewrite = RewriteResult{Query: text, Rewriter: service.rewriterName(), Reason: "fallback_after_rewriter_error"}
				} else {
					return SearchResponse{}, fmt.Errorf("rewrite bound dataset %q: %w", binding.DatasetID, rewriteErr)
				}
			}
			effectiveQuery = rewrite.Query
		}
		limit := binding.Policy.CandidateK
		if limit <= 0 {
			limit = globalTopK
		}
		if limit < globalTopK {
			limit = globalTopK
		}
		if limit > 20 {
			limit = 20
		}
		indexRelease := datasetaccess.IndexRelease{}
		if service.indexStore != nil {
			indexRelease, err = service.indexStore.ResolveIndexRelease(ctx, identity, appID, environmentID)
			if err != nil {
				return SearchResponse{}, err
			}
		}
		query := buildQuery(dataset, identity, effectiveQuery, limit)
		query.Collection = indexRelease.Collection
		retrievalCtx, retrievalSpan := service.tracer.Start(ctx, "rag.retrieval", trace.WithAttributes(attribute.String("rag.dataset_id", binding.DatasetID), attribute.Int("rag.candidate_k", limit)))
		result, err := service.searcher.Search(retrievalCtx, query)
		retrievalSpan.SetAttributes(attribute.Int("rag.hit_count", len(result.Hits)))
		retrievalSpan.End()
		if err != nil {
			return SearchResponse{}, fmt.Errorf("search bound dataset %q: %w", binding.DatasetID, err)
		}
		rerankTrace := RerankTrace{Candidates: len(result.Hits)}
		if binding.Policy.Rerank && len(result.Hits) > 1 {
			rerankCtx, rerankSpan := service.tracer.Start(ctx, "rag.rerank", trace.WithAttributes(attribute.String("rag.dataset_id", binding.DatasetID), attribute.Int("rag.candidates", len(result.Hits))))
			ranked, rerankErr := service.reranker.Rerank(rerankCtx, effectiveQuery, result.Hits)
			rerankSpan.End()
			if rerankErr != nil {
				if !binding.Policy.AllowFallback {
					return SearchResponse{}, fmt.Errorf("rerank bound dataset %q: %w", binding.DatasetID, rerankErr)
				}
			} else {
				result.Hits = ranked
				result.RerankApplied = true
				rerankTrace.Applied = true
				rerankTrace.Model = service.reranker.Name()
			}
		}
		// CandidateK controls how much evidence enters the merge; TopK is the
		// binding's published output budget. Enforce it before merging so one
		// binding cannot consume another binding's answer budget.
		bindingTopK := binding.Policy.TopK
		if bindingTopK <= 0 {
			bindingTopK = globalTopK
		}
		if bindingTopK > 20 {
			bindingTopK = 20
		}
		if len(result.Hits) > bindingTopK {
			result.Hits = result.Hits[:bindingTopK]
		}
		results = append(results, result)
		traces = append(traces, BindingTrace{
			DatasetID: binding.DatasetID, DatasetName: dataset.Name, Purpose: binding.Purpose,
			Priority: binding.Priority, Hits: len(result.Hits), Policy: binding.Policy,
			IndexVersion: indexRelease.Version, IndexCollection: indexRelease.Collection,
			Rewrite: rewrite, Rerank: rerankTrace,
		})
	}
	if len(results) == 0 {
		return SearchResponse{}, fmt.Errorf("no active knowledge bindings for application environment")
	}
	merged := mergeResults(text, appID, environmentID, results, globalTopK, time.Since(started))
	rewrittenQuery := text
	for _, trace := range traces {
		if trace.Rewrite.Applied {
			rewrittenQuery = trace.Rewrite.Query
			break
		}
	}
	record := querytrace.Record{
		TraceID: newTraceID(), AppID: appID, EnvironmentID: environmentID, TenantID: identity.TenantID, Subject: identity.Subject,
		Query: text, RewrittenQuery: rewrittenQuery, Status: "retrieved", TopK: globalTopK,
		CandidateCount: candidateCount(traces), HitCount: len(merged.Hits), EmbeddingMS: merged.EmbeddingLatencyMS,
		RetrievalMS: merged.SearchLatencyMS, TotalMS: merged.TotalLatencyMS, RerankApplied: hasRerank(traces), RewriteApplied: hasRewrite(traces),
		IndexCollection: firstIndexCollection(traces), IndexVersion: firstIndexVersion(traces), EmbeddingModel: merged.Embedder,
		Metadata: map[string]any{"bindings": traces, "filter": merged.Filter}, StartedAt: started,
	}
	record.TraceParent = traceParent(span.SpanContext())
	if span.SpanContext().IsValid() {
		record.SpanID = span.SpanContext().SpanID().String()
		record.TraceID = span.SpanContext().TraceID().String()
	}
	span.SetAttributes(attribute.Int("rag.candidate_count", record.CandidateCount), attribute.Int("rag.hit_count", record.HitCount),
		attribute.Bool("rag.rerank_applied", record.RerankApplied), attribute.Bool("rag.query_rewrite_applied", record.RewriteApplied),
		attribute.String("rag.index_version", record.IndexVersion), attribute.String("rag.index_collection", record.IndexCollection))
	if service.traceStore != nil {
		if err := service.traceStore.UpsertQueryTrace(ctx, record); err != nil {
			return SearchResponse{}, fmt.Errorf("persist query trace: %w", err)
		}
	}
	return SearchResponse{AppID: appID, EnvironmentID: environmentID, TraceID: record.TraceID, RewrittenQuery: rewrittenQuery,
		Bindings: traces, Result: merged, TraceRecord: record}, nil
}

func (service *Service) Answer(ctx context.Context, identity auth.Identity, request Request) (AnswerResponse, error) {
	if len(identity.Scopes) > 0 && !identity.HasScope("rag:answer") {
		return AnswerResponse{}, fmt.Errorf("credential scope rag:answer is required")
	}
	ctx, span := service.tracer.Start(ctx, "rag.answer", trace.WithAttributes(attribute.String("rag.app_id", request.AppID), attribute.String("rag.operation", "answer")))
	defer span.End()
	search, err := service.Search(ctx, identity, request)
	if err != nil {
		return AnswerResponse{}, err
	}
	answerService, err := generation.NewServiceWithOptions(staticSearcher{result: search.Result}, service.generator, generation.Options{GeneralGenerator: service.generator})
	if err != nil {
		return AnswerResponse{}, err
	}
	generationCtx, generationSpan := service.tracer.Start(ctx, "rag.generation", trace.WithAttributes(attribute.String("rag.generator", service.generator.Name())))
	result, err := answerService.Answer(generationCtx, milvus.Query{Text: request.Query, TopK: request.TopK})
	generationSpan.End()
	if err != nil {
		service.persistFailedTrace(ctx, search.TraceRecord, err)
		return AnswerResponse{}, err
	}
	if err := service.persistCompletedTrace(ctx, search.TraceRecord, result); err != nil {
		return AnswerResponse{}, err
	}
	service.setCostAttributes(span, result)
	return AnswerResponse{AppID: search.AppID, EnvironmentID: search.EnvironmentID, TraceID: search.TraceID, Bindings: search.Bindings, Result: result}, nil
}

func (service *Service) AnswerWithProgress(ctx context.Context, identity auth.Identity, request Request, sink generation.ProgressSink) (AnswerResponse, error) {
	if len(identity.Scopes) > 0 && !identity.HasScope("rag:answer") {
		return AnswerResponse{}, fmt.Errorf("credential scope rag:answer is required")
	}
	ctx, span := service.tracer.Start(ctx, "rag.answer.stream", trace.WithAttributes(attribute.String("rag.app_id", request.AppID), attribute.String("rag.operation", "answer_stream")))
	defer span.End()
	search, err := service.Search(ctx, identity, request)
	if err != nil {
		return AnswerResponse{}, err
	}
	answerService, err := generation.NewServiceWithOptions(staticSearcher{result: search.Result}, service.generator, generation.Options{GeneralGenerator: service.generator})
	if err != nil {
		return AnswerResponse{}, err
	}
	generationCtx, generationSpan := service.tracer.Start(ctx, "rag.generation.stream", trace.WithAttributes(attribute.String("rag.generator", service.generator.Name())))
	result, err := answerService.AnswerWithProgress(generationCtx, milvus.Query{Text: request.Query, TopK: request.TopK}, sink)
	generationSpan.End()
	if err != nil {
		service.persistFailedTrace(ctx, search.TraceRecord, err)
		return AnswerResponse{}, err
	}
	if err := service.persistCompletedTrace(ctx, search.TraceRecord, result); err != nil {
		return AnswerResponse{}, err
	}
	service.setCostAttributes(span, result)
	return AnswerResponse{AppID: search.AppID, EnvironmentID: search.EnvironmentID, TraceID: search.TraceID, Bindings: search.Bindings, Result: result}, nil
}

func (service *Service) setCostAttributes(span trace.Span, result generation.Response) {
	if service.cost == nil {
		return
	}
	estimate := service.cost.Estimate(result.Generation.PromptTokens, result.Generation.OutputTokens)
	span.SetAttributes(attribute.Int("llm.prompt_tokens", result.Generation.PromptTokens), attribute.Int("llm.output_tokens", result.Generation.OutputTokens),
		attribute.Float64("llm.input_cost_usd", estimate.InputUSD), attribute.Float64("llm.output_cost_usd", estimate.OutputUSD), attribute.Float64("llm.total_cost_usd", estimate.TotalUSD), attribute.String("llm.provider", result.Generation.Generator), attribute.String("llm.model", result.Generation.Model))
}

func (service *Service) persistCompletedTrace(ctx context.Context, record querytrace.Record, result generation.Response) error {
	if service.traceStore == nil {
		return nil
	}
	answerable := result.Answerable
	now := time.Now().UTC()
	record.Status, record.Answerable, record.RefusalReason = "completed", &answerable, result.RefusalReason
	record.Generator, record.Model, record.PromptVersion = result.Generation.Generator, result.Generation.Model, result.Generation.PromptVersion
	record.GenerationMS, record.PromptTokens, record.OutputTokens = result.Generation.LatencyMS, result.Generation.PromptTokens, result.Generation.OutputTokens
	record.Provider = result.Generation.Generator
	if service.cost != nil {
		estimate := service.cost.Estimate(record.PromptTokens, record.OutputTokens)
		record.InputCostUSD, record.OutputCostUSD, record.TotalCostUSD = estimate.InputUSD, estimate.OutputUSD, estimate.TotalUSD
	}
	record.TotalMS = result.Search.TotalLatencyMS + result.Generation.LatencyMS
	record.CompletedAt = &now
	if err := service.traceStore.UpsertQueryTrace(ctx, record); err != nil {
		return fmt.Errorf("persist answer trace: %w", err)
	}
	return nil
}

func (service *Service) persistFailedTrace(ctx context.Context, record querytrace.Record, generationErr error) {
	if service.traceStore == nil {
		return
	}
	now := time.Now().UTC()
	record.Status, record.Error, record.CompletedAt = "failed", generationErr.Error(), &now
	_ = service.traceStore.UpsertQueryTrace(ctx, record)
}

type staticSearcher struct{ result milvus.SearchResult }

func (searcher staticSearcher) Search(context.Context, milvus.Query) (milvus.SearchResult, error) {
	return searcher.result, nil
}

func buildQuery(dataset datasetaccess.Dataset, identity auth.Identity, text string, topK int) milvus.Query {
	query := milvus.Query{Text: text, TopK: topK, Product: dataset.Product, Status: "active"}
	if dataset.Visibility == "public" {
		query.Tenant, query.Role, query.AccessScope = "public", "viewer", "public_only"
	} else {
		query.Tenant, query.Role, query.AccessScope = identity.TenantID, identity.PrimaryRole(), "tenant_only"
	}
	return query
}

func mergeResults(query, appID, environmentID string, results []milvus.SearchResult, topK int, elapsed time.Duration) milvus.SearchResult {
	seen := make(map[string]struct{})
	hits := make([]milvus.SearchHit, 0)
	var merged milvus.SearchResult
	for index, result := range results {
		if index == 0 {
			merged = result
		} else {
			merged.EmbeddingLatencyMS += result.EmbeddingLatencyMS
			merged.SearchLatencyMS += result.SearchLatencyMS
		}
		for _, hit := range result.Hits {
			key := hit.ChunkID
			if key == "" {
				key = hit.DocumentID + "\x00" + hit.Title + "\x00" + hit.Content
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			hits = append(hits, hit)
		}
	}
	if !hasRerankedResults(results) {
		sort.SliceStable(hits, func(i, j int) bool { return hits[i].Distance < hits[j].Distance })
	}
	if len(hits) > topK {
		hits = hits[:topK]
	}
	merged.Query = query
	merged.Collection = "knowledge-gateway"
	merged.Filter = fmt.Sprintf("app_id=%q environment_id=%q bindings=%d", appID, environmentID, len(results))
	merged.TotalLatencyMS = milliseconds(elapsed)
	merged.Hits = hits
	return merged
}

func hasRerankedResults(results []milvus.SearchResult) bool {
	for _, result := range results {
		if result.RerankApplied {
			return true
		}
	}
	return false
}

func milliseconds(duration time.Duration) float64 { return float64(duration.Microseconds()) / 1000 }

func (service *Service) rewriterName() string {
	if _, ok := service.rewriter.(SemanticRewriter); ok {
		return "semantic-alias-v1"
	}
	return "query-rewriter"
}

func newTraceID() string { return fmt.Sprintf("gw_trace_%d", time.Now().UTC().UnixNano()) }

func candidateCount(traces []BindingTrace) int {
	total := 0
	for _, trace := range traces {
		total += trace.Rerank.Candidates
	}
	return total
}

func hasRerank(traces []BindingTrace) bool {
	for _, trace := range traces {
		if trace.Rerank.Applied {
			return true
		}
	}
	return false
}

func hasRewrite(traces []BindingTrace) bool {
	for _, trace := range traces {
		if trace.Rewrite.Applied {
			return true
		}
	}
	return false
}

func firstIndexCollection(traces []BindingTrace) string {
	for _, trace := range traces {
		if trace.IndexCollection != "" {
			return trace.IndexCollection
		}
	}
	return ""
}

func firstIndexVersion(traces []BindingTrace) string {
	for _, trace := range traces {
		if trace.IndexVersion != "" {
			return trace.IndexVersion
		}
	}
	return ""
}
