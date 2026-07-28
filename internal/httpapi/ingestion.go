package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/dingpuyu/rag-evolution-lab/internal/auth"
	"github.com/dingpuyu/rag-evolution-lab/internal/datasetaccess"
	"github.com/dingpuyu/rag-evolution-lab/internal/ingestionjob"
	"github.com/dingpuyu/rag-evolution-lab/internal/milvus"
)

type IngestionAPI struct {
	service      *ingestionjob.Service
	datasetStore datasetaccess.Store
}

func (api *IngestionAPI) list(writer http.ResponseWriter, request *http.Request) {
	if !requirePlatformAdmin(writer, request, "ingestion jobs require platform_admin") {
		return
	}
	writeJSON(writer, http.StatusOK, api.service.List())
}

func (api *IngestionAPI) detail(writer http.ResponseWriter, request *http.Request) {
	if !requirePlatformAdmin(writer, request, "ingestion job details require platform_admin") {
		return
	}
	job, err := api.service.Get(request.PathValue("job_id"))
	if err != nil {
		writeIngestionError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, job)
}

func (api *IngestionAPI) submit(writer http.ResponseWriter, request *http.Request) {
	if !requirePlatformAdmin(writer, request, "ingestion submission requires platform_admin") {
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maxLifecycleRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var input ingestionjob.SubmitRequest
	if err := decoder.Decode(&input); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if err := ensureEOF(decoder); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	job, duplicate, err := api.service.Submit(input)
	if err != nil {
		writeIngestionError(writer, err)
		return
	}
	status := http.StatusAccepted
	if duplicate {
		status = http.StatusOK
	}
	writeJSON(writer, status, map[string]any{"duplicate": duplicate, "job": job})
}

func (api *IngestionAPI) retry(writer http.ResponseWriter, request *http.Request) {
	if !requirePlatformAdmin(writer, request, "ingestion retry requires platform_admin") {
		return
	}
	job, err := api.service.Retry(request.PathValue("job_id"))
	if err != nil {
		writeIngestionError(writer, err)
		return
	}
	writeJSON(writer, http.StatusAccepted, job)
}

func (api *IngestionAPI) cancel(writer http.ResponseWriter, request *http.Request) {
	if !requirePlatformAdmin(writer, request, "ingestion cancellation requires platform_admin") {
		return
	}
	job, err := api.service.Cancel(request.PathValue("job_id"))
	if err != nil {
		writeIngestionError(writer, err)
		return
	}
	writeJSON(writer, http.StatusAccepted, job)
}

func (api *IngestionAPI) listDataset(writer http.ResponseWriter, request *http.Request) {
	identity, ok := api.authorizeDatasetAdmin(writer, request)
	if !ok {
		return
	}
	tenantID := identity.TenantID
	if identity.HasRole("platform_admin") {
		tenantID = ""
	}
	writeJSON(writer, http.StatusOK, api.service.ListFor(tenantID, request.PathValue("dataset_id")))
}

func (api *IngestionAPI) detailDataset(writer http.ResponseWriter, request *http.Request) {
	identity, ok := api.authorizeDatasetAdmin(writer, request)
	if !ok {
		return
	}
	job, err := api.service.Get(request.PathValue("job_id"))
	if err != nil {
		writeIngestionError(writer, err)
		return
	}
	if !jobVisibleToDataset(job, identity, request.PathValue("dataset_id")) {
		writeError(writer, http.StatusNotFound, "ingestion_job_not_found", "ingestion job not found")
		return
	}
	writeJSON(writer, http.StatusOK, job)
}

func (api *IngestionAPI) submitDataset(writer http.ResponseWriter, request *http.Request) {
	identity, ok := api.authorizeDatasetAdmin(writer, request)
	if !ok {
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maxLifecycleRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var input ingestionjob.SubmitRequest
	if err := decoder.Decode(&input); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if err := ensureEOF(decoder); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	datasetID := request.PathValue("dataset_id")
	dataset, err := api.datasetStore.Authorize(request.Context(), datasetID, identity)
	if err != nil {
		writeDatasetIngestionError(writer, err)
		return
	}
	input.TenantID = identity.TenantID
	input.DatasetID = dataset.ID
	input.CreatedBy = identity.Subject
	if input.Change.Operation == milvus.OperationUpsert {
		if input.Change.Document == nil {
			writeError(writer, http.StatusBadRequest, "invalid_json", "upsert requires document")
			return
		}
		input.Change.Document.Product = dataset.Product
		input.Change.Document.Visibility = dataset.Visibility
		input.Change.Document.AllowedRoles = append([]string(nil), dataset.AllowedRoles...)
		if dataset.Visibility == "tenant" {
			input.Change.Document.AllowedTenants = []string{dataset.OwnerTenant}
		} else {
			input.Change.Document.AllowedTenants = nil
		}
	}
	job, duplicate, err := api.service.Submit(input)
	if err != nil {
		writeIngestionError(writer, err)
		return
	}
	status := http.StatusAccepted
	if duplicate {
		status = http.StatusOK
	}
	writeJSON(writer, status, map[string]any{"duplicate": duplicate, "job": job})
}

func (api *IngestionAPI) retryDataset(writer http.ResponseWriter, request *http.Request) {
	identity, ok := api.authorizeDatasetAdmin(writer, request)
	if !ok {
		return
	}
	job, err := api.service.Get(request.PathValue("job_id"))
	if err != nil {
		writeIngestionError(writer, err)
		return
	}
	if !jobVisibleToDataset(job, identity, request.PathValue("dataset_id")) {
		writeError(writer, http.StatusNotFound, "ingestion_job_not_found", "ingestion job not found")
		return
	}
	job, err = api.service.Retry(job.ID)
	if err != nil {
		writeIngestionError(writer, err)
		return
	}
	writeJSON(writer, http.StatusAccepted, job)
}

func (api *IngestionAPI) cancelDataset(writer http.ResponseWriter, request *http.Request) {
	identity, ok := api.authorizeDatasetAdmin(writer, request)
	if !ok {
		return
	}
	job, err := api.service.Get(request.PathValue("job_id"))
	if err != nil {
		writeIngestionError(writer, err)
		return
	}
	if !jobVisibleToDataset(job, identity, request.PathValue("dataset_id")) {
		writeError(writer, http.StatusNotFound, "ingestion_job_not_found", "ingestion job not found")
		return
	}
	job, err = api.service.Cancel(job.ID)
	if err != nil {
		writeIngestionError(writer, err)
		return
	}
	writeJSON(writer, http.StatusAccepted, job)
}

func (api *IngestionAPI) authorizeDatasetAdmin(writer http.ResponseWriter, request *http.Request) (auth.Identity, bool) {
	identity := identityFromContext(request.Context())
	if !identity.HasRole("admin") && !identity.HasRole("platform_admin") {
		writeError(writer, http.StatusForbidden, "forbidden", "dataset ingestion requires admin")
		return auth.Identity{}, false
	}
	if api.datasetStore == nil {
		writeError(writer, http.StatusServiceUnavailable, "control_plane_unavailable", "dataset control plane is not configured")
		return auth.Identity{}, false
	}
	if _, err := api.datasetStore.Authorize(request.Context(), request.PathValue("dataset_id"), identity); err != nil {
		writeDatasetIngestionError(writer, err)
		return auth.Identity{}, false
	}
	return identity, true
}

func jobVisibleToDataset(job ingestionjob.Job, identity auth.Identity, datasetID string) bool {
	if job.DatasetID != datasetID {
		return false
	}
	return identity.HasRole("platform_admin") || job.TenantID == identity.TenantID
}

func writeDatasetIngestionError(writer http.ResponseWriter, err error) {
	if errors.Is(err, datasetaccess.ErrDatasetNotFound) || errors.Is(err, datasetaccess.ErrDatasetDenied) {
		writeError(writer, http.StatusNotFound, "dataset_not_found", "dataset not found")
		return
	}
	writeError(writer, http.StatusUnprocessableEntity, "dataset_ingestion_failed", err.Error())
}

func requirePlatformAdmin(writer http.ResponseWriter, request *http.Request, message string) bool {
	if !identityFromContext(request.Context()).HasRole("platform_admin") {
		writeError(writer, http.StatusForbidden, "forbidden", message)
		return false
	}
	return true
}

func writeIngestionError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ingestionjob.ErrJobNotFound):
		writeError(writer, http.StatusNotFound, "ingestion_job_not_found", err.Error())
	case errors.Is(err, ingestionjob.ErrIdempotencyConflict):
		writeError(writer, http.StatusConflict, "ingestion_idempotency_conflict", err.Error())
	case errors.Is(err, ingestionjob.ErrJobNotRetryable), errors.Is(err, ingestionjob.ErrJobNotCancellable):
		writeError(writer, http.StatusConflict, "ingestion_state_conflict", err.Error())
	default:
		writeError(writer, http.StatusUnprocessableEntity, "ingestion_job_failed", err.Error())
	}
}
