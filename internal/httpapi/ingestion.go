package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/dingpuyu/rag-evolution-lab/internal/ingestionjob"
)

type IngestionAPI struct {
	service *ingestionjob.Service
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
