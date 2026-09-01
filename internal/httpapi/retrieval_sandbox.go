package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/dingpuyu/rag-evolution-lab/internal/retrievallab"
)

type RetrievalSandboxAPI struct {
	service *retrievallab.Service
}

func (api *RetrievalSandboxAPI) run(writer http.ResponseWriter, request *http.Request) {
	identity := identityFromContext(request.Context())
	if !identity.HasRole("admin") && !identity.HasRole("platform_admin") {
		writeError(writer, http.StatusForbidden, "retrieval_sandbox_forbidden", "retrieval experiments require an administrator")
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, 2<<20)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var input retrievallab.RunInput
	if err := decoder.Decode(&input); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(writer, http.StatusRequestEntityTooLarge, "retrieval_sandbox_too_large", "request body must not exceed 2 MiB")
			return
		}
		writeError(writer, http.StatusBadRequest, "invalid_retrieval_sandbox", err.Error())
		return
	}
	if err := ensureEOF(decoder); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_retrieval_sandbox", err.Error())
		return
	}
	result, err := api.service.Run(request.Context(), retrievallab.Identity{
		TenantID: identity.TenantID, Role: identity.PrimaryRole(),
	}, input)
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "retrieval_sandbox_failed", fmt.Sprintf("isolated retrieval experiment failed: %v", err))
		return
	}
	writeJSON(writer, http.StatusOK, result)
}
