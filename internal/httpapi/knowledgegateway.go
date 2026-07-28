package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/dingpuyu/rag-evolution-lab/internal/datasetaccess"
	"github.com/dingpuyu/rag-evolution-lab/internal/knowledgegateway"
)

type KnowledgeGatewayAPI struct {
	service *knowledgegateway.Service
}

type gatewayQueryInput struct {
	EnvironmentID string `json:"environment_id,omitempty"`
	Query         string `json:"query"`
	TopK          int    `json:"top_k,omitempty"`
}

func (api *KnowledgeGatewayAPI) query(writer http.ResponseWriter, request *http.Request) {
	input, ok := decodeGatewayQuery(writer, request)
	if !ok {
		return
	}
	identity := identityFromContext(request.Context())
	result, err := api.service.Search(request.Context(), identity, knowledgegateway.Request{
		AppID: request.PathValue("app_id"), EnvironmentID: input.EnvironmentID, Query: input.Query, TopK: input.TopK,
	})
	if err != nil {
		writeGatewayError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func (api *KnowledgeGatewayAPI) answer(writer http.ResponseWriter, request *http.Request) {
	input, ok := decodeGatewayQuery(writer, request)
	if !ok {
		return
	}
	identity := identityFromContext(request.Context())
	result, err := api.service.Answer(request.Context(), identity, knowledgegateway.Request{
		AppID: request.PathValue("app_id"), EnvironmentID: input.EnvironmentID, Query: input.Query, TopK: input.TopK,
	})
	if err != nil {
		writeGatewayError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func decodeGatewayQuery(writer http.ResponseWriter, request *http.Request) (gatewayQueryInput, bool) {
	request.Body = http.MaxBytesReader(writer, request.Body, maxRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var input gatewayQueryInput
	if err := decoder.Decode(&input); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return gatewayQueryInput{}, false
	}
	if err := ensureEOF(decoder); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return gatewayQueryInput{}, false
	}
	if strings.TrimSpace(input.Query) == "" {
		writeError(writer, http.StatusBadRequest, "query_required", "query must not be empty")
		return gatewayQueryInput{}, false
	}
	return input, true
}

func writeGatewayError(writer http.ResponseWriter, err error) {
	if errors.Is(err, datasetaccess.ErrDatasetNotFound) || errors.Is(err, datasetaccess.ErrDatasetDenied) {
		writeError(writer, http.StatusNotFound, "application_not_found", "application was not found or is not accessible")
		return
	}
	message := err.Error()
	status, code := http.StatusUnprocessableEntity, "knowledge_gateway_failed"
	if strings.Contains(message, "no active knowledge bindings") {
		status, code = http.StatusConflict, "knowledge_bindings_required"
	}
	writeError(writer, status, code, message)
}
