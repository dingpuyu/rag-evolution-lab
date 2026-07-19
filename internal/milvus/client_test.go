package milvus

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCreateCollectionUsesExplicitHNSWCosineSchema(t *testing.T) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v2/vectordb/collections/create" {
			t.Fatalf("unexpected path %s", request.URL.Path)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("unexpected authorization %q", got)
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"code": 0, "data": map[string]any{}})
	}))
	defer server.Close()

	client := NewClient(Config{BaseURL: server.URL, Token: "test-token"})
	if err := client.CreateCollection(context.Background(), "chunks", 2560); err != nil {
		t.Fatal(err)
	}
	indexes := payload["indexParams"].([]any)
	index := indexes[0].(map[string]any)
	if index["indexType"] != "HNSW" || index["metricType"] != "COSINE" {
		t.Fatalf("unexpected index: %#v", index)
	}
	schema := payload["schema"].(map[string]any)
	fields := schema["fields"].([]any)
	vector := fields[len(fields)-1].(map[string]any)
	params := vector["elementTypeParams"].(map[string]any)
	if params["dim"] != "2560" {
		t.Fatalf("unexpected vector dimensions: %#v", params)
	}
}

func TestSearchSendsFilterAndDecodesHits(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["filter"] != `status == "active"` {
			t.Fatalf("unexpected filter %#v", payload["filter"])
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"code": 0,
			"data": []map[string]any{{"chunk_id": "doc#c001", "title": "SSO", "distance": 0.91}},
		})
	}))
	defer server.Close()

	client := NewClient(Config{BaseURL: server.URL})
	hits, err := client.Search(context.Background(), SearchRequest{
		Collection: "chunks", Vector: []float64{0.1, 0.2}, Filter: `status == "active"`, Limit: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].ChunkID != "doc#c001" || hits[0].Distance != 0.91 {
		t.Fatalf("unexpected hits: %#v", hits)
	}
}

func TestClientSurfacesMilvusError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]any{"code": 1100, "message": "collection not found"})
	}))
	defer server.Close()

	_, err := NewClient(Config{BaseURL: server.URL}).ListCollections(context.Background())
	if err == nil {
		t.Fatal("expected Milvus error")
	}
}

func TestListCollectionsDecodesMilvus26Array(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"code":0,"data":["chunks_a","chunks_b"]}`))
	}))
	defer server.Close()

	collections, err := NewClient(Config{BaseURL: server.URL}).ListCollections(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(collections) != 2 || collections[0] != "chunks_a" {
		t.Fatalf("unexpected collections: %#v", collections)
	}
}

func TestCollectionStatsAcceptsStringAndNumber(t *testing.T) {
	for _, rowCount := range []string{`38`, `"38"`} {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			_, _ = writer.Write([]byte(`{"code":0,"data":{"rowCount":` + rowCount + `}}`))
		}))
		stats, err := NewClient(Config{BaseURL: server.URL}).CollectionStats(context.Background(), "chunks")
		server.Close()
		if err != nil || stats.RowCount != 38 {
			t.Fatalf("rowCount=%s stats=%#v err=%v", rowCount, stats, err)
		}
	}
}
