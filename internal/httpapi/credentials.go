package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/dingpuyu/rag-evolution-lab/internal/auth"
	"github.com/dingpuyu/rag-evolution-lab/internal/datasetaccess"
)

type CredentialAPI struct{ store datasetaccess.CredentialStore }

func (api *CredentialAPI) create(writer http.ResponseWriter, request *http.Request) {
	identity, ok := api.requireAdmin(writer, request)
	if !ok {
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maxRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var input struct {
		Name   string   `json:"name"`
		Scopes []string `json:"scopes"`
	}
	if err := decoder.Decode(&input); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	credential, secret, err := api.store.CreateApplicationCredential(request.Context(), identity, request.PathValue("app_id"), input.Name, input.Scopes, nil)
	if err != nil {
		writeCredentialError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, map[string]any{"credential": credential, "secret": secret, "authorization": "AppCredential " + secret})
}

func (api *CredentialAPI) list(writer http.ResponseWriter, request *http.Request) {
	identity, ok := api.requireAdmin(writer, request)
	if !ok {
		return
	}
	items, err := api.store.ListApplicationCredentials(request.Context(), identity, request.PathValue("app_id"))
	if err != nil {
		writeCredentialError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"credentials": items})
}

func (api *CredentialAPI) revoke(writer http.ResponseWriter, request *http.Request) {
	identity, ok := api.requireAdmin(writer, request)
	if !ok {
		return
	}
	if err := api.store.RevokeApplicationCredential(request.Context(), identity, request.PathValue("app_id"), request.PathValue("credential_id")); err != nil {
		writeCredentialError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"status": "revoked"})
}

func (api *CredentialAPI) requireAdmin(writer http.ResponseWriter, request *http.Request) (auth.Identity, bool) {
	identity := identityFromContext(request.Context())
	if !identity.HasRole("admin") && !identity.HasRole("platform_admin") {
		writeError(writer, http.StatusForbidden, "credential_forbidden", "credential management requires admin")
		return auth.Identity{}, false
	}
	if api == nil || api.store == nil {
		writeError(writer, http.StatusServiceUnavailable, "credential_store_unavailable", "credential store is not configured")
		return auth.Identity{}, false
	}
	return identity, true
}

func writeCredentialError(writer http.ResponseWriter, err error) {
	if strings.Contains(err.Error(), "not found") || err == datasetaccess.ErrDatasetDenied {
		writeError(writer, http.StatusNotFound, "credential_resource_not_found", "credential resource was not found or is not accessible")
		return
	}
	writeError(writer, http.StatusUnprocessableEntity, "credential_request_failed", err.Error())
}
