package datasetaccess

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dingpuyu/rag-evolution-lab/internal/ingestionjob"
	"github.com/dingpuyu/rag-evolution-lab/internal/milvus"
)

type repositoryTestProcessor struct{}

func (repositoryTestProcessor) ApplyWithObserver(_ context.Context, change milvus.LifecycleChange, observer milvus.LifecycleObserver) (milvus.LifecycleResult, error) {
	for _, stage := range []string{milvus.LifecycleStageValidating, milvus.LifecycleStageChunking, milvus.LifecycleStageEmbedding, milvus.LifecycleStageIndexing, milvus.LifecycleStageVerifying} {
		observer(stage)
	}
	return milvus.LifecycleResult{EventID: change.EventID, Operation: change.Operation, DocumentID: change.Document.ID, Revision: change.Revision, CurrentChunks: 1, Verified: true, CompletedAt: time.Now().UTC()}, nil
}

func TestPostgresIngestionRepositoryPersistsJobAndEvents(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("RAGLAB_TEST_POSTGRES_URL"))
	if databaseURL == "" {
		t.Skip("set RAGLAB_TEST_POSTGRES_URL to run PostgreSQL ingestion repository integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store, err := OpenPostgres(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	key := "repository-test-" + time.Now().UTC().Format("20060102150405.000000000")
	tenantID := "tenant_repository_test_" + strings.ReplaceAll(key, ".", "-")
	datasetID := "repository-test-dataset-" + strings.ReplaceAll(key, ".", "-")
	service, err := ingestionjob.New(repositoryTestProcessor{}, ingestionjob.Config{
		Repository: store.IngestionRepository(), Workers: 1, MaxAttempts: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Start(ctx); err != nil {
		t.Fatal(err)
	}
	created, duplicate, err := service.Submit(ingestionjob.SubmitRequest{
		IdempotencyKey: key, TenantID: tenantID, DatasetID: datasetID, CreatedBy: "integration-test",
		Change: milvus.LifecycleChange{EventID: key + "-event", Operation: milvus.OperationUpsert, Revision: 1,
			Document: &milvus.LifecycleDocument{ID: key, Title: "Repository test", Content: "durable body", Visibility: "tenant"}},
	})
	if err != nil || duplicate {
		t.Fatalf("submit job=%#v duplicate=%v err=%v", created, duplicate, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		job, getErr := service.Get(created.ID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if job.Status == ingestionjob.StatusCompleted {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	completed, err := service.Get(created.ID)
	if err != nil || completed.Status != ingestionjob.StatusCompleted || completed.DatasetID != datasetID {
		t.Fatalf("job was not durably completed: %#v err=%v", completed, err)
	}
	service.Close()

	restarted, err := ingestionjob.New(repositoryTestProcessor{}, ingestionjob.Config{Repository: store.IngestionRepository()})
	if err != nil {
		t.Fatal(err)
	}
	replayed, duplicate, err := restarted.Submit(ingestionjob.SubmitRequest{
		IdempotencyKey: key, TenantID: tenantID, DatasetID: datasetID, CreatedBy: "integration-test",
		Change: milvus.LifecycleChange{EventID: key + "-event", Operation: milvus.OperationUpsert, Revision: 1,
			Document: &milvus.LifecycleDocument{ID: key, Title: "Repository test", Content: "durable body", Visibility: "tenant"}},
	})
	if err != nil || !duplicate || replayed.ID != created.ID || replayed.Status != ingestionjob.StatusCompleted {
		t.Fatalf("durable idempotent replay=%#v duplicate=%v err=%v", replayed, duplicate, err)
	}
	if summary := restarted.ListFor(tenantID, datasetID); summary.Total != 1 || summary.Completed != 1 {
		t.Fatalf("unexpected durable summary: %#v", summary)
	}
	restarted.Close()
}
