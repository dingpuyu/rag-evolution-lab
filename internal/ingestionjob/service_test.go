package ingestionjob

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dingpuyu/rag-evolution-lab/internal/milvus"
)

type fakeProcessor struct {
	mu        sync.Mutex
	calls     int
	failFirst bool
	block     bool
	started   chan struct{}
	stages    []string
}

func (processor *fakeProcessor) ApplyWithObserver(ctx context.Context, change milvus.LifecycleChange, observer milvus.LifecycleObserver) (milvus.LifecycleResult, error) {
	processor.mu.Lock()
	processor.calls++
	call := processor.calls
	processor.mu.Unlock()
	for _, stage := range []string{
		milvus.LifecycleStageValidating,
		milvus.LifecycleStageChunking,
		milvus.LifecycleStageEmbedding,
		milvus.LifecycleStageIndexing,
		milvus.LifecycleStageVerifying,
	} {
		observer(stage)
		processor.mu.Lock()
		processor.stages = append(processor.stages, stage)
		processor.mu.Unlock()
	}
	if processor.started != nil {
		select {
		case processor.started <- struct{}{}:
		default:
		}
	}
	if processor.block {
		<-ctx.Done()
		return milvus.LifecycleResult{}, ctx.Err()
	}
	if processor.failFirst && call == 1 {
		return milvus.LifecycleResult{}, errors.New("temporary embedding failure")
	}
	documentID := change.DocumentID
	if change.Document != nil {
		documentID = change.Document.ID
	}
	return milvus.LifecycleResult{
		EventID: change.EventID, Operation: change.Operation, DocumentID: documentID,
		Revision: change.Revision, CurrentChunks: 2, Verified: true, CompletedAt: time.Now().UTC(),
	}, nil
}

func TestSubmitIsIdempotentAndCompletedStateDropsDocumentContent(t *testing.T) {
	statePath := t.TempDir() + "/jobs.json"
	processor := &fakeProcessor{}
	service, err := New(processor, Config{StatePath: statePath})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer service.Close()

	request := testSubmitRequest("ingest-doc-1")
	created, duplicate, err := service.Submit(request)
	if err != nil || duplicate {
		t.Fatalf("submit result=%#v duplicate=%v err=%v", created, duplicate, err)
	}
	completed := waitForStatus(t, service, created.ID, StatusCompleted)
	if completed.Stage != StageCompleted || completed.Attempts != 1 || completed.Result == nil || !completed.Result.Verified {
		t.Fatalf("unexpected completed job: %#v", completed)
	}
	replayed, duplicate, err := service.Submit(request)
	if err != nil || !duplicate || replayed.ID != created.ID {
		t.Fatalf("idempotent replay=%#v duplicate=%v err=%v", replayed, duplicate, err)
	}
	conflict := request
	conflict.Change.Document.Content = "different payload"
	if _, _, err := service.Submit(conflict); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("expected idempotency conflict, got %v", err)
	}
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "confidential document body") {
		t.Fatal("completed job state retained source document content")
	}
}

func TestFailedJobCanBeRetriedWithinAttemptBudget(t *testing.T) {
	processor := &fakeProcessor{failFirst: true}
	service, err := New(processor, Config{StatePath: t.TempDir() + "/jobs.json", MaxAttempts: 2})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer service.Close()

	created, _, err := service.Submit(testSubmitRequest("retry-doc-1"))
	if err != nil {
		t.Fatal(err)
	}
	failed := waitForStatus(t, service, created.ID, StatusFailed)
	if !strings.Contains(failed.LastError, "temporary embedding failure") || failed.Attempts != 1 {
		t.Fatalf("unexpected failed job: %#v", failed)
	}
	if _, err := service.Retry(created.ID); err != nil {
		t.Fatal(err)
	}
	completed := waitForStatus(t, service, created.ID, StatusCompleted)
	if completed.Attempts != 2 {
		t.Fatalf("retry did not consume second attempt: %#v", completed)
	}
	if _, err := service.Retry(created.ID); !errors.Is(err, ErrJobNotRetryable) {
		t.Fatalf("completed job must not be retryable: %v", err)
	}
}

func TestRunningJobCancellationPropagatesToProcessor(t *testing.T) {
	processor := &fakeProcessor{block: true, started: make(chan struct{}, 1)}
	service, err := New(processor, Config{StatePath: t.TempDir() + "/jobs.json"})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer service.Close()

	created, _, err := service.Submit(testSubmitRequest("cancel-doc-1"))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-processor.started:
	case <-time.After(2 * time.Second):
		t.Fatal("processor did not start")
	}
	if _, err := service.Cancel(created.ID); err != nil {
		t.Fatal(err)
	}
	cancelled := waitForStatus(t, service, created.ID, StatusCancelled)
	if !cancelled.CancelRequested || cancelled.Stage != StageCancelled {
		t.Fatalf("unexpected cancelled job: %#v", cancelled)
	}
}

func TestQueuedJobIsRecoveredAfterServiceRestart(t *testing.T) {
	statePath := t.TempDir() + "/jobs.json"
	first, err := New(&fakeProcessor{}, Config{StatePath: statePath})
	if err != nil {
		t.Fatal(err)
	}
	created, _, err := first.Submit(testSubmitRequest("restart-doc-1"))
	if err != nil {
		t.Fatal(err)
	}

	restarted, err := New(&fakeProcessor{}, Config{StatePath: statePath})
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	waitForStatus(t, restarted, created.ID, StatusCompleted)
}

func testSubmitRequest(key string) SubmitRequest {
	return SubmitRequest{
		IdempotencyKey: key,
		Change: milvus.LifecycleChange{
			EventID: key + "-event", Operation: milvus.OperationUpsert, Revision: 1,
			Document: &milvus.LifecycleDocument{
				ID: "doc-1", Title: "Document", Content: "confidential document body", Visibility: "public",
			},
		},
	}
}

func waitForStatus(t *testing.T, service *Service, id, expected string) Job {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		job, err := service.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		if job.Status == expected {
			return job
		}
		time.Sleep(5 * time.Millisecond)
	}
	job, _ := service.Get(id)
	t.Fatalf("job did not reach %s: %#v", expected, job)
	return Job{}
}
