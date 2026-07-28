package datasetaccess

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/dingpuyu/rag-evolution-lab/internal/ingestionjob"
	"github.com/dingpuyu/rag-evolution-lab/internal/milvus"
)

// IngestionRepository returns the control-plane backed job repository. The
// same PostgreSQL connection used for tenants and datasets stores job state,
// so authorization and ingestion operations share one durable boundary.
func (store *PostgresStore) IngestionRepository() ingestionjob.Repository {
	return &postgresIngestionRepository{db: store.db}
}

type postgresIngestionRepository struct {
	db *sql.DB
}

func (repository *postgresIngestionRepository) Load(ctx context.Context) (ingestionjob.PersistedState, error) {
	state := ingestionjob.PersistedState{SchemaVersion: 1, Jobs: make(map[string]*ingestionjob.StoredJob), Keys: make(map[string]string)}
	rows, err := repository.db.QueryContext(ctx, `
		SELECT job_id, idempotency_key, tenant_id, dataset_id, created_by, status, stage,
		       attempts, max_attempts, cancel_requested, last_error, result, change,
		       created_at, updated_at, started_at, completed_at, worker_id,
		       lease_expires_at, last_heartbeat_at, payload_hash
		FROM ingestion_jobs ORDER BY created_at`)
	if err != nil {
		return state, fmt.Errorf("load ingestion jobs: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			job            ingestionjob.Job
			idempotencyKey string
			payloadHash    string
			resultJSON     []byte
			changeJSON     []byte
		)
		if err := rows.Scan(
			&job.ID, &idempotencyKey, &job.TenantID, &job.DatasetID, &job.CreatedBy,
			&job.Status, &job.Stage, &job.Attempts, &job.MaxAttempts, &job.CancelRequested,
			&job.LastError, &resultJSON, &changeJSON, &job.CreatedAt, &job.UpdatedAt,
			&job.StartedAt, &job.CompletedAt, &job.WorkerID, &job.LeaseExpiresAt,
			&job.LastHeartbeatAt, &payloadHash,
		); err != nil {
			return state, fmt.Errorf("scan ingestion job: %w", err)
		}
		job.IdempotencyKey = idempotencyKey
		var change milvus.LifecycleChange
		if err := json.Unmarshal(changeJSON, &change); err != nil {
			return state, fmt.Errorf("decode lifecycle change: %w", err)
		}
		if len(resultJSON) > 0 {
			var result milvus.LifecycleResult
			if err := json.Unmarshal(resultJSON, &result); err != nil {
				return state, fmt.Errorf("decode lifecycle result: %w", err)
			}
			job.Result = &result
		}
		state.Jobs[job.ID] = &ingestionjob.StoredJob{Job: job, PayloadHash: payloadHash, Change: change}
		state.Keys[idempotencyKey] = job.ID
	}
	if err := rows.Err(); err != nil {
		return state, err
	}
	return state, nil
}

func (repository *postgresIngestionRepository) Save(ctx context.Context, state ingestionjob.PersistedState) error {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, stored := range state.Jobs {
		resultJSON, err := nullableJSON(stored.Result)
		if err != nil {
			return err
		}
		changeJSON, err := json.Marshal(stored.Change)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO ingestion_jobs (
				job_id, idempotency_key, tenant_id, dataset_id, created_by, status, stage,
				attempts, max_attempts, cancel_requested, last_error, result, change,
				created_at, updated_at, started_at, completed_at, worker_id,
				lease_expires_at, last_heartbeat_at, payload_hash
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,NULLIF($12,'')::jsonb,$13,$14,$15,$16,$17,$18,$19,$20,$21)
			ON CONFLICT (job_id) DO UPDATE SET
				idempotency_key=EXCLUDED.idempotency_key, tenant_id=EXCLUDED.tenant_id,
				dataset_id=EXCLUDED.dataset_id, created_by=EXCLUDED.created_by,
				status=EXCLUDED.status, stage=EXCLUDED.stage, attempts=EXCLUDED.attempts,
				max_attempts=EXCLUDED.max_attempts, cancel_requested=EXCLUDED.cancel_requested,
				last_error=EXCLUDED.last_error, result=EXCLUDED.result, change=EXCLUDED.change,
				updated_at=EXCLUDED.updated_at, started_at=EXCLUDED.started_at,
				completed_at=EXCLUDED.completed_at, worker_id=EXCLUDED.worker_id,
				lease_expires_at=EXCLUDED.lease_expires_at,
				last_heartbeat_at=EXCLUDED.last_heartbeat_at, payload_hash=EXCLUDED.payload_hash`,
			stored.ID, stored.IdempotencyKey, stored.TenantID, stored.DatasetID, stored.CreatedBy,
			stored.Status, stored.Stage, stored.Attempts, stored.MaxAttempts, stored.CancelRequested,
			stored.LastError, string(resultJSON), changeJSON, stored.CreatedAt, stored.UpdatedAt,
			stored.StartedAt, stored.CompletedAt, stored.WorkerID, stored.LeaseExpiresAt,
			stored.LastHeartbeatAt, stored.PayloadHash); err != nil {
			return fmt.Errorf("save ingestion job %s: %w", stored.ID, err)
		}
	}
	return tx.Commit()
}

func (repository *postgresIngestionRepository) AppendEvent(ctx context.Context, event ingestionjob.Event) error {
	_, err := repository.db.ExecContext(ctx, `
		INSERT INTO ingestion_job_events (job_id, event_type, status, stage, attempt, worker_id, error, occurred_at)
		VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,''),$8)`,
		event.JobID, event.EventType, event.Status, event.Stage, event.Attempt, event.WorkerID, event.Error, event.OccurredAt)
	return err
}

func nullableJSON(value *milvus.LifecycleResult) ([]byte, error) {
	if value == nil {
		return nil, nil
	}
	return json.Marshal(value)
}

var _ ingestionjob.Repository = (*postgresIngestionRepository)(nil)
