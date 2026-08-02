package milvus

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dingpuyu/rag-evolution-lab/internal/domain"
	"github.com/dingpuyu/rag-evolution-lab/internal/ingest"
	"github.com/dingpuyu/rag-evolution-lab/internal/retrieval"
)

const (
	OperationUpsert = "upsert"
	OperationDelete = "delete"

	LifecycleStageValidating = "validating"
	LifecycleStageChunking   = "chunking"
	LifecycleStageEmbedding  = "embedding"
	LifecycleStageIndexing   = "indexing"
	LifecycleStageVerifying  = "verifying"
)

type LifecycleObserver func(stage string)

type LifecycleConfig struct {
	Collection       string
	Alias            string
	EmbeddingVersion string
	StatePath        string
	ChunkRunes       int
	Now              func() time.Time
}

type LifecycleService struct {
	client   *Client
	embedder retrieval.Embedder
	chunker  ingest.Chunker
	config   LifecycleConfig

	mu    sync.Mutex
	state lifecycleState
}

// Catalog exposes the lifecycle collection through the same metadata-only
// inventory used by the operator UI.
func (service *LifecycleService) Catalog(ctx context.Context) (Catalog, error) {
	return (&Service{client: service.client, embedder: service.embedder, collection: service.config.Collection}).Catalog(ctx)
}

// CatalogForQuery applies the same dataset authorization filter to the
// lifecycle collection used by the enterprise portal.
func (service *LifecycleService) CatalogForQuery(ctx context.Context, query Query) (Catalog, error) {
	return (&Service{client: service.client, embedder: service.embedder, collection: service.config.Collection}).CatalogForQuery(ctx, query)
}

type LifecycleDocument struct {
	ID             string   `json:"document_id"`
	Title          string   `json:"title"`
	Content        string   `json:"content"`
	Product        string   `json:"product"`
	Version        string   `json:"version"`
	Status         string   `json:"status"`
	Visibility     string   `json:"visibility"`
	AllowedTenants []string `json:"allowed_tenants"`
	AllowedRoles   []string `json:"allowed_roles"`
}

type LifecycleChange struct {
	EventID    string             `json:"event_id"`
	Operation  string             `json:"operation"`
	Revision   int64              `json:"source_revision"`
	DocumentID string             `json:"document_id,omitempty"`
	Document   *LifecycleDocument `json:"document,omitempty"`
}

type LifecycleResult struct {
	EventID          string    `json:"event_id"`
	Operation        string    `json:"operation"`
	DocumentID       string    `json:"document_id"`
	Revision         int64     `json:"source_revision"`
	Collection       string    `json:"collection"`
	Alias            string    `json:"alias,omitempty"`
	EmbeddingModel   string    `json:"embedding_model"`
	EmbeddingVersion string    `json:"embedding_version"`
	PreviousChunks   int       `json:"previous_chunks"`
	CurrentChunks    int       `json:"current_chunks"`
	UpsertedChunks   int64     `json:"upserted_chunks"`
	DeletedChunks    int       `json:"deleted_chunks"`
	Duplicate        bool      `json:"duplicate"`
	Verified         bool      `json:"verified"`
	CompletedAt      time.Time `json:"completed_at"`
}

type LifecycleStatus struct {
	Collection       string                   `json:"collection"`
	Alias            string                   `json:"alias,omitempty"`
	EmbeddingModel   string                   `json:"embedding_model"`
	EmbeddingVersion string                   `json:"embedding_version"`
	StatePath        string                   `json:"state_path"`
	Events           int                      `json:"events"`
	PendingEvents    int                      `json:"pending_events"`
	Documents        map[string]DocumentState `json:"documents"`
}

type DocumentState struct {
	Revision    int64     `json:"source_revision"`
	Deleted     bool      `json:"deleted"`
	Version     string    `json:"document_version,omitempty"`
	LastEventID string    `json:"last_event_id"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type lifecycleState struct {
	SchemaVersion int                                `json:"schema_version"`
	Events        map[string]persistedLifecycleEvent `json:"events"`
	Documents     map[string]DocumentState           `json:"documents"`
}

type persistedLifecycleEvent struct {
	Change     LifecycleChange `json:"change"`
	ChangeHash string          `json:"change_hash"`
	Status     string          `json:"status"`
	Result     LifecycleResult `json:"result,omitempty"`
	LastError  string          `json:"last_error,omitempty"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

func NewLifecycleService(client *Client, embedder retrieval.Embedder, config LifecycleConfig) (*LifecycleService, error) {
	if client == nil || embedder == nil {
		return nil, fmt.Errorf("lifecycle service requires Milvus client and embedder")
	}
	if strings.TrimSpace(config.Collection) == "" {
		config.Collection = "raglab_lifecycle_v1"
	}
	if strings.TrimSpace(config.EmbeddingVersion) == "" {
		return nil, fmt.Errorf("embedding version is required")
	}
	if strings.TrimSpace(config.StatePath) == "" {
		config.StatePath = filepath.Join("data", "lifecycle", "state.json")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	service := &LifecycleService{
		client: client, embedder: embedder, chunker: ingest.Chunker{MaxRunes: config.ChunkRunes}, config: config,
		state: lifecycleState{SchemaVersion: 1, Events: make(map[string]persistedLifecycleEvent), Documents: make(map[string]DocumentState)},
	}
	if err := service.loadState(); err != nil {
		return nil, err
	}
	return service, nil
}

func (service *LifecycleService) Apply(ctx context.Context, change LifecycleChange) (LifecycleResult, error) {
	return service.ApplyWithObserver(ctx, change, nil)
}

func (service *LifecycleService) ApplyWithObserver(ctx context.Context, change LifecycleChange, observer LifecycleObserver) (LifecycleResult, error) {
	service.mu.Lock()
	defer service.mu.Unlock()

	observeLifecycleStage(observer, LifecycleStageValidating)
	documentID, err := validateLifecycleChange(&change)
	if err != nil {
		return LifecycleResult{}, err
	}
	changeHash, err := lifecycleChangeHash(change)
	if err != nil {
		return LifecycleResult{}, err
	}
	if existing, ok := service.state.Events[change.EventID]; ok {
		if existing.ChangeHash != changeHash {
			return LifecycleResult{}, fmt.Errorf("event_id %q was already used with a different payload", change.EventID)
		}
		if existing.Status == "completed" {
			result := existing.Result
			result.Duplicate = true
			return result, nil
		}
	}
	if revision, eventID := service.highWatermark(documentID); change.Revision < revision || (change.Revision == revision && eventID != "" && eventID != change.EventID) {
		return LifecycleResult{}, fmt.Errorf("stale or conflicting revision %d; document %q already reached revision %d via event %q", change.Revision, documentID, revision, eventID)
	}

	now := service.config.Now().UTC()
	service.state.Events[change.EventID] = persistedLifecycleEvent{
		Change: change, ChangeHash: changeHash, Status: "pending", UpdatedAt: now,
	}
	if err := service.persistState(); err != nil {
		return LifecycleResult{}, fmt.Errorf("persist pending lifecycle event: %w", err)
	}

	var result LifecycleResult
	if change.Operation == OperationUpsert {
		result, err = service.applyUpsert(ctx, change, documentID, observer)
	} else {
		result, err = service.applyDelete(ctx, change, documentID, observer)
	}
	if err != nil {
		event := service.state.Events[change.EventID]
		event.LastError = err.Error()
		event.UpdatedAt = service.config.Now().UTC()
		service.state.Events[change.EventID] = event
		_ = service.persistState()
		return LifecycleResult{}, err
	}
	result.EventID = change.EventID
	result.Operation = change.Operation
	result.DocumentID = documentID
	result.Revision = change.Revision
	result.Collection = service.config.Collection
	result.Alias = service.config.Alias
	result.EmbeddingModel = service.embedder.Name()
	result.EmbeddingVersion = service.config.EmbeddingVersion
	result.Verified = true
	result.CompletedAt = service.config.Now().UTC()

	service.state.Events[change.EventID] = persistedLifecycleEvent{
		Change: lifecycleEventReference(change, documentID), ChangeHash: changeHash,
		Status: "completed", Result: result, UpdatedAt: result.CompletedAt,
	}
	documentVersion := ""
	if change.Document != nil {
		documentVersion = change.Document.Version
	}
	service.state.Documents[documentID] = DocumentState{
		Revision: change.Revision, Deleted: change.Operation == OperationDelete, Version: documentVersion,
		LastEventID: change.EventID, UpdatedAt: result.CompletedAt,
	}
	if err := service.persistState(); err != nil {
		return LifecycleResult{}, fmt.Errorf("Milvus mutation succeeded but lifecycle commit failed: %w", err)
	}
	return result, nil
}

func (service *LifecycleService) Status() LifecycleStatus {
	service.mu.Lock()
	defer service.mu.Unlock()
	documents := make(map[string]DocumentState, len(service.state.Documents))
	for id, state := range service.state.Documents {
		documents[id] = state
	}
	pending := 0
	for _, event := range service.state.Events {
		if event.Status != "completed" {
			pending++
		}
	}
	return LifecycleStatus{
		Collection: service.config.Collection, Alias: service.config.Alias,
		EmbeddingModel: service.embedder.Name(), EmbeddingVersion: service.config.EmbeddingVersion,
		StatePath: service.config.StatePath, Events: len(service.state.Events), PendingEvents: pending, Documents: documents,
	}
}

// ConfiguredAlias is the stable compatibility alias used by the legacy
// Dataset API. Application Gateway traffic uses the published physical
// collection, while this alias keeps the migration/compatibility route safe.
func (service *LifecycleService) ConfiguredAlias() string {
	service.mu.Lock()
	defer service.mu.Unlock()
	return service.config.Alias
}

func (service *LifecycleService) Search(ctx context.Context, query Query) (SearchResult, error) {
	query.Text = strings.TrimSpace(query.Text)
	if query.Text == "" {
		return SearchResult{}, fmt.Errorf("query must not be empty")
	}
	if query.TopK <= 0 || query.TopK > 20 {
		query.TopK = 5
	}
	totalStarted := time.Now()
	embedStarted := time.Now()
	vector, err := service.embedder.EmbedQuery(ctx, query.Text)
	if err != nil {
		return SearchResult{}, fmt.Errorf("embed lifecycle query: %w", err)
	}
	embedLatency := time.Since(embedStarted)
	filter := buildFilter(query) +
		` and embedding_model == "` + escapeFilter(service.embedder.Name()) + `"` +
		` and embedding_version == "` + escapeFilter(service.config.EmbeddingVersion) + `"`
	collection := strings.TrimSpace(query.Collection)
	if collection == "" {
		collection = service.config.Collection
	}
	if collection == service.config.Collection && service.config.Alias != "" {
		collection = service.config.Alias
	}
	searchStarted := time.Now()
	hits, err := service.client.Search(ctx, SearchRequest{
		Collection: collection, Vector: vector, Filter: filter, Limit: query.TopK,
	})
	if err != nil {
		return SearchResult{}, err
	}
	return SearchResult{
		Query: query.Text, Collection: collection, Embedder: service.embedder.Name(), Dimensions: len(vector),
		Metric: "COSINE", Filter: filter, EmbeddingLatencyMS: milliseconds(embedLatency),
		SearchLatencyMS: milliseconds(time.Since(searchStarted)), TotalLatencyMS: milliseconds(time.Since(totalStarted)), Hits: hits,
	}, nil
}

// ValidateCollection checks that a candidate physical index is query-ready
// for the currently running embedding build before an alias/pointer switch.
// It intentionally does not mutate Milvus.
func (service *LifecycleService) ValidateCollection(ctx context.Context, collection string) error {
	collection = strings.TrimSpace(collection)
	if collection == "" {
		return fmt.Errorf("index collection must not be empty")
	}
	collections, err := service.client.ListCollections(ctx)
	if err != nil {
		return err
	}
	if !contains(collections, collection) {
		return fmt.Errorf("index collection %q does not exist", collection)
	}
	description, err := service.client.DescribeCollection(ctx, collection)
	if err != nil {
		return err
	}
	requiredFields := map[string]bool{
		"chunk_id": false, "document_id": false, "title": false, "content": false,
		"tenant_id": false, "allowed_tenants": false, "allowed_roles": false,
		"product": false, "version": false, "status": false, "visibility": false,
		"embedding": false, "embedding_model": false, "embedding_version": false,
	}
	for _, field := range description.Fields {
		if _, required := requiredFields[field.Name]; required {
			requiredFields[field.Name] = true
		}
		if field.Name != "embedding" {
			continue
		}
		var dimensions int
		for _, parameter := range field.Params {
			if parameter.Key == "dim" {
				_, _ = fmt.Sscan(parameter.Value, &dimensions)
			}
		}
		vector, err := service.embedder.EmbedQuery(ctx, "index release readiness probe")
		if err != nil {
			return err
		}
		if dimensions != len(vector) {
			return fmt.Errorf("index collection %q dimensions=%d current_model=%d", collection, dimensions, len(vector))
		}
		stats, err := service.client.CollectionStats(ctx, collection)
		if err != nil {
			return err
		}
		if int64(stats.RowCount) == 0 {
			return fmt.Errorf("index collection %q is empty", collection)
		}
		for _, index := range description.Indexes {
			if index.FieldName != "embedding" {
				continue
			}
			state := index.IndexState
			// collections/describe omits index state on some Milvus REST
			// versions; ask the index endpoint for the authoritative state.
			if state == "" && index.IndexName != "" {
				indexes, indexErr := service.client.DescribeIndex(ctx, collection, index.IndexName)
				if indexErr != nil {
					return indexErr
				}
				for _, detail := range indexes {
					if detail.FieldName == "embedding" || detail.IndexName == index.IndexName {
						state = detail.IndexState
						break
					}
				}
			}
			if state != "Finished" && state != "finished" {
				return fmt.Errorf("index collection %q is not ready: state=%s", collection, state)
			}
		}
		for field, present := range requiredFields {
			if !present {
				return fmt.Errorf("index collection %q is missing field %q", collection, field)
			}
		}
		return nil
	}
	return fmt.Errorf("index collection %q is missing embedding field", collection)
}

// PublishCollection switches the configured Milvus alias after validation.
// Gateway queries use the control-plane pointer, so rollback can be performed
// without exposing an unvalidated collection to application traffic.
func (service *LifecycleService) PublishCollection(ctx context.Context, collection string) error {
	if strings.TrimSpace(service.config.Alias) == "" {
		return fmt.Errorf("lifecycle alias is not configured")
	}
	if err := service.ValidateCollection(ctx, collection); err != nil {
		return err
	}
	return service.client.AlterAlias(ctx, collection, service.config.Alias)
}

func (service *LifecycleService) applyUpsert(ctx context.Context, change LifecycleChange, documentID string, observer LifecycleObserver) (LifecycleResult, error) {
	observeLifecycleStage(observer, LifecycleStageChunking)
	document := change.Document
	chunks := service.chunker.Chunk(domain.Document{
		ID: document.ID, Title: document.Title, Content: document.Content, Product: document.Product,
		Version: document.Version, Status: document.Status, Visibility: document.Visibility,
		AllowedTenants: document.AllowedTenants, AllowedRoles: document.AllowedRoles,
	})
	if len(chunks) == 0 {
		return LifecycleResult{}, fmt.Errorf("document %q produced no chunks", documentID)
	}
	texts := make([]string, len(chunks))
	for index, chunk := range chunks {
		texts[index] = chunk.DocumentTitle + "\n" + chunk.Content
	}
	observeLifecycleStage(observer, LifecycleStageEmbedding)
	vectors, err := service.embedder.EmbedDocuments(ctx, texts)
	if err != nil {
		return LifecycleResult{}, fmt.Errorf("embed lifecycle document: %w", err)
	}
	if len(vectors) != len(chunks) || len(vectors[0]) == 0 {
		return LifecycleResult{}, fmt.Errorf("embedder returned %d vectors for %d chunks", len(vectors), len(chunks))
	}
	dimensions := len(vectors[0])
	for index := range vectors {
		if len(vectors[index]) != dimensions {
			return LifecycleResult{}, fmt.Errorf("embedding dimension mismatch at chunk %d", index)
		}
	}
	observeLifecycleStage(observer, LifecycleStageIndexing)
	if err := service.ensureCollection(ctx, dimensions); err != nil {
		return LifecycleResult{}, err
	}
	filter := `document_id == "` + escapeFilter(documentID) + `"`
	previous, err := service.client.QueryEntities(ctx, service.config.Collection, filter, 16_384)
	if err != nil {
		return LifecycleResult{}, fmt.Errorf("query previous document chunks: %w", err)
	}
	newIDs := make(map[string]struct{}, len(chunks))
	records := make([]Record, len(chunks))
	indexedAt := service.config.Now().UTC().UnixMilli()
	for index, chunk := range chunks {
		newIDs[chunk.ID] = struct{}{}
		tenant := "public"
		if len(chunk.AllowedTenants) > 0 {
			tenant = chunk.AllowedTenants[0]
		}
		records[index] = Record{
			ChunkID: chunk.ID, DocumentID: documentID, Title: chunk.DocumentTitle, Content: chunk.Content,
			TenantID: tenant, AllowedTenants: append([]string(nil), chunk.AllowedTenants...),
			AllowedRoles: append([]string(nil), chunk.AllowedRoles...), Product: chunk.Product,
			Version: chunk.Version, Status: chunk.Status, Visibility: chunk.Visibility,
			ContentHash:    contentHash(chunk.DocumentTitle + "\n" + chunk.Content),
			EmbeddingModel: service.embedder.Name(), EmbeddingVer: service.config.EmbeddingVersion,
			DocumentVer: document.Version, SourceRevision: change.Revision, IndexedAt: indexedAt, Embedding: vectors[index],
		}
	}
	rows, err := service.client.Upsert(ctx, service.config.Collection, records)
	if err != nil {
		return LifecycleResult{}, fmt.Errorf("upsert lifecycle chunks: %w", err)
	}
	staleIDs := make([]string, 0)
	for _, entity := range previous {
		if _, ok := newIDs[entity.ChunkID]; !ok {
			staleIDs = append(staleIDs, entity.ChunkID)
		}
	}
	if len(staleIDs) > 0 {
		if err := service.client.DeleteByFilter(ctx, service.config.Collection, stringInFilter("chunk_id", staleIDs)); err != nil {
			return LifecycleResult{}, fmt.Errorf("delete stale lifecycle chunks: %w", err)
		}
	}
	if err := service.client.FlushCollection(ctx, service.config.Collection); err != nil {
		return LifecycleResult{}, fmt.Errorf("flush lifecycle collection: %w", err)
	}
	observeLifecycleStage(observer, LifecycleStageVerifying)
	current, err := service.client.QueryEntities(ctx, service.config.Collection, filter, 16_384)
	if err != nil {
		return LifecycleResult{}, fmt.Errorf("verify lifecycle upsert: %w", err)
	}
	if err := verifyLifecycleEntities(current, records, change.Revision, service.embedder.Name(), service.config.EmbeddingVersion); err != nil {
		return LifecycleResult{}, err
	}
	return LifecycleResult{
		PreviousChunks: len(previous), CurrentChunks: len(current), UpsertedChunks: rows, DeletedChunks: len(staleIDs),
	}, nil
}

func (service *LifecycleService) applyDelete(ctx context.Context, _ LifecycleChange, documentID string, observer LifecycleObserver) (LifecycleResult, error) {
	observeLifecycleStage(observer, LifecycleStageIndexing)
	if err := service.ensureExistingCollection(ctx); err != nil {
		return LifecycleResult{}, err
	}
	filter := `document_id == "` + escapeFilter(documentID) + `"`
	previous, err := service.client.QueryEntities(ctx, service.config.Collection, filter, 16_384)
	if err != nil {
		return LifecycleResult{}, fmt.Errorf("query document before delete: %w", err)
	}
	if err := service.client.DeleteByFilter(ctx, service.config.Collection, filter); err != nil {
		return LifecycleResult{}, fmt.Errorf("delete lifecycle document: %w", err)
	}
	if err := service.client.FlushCollection(ctx, service.config.Collection); err != nil {
		return LifecycleResult{}, fmt.Errorf("flush lifecycle delete: %w", err)
	}
	observeLifecycleStage(observer, LifecycleStageVerifying)
	current, err := service.client.QueryEntities(ctx, service.config.Collection, filter, 1)
	if err != nil {
		return LifecycleResult{}, fmt.Errorf("verify lifecycle delete: %w", err)
	}
	if len(current) != 0 {
		return LifecycleResult{}, fmt.Errorf("delete verification failed: %d chunks remain", len(current))
	}
	return LifecycleResult{PreviousChunks: len(previous), DeletedChunks: len(previous), CurrentChunks: 0}, nil
}

func observeLifecycleStage(observer LifecycleObserver, stage string) {
	if observer != nil {
		observer(stage)
	}
}

func (service *LifecycleService) ensureCollection(ctx context.Context, dimensions int) error {
	collections, err := service.client.ListCollections(ctx)
	if err != nil {
		return err
	}
	if !contains(collections, service.config.Collection) {
		if err := service.client.CreateCollection(ctx, service.config.Collection, dimensions); err != nil {
			return fmt.Errorf("create lifecycle collection: %w", err)
		}
		if service.config.Alias != "" {
			if err := service.ensureAlias(ctx); err != nil {
				return err
			}
		}
		return nil
	}
	description, err := service.client.DescribeCollection(ctx, service.config.Collection)
	if err != nil {
		return err
	}
	required := map[string]bool{
		"chunk_id": false, "document_id": false, "embedding": false, "content_hash": false,
		"embedding_model": false, "embedding_version": false, "document_version": false,
		"source_revision": false, "indexed_at": false,
	}
	actualDimensions := 0
	for _, field := range description.Fields {
		if _, ok := required[field.Name]; ok {
			required[field.Name] = true
		}
		if field.Name == "embedding" {
			for _, parameter := range field.Params {
				if parameter.Key == "dim" {
					_, _ = fmt.Sscan(parameter.Value, &actualDimensions)
				}
			}
		}
	}
	for field, present := range required {
		if !present {
			return fmt.Errorf("lifecycle collection schema is missing field %q", field)
		}
	}
	if actualDimensions != dimensions {
		return fmt.Errorf("embedding dimension mismatch: collection=%d current_model=%d", actualDimensions, dimensions)
	}
	sample, err := service.client.QueryEntities(ctx, service.config.Collection, "source_revision > 0", 1)
	if err != nil {
		return fmt.Errorf("check lifecycle embedding version: %w", err)
	}
	if len(sample) > 0 && (sample[0].EmbeddingModel != service.embedder.Name() || sample[0].EmbeddingVer != service.config.EmbeddingVersion) {
		return fmt.Errorf(
			"embedding version mismatch: collection=%s/%s current=%s/%s; build a new collection and switch its alias",
			sample[0].EmbeddingModel, sample[0].EmbeddingVer, service.embedder.Name(), service.config.EmbeddingVersion,
		)
	}
	if err := service.ensureAlias(ctx); err != nil {
		return err
	}
	return nil
}

func (service *LifecycleService) ensureAlias(ctx context.Context) error {
	if service.config.Alias == "" {
		return nil
	}
	description, err := service.client.DescribeAlias(ctx, service.config.Alias)
	if err != nil {
		if createErr := service.client.CreateAlias(ctx, service.config.Collection, service.config.Alias); createErr != nil {
			return fmt.Errorf("ensure lifecycle alias after describe failed (%v): %w", err, createErr)
		}
		return nil
	}
	if description.CollectionName != service.config.Collection {
		return fmt.Errorf(
			"lifecycle alias %q points to %q instead of writer collection %q; stop this writer or update its release configuration",
			service.config.Alias, description.CollectionName, service.config.Collection,
		)
	}
	return nil
}

func (service *LifecycleService) ensureExistingCollection(ctx context.Context) error {
	collections, err := service.client.ListCollections(ctx)
	if err != nil {
		return err
	}
	if !contains(collections, service.config.Collection) {
		return fmt.Errorf("lifecycle collection %q does not exist", service.config.Collection)
	}
	return nil
}

func (service *LifecycleService) highWatermark(documentID string) (int64, string) {
	var revision int64
	var eventID string
	if document, ok := service.state.Documents[documentID]; ok {
		revision, eventID = document.Revision, document.LastEventID
	}
	for candidateID, event := range service.state.Events {
		candidateDocumentID, _ := lifecycleDocumentID(event.Change)
		if candidateDocumentID == documentID && event.Change.Revision > revision {
			revision, eventID = event.Change.Revision, candidateID
		}
	}
	return revision, eventID
}

func (service *LifecycleService) loadState() error {
	data, err := os.ReadFile(service.config.StatePath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read lifecycle state: %w", err)
	}
	if err := json.Unmarshal(data, &service.state); err != nil {
		return fmt.Errorf("decode lifecycle state: %w", err)
	}
	if service.state.SchemaVersion != 1 {
		return fmt.Errorf("unsupported lifecycle state schema version %d", service.state.SchemaVersion)
	}
	if service.state.Events == nil {
		service.state.Events = make(map[string]persistedLifecycleEvent)
	}
	if service.state.Documents == nil {
		service.state.Documents = make(map[string]DocumentState)
	}
	sanitized := false
	for eventID, event := range service.state.Events {
		if event.Status != "completed" || event.Change.Document == nil {
			continue
		}
		documentID, documentErr := lifecycleDocumentID(event.Change)
		if documentErr != nil {
			return fmt.Errorf("sanitize completed lifecycle event %q: %w", eventID, documentErr)
		}
		event.Change = lifecycleEventReference(event.Change, documentID)
		service.state.Events[eventID] = event
		sanitized = true
	}
	if sanitized {
		if err := service.persistState(); err != nil {
			return fmt.Errorf("sanitize lifecycle state: %w", err)
		}
	}
	return nil
}

func (service *LifecycleService) persistState() error {
	data, err := json.MarshalIndent(service.state, "", "  ")
	if err != nil {
		return err
	}
	directory := filepath.Dir(service.config.StatePath)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	file, err := os.CreateTemp(directory, ".lifecycle-*.tmp")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
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

func validateLifecycleChange(change *LifecycleChange) (string, error) {
	change.EventID = strings.TrimSpace(change.EventID)
	change.Operation = strings.ToLower(strings.TrimSpace(change.Operation))
	if change.EventID == "" || len(change.EventID) > 128 {
		return "", fmt.Errorf("event_id is required and must not exceed 128 characters")
	}
	if change.Revision <= 0 {
		return "", fmt.Errorf("source_revision must be positive")
	}
	documentID, err := lifecycleDocumentID(*change)
	if err != nil {
		return "", err
	}
	if len(documentID) > 200 {
		return "", fmt.Errorf("document_id must not exceed 200 characters")
	}
	switch change.Operation {
	case OperationUpsert:
		if change.Document == nil || strings.TrimSpace(change.Document.Content) == "" || strings.TrimSpace(change.Document.Title) == "" {
			return "", fmt.Errorf("upsert requires document title and content")
		}
		change.Document.ID = documentID
		if change.Document.Status == "" {
			change.Document.Status = "active"
		}
		if change.Document.Visibility == "" {
			change.Document.Visibility = "public"
		}
	case OperationDelete:
		change.Document = nil
		change.DocumentID = documentID
	default:
		return "", fmt.Errorf("operation must be %q or %q", OperationUpsert, OperationDelete)
	}
	return documentID, nil
}

func lifecycleDocumentID(change LifecycleChange) (string, error) {
	documentID := strings.TrimSpace(change.DocumentID)
	if change.Document != nil {
		if documentID != "" && strings.TrimSpace(change.Document.ID) != "" && documentID != strings.TrimSpace(change.Document.ID) {
			return "", fmt.Errorf("document_id conflicts with document.document_id")
		}
		if documentID == "" {
			documentID = strings.TrimSpace(change.Document.ID)
		}
	}
	if documentID == "" {
		return "", fmt.Errorf("document_id is required")
	}
	return documentID, nil
}

func lifecycleChangeHash(change LifecycleChange) (string, error) {
	data, err := json.Marshal(change)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func lifecycleEventReference(change LifecycleChange, documentID string) LifecycleChange {
	return LifecycleChange{
		EventID: change.EventID, Operation: change.Operation, Revision: change.Revision, DocumentID: documentID,
	}
}

func contentHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func stringInFilter(field string, values []string) string {
	values = append([]string(nil), values...)
	sort.Strings(values)
	quoted := make([]string, len(values))
	for index, value := range values {
		quoted[index] = `"` + escapeFilter(value) + `"`
	}
	return field + " in [" + strings.Join(quoted, ", ") + "]"
}

func verifyLifecycleEntities(entities []Entity, records []Record, revision int64, model, version string) error {
	if len(entities) != len(records) {
		return fmt.Errorf("upsert verification failed: expected %d chunks, found %d", len(records), len(entities))
	}
	expected := make(map[string]Record, len(records))
	for _, record := range records {
		expected[record.ChunkID] = record
	}
	for _, entity := range entities {
		record, ok := expected[entity.ChunkID]
		if !ok {
			return fmt.Errorf("upsert verification found stale chunk %q", entity.ChunkID)
		}
		if entity.ContentHash != record.ContentHash || entity.SourceRevision != revision ||
			entity.EmbeddingModel != model || entity.EmbeddingVer != version {
			return fmt.Errorf("upsert verification found inconsistent metadata for chunk %q", entity.ChunkID)
		}
	}
	return nil
}
