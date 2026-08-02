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
	vectors, err := embedder.EmbedDocuments(context.Background(), []string{"first", "second"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 2 || len(vectors[0]) != 3 {
		t.Fatalf("unexpected vectors: %#v", vectors)
	}
}

func TestOllamaEmbedderRequestsConfiguredDimensions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload struct {
			Dimensions int `json:"dimensions"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.Dimensions != 1024 {
			t.Fatalf("expected dimensions=1024, got %d", payload.Dimensions)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"embeddings":[[1,0,0]]}`))
	}))
	defer server.Close()

	embedder := OllamaEmbedder{BaseURL: server.URL, Model: "test-embedding", Dimensions: 1024, Client: server.Client()}
	if _, err := embedder.EmbedQuery(context.Background(), "query"); err == nil {
		t.Fatal("expected configured dimension validation error")
	}
}

func TestOllamaEmbedderRejectsInvalidVectorCount(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"embeddings":[[1,0]]}`))
	}))
	defer server.Close()

	embedder := OllamaEmbedder{BaseURL: server.URL, Model: "test-embedding", Client: server.Client()}
	if _, err := embedder.EmbedDocuments(context.Background(), []string{"first", "second"}); err == nil {
		t.Fatal("expected vector count validation error")
	}
}

func TestOllamaEmbedderSurfacesServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, `{"error":"model not found"}`, http.StatusNotFound)
	}))
	defer server.Close()

	embedder := OllamaEmbedder{BaseURL: server.URL, Model: "missing", Client: server.Client()}
	if _, err := embedder.EmbedQuery(context.Background(), "query"); err == nil {
		t.Fatal("expected server error")
	}
}

func TestOllamaEmbedderAppliesInstructionOnlyToQuery(t *testing.T) {
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
		_, _ = writer.Write([]byte(`{"embeddings":[[1,0]]}`))
	}))
	defer server.Close()

	embedder := OllamaEmbedder{
		BaseURL:          server.URL,
		Model:            "test-embedding",
		QueryInstruction: "retrieve relevant passages",
		Client:           server.Client(),
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
	want := "Instruct: retrieve relevant passages\nQuery: question"
	if inputs[1][0] != want {
		t.Fatalf("unexpected instructed query: %q", inputs[1][0])
	}
}
