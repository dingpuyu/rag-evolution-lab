package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dingpuyu/rag-evolution-lab/internal/ingestionjob"
	"github.com/dingpuyu/rag-evolution-lab/internal/milvus"
)

type ingestionAPIProcessor struct{}

func (ingestionAPIProcessor) ApplyWithObserver(_ context.Context, change milvus.LifecycleChange, observer milvus.LifecycleObserver) (milvus.LifecycleResult, error) {
	observer(milvus.LifecycleStageValidating)
	observer(milvus.LifecycleStageChunking)
	observer(milvus.LifecycleStageEmbedding)
	observer(milvus.LifecycleStageIndexing)
	observer(milvus.LifecycleStageVerifying)
	return milvus.LifecycleResult{
		EventID: change.EventID, Operation: change.Operation, DocumentID: change.Document.ID,
		Revision: change.Revision, CurrentChunks: 1, Verified: true, CompletedAt: time.Now().UTC(),
	}, nil
}

func TestIngestionJobAPIRequiresPlatformAdminAndSupportsIdempotentSubmit(t *testing.T) {
	jobs, err := ingestionjob.New(ingestionAPIProcessor{}, ingestionjob.Config{StatePath: t.TempDir() + "/jobs.json"})
	if err != nil {
		t.Fatal(err)
	}
	if err := jobs.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(jobs.Close)

	var filter string
	handler := newEnterpriseTestHandlerWithDevIssuer(t, &filter, true, jobs)
	payload := `{
		"idempotency_key":"api-job-1",
		"change":{
			"event_id":"api-event-1",
			"operation":"upsert",
			"source_revision":1,
			"document":{"document_id":"doc-api","title":"API","content":"body","visibility":"public"}
		}
	}`

	tenantAdmin := issueTestPersona(t, handler, "tenant037_admin")
	denied := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/ingestion/jobs", strings.NewReader(payload))
	request.Header.Set("Authorization", "Bearer "+tenantAdmin)
	handler.ServeHTTP(denied, request)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("tenant admin submitted ingestion job: status=%d body=%s", denied.Code, denied.Body.String())
	}

	platform := issueTestPersona(t, handler, "platform_admin")
	accepted := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/v1/ingestion/jobs", strings.NewReader(payload))
	request.Header.Set("Authorization", "Bearer "+platform)
	handler.ServeHTTP(accepted, request)
	if accepted.Code != http.StatusAccepted || !strings.Contains(accepted.Body.String(), `"duplicate":false`) {
		t.Fatalf("submit status=%d body=%s", accepted.Code, accepted.Body.String())
	}

	replayed := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/v1/ingestion/jobs", strings.NewReader(payload))
	request.Header.Set("Authorization", "Bearer "+platform)
	handler.ServeHTTP(replayed, request)
	if replayed.Code != http.StatusOK || !strings.Contains(replayed.Body.String(), `"duplicate":true`) {
		t.Fatalf("replay status=%d body=%s", replayed.Code, replayed.Body.String())
	}

	listed := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/api/v1/ingestion/jobs", nil)
	request.Header.Set("Authorization", "Bearer "+platform)
	handler.ServeHTTP(listed, request)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"total":1`) {
		t.Fatalf("list status=%d body=%s", listed.Code, listed.Body.String())
	}
}
