package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/dingpuyu/rag-evolution-lab/internal/milvus"
)

const maxLifecycleRequestBytes = 1 << 20

type LifecycleAPI struct {
	service *milvus.LifecycleService
}

func (api *LifecycleAPI) status(writer http.ResponseWriter, request *http.Request) {
	if !identityFromContext(request.Context()).HasRole("platform_admin") {
		writeError(writer, http.StatusForbidden, "forbidden", "lifecycle status requires platform_admin")
		return
	}
	writeJSON(writer, http.StatusOK, api.service.Status())
}

func (api *LifecycleAPI) apply(writer http.ResponseWriter, request *http.Request) {
	if !identityFromContext(request.Context()).HasRole("platform_admin") {
		writeError(writer, http.StatusForbidden, "forbidden", "knowledge lifecycle mutation requires platform_admin")
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maxLifecycleRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var change milvus.LifecycleChange
	if err := decoder.Decode(&change); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if err := ensureEOF(decoder); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	result, err := api.service.Apply(request.Context(), change)
	if err != nil {
		status := http.StatusUnprocessableEntity
		code := "lifecycle_change_failed"
		if strings.Contains(err.Error(), "stale or conflicting revision") || strings.Contains(err.Error(), "already used") {
			status = http.StatusConflict
			code = "lifecycle_conflict"
		}
		writeError(writer, status, code, err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func (api *LifecycleAPI) search(writer http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(writer, request.Body, maxRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var input milvus.Query
	if err := decoder.Decode(&input); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if err := ensureEOF(decoder); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	identity := identityFromContext(request.Context())
	input.Tenant = identity.TenantID
	input.Role = identity.PrimaryRole()
	result, err := api.service.Search(request.Context(), input)
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "lifecycle_search_failed", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, result)
}
