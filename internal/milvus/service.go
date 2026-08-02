package milvus

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dingpuyu/rag-evolution-lab/internal/domain"
	"github.com/dingpuyu/rag-evolution-lab/internal/retrieval"
)

type Service struct {
	client     *Client
	embedder   retrieval.Embedder
	collection string
}

type Status struct {
	Connected    bool          `json:"connected"`
	Collection   string        `json:"collection"`
	CollectionID string        `json:"collection_id"`
	RowCount     int64         `json:"row_count"`
	Dimensions   int           `json:"dimensions"`
	Metric       string        `json:"metric"`
	IndexType    string        `json:"index_type"`
	IndexName    string        `json:"index_name"`
	LoadState    string        `json:"load_state"`
	Embedder     string        `json:"embedder"`
	Fields       []FieldStatus `json:"fields"`
	CheckedAt    time.Time     `json:"checked_at"`
}

type FieldStatus struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type SeedResult struct {
	Collection string `json:"collection"`
	Documents  int    `json:"documents"`
	Chunks     int    `json:"chunks"`
	Rows       int64  `json:"rows"`
	Dimensions int    `json:"dimensions"`
	Embedder   string `json:"embedder"`
	IndexType  string `json:"index_type"`
	Metric     string `json:"metric"`
}

type Query struct {
	Text    string `json:"query"`
	Tenant  string `json:"tenant_id"`
	Role    string `json:"user_role"`
	Product string `json:"product"`
	Status  string `json:"status"`
	TopK    int    `json:"top_k"`
	// Collection is assigned by the trusted application index resolver. It is
	// excluded from JSON so callers cannot choose an arbitrary physical index.
	Collection string `json:"-"`
	// AccessScope is assigned by trusted server-side resource authorization.
	// It is intentionally excluded from JSON so clients cannot weaken filtering.
	AccessScope string `json:"-"`
}

type SearchResult struct {
	Query              string      `json:"query"`
	Collection         string      `json:"collection"`
	Embedder           string      `json:"embedder"`
	Dimensions         int         `json:"dimensions"`
	Metric             string      `json:"metric"`
	Filter             string      `json:"filter"`
	EmbeddingLatencyMS float64     `json:"embedding_latency_ms"`
	SearchLatencyMS    float64     `json:"search_latency_ms"`
	TotalLatencyMS     float64     `json:"total_latency_ms"`
	Hits               []SearchHit `json:"hits"`
	// RerankApplied is an internal merge hint. It is omitted from the API
	// response so the public contract remains a normal Milvus SearchResult.
	RerankApplied bool `json:"-"`
}

func NewService(client *Client, embedder retrieval.Embedder, collection string) (*Service, error) {
	if client == nil {
		return nil, fmt.Errorf("Milvus client must not be nil")
	}
	if embedder == nil {
		return nil, fmt.Errorf("embedder must not be nil")
	}
	if strings.TrimSpace(collection) == "" {
		collection = DefaultCollection
	}
	return &Service{client: client, embedder: embedder, collection: collection}, nil
}

func (s *Service) Status(ctx context.Context) (Status, error) {
	description, err := s.client.DescribeCollection(ctx, s.collection)
	if err != nil {
		return Status{}, err
	}
	stats, err := s.client.CollectionStats(ctx, s.collection)
	if err != nil {
		return Status{}, err
	}
	status := Status{
		Connected:    true,
		Collection:   s.collection,
		CollectionID: fmt.Sprint(description.CollectionID),
		RowCount:     int64(stats.RowCount),
		LoadState:    description.Load,
		Embedder:     s.embedder.Name(),
		CheckedAt:    time.Now().UTC(),
	}
	for _, field := range description.Fields {
		status.Fields = append(status.Fields, FieldStatus{Name: field.Name, Type: field.Type})
		if field.Name == "embedding" {
			for _, parameter := range field.Params {
				if parameter.Key == "dim" {
					_, _ = fmt.Sscan(parameter.Value, &status.Dimensions)
				}
			}
		}
	}
	for _, index := range description.Indexes {
		if index.FieldName == "embedding" {
			status.IndexName = index.IndexName
			status.Metric = index.MetricType
		}
	}
	if status.IndexName != "" {
		indexes, indexErr := s.client.DescribeIndex(ctx, s.collection, status.IndexName)
		if indexErr == nil && len(indexes) > 0 {
			status.IndexType = indexes[0].IndexType
			status.Metric = indexes[0].MetricType
		}
	}
	if status.Metric == "" {
		status.Metric = "COSINE"
	}
	if status.IndexType == "" {
		status.IndexType = "HNSW"
	}
	return status, nil
}

func (s *Service) Seed(ctx context.Context, chunks []domain.Chunk) (SeedResult, error) {
	if len(chunks) == 0 {
		return SeedResult{}, fmt.Errorf("no chunks to seed")
	}
	collections, err := s.client.ListCollections(ctx)
	if err != nil {
		return SeedResult{}, err
	}
	texts := make([]string, len(chunks))
	for index, chunk := range chunks {
		texts[index] = chunk.DocumentTitle + "\n" + chunk.Content
	}
	vectors, err := s.embedder.EmbedDocuments(ctx, texts)
	if err != nil {
		return SeedResult{}, fmt.Errorf("embed %d chunks with %s: %w", len(chunks), s.embedder.Name(), err)
	}
	if len(vectors) != len(chunks) || len(vectors[0]) == 0 {
		return SeedResult{}, fmt.Errorf("embedder returned invalid batch: vectors=%d chunks=%d", len(vectors), len(chunks))
	}
	dimensions := len(vectors[0])
	for index := range vectors {
		if len(vectors[index]) != dimensions {
			return SeedResult{}, fmt.Errorf("embedding %d dimensions=%d, expected=%d", index, len(vectors[index]), dimensions)
		}
	}
	if contains(collections, s.collection) {
		if err := s.client.DropCollection(ctx, s.collection); err != nil {
			return SeedResult{}, fmt.Errorf("drop existing collection: %w", err)
		}
	}
	if err := s.client.CreateCollection(ctx, s.collection, dimensions); err != nil {
		return SeedResult{}, fmt.Errorf("create collection: %w", err)
	}
	records := make([]Record, len(chunks))
	documents := make(map[string]struct{})
	indexedAt := time.Now().UnixMilli()
	for index, chunk := range chunks {
		documents[chunk.DocumentID] = struct{}{}
		tenant := "public"
		if len(chunk.AllowedTenants) > 0 {
			tenant = chunk.AllowedTenants[0]
		}
		records[index] = Record{
			ChunkID: chunk.ID, DocumentID: chunk.DocumentID, Title: chunk.DocumentTitle,
			Content: chunk.Content, TenantID: tenant, Product: chunk.Product, Version: chunk.Version,
			AllowedTenants: append([]string(nil), chunk.AllowedTenants...),
			AllowedRoles:   append([]string(nil), chunk.AllowedRoles...),
			Status:         chunk.Status, Visibility: chunk.Visibility, SourceRevision: 1,
			IndexedAt: indexedAt, Embedding: vectors[index],
		}
	}
	rows, err := s.client.Upsert(ctx, s.collection, records)
	if err != nil {
		return SeedResult{}, fmt.Errorf("upsert test corpus: %w", err)
	}
	if err := s.client.FlushCollection(ctx, s.collection); err != nil {
		return SeedResult{}, fmt.Errorf("flush test corpus: %w", err)
	}
	return SeedResult{
		Collection: s.collection, Documents: len(documents), Chunks: len(chunks), Rows: rows,
		Dimensions: dimensions, Embedder: s.embedder.Name(), IndexType: "HNSW", Metric: "COSINE",
	}, nil
}

func (s *Service) Search(ctx context.Context, query Query) (SearchResult, error) {
	query.Text = strings.TrimSpace(query.Text)
	if query.Text == "" {
		return SearchResult{}, fmt.Errorf("query must not be empty")
	}
	if query.TopK <= 0 || query.TopK > 20 {
		query.TopK = 5
	}
	totalStarted := time.Now()
	embedStarted := time.Now()
	vector, err := s.embedder.EmbedQuery(ctx, query.Text)
	if err != nil {
		return SearchResult{}, fmt.Errorf("embed query: %w", err)
	}
	embedLatency := time.Since(embedStarted)
	filter := buildFilter(query)
	searchStarted := time.Now()
	hits, err := s.client.Search(ctx, SearchRequest{Collection: s.collection, Vector: vector, Filter: filter, Limit: query.TopK})
	if err != nil {
		return SearchResult{}, err
	}
	return SearchResult{
		Query: query.Text, Collection: s.collection, Embedder: s.embedder.Name(), Dimensions: len(vector),
		Metric: "COSINE", Filter: filter, EmbeddingLatencyMS: milliseconds(embedLatency),
		SearchLatencyMS: milliseconds(time.Since(searchStarted)), TotalLatencyMS: milliseconds(time.Since(totalStarted)), Hits: hits,
	}, nil
}

func buildFilter(query Query) string {
	tenant, role := strings.TrimSpace(query.Tenant), strings.TrimSpace(query.Role)
	tenantAccess := `(array_contains(allowed_tenants, "` + escapeFilter(tenant) + `") and array_contains(allowed_roles, "` + escapeFilter(role) + `"))`
	access := `visibility == "public"`
	switch query.AccessScope {
	case "public_only":
		// Public datasets must not accidentally surface tenant rows that happen
		// to share the same product metadata.
	case "tenant_only":
		if tenant == "" || tenant == "public" || role == "" {
			access = "false"
		} else {
			access = tenantAccess
		}
	default:
		if tenant != "" && tenant != "public" && role != "" {
			access = "(" + access + " or " + tenantAccess + ")"
		}
	}
	filters := []string{access}
	if product := strings.TrimSpace(query.Product); product != "" {
		filters = append(filters, "product == \""+escapeFilter(product)+"\"")
	}
	status := strings.TrimSpace(query.Status)
	if status == "" {
		status = "active"
	}
	if status != "all" {
		filters = append(filters, "status == \""+escapeFilter(status)+"\"")
	}
	return strings.Join(filters, " and ")
}

func escapeFilter(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "\\", "\\\\"), "\"", "\\\"")
}

func contains(values []string, target string) bool {
	sort.Strings(values)
	index := sort.SearchStrings(values, target)
	return index < len(values) && values[index] == target
}

func milliseconds(duration time.Duration) float64 {
	return float64(duration.Microseconds()) / 1000
}
