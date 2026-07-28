package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/dingpuyu/rag-evolution-lab/internal/auth"
	"github.com/dingpuyu/rag-evolution-lab/internal/datasetaccess"
)

type ApplicationAPI struct {
	store        datasetaccess.ApplicationStore
	datasetStore datasetaccess.Store
}

func (api *ApplicationAPI) list(writer http.ResponseWriter, request *http.Request) {
	identity, ok := api.requireAdmin(writer, request)
	if !ok {
		return
	}
	applications, err := api.store.VisibleApplications(request.Context(), identity)
	if err != nil {
		writeApplicationError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"applications": applications})
}

func (api *ApplicationAPI) create(writer http.ResponseWriter, request *http.Request) {
	identity, ok := api.requireAdmin(writer, request)
	if !ok {
		return
	}
	var input datasetaccess.CreateApplication
	if !decodeApplicationJSON(writer, request, &input) {
		return
	}
	application, err := api.store.CreateApplication(request.Context(), identity, input)
	if err != nil {
		writeApplicationError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, application)
}

func (api *ApplicationAPI) environments(writer http.ResponseWriter, request *http.Request) {
	identity, ok := api.requireAdmin(writer, request)
	if !ok {
		return
	}
	environments, err := api.store.Environments(request.Context(), identity, request.PathValue("app_id"))
	if err != nil {
		writeApplicationError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"environments": environments})
}

func (api *ApplicationAPI) createEnvironment(writer http.ResponseWriter, request *http.Request) {
	identity, ok := api.requireAdmin(writer, request)
	if !ok {
		return
	}
	var input datasetaccess.CreateEnvironment
	if !decodeApplicationJSON(writer, request, &input) {
		return
	}
	environment, err := api.store.CreateEnvironment(request.Context(), identity, request.PathValue("app_id"), input)
	if err != nil {
		writeApplicationError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, environment)
}

func (api *ApplicationAPI) bindings(writer http.ResponseWriter, request *http.Request) {
	identity, ok := api.requireAdmin(writer, request)
	if !ok {
		return
	}
	bindings, err := api.store.Bindings(request.Context(), identity, request.PathValue("app_id"), request.URL.Query().Get("environment_id"))
	if err != nil {
		writeApplicationError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"bindings": bindings})
}

func (api *ApplicationAPI) createBinding(writer http.ResponseWriter, request *http.Request) {
	identity, ok := api.requireAdmin(writer, request)
	if !ok {
		return
	}
	var input datasetaccess.CreateBinding
	if !decodeApplicationJSON(writer, request, &input) {
		return
	}
	if api.datasetStore == nil {
		writeError(writer, http.StatusServiceUnavailable, "control_plane_unavailable", "dataset control plane is not configured")
		return
	}
	if _, err := api.datasetStore.Authorize(request.Context(), input.DatasetID, identity); err != nil {
		writeApplicationError(writer, err)
		return
	}
	binding, err := api.store.CreateBinding(request.Context(), identity, request.PathValue("app_id"), input)
	if err != nil {
		writeApplicationError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, binding)
}

func (api *ApplicationAPI) requireAdmin(writer http.ResponseWriter, request *http.Request) (auth.Identity, bool) {
	identity := identityFromContext(request.Context())
	if !identity.HasRole("admin") && !identity.HasRole("platform_admin") {
		writeError(writer, http.StatusForbidden, "application_forbidden", "application control plane requires admin")
		return auth.Identity{}, false
	}
	if api.store == nil {
		writeError(writer, http.StatusServiceUnavailable, "control_plane_unavailable", "application control plane is not configured")
		return auth.Identity{}, false
	}
	return identity, true
}

func decodeApplicationJSON(writer http.ResponseWriter, request *http.Request, destination any) bool {
	request.Body = http.MaxBytesReader(writer, request.Body, maxRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return false
	}
	if err := ensureEOF(decoder); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return false
	}
	return true
}

func writeApplicationError(writer http.ResponseWriter, err error) {
	if errors.Is(err, datasetaccess.ErrDatasetNotFound) || errors.Is(err, datasetaccess.ErrDatasetDenied) {
		writeError(writer, http.StatusNotFound, "application_resource_not_found", "application resource was not found or is not accessible")
		return
	}
	writeError(writer, http.StatusUnprocessableEntity, "application_request_failed", err.Error())
}
