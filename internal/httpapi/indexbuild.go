package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/dingpuyu/rag-evolution-lab/internal/auth"
	"github.com/dingpuyu/rag-evolution-lab/internal/indexbuild"
)

type IndexBuildAPI struct{ service *indexbuild.Service }

func (api *IndexBuildAPI) submit(writer http.ResponseWriter, request *http.Request) {
	identity, ok := api.requireAdmin(writer, request)
	if !ok {
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maxRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var input indexbuild.Request
	if err := decoder.Decode(&input); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if err := ensureEOF(decoder); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	input.ApplicationID = request.PathValue("app_id")
	input.EnvironmentID = request.PathValue("environment_id")
	build, existing, err := api.service.Submit(request.Context(), identity, input)
	if err != nil {
		writeBuildError(writer, err)
		return
	}
	status := http.StatusAccepted
	if existing {
		status = http.StatusOK
	}
	writeJSON(writer, status, map[string]any{"build": build, "idempotent": existing})
}

func (api *IndexBuildAPI) list(writer http.ResponseWriter, request *http.Request) {
	identity, ok := api.requireAdmin(writer, request)
	if !ok {
		return
	}
	builds, err := api.service.List(request.Context(), identity, request.PathValue("app_id"), request.PathValue("environment_id"))
	if err != nil {
		writeBuildError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"builds": builds})
}

func (api *IndexBuildAPI) detail(writer http.ResponseWriter, request *http.Request) {
	identity, ok := api.requireAdmin(writer, request)
	if !ok {
		return
	}
	build, err := api.service.Get(request.Context(), identity, request.PathValue("app_id"), request.PathValue("build_id"))
	if err != nil {
		writeBuildError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, build)
}

func (api *IndexBuildAPI) requireAdmin(writer http.ResponseWriter, request *http.Request) (auth.Identity, bool) {
	identity := identityFromContext(request.Context())
	if !identity.HasRole("admin") && !identity.HasRole("platform_admin") {
		writeError(writer, http.StatusForbidden, "index_build_forbidden", "index build management requires admin")
		return auth.Identity{}, false
	}
	if api == nil || api.service == nil {
		writeError(writer, http.StatusServiceUnavailable, "index_build_unavailable", "index build service is not configured")
		return auth.Identity{}, false
	}
	return identity, true
}

func writeBuildError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, indexbuild.ErrBuildNotFound):
		writeError(writer, http.StatusNotFound, "index_build_not_found", "index build was not found")
	case errors.Is(err, indexbuild.ErrBuildConflict):
		writeError(writer, http.StatusConflict, "index_build_conflict", "idempotency key was already used with a different build request")
	case strings.Contains(err.Error(), "required"):
		writeError(writer, http.StatusBadRequest, "index_build_invalid", err.Error())
	default:
		writeError(writer, http.StatusUnprocessableEntity, "index_build_failed", err.Error())
	}
}
