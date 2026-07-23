package scalebench

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/dingpuyu/rag-evolution-lab/internal/milvus"
)

const (
	ScenarioActiveAll         = "active_all"
	ScenarioPublicActive      = "public_active"
	ScenarioTenantAdminActive = "tenant_admin_active"
)

type DemoService struct {
	client      *milvus.Client
	generator   *Generator
	collections Collections
}

type DemoIndexStatus struct {
	Collection  string            `json:"collection"`
	Rows        int64             `json:"rows"`
	IndexName   string            `json:"index_name"`
	IndexType   string            `json:"index_type"`
	Metric      string            `json:"metric"`
	State       string            `json:"state"`
	IndexedRows int64             `json:"indexed_rows"`
	PendingRows int64             `json:"pending_rows"`
	Parameters  map[string]string `json:"parameters"`
}

type DemoStatus struct {
	Connected bool            `json:"connected"`
	Dataset   DatasetConfig   `json:"dataset"`
	Flat      DemoIndexStatus `json:"flat"`
	HNSW      DemoIndexStatus `json:"hnsw"`
	CheckedAt time.Time       `json:"checked_at"`
}

type DemoQuery struct {
	Topic    int    `json:"topic"`
	Scenario string `json:"scenario"`
	TopK     int    `json:"top_k"`
	EF       int    `json:"ef"`
}

type DemoHit struct {
	Rank          int     `json:"rank"`
	ChunkID       string  `json:"chunk_id"`
	Title         string  `json:"title"`
	Content       string  `json:"content"`
	TenantID      string  `json:"tenant_id"`
	Status        string  `json:"status"`
	Visibility    string  `json:"visibility"`
	Distance      float64 `json:"distance"`
	InExactTopK   bool    `json:"in_exact_top_k"`
	ExpectedTopic bool    `json:"expected_topic"`
}

type DemoSearchResult struct {
	Topic              int       `json:"topic"`
	Scenario           string    `json:"scenario"`
	Tenant             string    `json:"tenant"`
	TopK               int       `json:"top_k"`
	EF                 int       `json:"ef"`
	Filter             string    `json:"filter"`
	QueryVectorPreview []float64 `json:"query_vector_preview"`
	QueryL2Norm        float64   `json:"query_l2_norm"`
	ExactRecallAtK     float64   `json:"exact_recall_at_k"`
	TopicHitAtK        float64   `json:"topic_hit_at_k"`
	TopicPrecisionAtK  float64   `json:"topic_precision_at_k"`
	FlatLatencyMS      float64   `json:"flat_latency_ms"`
	HNSWLatencyMS      float64   `json:"hnsw_latency_ms"`
	TotalLatencyMS     float64   `json:"total_latency_ms"`
	ExactTopK          []string  `json:"exact_top_k"`
	Hits               []DemoHit `json:"hits"`
}

func NewDemoService(client *milvus.Client, generator *Generator, collections Collections) (*DemoService, error) {
	if client == nil || generator == nil {
		return nil, fmt.Errorf("scale demo requires client and generator")
	}
	if strings.TrimSpace(collections.Flat) == "" || strings.TrimSpace(collections.HNSW) == "" {
		return nil, fmt.Errorf("scale demo requires FLAT and HNSW collections")
	}
	return &DemoService{client: client, generator: generator, collections: collections}, nil
}

func (s *DemoService) Status(ctx context.Context) (DemoStatus, error) {
	flat, err := s.indexStatus(ctx, s.collections.Flat, "embedding_flat")
	if err != nil {
		return DemoStatus{}, fmt.Errorf("inspect FLAT collection: %w", err)
	}
	hnsw, err := s.indexStatus(ctx, s.collections.HNSW, "embedding_hnsw")
	if err != nil {
		return DemoStatus{}, fmt.Errorf("inspect HNSW collection: %w", err)
	}
	return DemoStatus{
		Connected: true,
		Dataset:   s.generator.config,
		Flat:      flat,
		HNSW:      hnsw,
		CheckedAt: time.Now().UTC(),
	}, nil
}

func (s *DemoService) Search(ctx context.Context, input DemoQuery) (DemoSearchResult, error) {
	if input.Topic < 0 || input.Topic >= s.generator.config.Topics {
		return DemoSearchResult{}, fmt.Errorf("topic must be between 0 and %d", s.generator.config.Topics-1)
	}
	if input.TopK <= 0 || input.TopK > 50 {
		return DemoSearchResult{}, fmt.Errorf("top_k must be between 1 and 50")
	}
	if input.EF < input.TopK || input.EF > 4096 {
		return DemoSearchResult{}, fmt.Errorf("ef must be between top_k and 4096")
	}
	filter, err := s.filter(input.Scenario, input.Topic)
	if err != nil {
		return DemoSearchResult{}, err
	}

	started := time.Now()
	vector := s.generator.BenchmarkQueryVector(input.Topic)
	flatStarted := time.Now()
	exactHits, err := s.client.Search(ctx, milvus.SearchRequest{
		Collection: s.collections.Flat, Vector: vector, Filter: filter, Limit: input.TopK, Exact: true,
	})
	if err != nil {
		return DemoSearchResult{}, fmt.Errorf("search FLAT ground truth: %w", err)
	}
	flatLatency := time.Since(flatStarted)
	hnswStarted := time.Now()
	approximateHits, err := s.client.Search(ctx, milvus.SearchRequest{
		Collection: s.collections.HNSW, Vector: vector, Filter: filter, Limit: input.TopK, EF: input.EF,
	})
	if err != nil {
		return DemoSearchResult{}, fmt.Errorf("search HNSW: %w", err)
	}
	hnswLatency := time.Since(hnswStarted)

	exactIDs := make(map[string]struct{}, len(exactHits))
	exactTopK := make([]string, 0, len(exactHits))
	for _, hit := range exactHits {
		exactIDs[hit.ChunkID] = struct{}{}
		exactTopK = append(exactTopK, hit.ChunkID)
	}
	topicPrefix := fmt.Sprintf("bench-t%04d-", input.Topic)
	result := DemoSearchResult{
		Topic: input.Topic, Scenario: input.Scenario, Tenant: s.generator.Tenant(input.Topic),
		TopK: input.TopK, EF: input.EF, Filter: filter,
		QueryVectorPreview: append([]float64(nil), vector[:min(8, len(vector))]...),
		QueryL2Norm:        l2Norm(vector),
		FlatLatencyMS:      milliseconds(flatLatency),
		HNSWLatencyMS:      milliseconds(hnswLatency),
		TotalLatencyMS:     milliseconds(time.Since(started)),
		ExactTopK:          exactTopK,
	}
	topicMatches := 0
	exactMatches := 0
	for index, hit := range approximateHits {
		_, exact := exactIDs[hit.ChunkID]
		expectedTopic := strings.HasPrefix(hit.ChunkID, topicPrefix)
		if exact {
			exactMatches++
		}
		if expectedTopic {
			topicMatches++
		}
		result.Hits = append(result.Hits, DemoHit{
			Rank: index + 1, ChunkID: hit.ChunkID, Title: hit.Title, Content: hit.Content,
			TenantID: hit.TenantID, Status: hit.Status, Visibility: hit.Visibility,
			Distance: hit.Distance, InExactTopK: exact, ExpectedTopic: expectedTopic,
		})
	}
	if len(exactHits) > 0 {
		result.ExactRecallAtK = float64(exactMatches) / float64(len(exactHits))
	}
	if len(approximateHits) > 0 {
		result.TopicPrecisionAtK = float64(topicMatches) / float64(len(approximateHits))
	}
	if topicMatches > 0 {
		result.TopicHitAtK = 1
	}
	return result, nil
}

func (s *DemoService) indexStatus(ctx context.Context, collection, indexName string) (DemoIndexStatus, error) {
	stats, err := s.client.CollectionStats(ctx, collection)
	if err != nil {
		return DemoIndexStatus{}, err
	}
	indexes, err := s.client.DescribeIndex(ctx, collection, indexName)
	if err != nil {
		return DemoIndexStatus{}, err
	}
	if len(indexes) == 0 {
		return DemoIndexStatus{}, fmt.Errorf("index %s not found", indexName)
	}
	index := indexes[0]
	parameters := make(map[string]string, len(index.IndexParams))
	for _, parameter := range index.IndexParams {
		if parameter.Key == "params" {
			var nested map[string]any
			if json.Unmarshal([]byte(parameter.Value), &nested) == nil {
				for key, value := range nested {
					parameters[key] = fmt.Sprint(value)
				}
				continue
			}
		}
		parameters[parameter.Key] = parameter.Value
	}
	return DemoIndexStatus{
		Collection: collection, Rows: int64(stats.RowCount), IndexName: index.IndexName,
		IndexType: index.IndexType, Metric: index.MetricType, State: index.IndexState,
		IndexedRows: index.IndexedRowCount(), PendingRows: index.PendingRowCount(), Parameters: parameters,
	}, nil
}

func (s *DemoService) filter(scenario string, topic int) (string, error) {
	switch scenario {
	case ScenarioActiveAll:
		return `status == "active"`, nil
	case ScenarioPublicActive:
		return `visibility == "public" and status == "active"`, nil
	case ScenarioTenantAdminActive:
		return `(visibility == "public" or (array_contains(allowed_tenants, "` + s.generator.Tenant(topic) + `") and array_contains(allowed_roles, "admin"))) and status == "active"`, nil
	default:
		return "", fmt.Errorf("unknown scenario %q", scenario)
	}
}

func l2Norm(vector []float64) float64 {
	var squared float64
	for _, value := range vector {
		squared += value * value
	}
	return math.Sqrt(squared)
}
