package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/dingpuyu/rag-evolution-lab/internal/datasetaccess"
	"github.com/dingpuyu/rag-evolution-lab/internal/milvus"
)

type DatasetAPI struct {
	catalog *datasetaccess.Catalog
	service *milvus.LifecycleService
}

func (api *DatasetAPI) list(writer http.ResponseWriter, request *http.Request) {
	identity := identityFromContext(request.Context())
	writeJSON(writer, http.StatusOK, map[string]any{
		"identity": identity,
		"datasets": api.catalog.Visible(identity),
	})
}

func (api *DatasetAPI) search(writer http.ResponseWriter, request *http.Request) {
	identity := identityFromContext(request.Context())
	dataset, err := api.catalog.Authorize(request.PathValue("dataset_id"), identity)
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
