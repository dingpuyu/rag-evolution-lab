package retrieval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const embeddingCacheVersion = 1

type CachedEmbedder struct {
	Inner Embedder
	Dir   string
}

type embeddingCacheEntry struct {
	Version  int         `json:"version"`
	Embedder string      `json:"embedder"`
	Vectors  [][]float64 `json:"vectors"`
}

func (c CachedEmbedder) Name() string { return c.Inner.Name() }

func (c CachedEmbedder) EmbedQuery(ctx context.Context, text string) ([]float64, error) {
	return c.Inner.EmbedQuery(ctx, text)
}

func (c CachedEmbedder) EmbedDocuments(ctx context.Context, texts []string) ([][]float64, error) {
	if c.Inner == nil {
		return nil, fmt.Errorf("cached embedder requires an inner embedder")
	}
	if c.Dir == "" {
		return c.Inner.EmbedDocuments(ctx, texts)
	}
	key, err := documentCacheKey(c.Inner.Name(), texts)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(c.Dir, key+".json")
	if vectors, ok := readEmbeddingCache(path, c.Inner.Name(), len(texts)); ok {
		return vectors, nil
	}

	vectors, err := c.Inner.EmbedDocuments(ctx, texts)
	if err != nil {
		return nil, err
	}
	if err := validateEmbeddingBatch(vectors, len(texts)); err != nil {
		return nil, fmt.Errorf("invalid embeddings from %s: %w", c.Inner.Name(), err)
	}
	if err := writeEmbeddingCache(path, embeddingCacheEntry{
		Version: embeddingCacheVersion, Embedder: c.Inner.Name(), Vectors: vectors,
	}); err != nil {
		return nil, err
	}
	return vectors, nil
}

func documentCacheKey(embedder string, texts []string) (string, error) {
	payload, err := json.Marshal(struct {
		Version  int      `json:"version"`
		Embedder string   `json:"embedder"`
		Texts    []string `json:"texts"`
	}{Version: embeddingCacheVersion, Embedder: embedder, Texts: texts})
	if err != nil {
		return "", fmt.Errorf("encode embedding cache key: %w", err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func readEmbeddingCache(path, embedder string, expected int) ([][]float64, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var entry embeddingCacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, false
	}
	if entry.Version != embeddingCacheVersion || entry.Embedder != embedder {
		return nil, false
	}
	if err := validateEmbeddingBatch(entry.Vectors, expected); err != nil {
		return nil, false
	}
	return entry.Vectors, true
}

func writeEmbeddingCache(path string, entry embeddingCacheEntry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create embedding cache directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".embedding-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary embedding cache: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	encoder := json.NewEncoder(temporary)
	if err := encoder.Encode(entry); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("encode embedding cache: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close embedding cache: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("publish embedding cache: %w", err)
	}
	return nil
}

func validateEmbeddingBatch(vectors [][]float64, expected int) error {
	if len(vectors) != expected {
		return fmt.Errorf("got %d vectors, expected %d", len(vectors), expected)
	}
	if expected == 0 {
		return nil
	}
	dimensions := len(vectors[0])
	if dimensions == 0 {
		return fmt.Errorf("vector dimensions must be positive")
	}
	for index, vector := range vectors {
		if len(vector) != dimensions {
			return fmt.Errorf("vector %d has %d dimensions, expected %d", index, len(vector), dimensions)
		}
	}
	return nil
}
