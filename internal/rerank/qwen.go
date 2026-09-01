package rerank

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

	"github.com/dingpuyu/rag-evolution-lab/internal/domain"
)

// Qwen calls the qwen3-rerank compatible endpoint for the offline harness.
// The production Knowledge Gateway has its own Milvus-hit adapter; this one
// keeps the evaluator on the same domain.RetrievedChunk contract as all other
// pipeline stages.
type Qwen struct {
	URL      string
	APIKey   string
	Model    string
	Instruct string
	Client   *http.Client
}

func (q Qwen) Name() string {
	if strings.TrimSpace(q.Model) == "" {
		return "qwen3-rerank"
	}
	return strings.TrimSpace(q.Model)
}

func (q Qwen) Rerank(ctx context.Context, request domain.QueryRequest, candidates []domain.RetrievedChunk) ([]domain.RetrievedChunk, error) {
	if len(candidates) < 2 {
		return append([]domain.RetrievedChunk(nil), candidates...), nil
	}
	url := strings.TrimSpace(q.URL)
	if url == "" {
		url = "https://dashscope.aliyuncs.com/compatible-api/v1/reranks"
	}
	if strings.TrimSpace(q.APIKey) == "" {
		return nil, fmt.Errorf("qwen reranker API key is required")
	}
	documents := make([]string, len(candidates))
	for index, candidate := range candidates {
		documents[index] = strings.TrimSpace(strings.Join([]string{
			candidate.Chunk.DocumentTitle,
			strings.Join(candidate.Chunk.HeadingPath, " > "),
			candidate.Chunk.Content,
		}, "\n"))
	}
	instruct := strings.TrimSpace(q.Instruct)
	if instruct == "" {
		instruct = "Retrieve authoritative medical-device product passages. Preserve exact manufacturer, product family, model, configuration and version boundaries."
	}
	body, err := json.Marshal(map[string]any{
		"model": q.Name(), "query": request.Query, "documents": documents,
		"top_n": len(documents), "instruct": instruct,
	})
	if err != nil {
		return nil, err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(url, "/"), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpRequest.Header.Set("Authorization", "Bearer "+strings.TrimSpace(q.APIKey))
	httpRequest.Header.Set("Content-Type", "application/json")
	client := q.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	response, err := client.Do(httpRequest)
	if err != nil {
		return nil, fmt.Errorf("qwen rerank request: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("qwen rerank returned %s: %s", response.Status, strings.TrimSpace(string(responseBody)))
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
	if err := json.Unmarshal(responseBody, &decoded); err != nil {
		return nil, fmt.Errorf("decode qwen rerank response: %w", err)
	}
	results := decoded.Results
	if len(results) == 0 {
		results = decoded.Output.Results
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("qwen rerank response contained no results")
	}
	sort.SliceStable(results, func(i, j int) bool { return results[i].RelevanceScore > results[j].RelevanceScore })
	ranked := make([]domain.RetrievedChunk, 0, len(candidates))
	seen := make(map[int]struct{}, len(results))
	for _, result := range results {
		if result.Index < 0 || result.Index >= len(candidates) {
			return nil, fmt.Errorf("qwen rerank returned invalid document index %d", result.Index)
		}
		if _, duplicate := seen[result.Index]; duplicate {
			continue
		}
		seen[result.Index] = struct{}{}
		candidate := candidates[result.Index]
		candidate.Score = result.RelevanceScore
		candidate.Rank = len(ranked) + 1
		candidate.Stage = "rerank:" + q.Name()
		ranked = append(ranked, candidate)
	}
	for index, candidate := range candidates {
		if _, ok := seen[index]; ok {
			continue
		}
		candidate.Rank = len(ranked) + 1
		candidate.Stage = "rerank:" + q.Name() + "/omitted"
		ranked = append(ranked, candidate)
	}
	return ranked, nil
}
