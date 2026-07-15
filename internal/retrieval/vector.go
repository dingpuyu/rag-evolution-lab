package retrieval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/dingpuyu/rag-evolution-lab/internal/domain"
	"github.com/dingpuyu/rag-evolution-lab/internal/textutil"
)

type Embedder interface {
	EmbedDocuments(ctx context.Context, texts []string) ([][]float64, error)
	EmbedQuery(ctx context.Context, text string) ([]float64, error)
	Name() string
}

type HashEmbedder struct {
	Dimensions int
}

func (h HashEmbedder) Name() string { return "semantic-hash-v1" }

func (h HashEmbedder) EmbedDocuments(_ context.Context, texts []string) ([][]float64, error) {
	vectors := make([][]float64, len(texts))
	for index, text := range texts {
		vectors[index] = textutil.HashVector(text, h.Dimensions)
	}
	return vectors, nil
}

func (h HashEmbedder) EmbedQuery(_ context.Context, text string) ([]float64, error) {
	return textutil.HashVector(text, h.Dimensions), nil
}

type OllamaEmbedder struct {
	BaseURL          string
	Model            string
	QueryInstruction string
	Client           *http.Client
}

func (o OllamaEmbedder) Name() string { return "ollama/" + o.Model }

func (o OllamaEmbedder) EmbedDocuments(ctx context.Context, texts []string) ([][]float64, error) {
	return o.embed(ctx, texts)
}

func (o OllamaEmbedder) EmbedQuery(ctx context.Context, text string) ([]float64, error) {
	if instruction := strings.TrimSpace(o.QueryInstruction); instruction != "" {
		text = "Instruct: " + instruction + "\nQuery: " + text
	}
	vectors, err := o.embed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return vectors[0], nil
}

func (o OllamaEmbedder) embed(ctx context.Context, texts []string) ([][]float64, error) {
	if strings.TrimSpace(o.Model) == "" {
		return nil, fmt.Errorf("ollama model must not be empty")
	}
	if len(texts) == 0 {
		return [][]float64{}, nil
	}
	baseURL := strings.TrimRight(o.BaseURL, "/")
	if baseURL == "" {
		baseURL = "http://127.0.0.1:11434"
	}
	payload, err := json.Marshal(struct {
		Model string   `json:"model"`
		Input []string `json:"input"`
	}{Model: o.Model, Input: texts})
	if err != nil {
		return nil, fmt.Errorf("encode ollama request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/embed", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create ollama request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	client := o.Client
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("call ollama: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4<<10))
		return nil, fmt.Errorf("ollama returned %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	var decoded struct {
		Embeddings [][]float64 `json:"embeddings"`
	}
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode ollama response: %w", err)
	}
	if len(decoded.Embeddings) != len(texts) {
		return nil, fmt.Errorf("ollama returned %d embeddings for %d inputs", len(decoded.Embeddings), len(texts))
	}
	dimensions := len(decoded.Embeddings[0])
	if dimensions == 0 {
		return nil, fmt.Errorf("ollama returned an empty embedding")
	}
	for index, vector := range decoded.Embeddings {
		if len(vector) != dimensions {
			return nil, fmt.Errorf("ollama embedding %d has %d dimensions, expected %d", index, len(vector), dimensions)
		}
	}
	return decoded.Embeddings, nil
}

type Vector struct {
	chunks   []domain.Chunk
	vectors  [][]float64
	embedder Embedder
	options  Options
}

func NewVector(ctx context.Context, chunks []domain.Chunk, embedder Embedder) (*Vector, error) {
	return NewVectorWithOptions(ctx, chunks, embedder, Options{})
}

func NewVectorWithOptions(ctx context.Context, chunks []domain.Chunk, embedder Embedder, options Options) (*Vector, error) {
	if embedder == nil {
		embedder = HashEmbedder{Dimensions: 512}
	}
	texts := make([]string, len(chunks))
	for index, chunk := range chunks {
		texts[index] = chunk.DocumentTitle + " " + chunk.Content
	}
	vectors, err := embedder.EmbedDocuments(ctx, texts)
	if err != nil {
		return nil, fmt.Errorf("embed corpus with %s: %w", embedder.Name(), err)
	}
	if err := validateEmbeddingBatch(vectors, len(texts)); err != nil {
		return nil, fmt.Errorf("invalid corpus embeddings from %s: %w", embedder.Name(), err)
	}
	index := &Vector{
		chunks:   append([]domain.Chunk(nil), chunks...),
		vectors:  vectors,
		embedder: embedder,
		options:  options,
	}
	return index, nil
}

func (v *Vector) Name() string { return "vector" }

// WithOptions reuses the immutable corpus embeddings while changing only the
// query-time filters. This avoids encoding the same corpus twice when building
// comparable filtered and unfiltered pipelines.
func (v *Vector) WithOptions(options Options) *Vector {
	clone := *v
	clone.options = options
	return &clone
}

func (v *Vector) Search(ctx context.Context, request domain.QueryRequest) ([]domain.RetrievedChunk, error) {
	queryVector, err := v.embedder.EmbedQuery(ctx, request.Query)
	if err != nil {
		return nil, fmt.Errorf("embed query with %s: %w", v.embedder.Name(), err)
	}
	results := make([]domain.RetrievedChunk, 0, len(v.chunks))
	for index, chunk := range v.chunks {
		if !allowed(chunk, request, v.options) {
			continue
		}
		score := textutil.Cosine(queryVector, v.vectors[index])
		if score > 0 {
			results = append(results, domain.RetrievedChunk{Chunk: chunk, Score: score, Stage: v.Name()})
		}
	}
	return rank(results, request.TopK), nil
}
