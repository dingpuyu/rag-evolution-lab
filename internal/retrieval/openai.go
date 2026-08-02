package retrieval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// OpenAICompatibleEmbedder calls providers that implement the OpenAI
// /v1/embeddings wire format. Keeping this adapter next to OllamaEmbedder
// means the rest of the RAG pipeline only depends on Embedder: the generation
// provider and embedding provider can be changed independently.
//
// TokenHub, private model gateways, and other OpenAI-compatible services can
// all use this adapter. Dimensions is optional; when set it is sent as the
// provider-specific dimensions hint and is also validated against the
// returned vectors.
type OpenAICompatibleEmbedder struct {
	BaseURL          string
	APIKey           string
	Model            string
	Dimensions       int
	BatchSize        int
	QueryInstruction string
	Client           *http.Client
}

func (e OpenAICompatibleEmbedder) Name() string {
	model := strings.TrimSpace(e.Model)
	if model == "" {
		model = "unknown"
	}
	return "openai-compatible/" + model
}

func (e OpenAICompatibleEmbedder) EmbedDocuments(ctx context.Context, texts []string) ([][]float64, error) {
	return e.embed(ctx, texts)
}

func (e OpenAICompatibleEmbedder) EmbedQuery(ctx context.Context, text string) ([]float64, error) {
	if instruction := strings.TrimSpace(e.QueryInstruction); instruction != "" {
		text = "Instruct: " + instruction + "\nQuery: " + text
	}
	vectors, err := e.embed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vectors) != 1 {
		return nil, fmt.Errorf("%s returned %d query embeddings", e.Name(), len(vectors))
	}
	return vectors[0], nil
}

func (e OpenAICompatibleEmbedder) embed(ctx context.Context, texts []string) ([][]float64, error) {
	if strings.TrimSpace(e.APIKey) == "" {
		return nil, fmt.Errorf("%s API key is not configured", e.Name())
	}
	if strings.TrimSpace(e.Model) == "" {
		return nil, fmt.Errorf("%s model must not be empty", e.Name())
	}
	if len(texts) == 0 {
		return [][]float64{}, nil
	}
	batchSize := e.BatchSize
	if batchSize <= 0 || batchSize > len(texts) {
		batchSize = len(texts)
	}
	vectors := make([][]float64, 0, len(texts))
	for start := 0; start < len(texts); start += batchSize {
		end := start + batchSize
		if end > len(texts) {
			end = len(texts)
		}
		batch, err := e.embedBatch(ctx, texts[start:end])
		if err != nil {
			return nil, fmt.Errorf("embed batch %d-%d with %s: %w", start, end, e.Name(), err)
		}
		vectors = append(vectors, batch...)
	}
	return vectors, nil
}

func (e OpenAICompatibleEmbedder) embedBatch(ctx context.Context, texts []string) ([][]float64, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(e.BaseURL), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("%s base URL must not be empty", e.Name())
	}
	payload := map[string]any{"model": e.Model, "input": texts}
	if e.Dimensions > 0 {
		payload["dimensions"] = e.Dimensions
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode %s request: %w", e.Name(), err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create %s request: %w", e.Name(), err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(e.APIKey))
	client := e.Client
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("call %s: %w", e.Name(), err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		responseBody, _ := io.ReadAll(io.LimitReader(response.Body, 16<<10))
		return nil, fmt.Errorf("%s returned %s: %s", e.Name(), response.Status, strings.TrimSpace(string(responseBody)))
	}
	var decoded struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode %s response: %w", e.Name(), err)
	}
	if len(decoded.Data) != len(texts) {
		return nil, fmt.Errorf("%s returned %d embeddings for %d inputs", e.Name(), len(decoded.Data), len(texts))
	}
	// Most providers return data in input order, but the OpenAI contract also
	// exposes an index. Sorting here makes batching deterministic if a gateway
	// reorders the response.
	sort.SliceStable(decoded.Data, func(i, j int) bool { return decoded.Data[i].Index < decoded.Data[j].Index })
	vectors := make([][]float64, len(decoded.Data))
	dimensions := 0
	for index, item := range decoded.Data {
		if len(item.Embedding) == 0 {
			return nil, fmt.Errorf("%s returned an empty embedding at index %d", e.Name(), index)
		}
		if dimensions == 0 {
			dimensions = len(item.Embedding)
		} else if len(item.Embedding) != dimensions {
			return nil, fmt.Errorf("%s embedding %d has %d dimensions, expected %d", e.Name(), index, len(item.Embedding), dimensions)
		}
		if e.Dimensions > 0 && len(item.Embedding) != e.Dimensions {
			return nil, fmt.Errorf("%s returned %d dimensions, expected configured %d", e.Name(), len(item.Embedding), e.Dimensions)
		}
		vectors[index] = item.Embedding
	}
	return vectors, nil
}
