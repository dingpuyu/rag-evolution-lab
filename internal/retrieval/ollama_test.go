package retrieval

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOllamaEmbedderBatchesInputs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/embed" {
			t.Fatalf("unexpected path: %s", request.URL.Path)
		}
		var payload struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.Model != "test-embedding" || len(payload.Input) != 2 {
			t.Fatalf("unexpected payload: %#v", payload)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"embeddings":[[1,0,0],[0,1,0]]}`))
	}))
	defer server.Close()

	embedder := OllamaEmbedder{BaseURL: server.URL, Model: "test-embedding", Client: server.Client()}
	vectors, err := embedder.Embed(context.Background(), []string{"first", "second"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 2 || len(vectors[0]) != 3 {
		t.Fatalf("unexpected vectors: %#v", vectors)
	}
}

func TestOllamaEmbedderRejectsInvalidVectorCount(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"embeddings":[[1,0]]}`))
	}))
	defer server.Close()

	embedder := OllamaEmbedder{BaseURL: server.URL, Model: "test-embedding", Client: server.Client()}
	if _, err := embedder.Embed(context.Background(), []string{"first", "second"}); err == nil {
		t.Fatal("expected vector count validation error")
	}
}

func TestOllamaEmbedderSurfacesServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, `{"error":"model not found"}`, http.StatusNotFound)
	}))
	defer server.Close()

	embedder := OllamaEmbedder{BaseURL: server.URL, Model: "missing", Client: server.Client()}
	if _, err := embedder.Embed(context.Background(), []string{"query"}); err == nil {
		t.Fatal("expected server error")
	}
}
