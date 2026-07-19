package app

import (
	"context"
	"path/filepath"
	"testing"
)

func TestBuildRegistersHybridExperimentPipelines(t *testing.T) {
	runtime, err := Build(filepath.Join("..", "..", "datasets", "corpus", "acmecloud"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"v3-hybrid",
		"v3-hybrid-metadata",
		"v3-hybrid-metadata-consensus",
		"v4-router",
		"v5-rerank",
	} {
		if _, err := runtime.Pipeline(name); err != nil {
			t.Errorf("expected pipeline %q to be registered: %v", name, err)
		}
	}
}

func TestBuildRegistersMilvusEvolutionWithoutBuildingMemoryIndex(t *testing.T) {
	runtime, err := BuildWithOptions(context.Background(), filepath.Join("..", "..", "datasets", "corpus", "acmecloud"), Options{
		OllamaModel:      "query-embedder",
		MilvusURL:        "http://127.0.0.1:19530",
		MilvusCollection: "chunks",
		SkipOllamaMemory: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"v1-milvus",
		"v3-milvus-hybrid",
		"v3-milvus-hybrid-metadata",
		"v3-milvus-hybrid-metadata-consensus",
		"v4-milvus-router",
		"v5-milvus-rerank",
	} {
		if _, err := runtime.Pipeline(name); err != nil {
			t.Errorf("expected pipeline %q to be registered: %v", name, err)
		}
	}
	if _, err := runtime.Pipeline("v1-ollama"); err == nil {
		t.Fatal("memory Ollama pipeline should be skipped in Milvus replacement mode")
	}
}
