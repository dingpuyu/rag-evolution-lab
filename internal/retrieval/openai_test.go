package retrieval

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAICompatibleEmbedderBatchesAndSortsByIndex(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/embeddings" {
			t.Fatalf("unexpected path: %s", request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("missing authorization header")
		}
		var payload struct {
			Model      string   `json:"model"`
			Input      []string `json:"input"`
			Dimensions int      `json:"dimensions"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.Model != "qwen3-embedding-4b" || len(payload.Input) != 2 || payload.Dimensions != 3 {
			t.Fatalf("unexpected payload: %#v", payload)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"data":[{"object":"embedding","index":1,"embedding":[0,1,0]},{"object":"embedding","index":0,"embedding":[1,0,0]}]}`))
	}))
	defer server.Close()

	embedder := OpenAICompatibleEmbedder{
		BaseURL: server.URL + "/v1", APIKey: "test-key", Model: "qwen3-embedding-4b", Dimensions: 3, Client: server.Client(),
	}
	vectors, err := embedder.EmbedDocuments(context.Background(), []string{"first", "second"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 2 || len(vectors[0]) != 3 || vectors[0][0] != 1 || vectors[1][1] != 1 {
		t.Fatalf("unexpected vectors: %#v", vectors)
	}
}

func TestOpenAICompatibleEmbedderAppliesInstructionOnlyToQuery(t *testing.T) {
	var inputs [][]string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		inputs = append(inputs, payload.Input)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"data":[{"index":0,"embedding":[1,0]}]}`))
	}))
	defer server.Close()

	embedder := OpenAICompatibleEmbedder{
		BaseURL: server.URL, APIKey: "key", Model: "qwen", QueryInstruction: "retrieve relevant passages", Client: server.Client(),
	}
	if _, err := embedder.EmbedDocuments(context.Background(), []string{"document"}); err != nil {
		t.Fatal(err)
	}
	if _, err := embedder.EmbedQuery(context.Background(), "question"); err != nil {
		t.Fatal(err)
	}
	if inputs[0][0] != "document" {
		t.Fatalf("document should not have query instruction: %q", inputs[0][0])
	}
	if want := "Instruct: retrieve relevant passages\nQuery: question"; inputs[1][0] != want {
		t.Fatalf("unexpected instructed query: %q", inputs[1][0])
	}
}

func TestOpenAICompatibleEmbedderValidatesDimensions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"data":[{"index":0,"embedding":[1,0]}]}`))
	}))
	defer server.Close()
	embedder := OpenAICompatibleEmbedder{BaseURL: server.URL, APIKey: "key", Model: "qwen", Dimensions: 3, Client: server.Client()}
	if _, err := embedder.EmbedQuery(context.Background(), "question"); err == nil {
		t.Fatal("expected dimensions validation error")
	}
}
