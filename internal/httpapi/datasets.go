package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/dingpuyu/rag-evolution-lab/internal/datasetaccess"
	"github.com/dingpuyu/rag-evolution-lab/internal/milvus"
)

type DatasetAPI struct {
	store   datasetaccess.Store
	service *milvus.LifecycleService
}

func (api *DatasetAPI) list(writer http.ResponseWriter, request *http.Request) {
	identity := identityFromContext(request.Context())
	datasets, err := api.store.Visible(request.Context(), identity)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "control_plane_unavailable", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"identity": identity,
		"datasets": datasets,
	})
}

func (api *DatasetAPI) search(writer http.ResponseWriter, request *http.Request) {
	identity := identityFromContext(request.Context())
	dataset, err := api.store.Authorize(request.Context(), request.PathValue("dataset_id"), identity)
	if err != nil {
		// Return the same response for a missing and a forbidden dataset. This
		// prevents resource enumeration across tenants.
		if errors.Is(err, datasetaccess.ErrDatasetNotFound) || errors.Is(err, datasetaccess.ErrDatasetDenied) {
			writeError(writer, http.StatusNotFound, "dataset_not_found", "dataset was not found or is not accessible")
			return
		}
		writeError(writer, http.StatusForbidden, "dataset_forbidden", err.Error())
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maxRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var input struct {
		Query string `json:"query"`
		TopK  int    `json:"top_k"`
	}
	if err := decoder.Decode(&input); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if err := ensureEOF(decoder); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	query := milvus.Query{
		Text: input.Query, TopK: input.TopK, Product: dataset.Product, Status: "active",
	}
	if dataset.Visibility == "public" {
		query.Tenant = "public"
		query.Role = "viewer"
		query.AccessScope = "public_only"
	} else {
		query.Tenant = identity.TenantID
		query.Role = identity.PrimaryRole()
		query.AccessScope = "tenant_only"
	}
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
		writeError(writer, http.StatusServiceUnavailable, "control_plane_unavailable", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, status)
}
