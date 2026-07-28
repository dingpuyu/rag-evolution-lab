package ingestionjob

import (
	"context"
	"time"

	"github.com/dingpuyu/rag-evolution-lab/internal/milvus"
)

// Repository is the durable boundary for ingestion jobs. The service keeps a
// hot in-memory view for workers, while every state transition is persisted by
// the repository before it is exposed through the API.
type Repository interface {
	Load(context.Context) (PersistedState, error)
	Save(context.Context, PersistedState) error
	AppendEvent(context.Context, Event) error
}

type PersistedState struct {
	SchemaVersion int                   `json:"schema_version"`
	Jobs          map[string]*StoredJob `json:"jobs"`
	Keys          map[string]string     `json:"idempotency_keys"`
}

type StoredJob struct {
	Job
	PayloadHash string                 `json:"payload_hash"`
	Change      milvus.LifecycleChange `json:"change"`
}

type Event struct {
	JobID      string    `json:"job_id"`
	EventType  string    `json:"event_type"`
	Status     string    `json:"status"`
	Stage      string    `json:"stage"`
	Attempt    int       `json:"attempt"`
	WorkerID   string    `json:"worker_id,omitempty"`
	Error      string    `json:"error,omitempty"`
	OccurredAt time.Time `json:"occurred_at"`
}
