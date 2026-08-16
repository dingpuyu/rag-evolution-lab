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
	fieldByName := make(map[string]map[string]any, len(fields))
	for _, raw := range fields {
		field := raw.(map[string]any)
		fieldByName[field["fieldName"].(string)] = field
	}
	for _, name := range []string{"allowed_tenants", "allowed_roles"} {
		field := fieldByName[name]
		if field["dataType"] != "Array" || field["elementDataType"] != "VarChar" {
			t.Fatalf("unexpected ACL field %s: %#v", name, field)
		}
		if field["nullable"] != true {
			t.Fatalf("ACL field %s must allow public rows to omit empty arrays: %#v", name, field)
		}
	}
	for _, name := range []string{"dataset_id", "domain", "model_codes", "software_version_from", "software_version_to", "effective_from", "effective_to", "document_revision", "supersedes", "source_file", "source_page", "heading_path", "content_hash", "embedding_model", "embedding_version", "document_version", "source_revision", "indexed_at", "sparse"} {
		if fieldByName[name] == nil {
			t.Fatalf("lifecycle field %s is missing", name)
		}
	}
	vector := fields[len(fields)-1].(map[string]any)
	params := vector["elementTypeParams"].(map[string]any)
	if params["dim"] != "2560" {
		t.Fatalf("unexpected vector dimensions: %#v", params)
	}
	functions := schema["functions"].([]any)
	if len(functions) != 1 || functions[0].(map[string]any)["type"] != "BM25" {
		t.Fatalf("BM25 function is missing: %#v", functions)
	}
	if len(indexes) != 2 || indexes[1].(map[string]any)["indexType"] != "SPARSE_INVERTED_INDEX" {
		t.Fatalf("sparse BM25 index is missing: %#v", indexes)
	}
}

func TestQueryDeleteAndAliasUseLifecycleContracts(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		paths = append(paths, request.URL.Path)
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		switch request.URL.Path {
		case "/v2/vectordb/entities/query":
			if payload["filter"] != `document_id == "doc-1"` || payload["consistencyLevel"] != "Strong" {
				t.Fatalf("unexpected query payload: %#v", payload)
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"code": 0, "data": []map[string]any{{"chunk_id": "doc-1#c001", "document_id": "doc-1", "source_revision": 7}},
			})
		case "/v2/vectordb/entities/delete":
			if payload["filter"] != `chunk_id in ["doc-1#c002"]` {
				t.Fatalf("unexpected delete payload: %#v", payload)
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"code": 0, "data": map[string]any{}})
		case "/v2/vectordb/aliases/create", "/v2/vectordb/aliases/alter":
			if payload["collectionName"] != "chunks_v2" || payload["aliasName"] != "chunks_active" {
				t.Fatalf("unexpected alias payload: %#v", payload)
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"code": 0, "data": map[string]any{}})
		case "/v2/vectordb/aliases/describe":
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"code": 0, "data": map[string]any{"aliasName": "chunks_active", "collectionName": "chunks_v2", "dbName": "default"},
			})
		default:
			t.Fatalf("unexpected path %s", request.URL.Path)
		}
	}))
	defer server.Close()
	client := NewClient(Config{BaseURL: server.URL})
	entities, err := client.QueryEntities(context.Background(), "chunks_active", `document_id == "doc-1"`, 100)
	if err != nil || len(entities) != 1 || entities[0].SourceRevision != 7 {
		t.Fatalf("entities=%#v err=%v", entities, err)
	}
	if err := client.DeleteByFilter(context.Background(), "chunks_active", `chunk_id in ["doc-1#c002"]`); err != nil {
		t.Fatal(err)
	}
	if err := client.CreateAlias(context.Background(), "chunks_v2", "chunks_active"); err != nil {
		t.Fatal(err)
	}
	alias, err := client.DescribeAlias(context.Background(), "chunks_active")
	if err != nil || alias.CollectionName != "chunks_v2" {
		t.Fatalf("alias=%#v err=%v", alias, err)
	}
	if err := client.AlterAlias(context.Background(), "chunks_v2", "chunks_active"); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 5 {
		t.Fatalf("unexpected calls: %#v", paths)
	}
}

func TestCreateCollectionSupportsFlatGroundTruthIndex(t *testing.T) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"code": 0, "data": map[string]any{}})
	}))
	defer server.Close()
	client := NewClient(Config{BaseURL: server.URL})
	if err := client.CreateCollectionWithOptions(context.Background(), "exact", CollectionOptions{Dimensions: 1024, IndexType: "FLAT"}); err != nil {
		t.Fatal(err)
	}
	index := payload["indexParams"].([]any)[0].(map[string]any)
	if index["indexType"] != "FLAT" || index["metricType"] != "COSINE" {
		t.Fatalf("unexpected flat index: %#v", index)
	}
	if len(index["params"].(map[string]any)) != 0 {
		t.Fatalf("FLAT index should not receive HNSW params: %#v", index)
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

func TestHybridSearchSendsDenseBM25AndRRF(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v2/vectordb/entities/hybrid_search" {
			t.Fatalf("unexpected path %s", request.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		searches := payload["search"].([]any)
		if len(searches) != 2 || searches[0].(map[string]any)["annsField"] != "embedding" || searches[1].(map[string]any)["annsField"] != "sparse" {
			t.Fatalf("unexpected hybrid branches: %#v", searches)
		}
		if searches[1].(map[string]any)["filter"] != `dataset_id == "medical"` {
			t.Fatalf("ACL filter must be present on every branch: %#v", searches)
		}
		rerank := payload["rerank"].(map[string]any)
		if rerank["strategy"] != "rrf" || rerank["params"].(map[string]any)["k"] != float64(60) {
			t.Fatalf("unexpected RRF contract: %#v", rerank)
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"code": 0, "data": []map[string]any{{"chunk_id": "exact", "distance": 0.032}, {"chunk_id": "semantic", "distance": 0.021}},
		})
	}))
	defer server.Close()

	hits, err := NewClient(Config{BaseURL: server.URL}).HybridSearch(context.Background(), HybridSearchRequest{
		Collection: "medical_v2", Vector: []float64{0.1, 0.2}, QueryText: "SYS-NET-042",
		Filter: `dataset_id == "medical"`, Limit: 2, CandidateK: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0].FusionScore != 0.032 || hits[0].Distance != 0 || hits[0].RecallSources[2] != "rrf" {
		t.Fatalf("unexpected hybrid hits: %#v", hits)
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

func TestDescribeIndexAcceptsStringAndNumberRowProgress(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"code":0,"data":[{"indexName":"embedding_hnsw","indexState":"Finished","indexedRows":"100000","pendingRows":0,"totalRows":"100000"}]}`))
	}))
	defer server.Close()

	indexes, err := NewClient(Config{BaseURL: server.URL}).DescribeIndex(context.Background(), "chunks", "embedding_hnsw")
	if err != nil {
		t.Fatal(err)
	}
	if len(indexes) != 1 || indexes[0].IndexedRowCount() != 100000 || indexes[0].PendingRowCount() != 0 || indexes[0].TotalRowCount() != 100000 {
		t.Fatalf("unexpected index progress: %#v", indexes)
	}
}
