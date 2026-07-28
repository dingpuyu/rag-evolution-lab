package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/dingpuyu/rag-evolution-lab/internal/auth"
	"github.com/dingpuyu/rag-evolution-lab/internal/datasetaccess"
	"github.com/dingpuyu/rag-evolution-lab/internal/milvus"
)

type IndexAPI struct {
	store   datasetaccess.IndexStore
	service *milvus.LifecycleService
}

func (api *IndexAPI) list(writer http.ResponseWriter, request *http.Request) {
	identity, ok := api.requireAdmin(writer, request)
	if !ok {
		return
	}
	releases, err := api.store.VisibleIndexReleases(request.Context(), identity, request.PathValue("app_id"), request.PathValue("environment_id"))
	if err != nil {
		writeIndexError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"releases": releases})
}

func (api *IndexAPI) publish(writer http.ResponseWriter, request *http.Request) {
	identity, ok := api.requireAdmin(writer, request)
	if !ok {
		return
	}
	input, ok := decodeIndexJSON(writer, request)
	if !ok {
		return
	}
	input.EnvironmentID = request.PathValue("environment_id")
	configuredAlias := api.service.ConfiguredAlias()
	if strings.TrimSpace(input.Alias) != "" && strings.TrimSpace(input.Alias) != configuredAlias {
		writeError(writer, http.StatusBadRequest, "invalid_index_alias", "alias must match the server-configured compatibility alias")
		return
	}
	input.Alias = configuredAlias
	if err := api.service.PublishCollection(request.Context(), input.Collection); err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "index_not_ready", err.Error())
		return
	}
	release, err := api.store.PublishIndexRelease(request.Context(), identity, request.PathValue("app_id"), input)
	if err != nil {
		writeIndexError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, release)
}

func (api *IndexAPI) rollback(writer http.ResponseWriter, request *http.Request) {
	identity, ok := api.requireAdmin(writer, request)
	if !ok {
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maxRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var input struct {
		ReleaseID string `json:"release_id"`
	}
	if err := decoder.Decode(&input); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if err := ensureEOF(decoder); err != nil || strings.TrimSpace(input.ReleaseID) == "" {
		writeError(writer, http.StatusBadRequest, "invalid_json", "release_id is required")
		return
	}
	releases, err := api.store.VisibleIndexReleases(request.Context(), identity, request.PathValue("app_id"), request.PathValue("environment_id"))
	if err != nil {
		writeIndexError(writer, err)
		return
	}
	var target *datasetaccess.IndexRelease
	for index := range releases {
		if releases[index].ReleaseID == strings.TrimSpace(input.ReleaseID) {
			target = &releases[index]
			break
		}
	}
	if target == nil {
		writeError(writer, http.StatusNotFound, "index_release_not_found", "index release was not found")
		return
	}
	if err := api.service.PublishCollection(request.Context(), target.Collection); err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "index_not_ready", err.Error())
		return
	}
	release, err := api.store.RollbackIndexRelease(request.Context(), identity, request.PathValue("app_id"), request.PathValue("environment_id"), input.ReleaseID)
	if err != nil {
		writeIndexError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, release)
}

func (api *IndexAPI) requireAdmin(writer http.ResponseWriter, request *http.Request) (auth.Identity, bool) {
	identity := identityFromContext(request.Context())
	if !identity.HasRole("admin") && !identity.HasRole("platform_admin") {
		writeError(writer, http.StatusForbidden, "index_forbidden", "index release management requires admin")
		return auth.Identity{}, false
	}
	if api.store == nil || api.service == nil {
		writeError(writer, http.StatusServiceUnavailable, "index_control_plane_unavailable", "index release service is not configured")
		return auth.Identity{}, false
	}
	return identity, true
}

func decodeIndexJSON(writer http.ResponseWriter, request *http.Request) (datasetaccess.PublishIndex, bool) {
	request.Body = http.MaxBytesReader(writer, request.Body, maxRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var input datasetaccess.PublishIndex
	if err := decoder.Decode(&input); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return datasetaccess.PublishIndex{}, false
	}
	if err := ensureEOF(decoder); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return datasetaccess.PublishIndex{}, false
	}
	return input, true
}

func writeIndexError(writer http.ResponseWriter, err error) {
	if errors.Is(err, datasetaccess.ErrDatasetNotFound) || errors.Is(err, datasetaccess.ErrDatasetDenied) {
		writeError(writer, http.StatusNotFound, "index_resource_not_found", "index resource was not found or is not accessible")
		return
	}
	writeError(writer, http.StatusUnprocessableEntity, "index_request_failed", err.Error())
}
