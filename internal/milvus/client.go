package milvus

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultURL        = "http://127.0.0.1:19530"
	DefaultToken      = "root:Milvus"
	DefaultCollection = "raglab_chunks_qwen3"
)

type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

type Config struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

type apiResponse struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type CollectionDescription struct {
	CollectionName string        `json:"collectionName"`
	CollectionID   int64         `json:"collectionID"`
	Load           string        `json:"load"`
	Fields         []Field       `json:"fields"`
	Indexes        []IndexDetail `json:"indexes"`
}

type Field struct {
	Name       string     `json:"name"`
	Type       string     `json:"type"`
	PrimaryKey bool       `json:"primaryKey"`
	Params     []KeyValue `json:"params"`
}

type KeyValue struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type IndexDetail struct {
	FieldName   string     `json:"fieldName"`
	IndexName   string     `json:"indexName"`
	IndexType   string     `json:"indexType"`
	MetricType  string     `json:"metricType"`
	IndexState  string     `json:"indexState"`
	IndexParams []KeyValue `json:"indexParams"`
}

type CollectionStats struct {
	RowCount flexibleInt64 `json:"rowCount"`
}

type flexibleInt64 int64

func (value *flexibleInt64) UnmarshalJSON(data []byte) error {
	raw := strings.Trim(string(data), `"`)
	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return fmt.Errorf("parse integer %q: %w", raw, err)
	}
	*value = flexibleInt64(parsed)
	return nil
}

type Record struct {
	ChunkID    string    `json:"chunk_id"`
	DocumentID string    `json:"document_id"`
	Title      string    `json:"title"`
	Content    string    `json:"content"`
	TenantID   string    `json:"tenant_id"`
	Product    string    `json:"product"`
	Version    string    `json:"version"`
	Status     string    `json:"status"`
	Visibility string    `json:"visibility"`
	Embedding  []float64 `json:"embedding"`
}

type SearchRequest struct {
	Collection string
	Vector     []float64
	Filter     string
	Limit      int
}

type SearchHit struct {
	ChunkID    string  `json:"chunk_id"`
	DocumentID string  `json:"document_id"`
	Title      string  `json:"title"`
	Content    string  `json:"content"`
	TenantID   string  `json:"tenant_id"`
	Product    string  `json:"product"`
	Version    string  `json:"version"`
	Status     string  `json:"status"`
	Visibility string  `json:"visibility"`
	Distance   float64 `json:"distance"`
}

func NewClient(config Config) *Client {
	baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	if baseURL == "" {
		baseURL = DefaultURL
	}
	token := strings.TrimSpace(config.Token)
	if token == "" {
		token = DefaultToken
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{baseURL: baseURL, token: token, http: httpClient}
}

func (c *Client) ListCollections(ctx context.Context) ([]string, error) {
	var collectionNames []string
	if err := c.post(ctx, "/v2/vectordb/collections/list", map[string]any{}, &collectionNames); err != nil {
		return nil, err
	}
	return collectionNames, nil
}

func (c *Client) DescribeCollection(ctx context.Context, collection string) (CollectionDescription, error) {
	var description CollectionDescription
	err := c.post(ctx, "/v2/vectordb/collections/describe", map[string]any{"collectionName": collection}, &description)
	return description, err
}

func (c *Client) CollectionStats(ctx context.Context, collection string) (CollectionStats, error) {
	var stats CollectionStats
	err := c.post(ctx, "/v2/vectordb/collections/get_stats", map[string]any{"collectionName": collection}, &stats)
	return stats, err
}

func (c *Client) DescribeIndex(ctx context.Context, collection, indexName string) ([]IndexDetail, error) {
	var indexes []IndexDetail
	err := c.post(ctx, "/v2/vectordb/indexes/describe", map[string]any{
		"collectionName": collection,
		"indexName":      indexName,
	}, &indexes)
	return indexes, err
}

func (c *Client) DropCollection(ctx context.Context, collection string) error {
	return c.post(ctx, "/v2/vectordb/collections/drop", map[string]any{"collectionName": collection}, nil)
}

func (c *Client) CreateCollection(ctx context.Context, collection string, dimensions int) error {
	payload := map[string]any{
		"collectionName":   collection,
		"consistencyLevel": "Strong",
		"schema": map[string]any{
			"autoId":             false,
			"enableDynamicField": false,
			"fields": []map[string]any{
				{"fieldName": "chunk_id", "dataType": "VarChar", "isPrimary": true, "elementTypeParams": map[string]string{"max_length": "256"}},
				{"fieldName": "document_id", "dataType": "VarChar", "elementTypeParams": map[string]string{"max_length": "256"}},
				{"fieldName": "title", "dataType": "VarChar", "elementTypeParams": map[string]string{"max_length": "512"}},
				{"fieldName": "content", "dataType": "VarChar", "elementTypeParams": map[string]string{"max_length": "8192"}},
				{"fieldName": "tenant_id", "dataType": "VarChar", "elementTypeParams": map[string]string{"max_length": "128"}},
				{"fieldName": "product", "dataType": "VarChar", "elementTypeParams": map[string]string{"max_length": "128"}},
				{"fieldName": "version", "dataType": "VarChar", "elementTypeParams": map[string]string{"max_length": "64"}},
				{"fieldName": "status", "dataType": "VarChar", "elementTypeParams": map[string]string{"max_length": "32"}},
				{"fieldName": "visibility", "dataType": "VarChar", "elementTypeParams": map[string]string{"max_length": "32"}},
				{"fieldName": "embedding", "dataType": "FloatVector", "elementTypeParams": map[string]string{"dim": fmt.Sprint(dimensions)}},
			},
		},
		"indexParams": []map[string]any{
			{
				"fieldName":  "embedding",
				"indexName":  "embedding_hnsw",
				"indexType":  "HNSW",
				"metricType": "COSINE",
				"params":     map[string]any{"M": 16, "efConstruction": 200},
			},
		},
	}
	return c.post(ctx, "/v2/vectordb/collections/create", payload, nil)
}

func (c *Client) Upsert(ctx context.Context, collection string, records []Record) (int64, error) {
	var data struct {
		UpsertCount int64 `json:"upsertCount"`
	}
	err := c.post(ctx, "/v2/vectordb/entities/upsert", map[string]any{
		"collectionName": collection,
		"data":           records,
	}, &data)
	return data.UpsertCount, err
}

func (c *Client) FlushCollection(ctx context.Context, collection string) error {
	return c.post(ctx, "/v2/vectordb/collections/flush", map[string]any{"collectionName": collection}, nil)
}

func (c *Client) Search(ctx context.Context, input SearchRequest) ([]SearchHit, error) {
	limit := input.Limit
	if limit <= 0 || limit > 50 {
		limit = 5
	}
	payload := map[string]any{
		"collectionName": input.Collection,
		"data":           [][]float64{input.Vector},
		"annsField":      "embedding",
		"limit":          limit,
		"outputFields":   []string{"chunk_id", "document_id", "title", "content", "tenant_id", "product", "version", "status", "visibility"},
		"searchParams": map[string]any{
			"metricType": "COSINE",
			"params":     map[string]any{"ef": 64},
		},
	}
	if strings.TrimSpace(input.Filter) != "" {
		payload["filter"] = input.Filter
	}
	var hits []SearchHit
	if err := c.post(ctx, "/v2/vectordb/entities/search", payload, &hits); err != nil {
		return nil, err
	}
	return hits, nil
}

func (c *Client) post(ctx context.Context, path string, payload any, output any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode Milvus request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create Milvus request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.token)
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("call Milvus %s: %w", path, err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("read Milvus response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Milvus %s returned %s: %s", path, response.Status, strings.TrimSpace(string(responseBody)))
	}
	var envelope apiResponse
	if err := json.Unmarshal(responseBody, &envelope); err != nil {
		return fmt.Errorf("decode Milvus response: %w", err)
	}
	if envelope.Code != 0 && envelope.Code != 200 {
		return fmt.Errorf("Milvus %s failed (code %d): %s", path, envelope.Code, envelope.Message)
	}
	if output != nil && len(envelope.Data) > 0 && string(envelope.Data) != "null" {
		if err := json.Unmarshal(envelope.Data, output); err != nil {
			return fmt.Errorf("decode Milvus data from %s: %w", path, err)
		}
	}
	return nil
}
