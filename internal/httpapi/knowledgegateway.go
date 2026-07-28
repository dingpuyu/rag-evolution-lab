package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/dingpuyu/rag-evolution-lab/internal/datasetaccess"
	"github.com/dingpuyu/rag-evolution-lab/internal/generation"
	"github.com/dingpuyu/rag-evolution-lab/internal/knowledgegateway"
	"github.com/dingpuyu/rag-evolution-lab/internal/querytrace"
)

type KnowledgeGatewayAPI struct {
	service *knowledgegateway.Service
	traces  querytrace.Store
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

func (api *KnowledgeGatewayAPI) answerStream(writer http.ResponseWriter, request *http.Request) {
	input, ok := decodeGatewayQuery(writer, request)
	if !ok {
		return
	}
	flusher, ok := writer.(http.Flusher)
	if !ok {
		writeError(writer, http.StatusNotImplemented, "sse_not_supported", "streaming responses are not supported by this server")
		return
	}
	writer.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-cache, no-transform")
	writer.Header().Set("Connection", "keep-alive")
	writer.Header().Set("X-Accel-Buffering", "no")
	writer.WriteHeader(http.StatusOK)
	identity := identityFromContext(request.Context())
	appID := request.PathValue("app_id")
	environmentID := input.EnvironmentID
	if strings.TrimSpace(environmentID) == "" {
		environmentID = appID + "-dev"
	}
	send := func(event generation.ProgressEvent) error {
		payload, err := json.Marshal(map[string]any{"app_id": appID, "environment_id": environmentID, "event": event})
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(writer, "event: %s\ndata: %s\n\n", event.Type, payload); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}
	result, err := api.service.AnswerWithProgress(request.Context(), identity, knowledgegateway.Request{
		AppID: appID, EnvironmentID: input.EnvironmentID, Query: input.Query, TopK: input.TopK,
	}, send)
	if err != nil {
		_ = send(generation.ProgressEvent{Type: "error", Error: err.Error()})
		return
	}
	payload, marshalErr := json.Marshal(map[string]any{"app_id": result.AppID, "environment_id": result.EnvironmentID, "result": result})
	if marshalErr == nil {
		_, _ = fmt.Fprintf(writer, "event: gateway_completed\ndata: %s\n\n", payload)
		flusher.Flush()
	}
}

func (api *KnowledgeGatewayAPI) trace(writer http.ResponseWriter, request *http.Request) {
	if api.traces == nil {
		writeError(writer, http.StatusNotFound, "trace_not_found", "query trace persistence is not configured")
		return
	}
	record, err := api.traces.GetQueryTrace(request.Context(), identityFromContext(request.Context()), request.PathValue("app_id"), request.PathValue("trace_id"))
	if err != nil {
		if errors.Is(err, querytrace.ErrNotFound) || errors.Is(err, querytrace.ErrDenied) {
			writeError(writer, http.StatusNotFound, "trace_not_found", "trace was not found or is not accessible")
			return
		}
		writeError(writer, http.StatusServiceUnavailable, "trace_store_unavailable", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, record)
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
	var limited *knowledgegateway.RateLimitError
	if errors.As(err, &limited) {
		seconds := int(limited.RetryAfter.Seconds())
		if seconds < 1 {
			seconds = 1
		}
		writer.Header().Set("Retry-After", fmt.Sprint(seconds))
		writeError(writer, http.StatusTooManyRequests, "rate_limit_exceeded", err.Error())
		return
	}
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
