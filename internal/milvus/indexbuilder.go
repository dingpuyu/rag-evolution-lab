package milvus

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dingpuyu/rag-evolution-lab/internal/indexbuild"
)

// CollectionManifest is the immutable readiness evidence attached to an
// asynchronous index build. It makes a build explainable and reproducible
// without coupling the control plane to Milvus internals.
type CollectionManifest struct {
	Collection     string
	RowCount       int64
	Dimensions     int
	SchemaHash     string
	EmbeddingModel string
	EmbeddingVer   string
}

type IndexBuilder struct {
	service *LifecycleService
}

func NewIndexBuilder(service *LifecycleService) *IndexBuilder { return &IndexBuilder{service: service} }

func (builder *IndexBuilder) BuildManifest(ctx context.Context, build indexbuild.Build) (indexbuild.Manifest, error) {
	if builder == nil || builder.service == nil {
		return indexbuild.Manifest{}, fmt.Errorf("index builder is not configured")
	}
	collection := strings.TrimSpace(build.Collection)
	if err := builder.service.ValidateCollection(ctx, collection); err != nil {
		return indexbuild.Manifest{}, err
	}
	manifest, err := builder.service.CollectionManifest(ctx, collection)
	if err != nil {
		return indexbuild.Manifest{}, err
	}
	if build.EmbeddingModel != "" && build.EmbeddingModel != manifest.EmbeddingModel {
		return indexbuild.Manifest{}, fmt.Errorf("embedding model mismatch: requested=%s current=%s", build.EmbeddingModel, manifest.EmbeddingModel)
	}
	if build.EmbeddingVer != "" && build.EmbeddingVer != manifest.EmbeddingVer {
		return indexbuild.Manifest{}, fmt.Errorf("embedding version mismatch: requested=%s current=%s", build.EmbeddingVer, manifest.EmbeddingVer)
	}
	if build.Alias != "" && build.Alias != builder.service.ConfiguredAlias() {
		return indexbuild.Manifest{}, fmt.Errorf("build alias must match configured alias")
	}
	if build.Manifest != nil {
		return indexbuild.Manifest{}, fmt.Errorf("build already has a manifest")
	}
	now := time.Now().UTC()
	result := indexbuild.Manifest{
		BuildID: build.BuildID, ApplicationID: build.ApplicationID, EnvironmentID: build.EnvironmentID,
		Version: build.Version, Collection: collection, Alias: build.Alias,
		SourceRevision: build.SourceRevision,
		EmbeddingModel: manifest.EmbeddingModel, EmbeddingVer: manifest.EmbeddingVer,
		ChunkerVersion: build.ChunkerVersion, RowCount: manifest.RowCount, Dimensions: manifest.Dimensions,
		SchemaHash: manifest.SchemaHash, CreatedAt: build.CreatedAt, ValidatedAt: now,
	}
	hash, err := indexbuild.ManifestHash(result)
	if err != nil {
		return indexbuild.Manifest{}, err
	}
	result.ManifestHash = hash
	return result, nil
}

func (service *LifecycleService) CollectionManifest(ctx context.Context, collection string) (CollectionManifest, error) {
	collection = strings.TrimSpace(collection)
	description, err := service.client.DescribeCollection(ctx, collection)
	if err != nil {
		return CollectionManifest{}, err
	}
	stats, err := service.client.CollectionStats(ctx, collection)
	if err != nil {
		return CollectionManifest{}, err
	}
	fields := append([]Field(nil), description.Fields...)
	indexes := append([]IndexDetail(nil), description.Indexes...)
	sort.Slice(fields, func(i, j int) bool { return fields[i].Name < fields[j].Name })
	sort.Slice(indexes, func(i, j int) bool { return indexes[i].IndexName < indexes[j].IndexName })
	data, err := json.Marshal(struct {
		Fields  []Field
		Indexes []IndexDetail
	}{fields, indexes})
	if err != nil {
		return CollectionManifest{}, err
	}
	hash := sha256.Sum256(data)
	dimensions := 0
	for _, field := range fields {
		if field.Name != "embedding" {
			continue
		}
		for _, parameter := range field.Params {
			if parameter.Key == "dim" {
				_, _ = fmt.Sscan(parameter.Value, &dimensions)
			}
		}
	}
	return CollectionManifest{Collection: collection, RowCount: int64(stats.RowCount), Dimensions: dimensions,
		SchemaHash: hex.EncodeToString(hash[:]), EmbeddingModel: service.embedder.Name(), EmbeddingVer: service.config.EmbeddingVersion}, nil
}
