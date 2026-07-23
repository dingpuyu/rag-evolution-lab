package scalebench

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dingpuyu/rag-evolution-lab/internal/milvus"
)

func TestDemoServiceComparesHNSWWithFilteredFlatGroundTruth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v2/vectordb/entities/search":
			var payload map[string]any
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			collection := payload["collectionName"].(string)
			hits := []map[string]any{
				{"chunk_id": "bench-t0003-c0001", "title": "topic 3", "tenant_id": "tenant_003", "status": "active", "visibility": "public", "distance": 0.99},
				{"chunk_id": "bench-t0003-c0002", "title": "topic 3", "tenant_id": "tenant_003", "status": "active", "visibility": "public", "distance": 0.98},
			}
			if collection == "hnsw" {
				hits[1]["chunk_id"] = "bench-t0003-c0009"
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"code": 0, "data": hits})
		default:
			t.Fatalf("unexpected path %s", request.URL.Path)
		}
	}))
	defer server.Close()

	generator, err := NewGenerator(DatasetConfig{Chunks: 100, Dimensions: 8, Topics: 10, Tenants: 10, Seed: 7, Profile: ProfileHardV2})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewDemoService(milvus.NewClient(milvus.Config{BaseURL: server.URL}), generator, Collections{Flat: "flat", HNSW: "hnsw"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Search(context.Background(), DemoQuery{Topic: 3, Scenario: ScenarioPublicActive, TopK: 2, EF: 16})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExactRecallAtK != 0.5 || result.TopicHitAtK != 1 || result.TopicPrecisionAtK != 1 {
		t.Fatalf("unexpected quality metrics: %#v", result)
	}
	if len(result.Hits) != 2 || !result.Hits[0].InExactTopK || result.Hits[1].InExactTopK {
		t.Fatalf("unexpected annotated hits: %#v", result.Hits)
	}
	if result.Filter != `visibility == "public" and status == "active"` {
		t.Fatalf("unexpected filter %q", result.Filter)
	}
}

func TestDemoServiceRejectsEFBelowTopK(t *testing.T) {
	generator, err := NewGenerator(DatasetConfig{Chunks: 10, Dimensions: 8, Topics: 2, Tenants: 1, Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewDemoService(milvus.NewClient(milvus.Config{}), generator, Collections{Flat: "flat", HNSW: "hnsw"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Search(context.Background(), DemoQuery{Topic: 0, Scenario: ScenarioActiveAll, TopK: 10, EF: 8})
	if err == nil {
		t.Fatal("expected ef validation error")
	}
}

func TestDemoStatusDecodesNestedHNSWParameters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v2/vectordb/collections/get_stats":
			_ = json.NewEncoder(writer).Encode(map[string]any{"code": 0, "data": map[string]any{"rowCount": "100000"}})
		case "/v2/vectordb/indexes/describe":
			var payload map[string]any
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			name := payload["indexName"].(string)
			indexType := "FLAT"
			params := "{}"
			if name == "embedding_hnsw" {
				indexType = "HNSW"
				params = `{"M":8,"efConstruction":160}`
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"code": 0, "data": []map[string]any{{
				"indexName": name, "indexType": indexType, "metricType": "COSINE", "indexState": "Finished",
				"indexedRows": "100000", "pendingRows": 0,
				"indexParams": []map[string]string{{"key": "params", "value": params}},
			}}})
		default:
			t.Fatalf("unexpected path %s", request.URL.Path)
		}
	}))
	defer server.Close()
	generator, err := NewGenerator(DatasetConfig{Chunks: 100_000, Dimensions: 8, Topics: 10, Tenants: 1, Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewDemoService(milvus.NewClient(milvus.Config{BaseURL: server.URL}), generator, Collections{Flat: "flat", HNSW: "hnsw"})
	if err != nil {
		t.Fatal(err)
	}
	status, err := service.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.HNSW.Parameters["M"] != "8" || status.HNSW.Parameters["efConstruction"] != "160" {
		t.Fatalf("unexpected HNSW parameters: %#v", status.HNSW.Parameters)
	}
}
