package datasetaccess

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dingpuyu/rag-evolution-lab/internal/auth"
)

func TestPostgresApplicationBindingsAreTenantScoped(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("RAGLAB_TEST_POSTGRES_URL"))
	if databaseURL == "" {
		t.Skip("set RAGLAB_TEST_POSTGRES_URL to run PostgreSQL application integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store, err := OpenPostgres(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	identity := auth.Identity{Subject: "application-test-a", TenantID: "tenant_a", Roles: []string{"admin"}}
	slug := "platform-test-" + time.Now().UTC().Format("20060102150405")
	application, err := store.CreateApplication(ctx, identity, CreateApplication{Name: "Platform Test App", Slug: slug})
	if err != nil {
		t.Fatal(err)
	}
	if application.CreatedAt.IsZero() {
		t.Fatal("application creation timestamp was not returned")
	}
	environments, err := store.Environments(ctx, identity, application.ID)
	if err != nil || len(environments) != 1 || environments[0].Name != "dev" {
		t.Fatalf("unexpected default environments=%#v err=%v", environments, err)
	}
	binding, err := store.CreateBinding(ctx, identity, application.ID, CreateBinding{
		EnvironmentID: environments[0].ID, DatasetID: "tenant-a-operations", Purpose: "customer support",
		Policy: RetrievalPolicy{TopK: 6, CandidateK: 24, Rerank: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if binding.Policy.TopK != 6 || binding.Policy.CandidateK != 24 || binding.DatasetName == "" {
		t.Fatalf("binding policy or dataset was not persisted: %#v", binding)
	}
	bindings, err := store.Bindings(ctx, identity, application.ID, environments[0].ID)
	if err != nil || len(bindings) != 1 || bindings[0].DatasetID != "tenant-a-operations" {
		t.Fatalf("unexpected bindings=%#v err=%v", bindings, err)
	}
	bob := auth.Identity{Subject: "application-test-b", TenantID: "tenant_b", Roles: []string{"admin"}}
	if _, err := store.Environments(ctx, bob, application.ID); err == nil {
		t.Fatal("cross-tenant admin could read application environments")
	}
}

func TestPostgresIndexReleasePublishAndRollback(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("RAGLAB_TEST_POSTGRES_URL"))
	if databaseURL == "" {
		t.Skip("set RAGLAB_TEST_POSTGRES_URL to run PostgreSQL index release integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store, err := OpenPostgres(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	identity := auth.Identity{Subject: "index-test-a", TenantID: "tenant_a", Roles: []string{"admin"}}
	slug := "index-release-" + time.Now().UTC().Format("20060102150405")
	application, err := store.CreateApplication(ctx, identity, CreateApplication{Name: "Index Release Test", Slug: slug})
	if err != nil {
		t.Fatal(err)
	}
	environmentID := application.ID + "-dev"
	first, err := store.PublishIndexRelease(ctx, identity, application.ID, PublishIndex{EnvironmentID: environmentID, Version: "v1", Collection: "raglab_index_v1", Alias: "raglab-test-active"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.PublishIndexRelease(ctx, identity, application.ID, PublishIndex{EnvironmentID: environmentID, Version: "v2", Collection: "raglab_index_v2", Alias: "raglab-test-active"})
	if err != nil {
		t.Fatal(err)
	}
	active, err := store.ResolveIndexRelease(ctx, identity, application.ID, environmentID)
	if err != nil || active.ReleaseID != second.ReleaseID || active.Collection != "raglab_index_v2" {
		t.Fatalf("unexpected active release=%#v err=%v", active, err)
	}
	rolledBack, err := store.RollbackIndexRelease(ctx, identity, application.ID, environmentID, first.ReleaseID)
	if err != nil || rolledBack.State != "published" {
		t.Fatalf("rollback failed release=%#v err=%v", rolledBack, err)
	}
	active, err = store.ResolveIndexRelease(ctx, identity, application.ID, environmentID)
	if err != nil || active.ReleaseID != first.ReleaseID {
		t.Fatalf("unexpected active release after rollback=%#v err=%v", active, err)
	}
}
