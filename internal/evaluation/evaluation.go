package evaluation

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/dingpuyu/rag-evolution-lab/internal/domain"
	"github.com/dingpuyu/rag-evolution-lab/internal/pipeline"
)

type CaseResult struct {
	CaseID          string   `json:"case_id"`
	Category        string   `json:"category"`
	Hit             bool     `json:"hit"`
	ReciprocalRank  float64  `json:"reciprocal_rank"`
	DocumentRecall  float64  `json:"document_recall"`
	Precision       float64  `json:"precision_at_k"`
	NDCG            float64  `json:"ndcg_at_k"`
	LatencyMS       float64  `json:"latency_ms"`
	AnswerableMatch bool     `json:"answerable_match"`
	RetrievedDocIDs []string `json:"retrieved_doc_ids"`
	Route           string   `json:"route,omitempty"`
}

type CategoryMetrics struct {
	Cases     int     `json:"cases"`
	HitRate   float64 `json:"hit_rate_at_k"`
	MRR       float64 `json:"mrr"`
	Recall    float64 `json:"document_recall_at_k"`
	Precision float64 `json:"precision_at_k"`
	NDCG      float64 `json:"ndcg_at_k"`
}

type Report struct {
	Pipeline               string                     `json:"pipeline"`
	Split                  string                     `json:"split"`
	Cases                  int                        `json:"cases"`
	HitRate                float64                    `json:"hit_rate_at_k"`
	MRR                    float64                    `json:"mrr"`
	Recall                 float64                    `json:"document_recall_at_k"`
	Precision              float64                    `json:"precision_at_k"`
	NDCG                   float64                    `json:"ndcg_at_k"`
	AnswerabilityAccuracy  float64                    `json:"answerability_accuracy"`
	OutcomeAccuracy        float64                    `json:"outcome_accuracy"`
	LatencyP50MS           float64                    `json:"latency_p50_ms"`
	LatencyP95MS           float64                    `json:"latency_p95_ms"`
	CitationViolations     int                        `json:"citation_violations"`
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
		cases         int
		hits          int
		mrr           float64
		recall        float64
		precision     float64
		ndcg          float64
		answerability int
		outcomes      int
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
		started := time.Now()
		response, err := target.Query(ctx, request)
		latencyMS := float64(time.Since(started).Microseconds()) / 1000
		if err != nil {
			return Report{}, fmt.Errorf("evaluate case %s: %w", golden.ID, err)
		}
		result := scoreCase(golden, response.Retrieval, request.TopK)
		result.LatencyMS = latencyMS
		result.AnswerableMatch = response.Answerable == golden.Expected.Answerable
		if !citationsInContext(response.Citations, response.Context) {
			report.CitationViolations++
		}
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
		value.precision += result.Precision
		value.ndcg += result.NDCG
		total.mrr += result.ReciprocalRank
		total.recall += result.DocumentRecall
		total.precision += result.Precision
		total.ndcg += result.NDCG
		if result.AnswerableMatch {
			value.answerability++
			total.answerability++
		}
		if result.Hit && result.AnswerableMatch {
			value.outcomes++
			total.outcomes++
		}
		categorySums[golden.Category] = value
	}

	report.Cases = total.cases
	if total.cases > 0 {
		report.HitRate = float64(total.hits) / float64(total.cases)
		report.MRR = total.mrr / float64(total.cases)
		report.Recall = total.recall / float64(total.cases)
		report.Precision = total.precision / float64(total.cases)
		report.NDCG = total.ndcg / float64(total.cases)
		report.AnswerabilityAccuracy = float64(total.answerability) / float64(total.cases)
		report.OutcomeAccuracy = float64(total.outcomes) / float64(total.cases)
	}
	latencies := make([]float64, 0, len(report.Results))
	for _, result := range report.Results {
		latencies = append(latencies, result.LatencyMS)
	}
	report.LatencyP50MS = percentile(latencies, 0.50)
	report.LatencyP95MS = percentile(latencies, 0.95)
	for category, value := range categorySums {
		report.ByCategory[category] = CategoryMetrics{
			Cases:     value.cases,
			HitRate:   divide(float64(value.hits), float64(value.cases)),
			MRR:       divide(value.mrr, float64(value.cases)),
			Recall:    divide(value.recall, float64(value.cases)),
			Precision: divide(value.precision, float64(value.cases)),
			NDCG:      divide(value.ndcg, float64(value.cases)),
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

func scoreCase(golden domain.GoldenCase, results []domain.RetrievedChunk, topK int) CaseResult {
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
			result.Precision = 1
			result.NDCG = 1
		}
		return result
	}
	if topK <= 0 {
		topK = 5
	}
	found := make(map[string]struct{})
	relevantRanks := make([]int, 0, len(expected))
	for index, retrieved := range results {
		id := retrieved.Chunk.DocumentID
		if !containsString(result.RetrievedDocIDs, id) {
			result.RetrievedDocIDs = append(result.RetrievedDocIDs, id)
		}
		if _, ok := expected[id]; ok {
			if _, duplicate := found[id]; duplicate {
				continue
			}
			found[id] = struct{}{}
			relevantRanks = append(relevantRanks, index+1)
			if result.ReciprocalRank == 0 {
				result.ReciprocalRank = 1 / float64(index+1)
			}
		}
	}
	result.Hit = len(found) > 0
	result.DocumentRecall = float64(len(found)) / float64(len(expected))
	result.Precision = float64(len(found)) / float64(topK)
	result.NDCG = ndcg(relevantRanks, min(len(expected), topK))
	return result
}

func citationsInContext(citations []domain.Citation, selected []domain.RetrievedChunk) bool {
	allowed := make(map[string]string, len(selected))
	for _, item := range selected {
		allowed[item.Chunk.ID] = item.Chunk.DocumentID
	}
	for _, citation := range citations {
		if allowed[citation.ChunkID] != citation.DocumentID {
			return false
		}
	}
	return true
}

func ndcg(relevantRanks []int, idealRelevant int) float64 {
	if idealRelevant == 0 {
		return 0
	}
	var dcg float64
	for _, rank := range relevantRanks {
		dcg += 1 / math.Log2(float64(rank)+1)
	}
	var ideal float64
	for rank := 1; rank <= idealRelevant; rank++ {
		ideal += 1 / math.Log2(float64(rank)+1)
	}
	return divide(dcg, ideal)
}

func percentile(values []float64, quantile float64) float64 {
	if len(values) == 0 {
		return 0
	}
	ordered := append([]float64(nil), values...)
	sort.Float64s(ordered)
	index := int(math.Ceil(quantile*float64(len(ordered)))) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(ordered) {
		index = len(ordered) - 1
	}
	return ordered[index]
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
