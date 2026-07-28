package indexbuild

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/dingpuyu/rag-evolution-lab/internal/auth"
)

type testStore struct {
	mu       sync.Mutex
	builds   map[string]Build
	attempts int
}

func newTestStore() *testStore { return &testStore{builds: map[string]Build{}} }
func (store *testStore) CreateIndexBuild(_ context.Context, identity auth.Identity, input Request) (Build, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, b := range store.builds {
		if b.IdempotencyKey == input.IdempotencyKey {
			return b, true, nil
		}
	}
	b := Build{BuildID: "b1", IdempotencyKey: input.IdempotencyKey, ApplicationID: input.ApplicationID, EnvironmentID: input.EnvironmentID, Version: input.Version, Collection: input.Collection, CreatedBy: identity.Subject, Status: StatusQueued, Stage: StageValidating, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	store.builds[b.BuildID] = b
	return b, false, nil
}
func (store *testStore) GetIndexBuild(_ context.Context, _ auth.Identity, _, _ string) (Build, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.builds["b1"], nil
}
func (store *testStore) ListIndexBuilds(context.Context, auth.Identity, string, string) ([]Build, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	out := make([]Build, 0, len(store.builds))
	for _, b := range store.builds {
		out = append(out, b)
	}
	return out, nil
}
func (store *testStore) PendingIndexBuilds(context.Context) ([]Build, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	out := []Build{}
	for _, b := range store.builds {
		if b.Status == StatusQueued || b.Status == StatusRunning {
			out = append(out, b)
		}
	}
	return out, nil
}
func (store *testStore) UpdateIndexBuild(_ context.Context, id, status, stage string, attempt int, lastError string, manifest *Manifest) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	b := store.builds[id]
	b.Status = status
	b.Stage = stage
	b.Attempts = attempt
	b.LastError = lastError
	b.Manifest = manifest
	b.UpdatedAt = time.Now()
	store.builds[id] = b
	store.attempts = attempt
	return nil
}

type flakyBuilder struct {
	calls int
	mu    sync.Mutex
}

func (builder *flakyBuilder) BuildManifest(context.Context, Build) (Manifest, error) {
	builder.mu.Lock()
	defer builder.mu.Unlock()
	builder.calls++
	if builder.calls < 2 {
		return Manifest{}, context.DeadlineExceeded
	}
	return Manifest{RowCount: 1, Dimensions: 3}, nil
}

func TestServiceRetriesTransientBuildAndIsIdempotent(t *testing.T) {
	store := newTestStore()
	builder := &flakyBuilder{}
	service, err := New(store, builder, Config{Workers: 1, QueueCapacity: 4, MaxAttempts: 3})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	identity := auth.Identity{Subject: "admin", TenantID: "tenant_a", Roles: []string{"admin"}}
	first, existing, err := service.Submit(context.Background(), identity, Request{IdempotencyKey: "k", ApplicationID: "app", EnvironmentID: "env", Version: "v1", Collection: "c"})
	if err != nil || existing || first.BuildID == "" {
		t.Fatalf("submit=%+v existing=%v err=%v", first, existing, err)
	}
	second, existing, err := service.Submit(context.Background(), identity, Request{IdempotencyKey: "k", ApplicationID: "app", EnvironmentID: "env", Version: "v1", Collection: "c"})
	if err != nil || !existing || second.BuildID != first.BuildID {
		t.Fatalf("idempotency=%+v existing=%v err=%v", second, existing, err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		store.mu.Lock()
		status := store.builds[first.BuildID].Status
		store.mu.Unlock()
		if status == StatusCompleted {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("build did not complete after retry")
}

func TestManifestHashIgnoresRuntimeTimestamps(t *testing.T) {
	base := Manifest{BuildID: "b", Collection: "c", RowCount: 4, Dimensions: 8, CreatedAt: time.Now(), ValidatedAt: time.Now()}
	first, err := ManifestHash(base)
	if err != nil {
		t.Fatal(err)
	}
	base.CreatedAt = base.CreatedAt.Add(time.Hour)
	base.ValidatedAt = base.ValidatedAt.Add(time.Hour)
	second, err := ManifestHash(base)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("manifest hash changed with timestamps: %s != %s", first, second)
	}
}
