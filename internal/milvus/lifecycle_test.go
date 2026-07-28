package milvus

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dingpuyu/rag-evolution-lab/internal/retrieval"
)

type lifecycleMilvusMock struct {
	mu         sync.Mutex
	collection bool
	dimensions int
	records    map[string]Record
	upserts    int
	deletes    int
	aliases    int
}

func (mock *lifecycleMilvusMock) serveHTTP(t *testing.T, writer http.ResponseWriter, request *http.Request) {
	t.Helper()
	mock.mu.Lock()
	defer mock.mu.Unlock()
	var payload map[string]json.RawMessage
	if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
		t.Error(err)
		return
	}
	switch request.URL.Path {
	case "/v2/vectordb/collections/list":
		collections := []string{}
		if mock.collection {
			collections = append(collections, "lifecycle_v1")
		}
		writeMockResponse(t, writer, collections)
	case "/v2/vectordb/collections/create":
		var decoded struct {
			Schema struct {
				Fields []struct {
					Name   string            `json:"fieldName"`
					Params map[string]string `json:"elementTypeParams"`
				} `json:"fields"`
			} `json:"schema"`
		}
		_ = json.Unmarshal(mustRaw(t, payload, ""), &decoded)
		for _, field := range decoded.Schema.Fields {
			if field.Name == "embedding" {
				_, _ = fmtSscan(field.Params["dim"], &mock.dimensions)
			}
		}
		mock.collection = true
		if mock.records == nil {
			mock.records = make(map[string]Record)
		}
		writeMockResponse(t, writer, map[string]any{})
	case "/v2/vectordb/collections/describe":
		fields := []map[string]any{
			{"name": "chunk_id"}, {"name": "document_id"}, {"name": "content_hash"},
			{"name": "embedding_model"}, {"name": "embedding_version"}, {"name": "document_version"},
			{"name": "source_revision"}, {"name": "indexed_at"},
			{"name": "embedding", "params": []map[string]string{{"key": "dim", "value": integerString(mock.dimensions)}}},
		}
		writeMockResponse(t, writer, map[string]any{"collectionName": "lifecycle_v1", "fields": fields})
	case "/v2/vectordb/aliases/create":
		mock.aliases++
		writeMockResponse(t, writer, map[string]any{})
	case "/v2/vectordb/aliases/describe":
		if mock.aliases == 0 {
			if err := json.NewEncoder(writer).Encode(map[string]any{"code": 100, "message": "alias not found"}); err != nil {
				t.Error(err)
			}
			return
		}
		writeMockResponse(t, writer, map[string]any{
			"aliasName": "knowledge_active", "collectionName": "lifecycle_v1", "dbName": "default",
		})
	case "/v2/vectordb/entities/upsert":
		var records []Record
		if err := json.Unmarshal(payload["data"], &records); err != nil {
			t.Error(err)
			return
		}
		for _, record := range records {
			mock.records[record.ChunkID] = record
		}
		mock.upserts++
		writeMockResponse(t, writer, map[string]any{"upsertCount": len(records)})
	case "/v2/vectordb/entities/query":
		var filter string
		_ = json.Unmarshal(payload["filter"], &filter)
		entities := make([]Entity, 0)
		for _, record := range mock.records {
			if lifecycleRecordMatches(record, filter) {
				entities = append(entities, Entity{
					ChunkID: record.ChunkID, DocumentID: record.DocumentID, ContentHash: record.ContentHash,
					EmbeddingModel: record.EmbeddingModel, EmbeddingVer: record.EmbeddingVer,
					DocumentVer: record.DocumentVer, SourceRevision: record.SourceRevision, IndexedAt: record.IndexedAt,
				})
			}
		}
		sort.Slice(entities, func(i, j int) bool { return entities[i].ChunkID < entities[j].ChunkID })
		if strings.Contains(filter, "source_revision > 0") && len(entities) > 1 {
			entities = entities[:1]
		}
		writeMockResponse(t, writer, entities)
	case "/v2/vectordb/entities/delete":
		var filter string
		_ = json.Unmarshal(payload["filter"], &filter)
		for id, record := range mock.records {
			if lifecycleRecordMatches(record, filter) {
				delete(mock.records, id)
			}
		}
		mock.deletes++
		writeMockResponse(t, writer, map[string]any{})
	case "/v2/vectordb/collections/flush":
		writeMockResponse(t, writer, map[string]any{})
	default:
		t.Errorf("unexpected Milvus path %s", request.URL.Path)
	}
}

func TestLifecycleUpsertUpdateDeleteIdempotencyAndRevisionOrdering(t *testing.T) {
	mock := &lifecycleMilvusMock{records: make(map[string]Record)}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mock.serveHTTP(t, writer, request)
	}))
	defer server.Close()
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	config := LifecycleConfig{
		Collection: "lifecycle_v1", Alias: "knowledge_active", EmbeddingVersion: "hash-v1",
		StatePath: t.TempDir() + "/state.json", ChunkRunes: 100, Now: func() time.Time { return now },
	}
	service, err := NewLifecycleService(
		NewClient(Config{BaseURL: server.URL}), retrieval.HashEmbedder{Dimensions: 8}, config,
	)
	if err != nil {
		t.Fatal(err)
	}
	first := LifecycleChange{
		EventID: "evt-1", Operation: OperationUpsert, Revision: 1,
		Document: &LifecycleDocument{
			ID: "doc-1", Title: "SSO指南", Content: "# 配置\n\n第一段。\n\n第二段。", Product: "identity", Version: "1",
			Visibility: "public",
		},
	}
	result, err := service.Apply(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	if result.CurrentChunks != 2 || result.PreviousChunks != 0 || !result.Verified || mock.aliases != 1 {
		t.Fatalf("unexpected first result=%#v aliases=%d", result, mock.aliases)
	}
	duplicate, err := service.Apply(context.Background(), first)
	if err != nil || !duplicate.Duplicate || mock.upserts != 1 {
		t.Fatalf("duplicate=%#v upserts=%d err=%v", duplicate, mock.upserts, err)
	}

	update := LifecycleChange{
		EventID: "evt-2", Operation: OperationUpsert, Revision: 2,
		Document: &LifecycleDocument{
			ID: "doc-1", Title: "SSO指南", Content: "# 配置\n\n更新后的唯一段落。", Product: "identity", Version: "2",
			Visibility: "public",
		},
	}
	updated, err := service.Apply(context.Background(), update)
	if err != nil {
		t.Fatal(err)
	}
	if updated.PreviousChunks != 2 || updated.CurrentChunks != 1 || updated.DeletedChunks != 1 {
		t.Fatalf("stale chunk was not removed: %#v", updated)
	}
	if _, err := service.Apply(context.Background(), LifecycleChange{
		EventID: "evt-stale", Operation: OperationDelete, Revision: 1, DocumentID: "doc-1",
	}); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("out-of-order event must be rejected: %v", err)
	}

	deleted, err := service.Apply(context.Background(), LifecycleChange{
		EventID: "evt-3", Operation: OperationDelete, Revision: 3, DocumentID: "doc-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if deleted.PreviousChunks != 1 || deleted.CurrentChunks != 0 || deleted.DeletedChunks != 1 {
		t.Fatalf("delete was not verified: %#v", deleted)
	}
	if len(mock.records) != 0 {
		t.Fatalf("deleted document still has records: %#v", mock.records)
	}

	restarted, err := NewLifecycleService(
		NewClient(Config{BaseURL: server.URL}), retrieval.HashEmbedder{Dimensions: 8}, config,
	)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := restarted.Apply(context.Background(), LifecycleChange{
		EventID: "evt-3", Operation: OperationDelete, Revision: 3, DocumentID: "doc-1",
	})
	if err != nil || !replayed.Duplicate || mock.deletes != 2 {
		t.Fatalf("persisted idempotency failed: result=%#v deletes=%d err=%v", replayed, mock.deletes, err)
	}
	status := restarted.Status()
	if !status.Documents["doc-1"].Deleted || status.Documents["doc-1"].Revision != 3 || status.PendingEvents != 0 {
		t.Fatalf("unexpected lifecycle status: %#v", status)
	}
	stateData, err := os.ReadFile(config.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stateData), "更新后的唯一段落") {
		t.Fatal("completed event ledger must not retain document content")
	}
}

func TestLifecycleRejectsEmbeddingVersionMixInSameCollection(t *testing.T) {
	mock := &lifecycleMilvusMock{
		collection: true, dimensions: 8,
		records: map[string]Record{"old#c001": {
			ChunkID: "old#c001", DocumentID: "old", ContentHash: "hash", EmbeddingModel: "hash/8",
			EmbeddingVer: "hash-v1", SourceRevision: 1,
		}},
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mock.serveHTTP(t, writer, request)
	}))
	defer server.Close()
	service, err := NewLifecycleService(
		NewClient(Config{BaseURL: server.URL}), retrieval.HashEmbedder{Dimensions: 8},
		LifecycleConfig{Collection: "lifecycle_v1", EmbeddingVersion: "hash-v2", StatePath: t.TempDir() + "/state.json"},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Apply(context.Background(), LifecycleChange{
		EventID: "evt-v2", Operation: OperationUpsert, Revision: 1,
		Document: &LifecycleDocument{ID: "new", Title: "New", Content: "New content", Visibility: "public"},
	})
	if err == nil || !strings.Contains(err.Error(), "build a new collection") {
		t.Fatalf("mixed embedding versions must fail closed: %v", err)
	}
	if mock.upserts != 0 {
		t.Fatal("version mismatch reached upsert")
	}
}

func TestLifecycleValidatesIndexStateBeforeAliasSwitch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v2/vectordb/collections/list":
			writeMockResponse(t, writer, []string{"candidate_v2"})
		case "/v2/vectordb/collections/describe":
			writeMockResponse(t, writer, map[string]any{
				"collectionName": "candidate_v2",
				"fields": []map[string]any{
					{"name": "chunk_id"}, {"name": "document_id"}, {"name": "title"}, {"name": "content"},
					{"name": "tenant_id"}, {"name": "allowed_tenants"}, {"name": "allowed_roles"}, {"name": "product"},
					{"name": "version"}, {"name": "status"}, {"name": "visibility"},
					{"name": "embedding", "params": []map[string]string{{"key": "dim", "value": "8"}}},
				},
				"indexes": []map[string]any{{"fieldName": "embedding", "indexName": "embedding_hnsw"}},
			})
		case "/v2/vectordb/collections/get_stats":
			writeMockResponse(t, writer, map[string]any{"rowCount": 12})
		case "/v2/vectordb/indexes/describe":
			writeMockResponse(t, writer, []map[string]any{{"fieldName": "embedding", "indexName": "embedding_hnsw", "indexState": "Finished"}})
		case "/v2/vectordb/aliases/alter":
			writeMockResponse(t, writer, map[string]any{})
		default:
			t.Errorf("unexpected readiness-check path %s", request.URL.Path)
		}
	}))
	defer server.Close()

	service, err := NewLifecycleService(NewClient(Config{BaseURL: server.URL}), retrieval.HashEmbedder{Dimensions: 8}, LifecycleConfig{
		Collection: "candidate_v2", Alias: "knowledge_active", EmbeddingVersion: "hash-v2", StatePath: t.TempDir() + "/state.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.PublishCollection(context.Background(), "candidate_v2"); err != nil {
		t.Fatalf("ready collection should publish: %v", err)
	}
}

func lifecycleRecordMatches(record Record, filter string) bool {
	if strings.Contains(filter, "source_revision > 0") {
		return record.SourceRevision > 0
	}
	if strings.HasPrefix(filter, `document_id == "`) {
		value := strings.TrimSuffix(strings.TrimPrefix(filter, `document_id == "`), `"`)
		return record.DocumentID == value
	}
	if strings.HasPrefix(filter, "chunk_id in [") {
		return strings.Contains(filter, `"`+record.ChunkID+`"`)
	}
	return false
}

func writeMockResponse(t *testing.T, writer http.ResponseWriter, data any) {
	t.Helper()
	if err := json.NewEncoder(writer).Encode(map[string]any{"code": 0, "data": data}); err != nil {
		t.Error(err)
	}
}

func mustRaw(t *testing.T, payload map[string]json.RawMessage, key string) json.RawMessage {
	t.Helper()
	if key == "" {
		data, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	return payload[key]
}

func fmtSscan(value string, target *int) (int, error) {
	result := 0
	for _, character := range value {
		if character < '0' || character > '9' {
			continue
		}
		result = result*10 + int(character-'0')
	}
	*target = result
	return 1, nil
}

func integerString(value int) string {
	if value == 0 {
		return "8"
	}
	const digits = "0123456789"
	var reversed []byte
	for value > 0 {
		reversed = append(reversed, digits[value%10])
		value /= 10
	}
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	return string(reversed)
}
