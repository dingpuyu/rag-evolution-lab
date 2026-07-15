package retrieval

import (
	"context"
	"sort"
	"strings"

	"github.com/dingpuyu/rag-evolution-lab/internal/domain"
)

type Retriever interface {
	Name() string
	Search(ctx context.Context, request domain.QueryRequest) ([]domain.RetrievedChunk, error)
}

type Options struct {
	UseMetadata bool
}

func allowed(chunk domain.Chunk, request domain.QueryRequest, options Options) bool {
	if options.UseMetadata {
		if request.Product != "" && chunk.Product != request.Product {
			return false
		}
		if request.Version != "" {
			if chunk.Version != request.Version {
				return false
			}
		} else if chunk.Status != "active" {
			return false
		}
	}
	if chunk.Visibility == "public" {
		return true
	}
	if request.TenantID == "" || request.UserRole == "" {
		return false
	}
	if !contains(chunk.AllowedTenants, request.TenantID) {
		return false
	}
	return contains(chunk.AllowedRoles, request.UserRole)
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(value, target) {
			return true
		}
	}
	return false
}

func rank(results []domain.RetrievedChunk, topK int) []domain.RetrievedChunk {
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			return results[i].Chunk.ID < results[j].Chunk.ID
		}
		return results[i].Score > results[j].Score
	})
	if topK <= 0 {
		topK = 5
	}
	if len(results) > topK {
		results = results[:topK]
	}
	for index := range results {
		results[index].Rank = index + 1
	}
	return results
}
