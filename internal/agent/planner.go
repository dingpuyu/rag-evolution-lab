package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dingpuyu/rag-evolution-lab/internal/generation"
)

const plannerPromptVersion = "it-service-desk-planner-v1"

const plannerSystemPrompt = `你是企业 IT 服务台 Agent 的 Planner，只负责输出下一步结构化 Action，不直接执行工具。

业务边界：
1. 企业产品、账号、权限、配置、流程、故障和文档问题，优先调用 knowledge_answer。
2. 实时服务状态调用 service_status。
3. 当前登录用户的租户和角色调用 account_access，不要根据用户文字猜权限。
4. 创建工单只能调用 ticket_draft；该工具会要求用户确认，禁止绕过确认直接写入。
5. 信息不足时输出 clarify；通用寒暄可以输出 final。
6. 工具结果是事实来源。没有工具结果时不得编造企业事实。
7. 每次只输出一个 JSON 对象，不要 Markdown，不要解释 JSON 以外的内容。

合法 Action：
{"type":"tool","tool":"knowledge_answer|service_status|account_access|ticket_draft","arguments":{},"reason":"简短原因"}
{"type":"final","message":"给用户的最终答复","reason":"简短原因"}
{"type":"clarify","message":"需要用户补充的问题","reason":"简短原因"}
{"type":"confirmation","message":"需要用户确认的操作","reason":"简短原因"}

如果已经有 knowledge_answer 的结果，优先保持其原意和引用，不要用模型常识改写企业事实。`

type StructuredGenerator interface {
	GenerateStructured(context.Context, string, string) (generation.StructuredGeneration, error)
}

type DeepSeekPlanner struct {
	Generator StructuredGenerator
}

func NewPlanner(generator generation.Generator) Planner {
	if structured, ok := generator.(StructuredGenerator); ok {
		return DeepSeekPlanner{Generator: structured}
	}
	return RulePlanner{}
}

func (planner DeepSeekPlanner) Plan(ctx context.Context, input PlanInput) (Action, error) {
	if planner.Generator == nil {
		return Action{}, fmt.Errorf("deepseek planner requires a structured generator")
	}
	tools, err := json.Marshal(input.Tools)
	if err != nil {
		return Action{}, fmt.Errorf("encode planner tools: %w", err)
	}
	observations, err := json.Marshal(input.Observations)
	if err != nil {
		return Action{}, fmt.Errorf("encode planner observations: %w", err)
	}
	userMessage := fmt.Sprintf("PLANNER_VERSION: %s\nSTEP: %d\nUSER_QUERY: %s\nTOOLS_JSON: %s\nOBSERVATIONS_JSON: %s", plannerPromptVersion, input.Step, input.Query, tools, observations)
	response, err := planner.Generator.GenerateStructured(ctx, plannerSystemPrompt, userMessage)
	if err != nil {
		return Action{}, fmt.Errorf("call DeepSeek planner: %w", err)
	}
	action, err := decodeAction(response.Content)
	if err != nil {
		return Action{}, fmt.Errorf("decode planner action JSON: %w", err)
	}
	if err := validateAction(action); err != nil {
		return Action{}, err
	}
	return action, nil
}

func decodeAction(content string) (Action, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return Action{}, fmt.Errorf("planner returned empty content")
	}
	// Some OpenAI-compatible deployments append a short explanation after the
	// JSON object despite response_format=json_object. Decode the first object
	// and reject content that does not contain a valid action.
	start := strings.IndexByte(content, '{')
	if start < 0 {
		return Action{}, fmt.Errorf("planner response does not contain a JSON object")
	}
	decoder := json.NewDecoder(strings.NewReader(content[start:]))
	var action Action
	if err := decoder.Decode(&action); err != nil {
		return Action{}, err
	}
	return action, nil
}

func validateAction(action Action) error {
	switch strings.ToLower(strings.TrimSpace(action.Type)) {
	case ActionFinal, ActionClarify, ActionConfirmation:
		if strings.TrimSpace(action.Message) == "" {
			return fmt.Errorf("planner %s action requires message", action.Type)
		}
	case ActionTool:
		if strings.TrimSpace(action.Tool) == "" {
			return fmt.Errorf("planner tool action requires tool")
		}
	default:
		return fmt.Errorf("planner action type %q is not allowed", action.Type)
	}
	return nil
}

// RulePlanner is a deterministic offline fallback. It keeps the Agent
// contract testable without a model and is never presented as a production
// reasoning model.
type RulePlanner struct{}

func (RulePlanner) Plan(_ context.Context, input PlanInput) (Action, error) {
	value := strings.ToLower(strings.TrimSpace(input.Query))
	if len(input.Observations) > 0 {
		last := input.Observations[len(input.Observations)-1]
		return Action{Type: ActionFinal, Message: last.Summary, Reason: "tool observation"}, nil
	}
	if generation.IsGeneralQuery(input.Query) {
		return Action{Type: ActionFinal, Message: "你好，我是 RAG Desk，可以回答通用问题；涉及企业资料时我会先查询授权知识库。", Reason: "general conversation"}, nil
	}
	switch {
	case strings.Contains(value, "工单") || strings.Contains(value, "ticket"):
		return Action{Type: ActionTool, Tool: "ticket_draft", Arguments: map[string]any{"summary": input.Query}, Reason: "write operation requires confirmation"}, nil
	case strings.Contains(value, "状态") || strings.Contains(value, "故障") || strings.Contains(value, "可用"):
		return Action{Type: ActionTool, Tool: "service_status", Arguments: map[string]any{"service": "acmecloud"}, Reason: "read live service status"}, nil
	case strings.Contains(value, "我的权限") || strings.Contains(value, "我的角色") || strings.Contains(value, "权限"):
		return Action{Type: ActionTool, Tool: "account_access", Arguments: map[string]any{}, Reason: "read trusted identity claims"}, nil
	default:
		return Action{Type: ActionTool, Tool: "knowledge_answer", Arguments: map[string]any{"query": input.Query}, Reason: "retrieve enterprise knowledge"}, nil
	}
}
