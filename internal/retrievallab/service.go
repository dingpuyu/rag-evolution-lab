package retrievallab

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dingpuyu/rag-evolution-lab/internal/knowledgegateway"
	"github.com/dingpuyu/rag-evolution-lab/internal/milvus"
	"github.com/dingpuyu/rag-evolution-lab/internal/retrieval"
)

const (
	maxChunks  = 80
	maxQueries = 20
)

type milvusClient interface {
	CreateCollectionWithOptions(context.Context, string, milvus.CollectionOptions) error
	DropCollection(context.Context, string) error
	Upsert(context.Context, string, []milvus.Record) (int64, error)
	FlushCollection(context.Context, string) error
	HybridSearch(context.Context, milvus.HybridSearchRequest) ([]milvus.SearchHit, error)
}

// Service runs retrieval experiments in short-lived Milvus collections. The
// caller never supplies a collection name, so an experiment cannot overwrite
// or query a production index.
type Service struct {
	client   milvusClient
	embedder retrieval.Embedder
	reranker knowledgegateway.HitReranker
	now      func() time.Time
	slots    chan struct{}
}

type Chunk struct {
	ChunkID         string   `json:"chunk_id"`
	DocumentID      string   `json:"document_id"`
	DatasetID       string   `json:"dataset_id,omitempty"`
	Title           string   `json:"title,omitempty"`
	Content         string   `json:"content"`
	SourceFile      string   `json:"source_file,omitempty"`
	SourcePage      int64    `json:"source_page,omitempty"`
	SourceSheet     string   `json:"source_sheet,omitempty"`
	SourceCellRange string   `json:"source_cell_range,omitempty"`
	HeadingPath     []string `json:"heading_path,omitempty"`
}

type Query struct {
	QueryID    string `json:"query_id"`
	Text       string `json:"query"`
	TopK       int    `json:"top_k,omitempty"`
	CandidateK int    `json:"candidate_k,omitempty"`
}

type RunInput struct {
	RunID   string  `json:"run_id"`
	Variant string  `json:"variant"`
	Chunks  []Chunk `json:"chunks"`
	Queries []Query `json:"queries"`
}

type Identity struct {
	TenantID string
	Role     string
}

type Hit struct {
	ChunkID         string   `json:"chunk_id"`
	DocumentID      string   `json:"document_id"`
	Title           string   `json:"title,omitempty"`
	Content         string   `json:"content"`
	SourceFile      string   `json:"source_file,omitempty"`
	SourcePage      int64    `json:"source_page,omitempty"`
	SourceSheet     string   `json:"source_sheet,omitempty"`
	SourceCellRange string   `json:"source_cell_range,omitempty"`
	HeadingPath     []string `json:"heading_path,omitempty"`
	PreRerankRank   int      `json:"pre_rerank_rank"`
	PostRerankRank  int      `json:"post_rerank_rank"`
	FusionScore     float64  `json:"fusion_score,omitempty"`
	RerankScore     *float64 `json:"rerank_score,omitempty"`
	RecallSources   []string `json:"recall_sources,omitempty"`
	ExactMatches    []string `json:"exact_matches,omitempty"`
}

type QueryResult struct {
	QueryID            string  `json:"query_id"`
	Query              string  `json:"query"`
	EmbeddingLatencyMS float64 `json:"embedding_latency_ms"`
	SearchLatencyMS    float64 `json:"search_latency_ms"`
	RerankLatencyMS    float64 `json:"rerank_latency_ms"`
	Hits               []Hit   `json:"hits"`
}

type RunResult struct {
	Schema              string        `json:"schema"`
	RunID               string        `json:"run_id"`
	Variant             string        `json:"variant"`
	CollectionScope     string        `json:"collection_scope"`
	Embedder            string        `json:"embedder"`
	Dimensions          int           `json:"dimensions"`
	Reranker            string        `json:"reranker"`
	Retrieval           string        `json:"retrieval"`
	Index               string        `json:"index"`
	ChunksIndexed       int64         `json:"chunks_indexed"`
	Queries             []QueryResult `json:"queries"`
	IndexBuildLatencyMS float64       `json:"index_build_latency_ms"`
	TotalLatencyMS      float64       `json:"total_latency_ms"`
	CleanupCompleted    bool          `json:"cleanup_completed"`
	ProductionMutation  bool          `json:"production_mutation"`
}

func New(client milvusClient, embedder retrieval.Embedder, reranker knowledgegateway.HitReranker) (*Service, error) {
	if client == nil || embedder == nil || reranker == nil {
		return nil, fmt.Errorf("retrieval sandbox requires Milvus, embedder and reranker")
	}
	return &Service{client: client, embedder: embedder, reranker: reranker, now: time.Now, slots: make(chan struct{}, 2)}, nil
}

func (service *Service) Run(ctx context.Context, identity Identity, input RunInput) (result RunResult, err error) {
	if err := validate(identity, &input); err != nil {
		return RunResult{}, err
	}
	select {
	case service.slots <- struct{}{}:
		defer func() { <-service.slots }()
	case <-ctx.Done():
		return RunResult{}, ctx.Err()
	}
	totalStarted := service.now()
	collection, err := temporaryCollectionName()
	if err != nil {
		return RunResult{}, err
	}
	texts := make([]string, len(input.Chunks))
	for index, chunk := range input.Chunks {
		texts[index] = chunk.Content
	}
	vectors, err := service.embedder.EmbedDocuments(ctx, texts)
	if err != nil {
		return RunResult{}, fmt.Errorf("embed sandbox chunks: %w", err)
	}
	if len(vectors) != len(input.Chunks) || len(vectors) == 0 || len(vectors[0]) == 0 {
		return RunResult{}, fmt.Errorf("embedder returned an invalid chunk vector batch")
	}
	dimensions := len(vectors[0])
	for index := range vectors {
		if len(vectors[index]) != dimensions {
			return RunResult{}, fmt.Errorf("embedding %d has inconsistent dimensions", index)
		}
	}
	indexStarted := service.now()
	if err = service.client.CreateCollectionWithOptions(ctx, collection, milvus.CollectionOptions{
		Dimensions: dimensions, IndexType: "HNSW", MetricType: "COSINE", M: 16, EFConstruction: 200,
	}); err != nil {
		return RunResult{}, fmt.Errorf("create retrieval sandbox: %w", err)
	}
	created := true
	defer func() {
		if !created {
			return
		}
		cleanupContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = service.client.DropCollection(cleanupContext, collection)
	}()
	records := make([]milvus.Record, len(input.Chunks))
	for index, chunk := range input.Chunks {
		datasetID := strings.TrimSpace(chunk.DatasetID)
		if datasetID == "" {
			datasetID = "document-quality-development"
		}
		records[index] = milvus.Record{
			ChunkID: chunk.ChunkID, DocumentID: chunk.DocumentID, DatasetID: datasetID,
			Title: chunk.Title, Content: chunk.Content, TenantID: identity.TenantID,
			AllowedTenants: []string{identity.TenantID}, AllowedRoles: []string{identity.Role},
			Domain: "document-quality-evaluation", SourceFile: chunk.SourceFile, SourcePage: chunk.SourcePage,
			SourceSheet: chunk.SourceSheet, SourceCellRange: chunk.SourceCellRange, HeadingPath: chunk.HeadingPath,
			Status: "active", Visibility: "tenant", EmbeddingModel: service.embedder.Name(),
			SourceRevision: 1, IndexedAt: service.now().UTC().Unix(), Embedding: vectors[index],
		}
	}
	upserted, err := service.client.Upsert(ctx, collection, records)
	if err != nil {
		return RunResult{}, fmt.Errorf("write retrieval sandbox: %w", err)
	}
	if err := service.client.FlushCollection(ctx, collection); err != nil {
		return RunResult{}, fmt.Errorf("flush retrieval sandbox: %w", err)
	}
	result = RunResult{
		Schema: "raglab.retrieval-sandbox.run.v1", RunID: input.RunID, Variant: input.Variant,
		CollectionScope: "temporary-isolated", Embedder: service.embedder.Name(), Dimensions: dimensions,
		Reranker: service.reranker.Name(), Retrieval: "exact+bm25+dense+rrf+rerank", Index: "HNSW/COSINE+SPARSE_INVERTED_INDEX/BM25",
		ChunksIndexed: upserted, IndexBuildLatencyMS: milliseconds(service.now().Sub(indexStarted)),
		ProductionMutation: false, Queries: make([]QueryResult, 0, len(input.Queries)),
	}
	filter := fmt.Sprintf(`tenant_id == "%s" and visibility == "tenant" and array_contains(allowed_tenants, "%s") and array_contains(allowed_roles, "%s")`, escape(identity.TenantID), escape(identity.TenantID), escape(identity.Role))
	for _, query := range input.Queries {
		embedStarted := service.now()
		vector, embedErr := service.embedder.EmbedQuery(ctx, query.Text)
		if embedErr != nil {
			return RunResult{}, fmt.Errorf("embed query %s: %w", query.QueryID, embedErr)
		}
		embedLatency := service.now().Sub(embedStarted)
		searchStarted := service.now()
		hits, searchErr := service.client.HybridSearch(ctx, milvus.HybridSearchRequest{
			Collection: collection, Vector: vector, QueryText: query.Text, Filter: filter,
			Limit: query.CandidateK, CandidateK: query.CandidateK, EF: 64, RRFK: 60,
		})
		if searchErr != nil {
			return RunResult{}, fmt.Errorf("hybrid search %s: %w", query.QueryID, searchErr)
		}
		milvus.ApplyExactIdentifierBoost(query.Text, hits)
		preRanks := make(map[string]int, len(hits))
		for index, hit := range hits {
			preRanks[hit.ChunkID] = index + 1
		}
		searchLatency := service.now().Sub(searchStarted)
		rerankStarted := service.now()
		ranked, rerankErr := service.reranker.Rerank(ctx, query.Text, hits)
		if rerankErr != nil {
			return RunResult{}, fmt.Errorf("rerank query %s with %s: %w", query.QueryID, service.reranker.Name(), rerankErr)
		}
		rerankLatency := service.now().Sub(rerankStarted)
		if len(ranked) > query.TopK {
			ranked = ranked[:query.TopK]
		}
		queryResult := QueryResult{QueryID: query.QueryID, Query: query.Text, EmbeddingLatencyMS: milliseconds(embedLatency), SearchLatencyMS: milliseconds(searchLatency), RerankLatencyMS: milliseconds(rerankLatency)}
		for index, hit := range ranked {
			var score *float64
			if hit.RerankScoreSet {
				value := hit.RerankScore
				score = &value
			}
			queryResult.Hits = append(queryResult.Hits, Hit{
				ChunkID: hit.ChunkID, DocumentID: hit.DocumentID, Title: hit.Title, Content: hit.Content,
				SourceFile: hit.SourceFile, SourcePage: hit.SourcePage, SourceSheet: hit.SourceSheet,
				SourceCellRange: hit.SourceCellRange, HeadingPath: append([]string(nil), hit.HeadingPath...),
				PreRerankRank: preRanks[hit.ChunkID], PostRerankRank: index + 1, FusionScore: hit.FusionScore,
				RerankScore: score, RecallSources: append([]string(nil), hit.RecallSources...), ExactMatches: append([]string(nil), hit.ExactMatches...),
			})
		}
		result.Queries = append(result.Queries, queryResult)
	}
	cleanupContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	cleanupErr := service.client.DropCollection(cleanupContext, collection)
	cancel()
	if cleanupErr != nil {
		return RunResult{}, fmt.Errorf("cleanup retrieval sandbox: %w", cleanupErr)
	}
	created = false
	result.CleanupCompleted = true
	result.TotalLatencyMS = milliseconds(service.now().Sub(totalStarted))
	return result, nil
}

func validate(identity Identity, input *RunInput) error {
	if strings.TrimSpace(identity.TenantID) == "" || strings.TrimSpace(identity.Role) == "" {
		return fmt.Errorf("trusted tenant and role are required")
	}
	if strings.TrimSpace(input.RunID) == "" || utf8.RuneCountInString(input.RunID) > 128 {
		return fmt.Errorf("run_id must contain 1-128 characters")
	}
	if strings.TrimSpace(input.Variant) == "" || utf8.RuneCountInString(input.Variant) > 64 {
		return fmt.Errorf("variant must contain 1-64 characters")
	}
	if len(input.Chunks) == 0 || len(input.Chunks) > maxChunks {
		return fmt.Errorf("chunks must contain 1-%d items", maxChunks)
	}
	if len(input.Queries) == 0 || len(input.Queries) > maxQueries {
		return fmt.Errorf("queries must contain 1-%d items", maxQueries)
	}
	seenChunks := make(map[string]struct{}, len(input.Chunks))
	for _, chunk := range input.Chunks {
		if strings.TrimSpace(chunk.ChunkID) == "" || strings.TrimSpace(chunk.DocumentID) == "" || strings.TrimSpace(chunk.Content) == "" {
			return fmt.Errorf("every chunk requires chunk_id, document_id and content")
		}
		if utf8.RuneCountInString(chunk.Content) > 7000 {
			return fmt.Errorf("chunk %s exceeds 7000 characters", chunk.ChunkID)
		}
		if _, duplicate := seenChunks[chunk.ChunkID]; duplicate {
			return fmt.Errorf("duplicate chunk_id %s", chunk.ChunkID)
		}
		seenChunks[chunk.ChunkID] = struct{}{}
	}
	seenQueries := make(map[string]struct{}, len(input.Queries))
	for index := range input.Queries {
		query := &input.Queries[index]
		if strings.TrimSpace(query.QueryID) == "" || strings.TrimSpace(query.Text) == "" || utf8.RuneCountInString(query.Text) > 1000 {
			return fmt.Errorf("every query requires query_id and 1-1000 characters")
		}
		if _, duplicate := seenQueries[query.QueryID]; duplicate {
			return fmt.Errorf("duplicate query_id %s", query.QueryID)
		}
		seenQueries[query.QueryID] = struct{}{}
		if query.TopK <= 0 {
			query.TopK = 5
		}
		if query.TopK > 10 {
			return fmt.Errorf("top_k must not exceed 10")
		}
		if query.CandidateK <= 0 {
			query.CandidateK = max(query.TopK*3, 10)
		}
		if query.CandidateK < query.TopK || query.CandidateK > 50 {
			return fmt.Errorf("candidate_k must be between top_k and 50")
		}
	}
	return nil
}

func temporaryCollectionName() (string, error) {
	buffer := make([]byte, 10)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("create sandbox identifier: %w", err)
	}
	return "raglab_eval_" + hex.EncodeToString(buffer), nil
}

func escape(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "\\", "\\\\"), "\"", "\\\"")
}

func milliseconds(duration time.Duration) float64 {
	return float64(duration.Microseconds()) / 1000
}
