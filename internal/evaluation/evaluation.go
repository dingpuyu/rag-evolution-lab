package evaluation

import (
	"context"
	"fmt"
	"sort"

	"github.com/dingpuyu/rag-evolution-lab/internal/domain"
	"github.com/dingpuyu/rag-evolution-lab/internal/pipeline"
)

type CaseResult struct {
	CaseID          string   `json:"case_id"`
	Category        string   `json:"category"`
	Hit             bool     `json:"hit"`
	ReciprocalRank  float64  `json:"reciprocal_rank"`
	DocumentRecall  float64  `json:"document_recall"`
	RetrievedDocIDs []string `json:"retrieved_doc_ids"`
	Route           string   `json:"route,omitempty"`
}

type CategoryMetrics struct {
	Cases   int     `json:"cases"`
	HitRate float64 `json:"hit_rate_at_k"`
	MRR     float64 `json:"mrr"`
	Recall  float64 `json:"document_recall_at_k"`
}

type Report struct {
	Pipeline               string                     `json:"pipeline"`
	Split                  string                     `json:"split"`
	Cases                  int                        `json:"cases"`
	HitRate                float64                    `json:"hit_rate_at_k"`
	MRR                    float64                    `json:"mrr"`
	Recall                 float64                    `json:"document_recall_at_k"`
	UnauthorizedRetrievals int                        `json:"unauthorized_retrievals"`
	MetadataViolations     int                        `json:"metadata_violations"`
	ByCategory             map[string]CategoryMetrics `json:"by_category"`
	Results                []CaseResult               `json:"results"`
	Routes                 map[string]int             `json:"routes,omitempty"`
}

func Run(ctx context.Context, target *pipeline.Pipeline, split string, cases []domain.GoldenCase) (Report, error) {
	report := Report{
		Pipeline:   target.Name(),
		Split:      split,
		ByCategory: make(map[string]CategoryMetrics),
		Routes:     make(map[string]int),
	}
	type sums struct {
		cases  int
		hits   int
		mrr    float64
		recall float64
	}
	categorySums := make(map[string]sums)
	var total sums

	for _, golden := range cases {
		request := domain.QueryRequest{
			Query:    golden.Query,
			Pipeline: target.Name(),
			TenantID: golden.Context.TenantID,
			UserRole: golden.Context.UserRole,
			TopK:     5,
		}
		if golden.Context.Product != nil {
			request.Product = *golden.Context.Product
		}
		if golden.Context.Version != nil {
			request.Version = *golden.Context.Version
		}
		response, err := target.Query(ctx, request)
		if err != nil {
			return Report{}, fmt.Errorf("evaluate case %s: %w", golden.ID, err)
		}
		result := scoreCase(golden, response.Retrieval)
		result.Route = routeFromTrace(response.Trace)
		if result.Route != "" {
			report.Routes[result.Route]++
		}
		for _, retrieved := range response.Retrieval {
			if !isAuthorized(retrieved.Chunk, golden.Context) {
				report.UnauthorizedRetrievals++
			}
			if !isMetadataCompatible(retrieved.Chunk, golden.Context) {
				report.MetadataViolations++
			}
		}
		report.Results = append(report.Results, result)
		value := categorySums[golden.Category]
		value.cases++
		total.cases++
		if result.Hit {
			value.hits++
			total.hits++
		}
		value.mrr += result.ReciprocalRank
		value.recall += result.DocumentRecall
		total.mrr += result.ReciprocalRank
		total.recall += result.DocumentRecall
		categorySums[golden.Category] = value
	}

	report.Cases = total.cases
	if total.cases > 0 {
		report.HitRate = float64(total.hits) / float64(total.cases)
		report.MRR = total.mrr / float64(total.cases)
		report.Recall = total.recall / float64(total.cases)
	}
	for category, value := range categorySums {
		report.ByCategory[category] = CategoryMetrics{
			Cases:   value.cases,
			HitRate: divide(float64(value.hits), float64(value.cases)),
			MRR:     divide(value.mrr, float64(value.cases)),
			Recall:  divide(value.recall, float64(value.cases)),
		}
	}
	return report, nil
}

func routeFromTrace(value domain.QueryTrace) string {
	for _, event := range value.Events {
		if route, ok := event.Attributes["route"].(string); ok {
			return route
		}
	}
	return ""
}

func isMetadataCompatible(chunk domain.Chunk, context domain.GoldenContext) bool {
	if context.Product != nil && chunk.Product != *context.Product {
		return false
	}
	if context.Version != nil {
		return chunk.Version == *context.Version
	}
	return chunk.Status == "active"
}

func isAuthorized(chunk domain.Chunk, context domain.GoldenContext) bool {
	if chunk.Visibility == "public" {
		return true
	}
	return containsString(chunk.AllowedTenants, context.TenantID) && containsString(chunk.AllowedRoles, context.UserRole)
}

func scoreCase(golden domain.GoldenCase, results []domain.RetrievedChunk) CaseResult {
	expected := make(map[string]struct{}, len(golden.Expected.RelevantDocumentIDs))
	for _, id := range golden.Expected.RelevantDocumentIDs {
		expected[id] = struct{}{}
	}
	result := CaseResult{CaseID: golden.ID, Category: golden.Category}
	if len(expected) == 0 {
		for _, retrieved := range results {
			id := retrieved.Chunk.DocumentID
			if !containsString(result.RetrievedDocIDs, id) {
				result.RetrievedDocIDs = append(result.RetrievedDocIDs, id)
			}
		}
		result.Hit = len(results) == 0
		if result.Hit {
			result.ReciprocalRank = 1
			result.DocumentRecall = 1
		}
		return result
	}
	found := make(map[string]struct{})
	for index, retrieved := range results {
		id := retrieved.Chunk.DocumentID
		if !containsString(result.RetrievedDocIDs, id) {
			result.RetrievedDocIDs = append(result.RetrievedDocIDs, id)
		}
		if _, ok := expected[id]; ok {
			found[id] = struct{}{}
			if result.ReciprocalRank == 0 {
				result.ReciprocalRank = 1 / float64(index+1)
			}
		}
	}
	result.Hit = len(found) > 0
	result.DocumentRecall = float64(len(found)) / float64(len(expected))
	return result
}

func SortedCategories(report Report) []string {
	values := make([]string, 0, len(report.ByCategory))
	for category := range report.ByCategory {
		values = append(values, category)
	}
	sort.Strings(values)
	return values
}

func divide(numerator, denominator float64) float64 {
	if denominator == 0 {
		return 0
	}
	return numerator / denominator
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
