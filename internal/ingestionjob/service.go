package ingestionjob

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dingpuyu/rag-evolution-lab/internal/milvus"
)

const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"

	StageQueued    = "queued"
	StageCompleted = "completed"
	StageFailed    = "failed"
	StageCancelled = "cancelled"

	EventSubmitted  = "submitted"
	EventStarted    = "started"
	EventProgressed = "progressed"
	EventRetried    = "retried"
	EventCancelled  = "cancelled"
	EventCompleted  = "completed"
	EventFailed     = "failed"
)

var (
	ErrJobNotFound         = errors.New("ingestion job not found")
	ErrIdempotencyConflict = errors.New("idempotency key was already used with a different payload")
	ErrJobNotRetryable     = errors.New("ingestion job is not retryable")
	ErrJobNotCancellable   = errors.New("ingestion job is not cancellable")
)

type Processor interface {
	ApplyWithObserver(context.Context, milvus.LifecycleChange, milvus.LifecycleObserver) (milvus.LifecycleResult, error)
}

type Config struct {
	StatePath         string
	Workers           int
	QueueCapacity     int
	MaxAttempts       int
	WorkerID          string
	LeaseDuration     time.Duration
	HeartbeatInterval time.Duration
	Repository        Repository
	Now               func() time.Time
}

type SubmitRequest struct {
	IdempotencyKey string                 `json:"idempotency_key"`
	TenantID       string                 `json:"tenant_id,omitempty"`
	DatasetID      string                 `json:"dataset_id,omitempty"`
	CreatedBy      string                 `json:"created_by,omitempty"`
	Change         milvus.LifecycleChange `json:"change"`
}

type Job struct {
	ID              string                  `json:"job_id"`
	IdempotencyKey  string                  `json:"idempotency_key"`
	TenantID        string                  `json:"tenant_id,omitempty"`
	DatasetID       string                  `json:"dataset_id,omitempty"`
	CreatedBy       string                  `json:"created_by,omitempty"`
	Status          string                  `json:"status"`
	Stage           string                  `json:"stage"`
	Attempts        int                     `json:"attempts"`
	MaxAttempts     int                     `json:"max_attempts"`
	CancelRequested bool                    `json:"cancel_requested"`
	LastError       string                  `json:"last_error,omitempty"`
	Result          *milvus.LifecycleResult `json:"result,omitempty"`
	CreatedAt       time.Time               `json:"created_at"`
	UpdatedAt       time.Time               `json:"updated_at"`
	StartedAt       *time.Time              `json:"started_at,omitempty"`
	CompletedAt     *time.Time              `json:"completed_at,omitempty"`
	WorkerID        string                  `json:"worker_id,omitempty"`
	LeaseExpiresAt  *time.Time              `json:"lease_expires_at,omitempty"`
	LastHeartbeatAt *time.Time              `json:"last_heartbeat_at,omitempty"`
}

type Summary struct {
	Total     int   `json:"total"`
	Queued    int   `json:"queued"`
	Running   int   `json:"running"`
	Completed int   `json:"completed"`
	Failed    int   `json:"failed"`
	Cancelled int   `json:"cancelled"`
	Jobs      []Job `json:"jobs"`
}

type Service struct {
	processor Processor
	config    Config
	queue     chan string

	mu      sync.Mutex
	state   PersistedState
	started bool
	closed  bool
	ctx     context.Context
	cancel  context.CancelFunc
	running map[string]context.CancelFunc
	wg      sync.WaitGroup
}

func New(processor Processor, config Config) (*Service, error) {
	if processor == nil {
		return nil, fmt.Errorf("ingestion job service requires a processor")
	}
	if strings.TrimSpace(config.StatePath) == "" {
		config.StatePath = filepath.Join("data", "ingestion", "jobs.json")
	}
	if config.Workers <= 0 {
		config.Workers = 1
	}
	if config.QueueCapacity <= 0 {
		config.QueueCapacity = 1_024
	}
	if config.MaxAttempts <= 0 {
		config.MaxAttempts = 3
	}
	if strings.TrimSpace(config.WorkerID) == "" {
		config.WorkerID = defaultWorkerID()
	}
	if config.LeaseDuration <= 0 {
		config.LeaseDuration = 2 * time.Minute
	}
	if config.HeartbeatInterval <= 0 {
		config.HeartbeatInterval = 15 * time.Second
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	service := &Service{
		processor: processor,
		config:    config,
		queue:     make(chan string, config.QueueCapacity),
		state: PersistedState{
			SchemaVersion: 1,
			Jobs:          make(map[string]*StoredJob),
			Keys:          make(map[string]string),
		},
		running: make(map[string]context.CancelFunc),
	}
	if err := service.load(); err != nil {
		return nil, err
	}
	return service, nil
}

func (service *Service) Start(parent context.Context) error {
	service.mu.Lock()
	if service.closed {
		service.mu.Unlock()
		return fmt.Errorf("ingestion job service is closed")
	}
	if service.started {
		service.mu.Unlock()
		return nil
	}
	service.ctx, service.cancel = context.WithCancel(parent)
	service.started = true
	queued := make([]string, 0)
	now := service.now()
	for id, job := range service.state.Jobs {
		if job.Status == StatusRunning {
			job.Status = StatusQueued
			job.Stage = StageQueued
			job.CancelRequested = false
			job.LastError = "worker interrupted before completion; job recovered after restart"
			job.WorkerID = ""
			job.LeaseExpiresAt = nil
			job.LastHeartbeatAt = nil
			job.UpdatedAt = now
			service.appendEventLocked(Event{JobID: id, EventType: EventFailed, Status: job.Status, Stage: job.Stage, Attempt: job.Attempts, Error: job.LastError, OccurredAt: now})
		}
		if job.Status == StatusQueued {
			queued = append(queued, id)
		}
	}
	sort.Strings(queued)
	if err := service.persistLocked(); err != nil {
		service.started = false
		service.cancel()
		service.mu.Unlock()
		return fmt.Errorf("persist recovered ingestion jobs: %w", err)
	}
	for worker := 0; worker < service.config.Workers; worker++ {
		service.wg.Add(1)
		go service.worker()
	}
	service.mu.Unlock()

	for _, id := range queued {
		select {
		case service.queue <- id:
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

func (service *Service) Submit(request SubmitRequest) (Job, bool, error) {
	key := strings.TrimSpace(request.IdempotencyKey)
	if key == "" || len(key) > 128 {
		return Job{}, false, fmt.Errorf("idempotency_key is required and must not exceed 128 characters")
	}
	hash, err := hashPayload(request)
	if err != nil {
		return Job{}, false, err
	}

	service.mu.Lock()
	if service.closed {
		service.mu.Unlock()
		return Job{}, false, fmt.Errorf("ingestion job service is closed")
	}
	if existingID, ok := service.state.Keys[key]; ok {
		existing := service.state.Jobs[existingID]
		if existing.PayloadHash != hash {
			service.mu.Unlock()
			return Job{}, false, ErrIdempotencyConflict
		}
		job := existing.Job
		service.mu.Unlock()
		return job, true, nil
	}
	now := service.now()
	id := jobID(key)
	record := &StoredJob{
		Job: Job{
			ID: id, IdempotencyKey: key, Status: StatusQueued, Stage: StageQueued,
			TenantID: strings.TrimSpace(request.TenantID), DatasetID: strings.TrimSpace(request.DatasetID), CreatedBy: strings.TrimSpace(request.CreatedBy),
			MaxAttempts: service.config.MaxAttempts, CreatedAt: now, UpdatedAt: now,
		},
		PayloadHash: hash,
		Change:      request.Change,
	}
	service.state.Jobs[id] = record
	service.state.Keys[key] = id
	if err := service.persistLocked(); err != nil {
		delete(service.state.Jobs, id)
		delete(service.state.Keys, key)
		service.mu.Unlock()
		return Job{}, false, fmt.Errorf("persist queued ingestion job: %w", err)
	}
	job := record.Job
	service.appendEventLocked(Event{JobID: id, EventType: EventSubmitted, Status: record.Status, Stage: record.Stage, Attempt: record.Attempts, OccurredAt: now})
	service.mu.Unlock()

	if err := service.enqueue(id); err != nil {
		service.failQueuedJob(id, err)
		return Job{}, false, err
	}
	return job, false, nil
}

func (service *Service) Get(id string) (Job, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	record, ok := service.state.Jobs[strings.TrimSpace(id)]
	if !ok {
		return Job{}, ErrJobNotFound
	}
	return record.Job, nil
}

func (service *Service) List() Summary {
	return service.listFor("", "")
}

func (service *Service) ListFor(tenantID, datasetID string) Summary {
	return service.listFor(strings.TrimSpace(tenantID), strings.TrimSpace(datasetID))
}

func (service *Service) listFor(tenantID, datasetID string) Summary {
	service.mu.Lock()
	defer service.mu.Unlock()
	summary := Summary{Jobs: make([]Job, 0, len(service.state.Jobs))}
	for _, record := range service.state.Jobs {
		job := record.Job
		if tenantID != "" && job.TenantID != tenantID {
			continue
		}
		if datasetID != "" && job.DatasetID != datasetID {
			continue
		}
		summary.Jobs = append(summary.Jobs, job)
		switch job.Status {
		case StatusQueued:
			summary.Queued++
		case StatusRunning:
			summary.Running++
		case StatusCompleted:
			summary.Completed++
		case StatusFailed:
			summary.Failed++
		case StatusCancelled:
			summary.Cancelled++
		}
	}
	summary.Total = len(summary.Jobs)
	sort.Slice(summary.Jobs, func(i, j int) bool {
		if summary.Jobs[i].CreatedAt.Equal(summary.Jobs[j].CreatedAt) {
			return summary.Jobs[i].ID > summary.Jobs[j].ID
		}
		return summary.Jobs[i].CreatedAt.After(summary.Jobs[j].CreatedAt)
	})
	return summary
}

func (service *Service) Retry(id string) (Job, error) {
	service.mu.Lock()
	record, ok := service.state.Jobs[strings.TrimSpace(id)]
	if !ok {
		service.mu.Unlock()
		return Job{}, ErrJobNotFound
	}
	if record.Status != StatusFailed && record.Status != StatusCancelled {
		service.mu.Unlock()
		return Job{}, ErrJobNotRetryable
	}
	if record.Attempts >= record.MaxAttempts {
		service.mu.Unlock()
		return Job{}, fmt.Errorf("%w: maximum attempts reached", ErrJobNotRetryable)
	}
	now := service.now()
	record.Status = StatusQueued
	record.Stage = StageQueued
	record.CancelRequested = false
	record.LastError = ""
	record.Result = nil
	record.StartedAt = nil
	record.CompletedAt = nil
	record.WorkerID = ""
	record.LeaseExpiresAt = nil
	record.LastHeartbeatAt = nil
	record.UpdatedAt = now
	if err := service.persistLocked(); err != nil {
		service.mu.Unlock()
		return Job{}, fmt.Errorf("persist ingestion retry: %w", err)
	}
	job := record.Job
	service.appendEventLocked(Event{JobID: record.ID, EventType: EventRetried, Status: record.Status, Stage: record.Stage, Attempt: record.Attempts, OccurredAt: now})
	service.mu.Unlock()
	if err := service.enqueue(record.ID); err != nil {
		service.failQueuedJob(record.ID, err)
		return Job{}, err
	}
	return job, nil
}

func (service *Service) Cancel(id string) (Job, error) {
	service.mu.Lock()
	record, ok := service.state.Jobs[strings.TrimSpace(id)]
	if !ok {
		service.mu.Unlock()
		return Job{}, ErrJobNotFound
	}
	now := service.now()
	switch record.Status {
	case StatusQueued:
		record.Status = StatusCancelled
		record.Stage = StageCancelled
		record.CancelRequested = true
		record.UpdatedAt = now
		record.CompletedAt = &now
	case StatusRunning:
		record.CancelRequested = true
		record.UpdatedAt = now
		if cancel := service.running[record.ID]; cancel != nil {
			cancel()
		}
	default:
		service.mu.Unlock()
		return Job{}, ErrJobNotCancellable
	}
	if err := service.persistLocked(); err != nil {
		service.mu.Unlock()
		return Job{}, fmt.Errorf("persist ingestion cancellation: %w", err)
	}
	job := record.Job
	if job.Status == StatusCancelled {
		service.appendEventLocked(Event{JobID: record.ID, EventType: EventCancelled, Status: record.Status, Stage: record.Stage, Attempt: record.Attempts, OccurredAt: now})
	}
	service.mu.Unlock()
	return job, nil
}

func (service *Service) worker() {
	defer service.wg.Done()
	for {
		select {
		case <-service.ctx.Done():
			return
		case id := <-service.queue:
			service.process(id)
		}
	}
}

func (service *Service) process(id string) {
	service.mu.Lock()
	record, ok := service.state.Jobs[id]
	if !ok || record.Status != StatusQueued {
		service.mu.Unlock()
		return
	}
	if record.Attempts >= record.MaxAttempts {
		now := service.now()
		record.Status = StatusFailed
		record.Stage = StageFailed
		record.LastError = "maximum attempts reached"
		record.UpdatedAt = now
		record.CompletedAt = &now
		_ = service.persistLocked()
		service.mu.Unlock()
		return
	}
	jobContext, cancel := context.WithCancel(service.ctx)
	service.running[id] = cancel
	now := service.now()
	record.Status = StatusRunning
	record.Stage = milvus.LifecycleStageValidating
	record.Attempts++
	record.StartedAt = &now
	record.UpdatedAt = now
	record.CompletedAt = nil
	record.WorkerID = service.config.WorkerID
	leaseExpires := now.Add(service.config.LeaseDuration)
	record.LeaseExpiresAt = &leaseExpires
	record.LastHeartbeatAt = &now
	change := record.Change
	_ = service.persistLocked()
	service.appendEventLocked(Event{JobID: id, EventType: EventStarted, Status: record.Status, Stage: record.Stage, Attempt: record.Attempts, WorkerID: record.WorkerID, OccurredAt: now})
	service.mu.Unlock()
	heartbeatDone := make(chan struct{})
	go service.heartbeat(id, heartbeatDone)

	result, err := service.processor.ApplyWithObserver(jobContext, change, func(stage string) {
		service.updateStage(id, stage)
	})
	cancel()
	close(heartbeatDone)

	service.mu.Lock()
	delete(service.running, id)
	record = service.state.Jobs[id]
	finished := service.now()
	record.UpdatedAt = finished
	record.CompletedAt = &finished
	if err != nil {
		if record.CancelRequested || errors.Is(err, context.Canceled) {
			record.Status = StatusCancelled
			record.Stage = StageCancelled
			record.LastError = "cancelled"
		} else {
			record.Status = StatusFailed
			record.Stage = StageFailed
			record.LastError = err.Error()
		}
	} else {
		record.Status = StatusCompleted
		record.Stage = StageCompleted
		record.LastError = ""
		record.Result = &result
		record.Change = lifecycleReference(record.Change, result.DocumentID)
	}
	record.LastHeartbeatAt = &finished
	record.LeaseExpiresAt = nil
	workerID := record.WorkerID
	record.WorkerID = ""
	_ = service.persistLocked()
	eventType := EventCompleted
	if err != nil {
		eventType = EventFailed
		if record.Status == StatusCancelled {
			eventType = EventCancelled
		}
	}
	service.appendEventLocked(Event{JobID: id, EventType: eventType, Status: record.Status, Stage: record.Stage, Attempt: record.Attempts, WorkerID: workerID, Error: record.LastError, OccurredAt: finished})
	service.mu.Unlock()
}

func (service *Service) updateStage(id, stage string) {
	service.mu.Lock()
	defer service.mu.Unlock()
	record, ok := service.state.Jobs[id]
	if !ok || record.Status != StatusRunning {
		return
	}
	record.Stage = stage
	record.UpdatedAt = service.now()
	now := record.UpdatedAt
	record.LastHeartbeatAt = &now
	_ = service.persistLocked()
	service.appendEventLocked(Event{JobID: id, EventType: EventProgressed, Status: record.Status, Stage: stage, Attempt: record.Attempts, WorkerID: record.WorkerID, OccurredAt: now})
}

func (service *Service) heartbeat(id string, done <-chan struct{}) {
	ticker := time.NewTicker(service.config.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			service.mu.Lock()
			record, ok := service.state.Jobs[id]
			if !ok || record.Status != StatusRunning {
				service.mu.Unlock()
				return
			}
			now := service.now()
			expires := now.Add(service.config.LeaseDuration)
			record.LastHeartbeatAt = &now
			record.LeaseExpiresAt = &expires
			record.UpdatedAt = now
			_ = service.persistLocked()
			service.mu.Unlock()
		}
	}
}

func (service *Service) enqueue(id string) error {
	service.mu.Lock()
	started := service.started
	closed := service.closed
	service.mu.Unlock()
	if closed {
		return fmt.Errorf("ingestion job service is closed")
	}
	if !started {
		return nil
	}
	select {
	case service.queue <- id:
		return nil
	default:
		return fmt.Errorf("ingestion queue is full")
	}
}

func (service *Service) failQueuedJob(id string, cause error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	record, ok := service.state.Jobs[id]
	if !ok || record.Status != StatusQueued {
		return
	}
	now := service.now()
	record.Status = StatusFailed
	record.Stage = StageFailed
	record.LastError = cause.Error()
	record.UpdatedAt = now
	record.CompletedAt = &now
	_ = service.persistLocked()
	service.appendEventLocked(Event{JobID: id, EventType: EventFailed, Status: record.Status, Stage: record.Stage, Attempt: record.Attempts, Error: record.LastError, OccurredAt: now})
}

func (service *Service) load() error {
	if service.config.Repository != nil {
		state, err := service.config.Repository.Load(context.Background())
		if err != nil {
			return fmt.Errorf("load ingestion job repository: %w", err)
		}
		if state.SchemaVersion == 0 {
			state.SchemaVersion = 1
		}
		if state.SchemaVersion != 1 {
			return fmt.Errorf("unsupported ingestion job state schema version %d", state.SchemaVersion)
		}
		if state.Jobs == nil {
			state.Jobs = make(map[string]*StoredJob)
		}
		if state.Keys == nil {
			state.Keys = make(map[string]string)
		}
		service.state = state
		return nil
	}
	data, err := os.ReadFile(service.config.StatePath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read ingestion job state: %w", err)
	}
	if err := json.Unmarshal(data, &service.state); err != nil {
		return fmt.Errorf("decode ingestion job state: %w", err)
	}
	if service.state.SchemaVersion != 1 {
		return fmt.Errorf("unsupported ingestion job state schema version %d", service.state.SchemaVersion)
	}
	if service.state.Jobs == nil {
		service.state.Jobs = make(map[string]*StoredJob)
	}
	if service.state.Keys == nil {
		service.state.Keys = make(map[string]string)
	}
	return nil
}

func (service *Service) persistLocked() error {
	if service.config.Repository != nil {
		return service.config.Repository.Save(context.Background(), service.state)
	}
	data, err := json.MarshalIndent(service.state, "", "  ")
	if err != nil {
		return err
	}
	directory := filepath.Dir(service.config.StatePath)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(directory, ".ingestion-jobs-*.tmp")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(name, service.config.StatePath)
}

func (service *Service) now() time.Time {
	return service.config.Now().UTC()
}

func (service *Service) appendEventLocked(event Event) {
	if service.config.Repository == nil {
		return
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = service.now()
	}
	_ = service.config.Repository.AppendEvent(context.Background(), event)
}

func hashPayload(request SubmitRequest) (string, error) {
	data, err := json.Marshal(struct {
		TenantID  string                 `json:"tenant_id,omitempty"`
		DatasetID string                 `json:"dataset_id,omitempty"`
		Change    milvus.LifecycleChange `json:"change"`
	}{TenantID: strings.TrimSpace(request.TenantID), DatasetID: strings.TrimSpace(request.DatasetID), Change: request.Change})
	if err != nil {
		return "", fmt.Errorf("encode ingestion payload: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func jobID(key string) string {
	sum := sha256.Sum256([]byte(key))
	return "job_" + hex.EncodeToString(sum[:12])
}

func lifecycleReference(change milvus.LifecycleChange, documentID string) milvus.LifecycleChange {
	return milvus.LifecycleChange{
		EventID: change.EventID, Operation: change.Operation, Revision: change.Revision, DocumentID: documentID,
	}
}

func defaultWorkerID() string {
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		host = "unknown-host"
	}
	return fmt.Sprintf("%s-%d", host, os.Getpid())
}
