package rerank

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dingpuyu/rag-evolution-lab/internal/domain"
)

func TestQwenRerankerMapsProviderIndexes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatal("missing authorization")
		}
		var payload struct {
			Documents []string `json:"documents"`
			TopN      int      `json:"top_n"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.Documents) != 2 || payload.TopN != 2 {
			t.Fatalf("payload=%#v", payload)
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"results": []map[string]any{
			{"index": 1, "relevance_score": 0.92}, {"index": 0, "relevance_score": 0.31},
		}})
	}))
	defer server.Close()

	candidates := []domain.RetrievedChunk{
		{Chunk: domain.Chunk{ID: "a", DocumentTitle: "A"}},
		{Chunk: domain.Chunk{ID: "b", DocumentTitle: "B"}},
	}
	results, err := (Qwen{URL: server.URL, APIKey: "test-key", Model: "qwen3-rerank"}).Rerank(
		context.Background(), domain.QueryRequest{Query: "query"}, candidates,
	)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Chunk.ID != "b" || results[0].Score != 0.92 || results[0].Rank != 1 {
		t.Fatalf("results=%#v", results)
	}
}
