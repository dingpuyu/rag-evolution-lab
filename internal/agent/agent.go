// Package agent contains the bounded business Agent runtime. It deliberately
// keeps planning, tool execution and the knowledge gateway separate so a
// LangChain/Spring AI adapter can call the same contract without bypassing
// server-side authorization.
package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/dingpuyu/rag-evolution-lab/internal/auth"
)

const (
	ActionTool         = "tool"
	ActionFinal        = "final"
	ActionClarify      = "clarify"
	ActionConfirmation = "confirmation"
	StatusCompleted    = "completed"
	StatusClarify      = "needs_clarification"
	StatusConfirmation = "needs_confirmation"
)

type ToolSpec struct {
	Name                 string `json:"name"`
	Description          string `json:"description"`
	ReadOnly             bool   `json:"read_only"`
	RequiresConfirmation bool   `json:"requires_confirmation"`
}

type Tool interface {
	Spec() ToolSpec
	Execute(context.Context, ToolContext, map[string]any) (ToolResult, error)
}

type ToolContext struct {
	Identity      auth.Identity
	ApplicationID string
	EnvironmentID string
}

type Action struct {
	Type      string         `json:"type"`
	Tool      string         `json:"tool,omitempty"`
	Arguments map[string]any `json:"arguments,omitempty"`
	Message   string         `json:"message,omitempty"`
	Reason    string         `json:"reason,omitempty"`
}

type Observation struct {
	Tool    string `json:"tool"`
	Status  string `json:"status"`
	Summary string `json:"summary,omitempty"`
	Data    any    `json:"data,omitempty"`
}

type PlanInput struct {
	Query        string
	Step         int
	Tools        []ToolSpec
	Observations []Observation
}

type Planner interface {
	Plan(context.Context, PlanInput) (Action, error)
}

type Citation struct {
	ChunkID    string `json:"chunk_id"`
	DocumentID string `json:"document_id"`
	Document   string `json:"document"`
	Excerpt    string `json:"excerpt"`
}

type ToolResult struct {
	Tool                 string
	Status               string
	Summary              string
	Data                 any
	Answer               string
	AnswerSource         string
	Citations            []Citation
	RequiresConfirmation bool
	Terminal             bool
}

type Step struct {
	Step        int          `json:"step"`
	Action      Action       `json:"action"`
	Observation *Observation `json:"observation,omitempty"`
}

type Response struct {
	Status            string     `json:"status"`
	Answer            string     `json:"answer"`
	AnswerSource      string     `json:"answer_source,omitempty"`
	Citations         []Citation `json:"citations,omitempty"`
	NeedsConfirmation bool       `json:"needs_confirmation,omitempty"`
	Steps             []Step     `json:"steps"`
	ToolCalls         []string   `json:"tool_calls,omitempty"`
	PlannerModel      string     `json:"planner_model,omitempty"`
	PlannerLatencyMS  float64    `json:"planner_latency_ms,omitempty"`
}

type Service struct {
	planner Planner
	tools   map[string]Tool
	maxStep int
}

type Config struct {
	Planner Planner
	Tools   []Tool
	MaxStep int
}

func NewService(config Config) (*Service, error) {
	if config.Planner == nil {
		return nil, fmt.Errorf("agent planner is required")
	}
	maxStep := config.MaxStep
	if maxStep <= 0 {
		maxStep = 4
	}
	tools := make(map[string]Tool, len(config.Tools))
	for _, tool := range config.Tools {
		if tool == nil {
			return nil, fmt.Errorf("agent tool must not be nil")
		}
		spec := tool.Spec()
		name := strings.TrimSpace(spec.Name)
		if name == "" {
			return nil, fmt.Errorf("agent tool name must not be empty")
		}
		if _, exists := tools[name]; exists {
			return nil, fmt.Errorf("duplicate agent tool %q", name)
		}
		tools[name] = tool
	}
	return &Service{planner: config.Planner, tools: tools, maxStep: maxStep}, nil
}

func (service *Service) ToolSpecs() []ToolSpec {
	result := make([]ToolSpec, 0, len(service.tools))
	for _, tool := range service.tools {
		result = append(result, tool.Spec())
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

func (service *Service) Run(ctx context.Context, toolContext ToolContext, query string) (Response, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return Response{}, fmt.Errorf("agent query must not be empty")
	}
	observations := make([]Observation, 0, service.maxStep)
	steps := make([]Step, 0, service.maxStep)
	toolCalls := make([]string, 0, service.maxStep)
	for step := 1; step <= service.maxStep; step++ {
		action, err := service.planner.Plan(ctx, PlanInput{
			Query: query, Step: step, Tools: service.ToolSpecs(), Observations: append([]Observation(nil), observations...),
		})
		if err != nil {
			return Response{}, fmt.Errorf("agent plan step %d: %w", step, err)
		}
		action.Type = strings.ToLower(strings.TrimSpace(action.Type))
		action.Tool = strings.TrimSpace(action.Tool)
		if action.Type == "" {
			return Response{}, fmt.Errorf("agent plan step %d returned an empty action type", step)
		}
		stepRecord := Step{Step: step, Action: action}
		switch action.Type {
		case ActionFinal:
			answer := strings.TrimSpace(action.Message)
			if answer == "" {
				return Response{}, fmt.Errorf("agent final action must include a message")
			}
			steps = append(steps, stepRecord)
			return Response{Status: StatusCompleted, Answer: answer, Steps: steps, ToolCalls: toolCalls}, nil
		case ActionClarify:
			message := strings.TrimSpace(action.Message)
			if message == "" {
				message = "为了继续处理，还需要你补充一些信息。"
			}
			steps = append(steps, stepRecord)
			return Response{Status: StatusClarify, Answer: message, Steps: steps, ToolCalls: toolCalls}, nil
		case ActionConfirmation:
			message := strings.TrimSpace(action.Message)
			if message == "" {
				message = "该操作需要你的确认后才能执行。"
			}
			steps = append(steps, stepRecord)
			return Response{Status: StatusConfirmation, Answer: message, NeedsConfirmation: true, Steps: steps, ToolCalls: toolCalls}, nil
		case ActionTool:
			tool, ok := service.tools[action.Tool]
			if !ok {
				return Response{}, fmt.Errorf("agent requested unknown tool %q", action.Tool)
			}
			spec := tool.Spec()
			// A read-only draft may run to prepare a confirmation payload. A
			// non-read-only tool is blocked before execution unless a future
			// explicit confirmation token is added to the request contract.
			if spec.RequiresConfirmation && !spec.ReadOnly {
				result := ToolResult{Tool: spec.Name, Status: "confirmation_required", Summary: "该操作需要用户确认后才能执行。", RequiresConfirmation: true}
				observation := observationFromResult(result)
				stepRecord.Observation = &observation
				steps = append(steps, stepRecord)
				return Response{Status: StatusConfirmation, Answer: result.Summary, NeedsConfirmation: true, Steps: steps, ToolCalls: append(toolCalls, spec.Name)}, nil
			}
			arguments := normalizeArguments(action.Arguments)
			// Models occasionally omit the obvious user payload. The server can
			// safely fill these two non-sensitive fields from the original query;
			// it must never infer tenant, role or authorization arguments.
			if action.Tool == "knowledge_answer" {
				if _, exists := arguments["query"]; !exists {
					arguments["query"] = query
				}
			}
			if action.Tool == "ticket_draft" {
				if _, exists := arguments["summary"]; !exists {
					arguments["summary"] = query
				}
			}
			result, err := tool.Execute(ctx, toolContext, arguments)
			if err != nil {
				return Response{}, fmt.Errorf("agent tool %s at step %d: %w", spec.Name, step, err)
			}
			if strings.TrimSpace(result.Tool) == "" {
				result.Tool = spec.Name
			}
			if strings.TrimSpace(result.Status) == "" {
				result.Status = "ok"
			}
			observation := observationFromResult(result)
			stepRecord.Observation = &observation
			steps = append(steps, stepRecord)
			observations = append(observations, observation)
			toolCalls = append(toolCalls, spec.Name)
			if result.RequiresConfirmation {
				message := strings.TrimSpace(result.Answer)
				if message == "" {
					message = result.Summary
				}
				return Response{Status: StatusConfirmation, Answer: message, AnswerSource: result.AnswerSource, Citations: result.Citations, NeedsConfirmation: true, Steps: steps, ToolCalls: toolCalls}, nil
			}
			if result.Terminal {
				message := strings.TrimSpace(result.Answer)
				if message == "" {
					message = result.Summary
				}
				return Response{Status: StatusCompleted, Answer: message, AnswerSource: result.AnswerSource, Citations: result.Citations, Steps: steps, ToolCalls: toolCalls}, nil
			}
		default:
			return Response{}, fmt.Errorf("agent action type %q is not supported", action.Type)
		}
	}
	return Response{Status: StatusConfirmation, Answer: "为了避免工具重复调用，本次处理达到安全步数上限，请缩小问题范围后重试。", Steps: steps, ToolCalls: toolCalls}, nil
}

func normalizeArguments(arguments map[string]any) map[string]any {
	if arguments == nil {
		return map[string]any{}
	}
	return arguments
}

func observationFromResult(result ToolResult) Observation {
	return Observation{Tool: result.Tool, Status: result.Status, Summary: result.Summary, Data: result.Data}
}
