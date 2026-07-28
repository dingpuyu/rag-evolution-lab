// Package indexbuild provides a durable, asynchronous index-build boundary.
// The builder is deliberately independent from Milvus: a build produces a
// signed-by-content manifest, while publication remains an explicit control
// plane operation.
package indexbuild

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dingpuyu/rag-evolution-lab/internal/auth"
)

const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"

	StageValidating = "validating"
	StageManifest   = "manifest"
	StageCompleted  = "completed"
	StageFailed     = "failed"
)

var (
	ErrBuildNotFound = errors.New("index build not found")
	ErrBuildConflict = errors.New("index build idempotency conflict")
)

type Request struct {
	IdempotencyKey string `json:"idempotency_key"`
	ApplicationID  string `json:"app_id"`
	EnvironmentID  string `json:"environment_id"`
	Version        string `json:"version"`
	Collection     string `json:"collection"`
	Alias          string `json:"alias,omitempty"`
	EmbeddingModel string `json:"embedding_model,omitempty"`
	EmbeddingVer   string `json:"embedding_version,omitempty"`
	ChunkerVersion string `json:"chunker_version,omitempty"`
	SourceRevision int64  `json:"source_revision,omitempty"`
}

type Manifest struct {
	BuildID        string    `json:"build_id"`
	ApplicationID  string    `json:"app_id"`
	EnvironmentID  string    `json:"environment_id"`
	Version        string    `json:"version"`
	Collection     string    `json:"collection"`
	Alias          string    `json:"alias,omitempty"`
	EmbeddingModel string    `json:"embedding_model"`
	EmbeddingVer   string    `json:"embedding_version"`
	ChunkerVersion string    `json:"chunker_version,omitempty"`
	SourceRevision int64     `json:"source_revision,omitempty"`
	RowCount       int64     `json:"row_count"`
	Dimensions     int       `json:"dimensions"`
	SchemaHash     string    `json:"schema_hash"`
	ManifestHash   string    `json:"manifest_hash"`
	CreatedAt      time.Time `json:"created_at"`
	ValidatedAt    time.Time `json:"validated_at"`
}

type Build struct {
	BuildID        string     `json:"build_id"`
	IdempotencyKey string     `json:"idempotency_key"`
	ApplicationID  string     `json:"app_id"`
	EnvironmentID  string     `json:"environment_id"`
	Version        string     `json:"version"`
	Collection     string     `json:"collection"`
	Alias          string     `json:"alias,omitempty"`
	EmbeddingModel string     `json:"embedding_model,omitempty"`
	EmbeddingVer   string     `json:"embedding_version,omitempty"`
	ChunkerVersion string     `json:"chunker_version,omitempty"`
	SourceRevision int64      `json:"source_revision,omitempty"`
	Status         string     `json:"status"`
	Stage          string     `json:"stage"`
	Attempts       int        `json:"attempts"`
	LastError      string     `json:"last_error,omitempty"`
	Manifest       *Manifest  `json:"manifest,omitempty"`
	CreatedBy      string     `json:"created_by"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
}

type Builder interface {
	BuildManifest(context.Context, Build) (Manifest, error)
}

type Store interface {
	CreateIndexBuild(context.Context, auth.Identity, Request) (Build, bool, error)
	GetIndexBuild(context.Context, auth.Identity, string, string) (Build, error)
	ListIndexBuilds(context.Context, auth.Identity, string, string) ([]Build, error)
	PendingIndexBuilds(context.Context) ([]Build, error)
	UpdateIndexBuild(context.Context, string, string, string, int, string, *Manifest) error
}

// Claimer is an optional stronger store contract. PostgreSQL implementations
// atomically claim a job so two workers cannot build the same collection at
// once; lightweight test stores can use the baseline pending query.
type Claimer interface {
	ClaimIndexBuild(context.Context, string, int) (Build, bool, error)
}

type Config struct {
	Workers       int
	QueueCapacity int
	MaxAttempts   int
	Now           func() time.Time
}

type Service struct {
	store   Store
	builder Builder
	config  Config
	queue   chan string
	mu      sync.Mutex
	started bool
	closed  bool
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

func New(store Store, builder Builder, config Config) (*Service, error) {
	if store == nil || builder == nil {
		return nil, fmt.Errorf("index build service requires store and builder")
	}
	if config.Workers <= 0 {
		config.Workers = 1
	}
	if config.QueueCapacity <= 0 {
		config.QueueCapacity = 128
	}
	if config.MaxAttempts <= 0 {
		config.MaxAttempts = 3
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Service{store: store, builder: builder, config: config, queue: make(chan string, config.QueueCapacity)}, nil
}

func (service *Service) Start(parent context.Context) error {
	service.mu.Lock()
	if service.closed {
		service.mu.Unlock()
		return fmt.Errorf("index build service is closed")
	}
	if service.started {
		service.mu.Unlock()
		return nil
	}
	service.ctx, service.cancel = context.WithCancel(parent)
	service.started = true
	for i := 0; i < service.config.Workers; i++ {
		service.wg.Add(1)
		go service.worker()
	}
	service.mu.Unlock()
	pending, err := service.store.PendingIndexBuilds(parent)
	if err != nil {
		return fmt.Errorf("recover index builds: %w", err)
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i].CreatedAt.Before(pending[j].CreatedAt) })
	for _, build := range pending {
		select {
		case service.queue <- build.BuildID:
		case <-service.ctx.Done():
			return service.ctx.Err()
		}
	}
	return nil
}

func (service *Service) Close() {
	service.mu.Lock()
	if service.closed {
		service.mu.Unlock()
		return
	}
	service.closed = true
	if service.cancel != nil {
		service.cancel()
	}
	service.mu.Unlock()
	service.wg.Wait()
}

func (service *Service) Submit(ctx context.Context, identity auth.Identity, request Request) (Build, bool, error) {
	if strings.TrimSpace(request.IdempotencyKey) == "" {
		return Build{}, false, fmt.Errorf("idempotency_key is required")
	}
	if strings.TrimSpace(request.ApplicationID) == "" || strings.TrimSpace(request.EnvironmentID) == "" || strings.TrimSpace(request.Version) == "" || strings.TrimSpace(request.Collection) == "" {
		return Build{}, false, fmt.Errorf("app_id, environment_id, version and collection are required")
	}
	build, existing, err := service.store.CreateIndexBuild(ctx, identity, request)
	if err != nil || existing {
		return build, existing, err
	}
	if err := service.enqueue(build.BuildID); err != nil {
		return Build{}, false, err
	}
	return build, false, nil
}

func (service *Service) Get(ctx context.Context, identity auth.Identity, appID, buildID string) (Build, error) {
	return service.store.GetIndexBuild(ctx, identity, appID, buildID)
}

func (service *Service) List(ctx context.Context, identity auth.Identity, appID, environmentID string) ([]Build, error) {
	return service.store.ListIndexBuilds(ctx, identity, appID, environmentID)
}

func (service *Service) enqueue(buildID string) error {
	service.mu.Lock()
	ctx, started, closed := service.ctx, service.started, service.closed
	service.mu.Unlock()
	if closed {
		return fmt.Errorf("index build service is closed")
	}
	if !started || ctx == nil {
		return fmt.Errorf("index build service has not started")
	}
	select {
	case service.queue <- buildID:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (service *Service) worker() {
	defer service.wg.Done()
	for {
		select {
		case <-service.ctx.Done():
			return
		case buildID := <-service.queue:
			service.process(buildID)
		}
	}
}

func (service *Service) process(buildID string) {
	attempt := 1
	if claimer, ok := service.store.(Claimer); ok {
		build, claimed, claimErr := claimer.ClaimIndexBuild(context.Background(), buildID, attempt)
		if claimErr != nil || !claimed {
			return
		}
		attempt = build.Attempts
		manifest, buildErr := service.builder.BuildManifest(context.Background(), build)
		if buildErr != nil {
			if attempt < service.config.MaxAttempts {
				_ = service.store.UpdateIndexBuild(context.Background(), buildID, StatusQueued, StageFailed, attempt, buildErr.Error(), nil)
				_ = service.enqueue(buildID)
			} else {
				_ = service.store.UpdateIndexBuild(context.Background(), buildID, StatusFailed, StageFailed, attempt, buildErr.Error(), nil)
			}
			return
		}
		_ = service.store.UpdateIndexBuild(context.Background(), buildID, StatusCompleted, StageCompleted, attempt, "", &manifest)
		return
	}
	builds, err := service.store.PendingIndexBuilds(context.Background())
	var build Build
	if err == nil {
		for _, candidate := range builds {
			if candidate.BuildID == buildID {
				build = candidate
				break
			}
		}
	}
	if build.BuildID == "" {
		// A queued job can be transiently absent from the pending query after a
		// retry. The store update remains idempotent and the worker can continue.
		return
	}
	attempt = build.Attempts + 1
	if attempt > service.config.MaxAttempts {
		attempt = service.config.MaxAttempts
	}
	_ = service.store.UpdateIndexBuild(context.Background(), buildID, StatusRunning, StageValidating, attempt, "", nil)
	manifest, buildErr := service.builder.BuildManifest(context.Background(), build)
	if buildErr != nil {
		if attempt < service.config.MaxAttempts {
			// Keep the job recoverable. A transient Milvus readiness failure is
			// common while an index is still flushing/building.
			_ = service.store.UpdateIndexBuild(context.Background(), buildID, StatusQueued, StageFailed, attempt, buildErr.Error(), nil)
			_ = service.enqueue(buildID)
		} else {
			_ = service.store.UpdateIndexBuild(context.Background(), buildID, StatusFailed, StageFailed, attempt, buildErr.Error(), nil)
		}
		return
	}
	_ = service.store.UpdateIndexBuild(context.Background(), buildID, StatusCompleted, StageCompleted, attempt, "", &manifest)
}

func ManifestHash(manifest Manifest) (string, error) {
	manifest.ManifestHash = ""
	// Timestamps describe the build event, not its content. Excluding them
	// keeps the hash stable when a worker retries the same validation.
	manifest.CreatedAt = time.Time{}
	manifest.ValidatedAt = time.Time{}
	data, err := json.Marshal(manifest)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}
