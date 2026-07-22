package scalebench

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/dingpuyu/rag-evolution-lab/internal/milvus"
)

func TestCheckpointRoundTripPreservesResumeContract(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "checkpoint.json")
	want := seedCheckpoint{
		Dataset:     DatasetConfig{Chunks: 100_000, Dimensions: 1024, Topics: 1_000, Tenants: 100, Seed: 7, Profile: ProfileHardV2},
		Collections: Collections{Flat: "flat", HNSW: "hnsw"},
		NextOffset:  42_000,
	}
	if err := writeCheckpoint(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := readCheckpoint(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Dataset != want.Dataset || got.Collections != want.Collections || got.NextOffset != want.NextOffset || got.Version != 1 {
		t.Fatalf("checkpoint mismatch: got=%#v want=%#v", got, want)
	}
}

func TestUpsertRetriesTransientMilvusFailure(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		attempts++
		if attempts < 3 {
			_ = json.NewEncoder(writer).Encode(map[string]any{"code": 1100, "message": "temporary failure"})
			return
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"code": 0, "data": map[string]any{"upsertCount": 1}})
	}))
	defer server.Close()
	generator, err := NewGenerator(DatasetConfig{Chunks: 10, Dimensions: 8, Topics: 2, Tenants: 1, Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewRunner(milvus.NewClient(milvus.Config{BaseURL: server.URL}), generator, Collections{Flat: "flat", HNSW: "hnsw"})
	if err != nil {
		t.Fatal(err)
	}
	retries, err := runner.upsertWithRetry(context.Background(), "hnsw", generator.Records(0, 1), 2)
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 3 || retries != 2 {
		t.Fatalf("attempts=%d retries=%d", attempts, retries)
	}
}

func TestWaitForIndexRequiresFinishedAndAllRowsIndexed(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v2/vectordb/indexes/describe" {
			t.Fatalf("unexpected path %s", request.URL.Path)
		}
		attempts++
		state := "InProgress"
		indexedRows := 80
		pendingRows := 20
		if attempts >= 2 {
			state = "Finished"
			indexedRows = 100
			pendingRows = 0
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"code": 0, "data": []map[string]any{{
			"indexName": "embedding_hnsw", "indexState": state,
			"indexedRows": indexedRows, "pendingRows": pendingRows, "totalRows": 100,
		}}})
	}))
	defer server.Close()

	generator, err := NewGenerator(DatasetConfig{Chunks: 100, Dimensions: 8, Topics: 10, Tenants: 1, Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewRunner(milvus.NewClient(milvus.Config{BaseURL: server.URL}), generator, Collections{Flat: "flat", HNSW: "hnsw"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := runner.waitForIndex(ctx, "hnsw", "embedding_hnsw", 100); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 {
		t.Fatalf("attempts=%d want=2", attempts)
	}
}
