package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/dingpuyu/rag-evolution-lab/internal/auth"
	"github.com/dingpuyu/rag-evolution-lab/internal/datasetaccess"
	"github.com/dingpuyu/rag-evolution-lab/internal/documentparser"
	"github.com/dingpuyu/rag-evolution-lab/internal/documentstore"
	"github.com/dingpuyu/rag-evolution-lab/internal/generation"
	"github.com/dingpuyu/rag-evolution-lab/internal/ingestionjob"
	"github.com/dingpuyu/rag-evolution-lab/internal/milvus"
)

type DatasetAPI struct {
	store         datasetaccess.Store
	service       *milvus.LifecycleService
	answerService *generation.Service
	parser        *documentparser.Client
	documentStore documentstore.Store
	ingestionJobs *ingestionjob.Service
}

func (api *DatasetAPI) list(writer http.ResponseWriter, request *http.Request) {
	identity := identityFromContext(request.Context())
	datasets, err := api.store.Visible(request.Context(), identity)
	if err != nil {
		if errors.Is(err, datasetaccess.ErrDatasetDenied) {
			writeError(writer, http.StatusForbidden, "dataset_forbidden", "active tenant membership is required")
			return
		}
		writeError(writer, http.StatusServiceUnavailable, "control_plane_unavailable", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"identity": identity,
		"datasets": datasets,
	})
}

// documents returns a metadata-only inventory for one authorized dataset.
// The product/ACL filter is resolved from the server-side dataset policy; the
// caller cannot supply a Milvus filter or inspect another dataset's rows.
func (api *DatasetAPI) documents(writer http.ResponseWriter, request *http.Request) {
	dataset, identity, ok := api.authorizeDataset(writer, request)
	if !ok {
		return
	}
	catalog, err := api.service.CatalogForQuery(request.Context(), buildDatasetQuery(dataset, identity, "", 0))
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "dataset_catalog_unavailable", err.Error())
		return
	}
	uploads := []datasetaccess.KnowledgeDocumentRevision{}
	if registry, ok := api.store.(datasetaccess.DocumentRegistry); ok {
		uploads, err = registry.ListKnowledgeDocuments(request.Context(), dataset.ID)
		if err != nil {
			writeError(writer, http.StatusServiceUnavailable, "document_registry_unavailable", err.Error())
			return
		}
	}
	// The durable ingestion job table is the source of truth for live index
	// progress. Overlay it on document metadata without duplicating worker state.
	if api.ingestionJobs != nil {
		for index := range uploads {
			if uploads[index].JobID == "" {
				continue
			}
			job, jobErr := api.ingestionJobs.Get(uploads[index].JobID)
			if jobErr != nil {
				continue
			}
			uploads[index].IndexStatus = job.Status
			uploads[index].LastError = job.LastError
			if job.Result != nil {
				uploads[index].ChunkCount = job.Result.CurrentChunks
				uploads[index].IndexVersion = job.Result.EmbeddingVersion
			}
		}
	}
	writeJSON(writer, http.StatusOK, map[string]any{"dataset": dataset, "catalog": catalog, "uploads": uploads})
}

func (api *DatasetAPI) search(writer http.ResponseWriter, request *http.Request) {
	dataset, identity, ok := api.authorizeDataset(writer, request)
	if !ok {
		return
	}
	input, ok := decodeDatasetQuery(writer, request)
	if !ok {
		return
	}
	query := buildDatasetQuery(dataset, identity, input.Query, input.TopK)
	result, err := api.service.Search(request.Context(), query)
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "dataset_search_failed", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"dataset": dataset,
		"result":  result,
	})
}

func (api *DatasetAPI) answer(writer http.ResponseWriter, request *http.Request) {
	dataset, identity, ok := api.authorizeDataset(writer, request)
	if !ok {
		return
	}
	input, ok := decodeDatasetQuery(writer, request)
	if !ok {
		return
	}
	result, err := api.answerService.Answer(
		request.Context(), buildDatasetQuery(dataset, identity, input.Query, input.TopK),
	)
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "dataset_answer_failed", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"dataset": dataset,
		"result":  result,
	})
}

func (api *DatasetAPI) answerStream(writer http.ResponseWriter, request *http.Request) {
	dataset, identity, ok := api.authorizeDataset(writer, request)
	if !ok {
		return
	}
	input, ok := decodeDatasetQuery(writer, request)
	if !ok {
		return
	}
	flusher, ok := writer.(http.Flusher)
	if !ok {
		writeError(writer, http.StatusNotImplemented, "sse_not_supported", "streaming responses are not supported by this server")
		return
	}
	writer.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-cache, no-transform")
	writer.Header().Set("Connection", "keep-alive")
	writer.Header().Set("X-Accel-Buffering", "no")
	writer.WriteHeader(http.StatusOK)
	started := time.Now()
	send := func(event generation.ProgressEvent) error {
		payload, err := json.Marshal(map[string]any{
			"dataset": dataset,
			"event":   event,
		})
		if err != nil {
			return fmt.Errorf("encode SSE event: %w", err)
		}
		if _, err := fmt.Fprintf(writer, "event: %s\ndata: %s\n\n", event.Type, payload); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}
	_, err := api.answerService.AnswerWithProgress(
		request.Context(), buildDatasetQuery(dataset, identity, input.Query, input.TopK), send,
	)
	if err != nil {
		// The response headers are already committed. Report failures as an SSE
		// event so clients can distinguish a generation failure from a network
		// disconnect without parsing an HTML error page.
		_ = send(generation.ProgressEvent{Type: "error", Error: err.Error()})
		return
	}
	_ = send(generation.ProgressEvent{Type: "done", ElapsedMS: float64(time.Since(started).Microseconds()) / 1000})
}

func (api *DatasetAPI) authorizeDataset(writer http.ResponseWriter, request *http.Request) (datasetaccess.Dataset, auth.Identity, bool) {
	identity := identityFromContext(request.Context())
	dataset, err := api.store.Authorize(request.Context(), request.PathValue("dataset_id"), identity)
	if err == nil {
		return dataset, identity, true
	}
	// Return the same response for a missing and a forbidden dataset. This
	// prevents resource enumeration across tenants.
	if errors.Is(err, datasetaccess.ErrDatasetNotFound) || errors.Is(err, datasetaccess.ErrDatasetDenied) {
		writeError(writer, http.StatusNotFound, "dataset_not_found", "dataset was not found or is not accessible")
		return datasetaccess.Dataset{}, auth.Identity{}, false
	}
	writeError(writer, http.StatusForbidden, "dataset_forbidden", err.Error())
	return datasetaccess.Dataset{}, auth.Identity{}, false
}

type datasetQueryInput struct {
	Query string `json:"query"`
	TopK  int    `json:"top_k"`
}

func decodeDatasetQuery(writer http.ResponseWriter, request *http.Request) (datasetQueryInput, bool) {
	request.Body = http.MaxBytesReader(writer, request.Body, maxRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var input datasetQueryInput
	if err := decoder.Decode(&input); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return datasetQueryInput{}, false
	}
	if err := ensureEOF(decoder); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return datasetQueryInput{}, false
	}
	return input, true
}

func buildDatasetQuery(dataset datasetaccess.Dataset, identity auth.Identity, text string, topK int) milvus.Query {
	query := milvus.Query{Text: text, TopK: topK, DatasetID: dataset.ID, Product: dataset.Product, Status: "active"}
	if dataset.Visibility == "public" {
		query.Tenant = "public"
		query.Role = "viewer"
		query.AccessScope = "public_only"
	} else if identity.HasRole("platform_admin") {
		// Platform operators may inspect a tenant space, but the Milvus filter
		// still evaluates that space with its owner tenant and an allowed role.
		// This keeps the detail/search path bounded to the selected dataset.
		query.Tenant = dataset.OwnerTenant
		query.Role = "admin"
		if len(dataset.AllowedRoles) > 0 {
			query.Role = dataset.AllowedRoles[0]
		}
		query.AccessScope = "tenant_only"
	} else {
		query.Tenant = identity.TenantID
		query.Role = identity.PrimaryRole()
		query.AccessScope = "tenant_only"
	}
	return query
}

func (api *DatasetAPI) create(writer http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(writer, request.Body, maxRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var input datasetaccess.CreateDataset
	if err := decoder.Decode(&input); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if err := ensureEOF(decoder); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	dataset, err := api.store.Create(request.Context(), identityFromContext(request.Context()), input)
	if err != nil {
		if errors.Is(err, datasetaccess.ErrDatasetDenied) {
			writeError(writer, http.StatusForbidden, "dataset_forbidden", "active tenant admin membership is required")
			return
		}
		status := http.StatusUnprocessableEntity
		if strings.Contains(err.Error(), "duplicate key") {
			status = http.StatusConflict
		}
		writeError(writer, status, "dataset_create_failed", err.Error())
		return
	}
	writeJSON(writer, http.StatusCreated, dataset)
}

func (api *DatasetAPI) members(writer http.ResponseWriter, request *http.Request) {
	members, err := api.store.Members(request.Context(), identityFromContext(request.Context()))
	if err != nil {
		if errors.Is(err, datasetaccess.ErrDatasetDenied) {
			writeError(writer, http.StatusForbidden, "membership_forbidden", "tenant admin membership is required")
			return
		}
		writeError(writer, http.StatusServiceUnavailable, "control_plane_unavailable", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"members": members})
}

func (api *DatasetAPI) status(writer http.ResponseWriter, request *http.Request) {
	status, err := api.store.Status(request.Context(), identityFromContext(request.Context()))
	if err != nil {
		if errors.Is(err, datasetaccess.ErrDatasetDenied) {
			writeError(writer, http.StatusForbidden, "control_plane_forbidden", "active tenant membership is required")
			return
		}
		writeError(writer, http.StatusServiceUnavailable, "control_plane_unavailable", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, status)
}
