package milvus

import (
	"context"
	"fmt"
	"strings"

	"github.com/dingpuyu/rag-evolution-lab/internal/domain"
	"github.com/dingpuyu/rag-evolution-lab/internal/retrieval"
)

// Retriever swaps the in-process brute-force vector index for Milvus without
// changing routing, reranking, context packing, or evaluation code.
type Retriever struct {
	client      *Client
	embedder    retrieval.Embedder
	collection  string
	useMetadata bool
	searchEF    int
}

type RetrieverOptions struct {
	UseMetadata bool
	SearchEF    int
}

func NewRetriever(client *Client, embedder retrieval.Embedder, collection string, options RetrieverOptions) (*Retriever, error) {
	if client == nil {
		return nil, fmt.Errorf("Milvus retriever requires a client")
	}
	if embedder == nil {
		return nil, fmt.Errorf("Milvus retriever requires an embedder")
	}
	if strings.TrimSpace(collection) == "" {
		collection = DefaultCollection
	}
	searchEF := options.SearchEF
	if searchEF <= 0 {
		searchEF = 64
	}
	return &Retriever{client: client, embedder: embedder, collection: collection, useMetadata: options.UseMetadata, searchEF: searchEF}, nil
}

func (r *Retriever) Name() string {
	if r.useMetadata {
		return "milvus-hnsw-metadata"
	}
	return "milvus-hnsw"
}

func (r *Retriever) Search(ctx context.Context, request domain.QueryRequest) ([]domain.RetrievedChunk, error) {
	if strings.TrimSpace(request.Query) == "" {
		return nil, fmt.Errorf("query must not be empty")
	}
	vector, err := r.embedder.EmbedQuery(ctx, request.Query)
	if err != nil {
		return nil, fmt.Errorf("embed Milvus query with %s: %w", r.embedder.Name(), err)
	}
	hits, err := r.client.Search(ctx, SearchRequest{
		Collection: r.collection,
		Vector:     vector,
		Filter:     buildRetrieverFilter(request, r.useMetadata),
		Limit:      request.TopK,
		EF:         r.searchEF,
	})
	if err != nil {
		return nil, fmt.Errorf("search Milvus collection %s: %w", r.collection, err)
	}
	results := make([]domain.RetrievedChunk, 0, len(hits))
	for index, hit := range hits {
		results = append(results, domain.RetrievedChunk{
			Chunk: domain.Chunk{
				ID: hit.ChunkID, DocumentID: hit.DocumentID, DocumentTitle: hit.Title, Content: hit.Content,
				Product: hit.Product, Version: hit.Version, Status: hit.Status, Visibility: hit.Visibility,
				AllowedTenants: append([]string(nil), hit.AllowedTenants...),
				AllowedRoles:   append([]string(nil), hit.AllowedRoles...),
			},
			Score: hit.Distance, Rank: index + 1, Stage: r.Name(),
		})
	}
	return results, nil
}

func (r *Retriever) TraceAttributes(request domain.QueryRequest) map[string]any {
	return map[string]any{
		"vector_backend": "milvus",
		"collection":     r.collection,
		"index_type":     "HNSW",
		"metric_type":    "COSINE",
		"search_ef":      r.searchEF,
		"filter_stage":   "pre_ann",
		"scalar_filter":  buildRetrieverFilter(request, r.useMetadata),
		"embedder":       r.embedder.Name(),
	}
}

func buildRetrieverFilter(request domain.QueryRequest, useMetadata bool) string {
	access := `visibility == "public"`
	if tenant, role := strings.TrimSpace(request.TenantID), strings.TrimSpace(request.UserRole); tenant != "" && role != "" {
		access = "(" + access + ` or (array_contains(allowed_tenants, "` + escapeFilter(tenant) + `") and array_contains(allowed_roles, "` + escapeFilter(role) + `")))`
	}
	filters := []string{access}
	if useMetadata {
		if product := strings.TrimSpace(request.Product); product != "" {
			filters = append(filters, `product == "`+escapeFilter(product)+`"`)
		}
		if version := strings.TrimSpace(request.Version); version != "" {
			filters = append(filters, `version == "`+escapeFilter(version)+`"`)
		} else {
			filters = append(filters, `status == "active"`)
		}
	}
	return strings.Join(filters, " and ")
}
