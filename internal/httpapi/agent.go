package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/dingpuyu/rag-evolution-lab/internal/agent"
)

type AgentAPI struct {
	service *agent.Service
}

type agentAnswerInput struct {
	EnvironmentID string `json:"environment_id,omitempty"`
	Query         string `json:"query"`
}

type agentAnswerResponse struct {
	AppID         string         `json:"app_id"`
	EnvironmentID string         `json:"environment_id"`
	Result        agent.Response `json:"result"`
}

func (api *AgentAPI) answer(writer http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(writer, request.Body, maxRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var input agentAnswerInput
	if err := decoder.Decode(&input); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if err := ensureEOF(decoder); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if strings.TrimSpace(input.Query) == "" {
		writeError(writer, http.StatusBadRequest, "query_required", "query must not be empty")
		return
	}
	appID := request.PathValue("app_id")
	environmentID := strings.TrimSpace(input.EnvironmentID)
	if environmentID == "" {
		environmentID = appID + "-dev"
	}
	result, err := api.service.Run(request.Context(), agent.ToolContext{
		Identity: identityFromContext(request.Context()), ApplicationID: appID, EnvironmentID: environmentID,
	}, input.Query)
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "agent_failed", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, agentAnswerResponse{AppID: appID, EnvironmentID: environmentID, Result: result})
}
