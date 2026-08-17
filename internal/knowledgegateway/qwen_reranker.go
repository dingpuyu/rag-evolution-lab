package knowledgegateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/dingpuyu/rag-evolution-lab/internal/milvus"
)

// QwenReranker calls the Alibaba Cloud qwen3-rerank text API. The API is kept
// behind HitReranker so CI can use the deterministic implementation while the
// production profile changes no Gateway contract.
type QwenReranker struct {
	url      string
	apiKey   string
	model    string
	instruct string
	client   *http.Client
	fallback HitReranker
	strict   bool
}

type QwenRerankerConfig struct {
	URL        string
	APIKey     string
	Model      string
	Instruct   string
	Timeout    time.Duration
	Fallback   HitReranker
	StrictMode bool
}

func NewQwenReranker(config QwenRerankerConfig) (*QwenReranker, error) {
	config.URL = strings.TrimRight(strings.TrimSpace(config.URL), "/")
	if config.URL == "" {
		config.URL = "https://dashscope.aliyuncs.com/compatible-api/v1/reranks"
	}
	if strings.TrimSpace(config.APIKey) == "" {
		return nil, fmt.Errorf("qwen reranker API key is required")
	}
	if strings.TrimSpace(config.Model) == "" {
		config.Model = "qwen3-rerank"
	}
	if strings.TrimSpace(config.Instruct) == "" {
		config.Instruct = "Given a device maintenance question, retrieve authoritative passages that answer the question while respecting exact model, version, error-code and lot identifiers."
	}
	if config.Timeout <= 0 {
		config.Timeout = 20 * time.Second
	}
	if config.Fallback == nil && !config.StrictMode {
		fallback := NewHeuristicReranker()
		config.Fallback = fallback
	}
	return &QwenReranker{
		url: config.URL, apiKey: strings.TrimSpace(config.APIKey), model: strings.TrimSpace(config.Model),
		instruct: config.Instruct, client: &http.Client{Timeout: config.Timeout}, fallback: config.Fallback, strict: config.StrictMode,
	}, nil
}

func (reranker *QwenReranker) Name() string { return reranker.model }

func (reranker *QwenReranker) Rerank(ctx context.Context, query string, hits []milvus.SearchHit) ([]milvus.SearchHit, error) {
	if len(hits) < 2 {
		return append([]milvus.SearchHit(nil), hits...), nil
	}
	documents := make([]string, len(hits))
	for index, hit := range hits {
		documents[index] = strings.TrimSpace(strings.Join([]string{hit.Title, strings.Join(hit.HeadingPath, " > "), hit.Content}, "\n"))
	}
	payload, err := json.Marshal(map[string]any{
		"model": reranker.model, "query": query, "documents": documents, "top_n": len(documents), "instruct": reranker.instruct,
	})
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, reranker.url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+reranker.apiKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := reranker.client.Do(request)
	if err != nil {
		return reranker.fail(ctx, query, hits, fmt.Errorf("qwen rerank request: %w", err))
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return reranker.fail(ctx, query, hits, err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return reranker.fail(ctx, query, hits, fmt.Errorf("qwen rerank returned %d: %s", response.StatusCode, strings.TrimSpace(string(body))))
	}
	var decoded struct {
		Results []struct {
			Index          int     `json:"index"`
			RelevanceScore float64 `json:"relevance_score"`
		} `json:"results"`
		Output struct {
			Results []struct {
				Index          int     `json:"index"`
				RelevanceScore float64 `json:"relevance_score"`
			} `json:"results"`
		} `json:"output"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return reranker.fail(ctx, query, hits, fmt.Errorf("decode qwen rerank response: %w", err))
	}
	results := decoded.Results
	if len(results) == 0 {
		results = decoded.Output.Results
	}
	if len(results) == 0 {
		return reranker.fail(ctx, query, hits, fmt.Errorf("qwen rerank response contained no results"))
	}
	ranked := make([]milvus.SearchHit, 0, len(results))
	seen := make(map[int]struct{}, len(results))
	for _, result := range results {
		if result.Index < 0 || result.Index >= len(hits) {
			return reranker.fail(ctx, query, hits, fmt.Errorf("qwen rerank returned invalid document index %d", result.Index))
		}
		if _, duplicate := seen[result.Index]; duplicate {
			continue
		}
		seen[result.Index] = struct{}{}
		hit := hits[result.Index]
		hit.RerankScore, hit.RerankScoreSet = result.RelevanceScore, true
		ranked = append(ranked, hit)
	}
	// top_n asks for every candidate, but retaining omitted hits makes the
	// adapter robust to provider-side truncation without losing evidence.
	for index, hit := range hits {
		if _, ok := seen[index]; !ok {
			ranked = append(ranked, hit)
		}
	}
	return ranked, nil
}

func (reranker *QwenReranker) fail(ctx context.Context, query string, hits []milvus.SearchHit, cause error) ([]milvus.SearchHit, error) {
	if reranker.strict || reranker.fallback == nil {
		return nil, cause
	}
	return reranker.fallback.Rerank(ctx, query, hits)
}
