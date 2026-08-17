package knowledgegateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dingpuyu/rag-evolution-lab/internal/milvus"
)

func TestQwenRerankerMapsProviderIndexesBackToHits(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("missing bearer token")
		}
		var payload struct {
			Model     string   `json:"model"`
			Query     string   `json:"query"`
			Documents []string `json:"documents"`
			TopN      int      `json:"top_n"`
			Instruct  string   `json:"instruct"`
			Input     any      `json:"input"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.Model != "qwen3-rerank" || payload.Query != "SYS-NET-042" || len(payload.Documents) != 2 || payload.TopN != 2 || payload.Instruct == "" || payload.Input != nil {
			t.Fatalf("unexpected qwen3-rerank payload: %#v", payload)
		}
		writeTestJSON(t, writer, map[string]any{"results": []map[string]any{
			{"index": 1, "relevance_score": 0.98}, {"index": 0, "relevance_score": 0.12},
		}})
	}))
	defer server.Close()
	reranker, err := NewQwenReranker(QwenRerankerConfig{URL: server.URL, APIKey: "test-key", StrictMode: true})
	if err != nil {
		t.Fatal(err)
	}
	result, err := reranker.Rerank(context.Background(), "SYS-NET-042", []milvus.SearchHit{
		{ChunkID: "noise", Content: "普通网络说明"}, {ChunkID: "exact", Content: "SYS-NET-042 表示 DHCP 地址获取失败"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result[0].ChunkID != "exact" || !result[0].RerankScoreSet || result[0].RerankScore != 0.98 {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestQwenRerankerUsesCompatibleEndpointByDefault(t *testing.T) {
	reranker, err := NewQwenReranker(QwenRerankerConfig{APIKey: "test-key", StrictMode: true})
	if err != nil {
		t.Fatal(err)
	}
	if reranker.url != "https://dashscope.aliyuncs.com/compatible-api/v1/reranks" {
		t.Fatalf("unexpected default endpoint: %s", reranker.url)
	}
}

func TestQwenRerankerFallsBackWhenProviderFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "temporary", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	reranker, err := NewQwenReranker(QwenRerankerConfig{URL: server.URL, APIKey: "test-key"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := reranker.Rerank(context.Background(), "E1027", []milvus.SearchHit{
		{ChunkID: "noise", Content: "普通 API 错误"}, {ChunkID: "exact", Content: "E1027 表示配额用尽"},
	})
	if err != nil || result[0].ChunkID != "exact" {
		t.Fatalf("fallback failed: result=%#v err=%v", result, err)
	}
}

func writeTestJSON(t *testing.T, writer http.ResponseWriter, value any) {
	t.Helper()
	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		t.Fatal(err)
	}
}
