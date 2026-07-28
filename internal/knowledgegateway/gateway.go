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
	"github.com/dingpuyu/rag-evolution-lab/internal/datasetaccess"
	"github.com/dingpuyu/rag-evolution-lab/internal/generation"
	"github.com/dingpuyu/rag-evolution-lab/internal/milvus"
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
	DatasetID   string                        `json:"dataset_id"`
	DatasetName string                        `json:"dataset_name"`
	Purpose     string                        `json:"purpose,omitempty"`
	Priority    int                           `json:"priority"`
	Hits        int                           `json:"hits"`
	Policy      datasetaccess.RetrievalPolicy `json:"policy"`
}

type SearchResponse struct {
	AppID         string              `json:"app_id"`
	EnvironmentID string              `json:"environment_id"`
	Bindings      []BindingTrace      `json:"bindings"`
	Result        milvus.SearchResult `json:"result"`
}

type AnswerResponse struct {
	AppID         string              `json:"app_id"`
	EnvironmentID string              `json:"environment_id"`
	Bindings      []BindingTrace      `json:"bindings"`
	Result        generation.Response `json:"result"`
}

type Service struct {
	searcher  Searcher
	datasets  datasetaccess.Store
	apps      datasetaccess.ApplicationStore
	generator generation.Generator
}

func New(searcher Searcher, datasets datasetaccess.Store, apps datasetaccess.ApplicationStore, generator generation.Generator) (*Service, error) {
	if searcher == nil || datasets == nil || apps == nil || generator == nil {
		return nil, fmt.Errorf("knowledge gateway requires searcher, dataset store, application store and generator")
	}
	return &Service{searcher: searcher, datasets: datasets, apps: apps, generator: generator}, nil
}

func (service *Service) Search(ctx context.Context, identity auth.Identity, request Request) (SearchResponse, error) {
	appID := strings.TrimSpace(request.AppID)
	if appID == "" {
		return SearchResponse{}, fmt.Errorf("app_id is required")
	}
	text := strings.TrimSpace(request.Query)
	if text == "" {
		return SearchResponse{}, fmt.Errorf("query must not be empty")
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
		result, err := service.searcher.Search(ctx, buildQuery(dataset, identity, text, limit))
		if err != nil {
			return SearchResponse{}, fmt.Errorf("search bound dataset %q: %w", binding.DatasetID, err)
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
		})
	}
	if len(results) == 0 {
		return SearchResponse{}, fmt.Errorf("no active knowledge bindings for application environment")
	}
	merged := mergeResults(text, appID, environmentID, results, globalTopK, time.Since(started))
	return SearchResponse{AppID: appID, EnvironmentID: environmentID, Bindings: traces, Result: merged}, nil
}

func (service *Service) Answer(ctx context.Context, identity auth.Identity, request Request) (AnswerResponse, error) {
	search, err := service.Search(ctx, identity, request)
	if err != nil {
		return AnswerResponse{}, err
	}
	answerService, err := generation.NewService(staticSearcher{result: search.Result}, service.generator)
	if err != nil {
		return AnswerResponse{}, err
	}
	result, err := answerService.Answer(ctx, milvus.Query{Text: request.Query, TopK: request.TopK})
	if err != nil {
		return AnswerResponse{}, err
	}
	return AnswerResponse{AppID: search.AppID, EnvironmentID: search.EnvironmentID, Bindings: search.Bindings, Result: result}, nil
}

func (service *Service) AnswerWithProgress(ctx context.Context, identity auth.Identity, request Request, sink generation.ProgressSink) (AnswerResponse, error) {
	search, err := service.Search(ctx, identity, request)
	if err != nil {
		return AnswerResponse{}, err
	}
	answerService, err := generation.NewService(staticSearcher{result: search.Result}, service.generator)
	if err != nil {
		return AnswerResponse{}, err
	}
	result, err := answerService.AnswerWithProgress(ctx, milvus.Query{Text: request.Query, TopK: request.TopK}, sink)
	if err != nil {
		return AnswerResponse{}, err
	}
	return AnswerResponse{AppID: search.AppID, EnvironmentID: search.EnvironmentID, Bindings: search.Bindings, Result: result}, nil
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
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Distance < hits[j].Distance })
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

func milliseconds(duration time.Duration) float64 { return float64(duration.Microseconds()) / 1000 }
