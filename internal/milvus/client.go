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
	FieldName   string        `json:"fieldName"`
	IndexName   string        `json:"indexName"`
	IndexType   string        `json:"indexType"`
	MetricType  string        `json:"metricType"`
	IndexState  string        `json:"indexState"`
	IndexedRows flexibleInt64 `json:"indexedRows"`
	PendingRows flexibleInt64 `json:"pendingRows"`
	TotalRows   flexibleInt64 `json:"totalRows"`
	IndexParams []KeyValue    `json:"indexParams"`
}

func (detail IndexDetail) IndexedRowCount() int64 { return int64(detail.IndexedRows) }
func (detail IndexDetail) PendingRowCount() int64 { return int64(detail.PendingRows) }
func (detail IndexDetail) TotalRowCount() int64   { return int64(detail.TotalRows) }

type CollectionStats struct {
	RowCount flexibleInt64 `json:"rowCount"`
}

type AliasDescription struct {
	Database       string `json:"dbName"`
	CollectionName string `json:"collectionName"`
	AliasName      string `json:"aliasName"`
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
	ChunkID             string   `json:"chunk_id"`
	DocumentID          string   `json:"document_id"`
	DatasetID           string   `json:"dataset_id"`
	Title               string   `json:"title"`
	Content             string   `json:"content"`
	TenantID            string   `json:"tenant_id"`
	AllowedTenants      []string `json:"allowed_tenants,omitempty"`
	AllowedRoles        []string `json:"allowed_roles,omitempty"`
	Domain              string   `json:"domain"`
	Manufacturer        string   `json:"manufacturer"`
	ProductFamily       string   `json:"product_family"`
	ModelCodes          []string `json:"model_codes,omitempty"`
	SoftwareVersionFrom string   `json:"software_version_from"`
	SoftwareVersionTo   string   `json:"software_version_to"`
	HardwareRevision    string   `json:"hardware_revision"`
	Region              string   `json:"region"`
	Language            string   `json:"language"`
	EffectiveFrom       string   `json:"effective_from"`
	EffectiveTo         string   `json:"effective_to"`
	AuthorityLevel      string   `json:"authority_level"`
	DocumentRevision    string   `json:"document_revision"`
	Supersedes          []string `json:"supersedes,omitempty"`
	SourceFile          string   `json:"source_file"`
	SourcePage          int64    `json:"source_page"`
	SourceSheet         string   `json:"source_sheet"`
	SourceCellRange     string   `json:"source_cell_range"`
	HeadingPath         []string `json:"heading_path,omitempty"`
	DeviceIdentifiers   []string `json:"device_identifiers,omitempty"`
	AffectedLots        []string `json:"affected_lots,omitempty"`
	Product             string   `json:"product"`
	Version             string   `json:"version"`
	Status              string   `json:"status"`
	Visibility          string   `json:"visibility"`
	ContentHash         string   `json:"content_hash,omitempty"`
	EmbeddingModel      string   `json:"embedding_model,omitempty"`
	EmbeddingVer        string   `json:"embedding_version,omitempty"`
	DocumentVer         string   `json:"document_version,omitempty"`
	// Int64 fields are required by Milvus when dynamic fields are disabled.
	// Do not omit zero values: the v2 REST row encoder otherwise sends an empty
	// string for a missing scalar and Milvus rejects the whole batch.
	SourceRevision int64     `json:"source_revision"`
	IndexedAt      int64     `json:"indexed_at"`
	Embedding      []float64 `json:"embedding"`
}

type Entity struct {
	ChunkID          string      `json:"chunk_id"`
	DocumentID       string      `json:"document_id"`
	DatasetID        string      `json:"dataset_id"`
	Title            string      `json:"title"`
	TenantID         string      `json:"tenant_id"`
	Product          string      `json:"product"`
	Version          string      `json:"version"`
	Status           string      `json:"status"`
	Visibility       string      `json:"visibility"`
	Manufacturer     string      `json:"manufacturer"`
	ProductFamily    string      `json:"product_family"`
	ModelCodes       stringArray `json:"model_codes"`
	Region           string      `json:"region"`
	Language         string      `json:"language"`
	AuthorityLevel   string      `json:"authority_level"`
	DocumentRevision string      `json:"document_revision"`
	SourceFile       string      `json:"source_file"`
	ContentHash      string      `json:"content_hash"`
	EmbeddingModel   string      `json:"embedding_model"`
	EmbeddingVer     string      `json:"embedding_version"`
	DocumentVer      string      `json:"document_version"`
	SourceRevision   int64       `json:"source_revision"`
	IndexedAt        int64       `json:"indexed_at"`
}

type SearchRequest struct {
	Collection string
	Vector     []float64
	Filter     string
	Limit      int
	EF         int
	Exact      bool
}

// HybridSearchRequest executes Milvus-side reciprocal-rank fusion over the
// dense embedding and the BM25 sparse vector generated from content. QueryText
// deliberately remains the original/rewrite-safe text so equipment model,
// error-code and batch identifiers reach the lexical branch unchanged.
type HybridSearchRequest struct {
	Collection string
	Vector     []float64
	QueryText  string
	Filter     string
	Limit      int
	CandidateK int
	EF         int
	RRFK       int
}

type SearchHit struct {
	ChunkID             string      `json:"chunk_id"`
	DocumentID          string      `json:"document_id"`
	DatasetID           string      `json:"dataset_id"`
	Title               string      `json:"title"`
	Content             string      `json:"content"`
	TenantID            string      `json:"tenant_id"`
	AllowedTenants      stringArray `json:"allowed_tenants"`
	AllowedRoles        stringArray `json:"allowed_roles"`
	Domain              string      `json:"domain"`
	Manufacturer        string      `json:"manufacturer"`
	ProductFamily       string      `json:"product_family"`
	ModelCodes          stringArray `json:"model_codes"`
	SoftwareVersionFrom string      `json:"software_version_from"`
	SoftwareVersionTo   string      `json:"software_version_to"`
	HardwareRevision    string      `json:"hardware_revision"`
	Region              string      `json:"region"`
	Language            string      `json:"language"`
	EffectiveFrom       string      `json:"effective_from"`
	EffectiveTo         string      `json:"effective_to"`
	AuthorityLevel      string      `json:"authority_level"`
	DocumentRevision    string      `json:"document_revision"`
	Supersedes          stringArray `json:"supersedes"`
	SourceFile          string      `json:"source_file"`
	SourcePage          int64       `json:"source_page"`
	SourceSheet         string      `json:"source_sheet"`
	SourceCellRange     string      `json:"source_cell_range"`
	HeadingPath         stringArray `json:"heading_path"`
	DeviceIdentifiers   stringArray `json:"device_identifiers"`
	AffectedLots        stringArray `json:"affected_lots"`
	Product             string      `json:"product"`
	Version             string      `json:"version"`
	Status              string      `json:"status"`
	Visibility          string      `json:"visibility"`
	Distance            float64     `json:"distance"`
	FusionScore         float64     `json:"fusion_score,omitempty"`
	RecallSources       []string    `json:"recall_sources,omitempty"`
	ExactMatches        []string    `json:"exact_matches,omitempty"`
	// RerankScore is populated only inside the gateway after a local or hosted
	// reranker runs. It is intentionally hidden from the Milvus/API payload;
	// the gateway uses it to merge independently reranked bindings globally.
	RerankScore    float64 `json:"-"`
	RerankScoreSet bool    `json:"-"`
}

// stringArray accepts both the intuitive JSON array used by mocks and the
// typed FieldData envelope returned by the Milvus v2 REST gateway.
type stringArray []string

func (value *stringArray) UnmarshalJSON(data []byte) error {
	var direct []string
	if err := json.Unmarshal(data, &direct); err == nil {
		*value = direct
		return nil
	}
	var wrapped struct {
		Data struct {
			StringData struct {
				Data []string `json:"data"`
			} `json:"StringData"`
		} `json:"Data"`
	}
	if err := json.Unmarshal(data, &wrapped); err != nil {
		return fmt.Errorf("decode Milvus VarChar array: %w", err)
	}
	*value = wrapped.Data.StringData.Data
	return nil
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
	return c.CreateCollectionWithOptions(ctx, collection, CollectionOptions{
		Dimensions: dimensions, IndexType: "HNSW", MetricType: "COSINE", M: 16, EFConstruction: 200,
	})
}

type CollectionOptions struct {
	Dimensions     int
	IndexType      string
	MetricType     string
	M              int
	EFConstruction int
}

func (c *Client) CreateCollectionWithOptions(ctx context.Context, collection string, options CollectionOptions) error {
	if options.Dimensions <= 0 {
		return fmt.Errorf("vector dimensions must be positive")
	}
	indexType := strings.ToUpper(strings.TrimSpace(options.IndexType))
	if indexType == "" {
		indexType = "HNSW"
	}
	metricType := strings.ToUpper(strings.TrimSpace(options.MetricType))
	if metricType == "" {
		metricType = "COSINE"
	}
	indexParameters := map[string]any{}
	if indexType == "HNSW" {
		m := options.M
		if m <= 0 {
			m = 16
		}
		efConstruction := options.EFConstruction
		if efConstruction <= 0 {
			efConstruction = 200
		}
		indexParameters = map[string]any{"M": m, "efConstruction": efConstruction}
	}
	payload := map[string]any{
		"collectionName":   collection,
		"consistencyLevel": "Strong",
		"schema": map[string]any{
			"autoId":             false,
			"enableDynamicField": false,
			"functions": []map[string]any{
				{
					"name": "content_bm25", "description": "BM25 sparse vectors generated from document content",
					"type": "BM25", "inputFieldNames": []string{"content"}, "outputFieldNames": []string{"sparse"},
					"params": map[string]any{},
				},
			},
			"fields": []map[string]any{
				{"fieldName": "chunk_id", "dataType": "VarChar", "isPrimary": true, "elementTypeParams": map[string]string{"max_length": "256"}},
				{"fieldName": "document_id", "dataType": "VarChar", "elementTypeParams": map[string]string{"max_length": "256"}},
				{"fieldName": "dataset_id", "dataType": "VarChar", "elementTypeParams": map[string]string{"max_length": "256"}},
				{"fieldName": "title", "dataType": "VarChar", "elementTypeParams": map[string]string{"max_length": "512"}},
				{"fieldName": "content", "dataType": "VarChar", "elementTypeParams": map[string]any{"max_length": "8192", "enable_analyzer": true, "enable_match": true}},
				{"fieldName": "tenant_id", "dataType": "VarChar", "elementTypeParams": map[string]string{"max_length": "128"}},
				{"fieldName": "allowed_tenants", "dataType": "Array", "elementDataType": "VarChar", "nullable": true, "elementTypeParams": map[string]string{"max_capacity": "16", "max_length": "128"}},
				{"fieldName": "allowed_roles", "dataType": "Array", "elementDataType": "VarChar", "nullable": true, "elementTypeParams": map[string]string{"max_capacity": "16", "max_length": "128"}},
				{"fieldName": "domain", "dataType": "VarChar", "elementTypeParams": map[string]string{"max_length": "128"}},
				{"fieldName": "manufacturer", "dataType": "VarChar", "elementTypeParams": map[string]string{"max_length": "128"}},
				{"fieldName": "product_family", "dataType": "VarChar", "elementTypeParams": map[string]string{"max_length": "128"}},
				{"fieldName": "model_codes", "dataType": "Array", "elementDataType": "VarChar", "nullable": true, "elementTypeParams": map[string]string{"max_capacity": "32", "max_length": "128"}},
				{"fieldName": "software_version_from", "dataType": "VarChar", "elementTypeParams": map[string]string{"max_length": "64"}},
				{"fieldName": "software_version_to", "dataType": "VarChar", "elementTypeParams": map[string]string{"max_length": "64"}},
				{"fieldName": "hardware_revision", "dataType": "VarChar", "elementTypeParams": map[string]string{"max_length": "64"}},
				{"fieldName": "region", "dataType": "VarChar", "elementTypeParams": map[string]string{"max_length": "64"}},
				{"fieldName": "language", "dataType": "VarChar", "elementTypeParams": map[string]string{"max_length": "32"}},
				{"fieldName": "effective_from", "dataType": "VarChar", "elementTypeParams": map[string]string{"max_length": "32"}},
				{"fieldName": "effective_to", "dataType": "VarChar", "elementTypeParams": map[string]string{"max_length": "32"}},
				{"fieldName": "authority_level", "dataType": "VarChar", "elementTypeParams": map[string]string{"max_length": "64"}},
				{"fieldName": "document_revision", "dataType": "VarChar", "elementTypeParams": map[string]string{"max_length": "64"}},
				{"fieldName": "supersedes", "dataType": "Array", "elementDataType": "VarChar", "nullable": true, "elementTypeParams": map[string]string{"max_capacity": "32", "max_length": "256"}},
				{"fieldName": "source_file", "dataType": "VarChar", "elementTypeParams": map[string]string{"max_length": "512"}},
				{"fieldName": "source_page", "dataType": "Int64"},
				{"fieldName": "source_sheet", "dataType": "VarChar", "elementTypeParams": map[string]string{"max_length": "256"}},
				{"fieldName": "source_cell_range", "dataType": "VarChar", "elementTypeParams": map[string]string{"max_length": "128"}},
				{"fieldName": "heading_path", "dataType": "Array", "elementDataType": "VarChar", "nullable": true, "elementTypeParams": map[string]string{"max_capacity": "32", "max_length": "256"}},
				{"fieldName": "device_identifiers", "dataType": "Array", "elementDataType": "VarChar", "nullable": true, "elementTypeParams": map[string]string{"max_capacity": "32", "max_length": "128"}},
				{"fieldName": "affected_lots", "dataType": "Array", "elementDataType": "VarChar", "nullable": true, "elementTypeParams": map[string]string{"max_capacity": "64", "max_length": "128"}},
				{"fieldName": "product", "dataType": "VarChar", "elementTypeParams": map[string]string{"max_length": "128"}},
				{"fieldName": "version", "dataType": "VarChar", "elementTypeParams": map[string]string{"max_length": "64"}},
				{"fieldName": "status", "dataType": "VarChar", "elementTypeParams": map[string]string{"max_length": "32"}},
				{"fieldName": "visibility", "dataType": "VarChar", "elementTypeParams": map[string]string{"max_length": "32"}},
				{"fieldName": "content_hash", "dataType": "VarChar", "elementTypeParams": map[string]string{"max_length": "64"}},
				{"fieldName": "embedding_model", "dataType": "VarChar", "elementTypeParams": map[string]string{"max_length": "256"}},
				{"fieldName": "embedding_version", "dataType": "VarChar", "elementTypeParams": map[string]string{"max_length": "64"}},
				{"fieldName": "document_version", "dataType": "VarChar", "elementTypeParams": map[string]string{"max_length": "64"}},
				{"fieldName": "source_revision", "dataType": "Int64"},
				{"fieldName": "indexed_at", "dataType": "Int64"},
				{"fieldName": "sparse", "dataType": "SparseFloatVector"},
				{"fieldName": "embedding", "dataType": "FloatVector", "elementTypeParams": map[string]string{"dim": fmt.Sprint(options.Dimensions)}},
			},
		},
		"indexParams": []map[string]any{
			{
				"fieldName":  "embedding",
				"indexName":  "embedding_" + strings.ToLower(indexType),
				"indexType":  indexType,
				"metricType": metricType,
				"params":     indexParameters,
			},
			{
				"fieldName": "sparse", "indexName": "sparse_bm25", "indexType": "SPARSE_INVERTED_INDEX",
				"metricType": "BM25", "params": map[string]any{},
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
	// Milvus applies a deliberately low-rate limiter to flush operations. A
	// document lifecycle update flushes before its read-after-write verify, so
	// back off here instead of turning a harmless burst of imports into a
	// failed ingestion job.
	delays := []time.Duration{0, 2 * time.Second, 4 * time.Second, 8 * time.Second, 12 * time.Second}
	var lastErr error
	for index, delay := range delays {
		if index > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		lastErr = c.post(ctx, "/v2/vectordb/collections/flush", map[string]any{"collectionName": collection}, nil)
		if lastErr == nil {
			return nil
		}
		if !strings.Contains(strings.ToLower(lastErr.Error()), "rate limit") {
			return lastErr
		}
	}
	return lastErr
}

func (c *Client) QueryEntities(ctx context.Context, collection, filter string, limit int) ([]Entity, error) {
	if strings.TrimSpace(filter) == "" {
		return nil, fmt.Errorf("Milvus query filter must not be empty")
	}
	if limit <= 0 || limit > 16_384 {
		limit = 16_384
	}
	var entities []Entity
	err := c.post(ctx, "/v2/vectordb/entities/query", map[string]any{
		"collectionName": collection,
		"filter":         filter,
		"outputFields": []string{
			"chunk_id", "document_id", "dataset_id", "title", "tenant_id", "product", "version", "status", "visibility",
			"manufacturer", "product_family", "model_codes", "region", "language", "authority_level", "document_revision", "source_file",
			"content_hash", "embedding_model", "embedding_version", "document_version", "source_revision", "indexed_at",
		},
		"limit":            limit,
		"consistencyLevel": "Strong",
	}, &entities)
	return entities, err
}

func (c *Client) DeleteByFilter(ctx context.Context, collection, filter string) error {
	if strings.TrimSpace(filter) == "" {
		return fmt.Errorf("Milvus delete filter must not be empty")
	}
	return c.post(ctx, "/v2/vectordb/entities/delete", map[string]any{
		"collectionName": collection,
		"filter":         filter,
	}, nil)
}

func (c *Client) CreateAlias(ctx context.Context, collection, alias string) error {
	return c.post(ctx, "/v2/vectordb/aliases/create", map[string]any{
		"collectionName": collection,
		"aliasName":      alias,
	}, nil)
}

func (c *Client) DescribeAlias(ctx context.Context, alias string) (AliasDescription, error) {
	var description AliasDescription
	err := c.post(ctx, "/v2/vectordb/aliases/describe", map[string]any{"aliasName": alias}, &description)
	return description, err
}

func (c *Client) AlterAlias(ctx context.Context, collection, alias string) error {
	return c.post(ctx, "/v2/vectordb/aliases/alter", map[string]any{
		"collectionName": collection,
		"aliasName":      alias,
	}, nil)
}

func (c *Client) Search(ctx context.Context, input SearchRequest) ([]SearchHit, error) {
	limit := input.Limit
	if limit <= 0 || limit > 50 {
		limit = 5
	}
	ef := input.EF
	if ef <= 0 {
		ef = 64
	}
	searchParameters := map[string]any{}
	if !input.Exact {
		searchParameters["ef"] = ef
	}
	payload := map[string]any{
		"collectionName": input.Collection,
		"data":           [][]float64{input.Vector},
		"annsField":      "embedding",
		"limit":          limit,
		"outputFields":   searchOutputFields(),
		"searchParams": map[string]any{
			"metricType": "COSINE",
			"params":     searchParameters,
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

// HybridSearch lets Milvus fuse two independently ranked candidate lists.
// The REST API returns an RRF relevance score in distance; callers in this
// repository historically treat a smaller Distance as better, so preserve the
// raw score in FusionScore and normalize Distance to the stable result rank.
func (c *Client) HybridSearch(ctx context.Context, input HybridSearchRequest) ([]SearchHit, error) {
	limit := input.Limit
	if limit <= 0 || limit > 50 {
		limit = 5
	}
	candidateK := input.CandidateK
	if candidateK < limit {
		candidateK = limit
	}
	if candidateK <= 0 || candidateK > 100 {
		candidateK = min(limit*4, 100)
	}
	ef := input.EF
	if ef <= 0 {
		ef = 64
	}
	rrfK := input.RRFK
	if rrfK <= 0 {
		rrfK = 60
	}
	filter := strings.TrimSpace(input.Filter)
	dense := map[string]any{
		"data": [][]float64{input.Vector}, "annsField": "embedding", "limit": candidateK,
		"searchParams": map[string]any{"metricType": "COSINE", "params": map[string]any{"ef": ef}},
	}
	sparse := map[string]any{
		"data": []string{input.QueryText}, "annsField": "sparse", "limit": candidateK,
		"searchParams": map[string]any{"metricType": "BM25", "params": map[string]any{}},
	}
	if filter != "" {
		dense["filter"] = filter
		sparse["filter"] = filter
	}
	payload := map[string]any{
		"collectionName": input.Collection,
		"search":         []map[string]any{dense, sparse},
		"rerank":         map[string]any{"strategy": "rrf", "params": map[string]any{"k": rrfK}},
		"limit":          limit,
		"outputFields":   searchOutputFields(),
	}
	var hits []SearchHit
	if err := c.post(ctx, "/v2/vectordb/entities/hybrid_search", payload, &hits); err != nil {
		return nil, err
	}
	for index := range hits {
		hits[index].FusionScore = hits[index].Distance
		hits[index].Distance = float64(index)
		hits[index].RecallSources = []string{"dense", "bm25", "rrf"}
	}
	return hits, nil
}

func searchOutputFields() []string {
	return []string{"chunk_id", "document_id", "dataset_id", "title", "content", "tenant_id", "allowed_tenants", "allowed_roles", "domain", "manufacturer", "product_family", "model_codes", "software_version_from", "software_version_to", "hardware_revision", "region", "language", "effective_from", "effective_to", "authority_level", "document_revision", "supersedes", "source_file", "source_page", "source_sheet", "source_cell_range", "heading_path", "device_identifiers", "affected_lots", "product", "version", "status", "visibility"}
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
