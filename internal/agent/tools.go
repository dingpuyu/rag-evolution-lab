package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/dingpuyu/rag-evolution-lab/internal/auth"
	"github.com/dingpuyu/rag-evolution-lab/internal/knowledgegateway"
)

type KnowledgeAnswerer interface {
	Answer(context.Context, auth.Identity, knowledgegateway.Request) (knowledgegateway.AnswerResponse, error)
}

type KnowledgeAnswerTool struct {
	Service KnowledgeAnswerer
}

func (tool KnowledgeAnswerTool) Spec() ToolSpec {
	return ToolSpec{Name: "knowledge_answer", Description: "在当前应用绑定且授权的企业知识库中检索并生成带引用的回答。", ReadOnly: true}
}

func (tool KnowledgeAnswerTool) Execute(ctx context.Context, toolContext ToolContext, arguments map[string]any) (ToolResult, error) {
	if tool.Service == nil {
		return ToolResult{}, fmt.Errorf("knowledge answer service is not configured")
	}
	query := stringArgument(arguments, "query")
	if query == "" {
		return ToolResult{}, fmt.Errorf("knowledge_answer requires query")
	}
	result, err := tool.Service.Answer(ctx, toolContext.Identity, knowledgegateway.Request{
		AppID: toolContext.ApplicationID, EnvironmentID: toolContext.EnvironmentID, Query: query, TopK: 5,
	})
	if err != nil {
		return ToolResult{}, err
	}
	citations := make([]Citation, 0, len(result.Result.Citations))
	for _, citation := range result.Result.Citations {
		citations = append(citations, Citation{ChunkID: citation.ChunkID, DocumentID: citation.DocumentID, Document: citation.Document, Excerpt: citation.Excerpt})
	}
	return ToolResult{
		Tool: tool.Spec().Name, Status: "ok", Summary: result.Result.Answer, Answer: result.Result.Answer,
		AnswerSource: result.Result.AnswerSource, Citations: citations, Terminal: true,
		Data: map[string]any{"answerable": result.Result.Answerable, "refusal_reason": result.Result.RefusalReason, "trace_id": result.TraceID, "citations": citations},
	}, nil
}

type ServiceStatus struct {
	Service   string    `json:"service"`
	Status    string    `json:"status"`
	Message   string    `json:"message"`
	CheckedAt time.Time `json:"checked_at"`
}

type ServiceStatusReader interface {
	ReadStatus(context.Context, string) (ServiceStatus, error)
}

type StaticServiceStatus map[string]ServiceStatus

func (reader StaticServiceStatus) ReadStatus(_ context.Context, service string) (ServiceStatus, error) {
	service = strings.TrimSpace(service)
	if service == "" {
		service = "acmecloud"
	}
	status, ok := reader[service]
	if !ok {
		return ServiceStatus{Service: service, Status: "unknown", Message: "当前没有该服务的状态数据", CheckedAt: time.Now().UTC()}, nil
	}
	if status.Service == "" {
		status.Service = service
	}
	if status.CheckedAt.IsZero() {
		status.CheckedAt = time.Now().UTC()
	}
	return status, nil
}

type ServiceStatusTool struct {
	Reader ServiceStatusReader
}

func (tool ServiceStatusTool) Spec() ToolSpec {
	return ToolSpec{Name: "service_status", Description: "读取指定服务的当前运行状态，只读，不修改任何系统。", ReadOnly: true}
}

func (tool ServiceStatusTool) Execute(ctx context.Context, _ ToolContext, arguments map[string]any) (ToolResult, error) {
	if tool.Reader == nil {
		return ToolResult{}, fmt.Errorf("service status reader is not configured")
	}
	service := stringArgument(arguments, "service")
	if service == "" {
		service = "acmecloud"
	}
	status, err := tool.Reader.ReadStatus(ctx, service)
	if err != nil {
		return ToolResult{}, err
	}
	summary := fmt.Sprintf("%s 当前状态：%s。%s", status.Service, status.Status, status.Message)
	return ToolResult{Tool: tool.Spec().Name, Status: "ok", Summary: summary, Answer: summary, Data: status, Terminal: true}, nil
}

type AccountAccessTool struct{}

func (AccountAccessTool) Spec() ToolSpec {
	return ToolSpec{Name: "account_access", Description: "读取当前认证主体的租户、角色和应用范围，只读，不根据自然语言猜测权限。", ReadOnly: true}
}

func (tool AccountAccessTool) Execute(_ context.Context, toolContext ToolContext, _ map[string]any) (ToolResult, error) {
	identity := toolContext.Identity
	if strings.TrimSpace(identity.Subject) == "" {
		return ToolResult{}, fmt.Errorf("authenticated subject is required")
	}
	data := map[string]any{"tenant_id": identity.TenantID, "roles": identity.Roles, "scopes": identity.Scopes, "application_id": identity.ApplicationID}
	summary := fmt.Sprintf("当前账号属于租户 %s，角色为 %s。", identity.TenantID, strings.Join(identity.Roles, "、"))
	return ToolResult{Tool: tool.Spec().Name, Status: "ok", Summary: summary, Answer: summary, Data: data, Terminal: true}, nil
}

type TicketDraftTool struct{}

func (TicketDraftTool) Spec() ToolSpec {
	return ToolSpec{Name: "ticket_draft", Description: "生成客服工单草稿；不会提交工单，提交前必须得到用户明确确认。", ReadOnly: true, RequiresConfirmation: true}
}

func (tool TicketDraftTool) Execute(_ context.Context, toolContext ToolContext, arguments map[string]any) (ToolResult, error) {
	summary := stringArgument(arguments, "summary")
	if summary == "" {
		return ToolResult{}, fmt.Errorf("ticket_draft requires summary")
	}
	draft := map[string]any{"summary": summary, "tenant_id": toolContext.Identity.TenantID, "requester": toolContext.Identity.Subject, "status": "draft"}
	answer := "已生成工单草稿，内容为：" + summary + "。请确认后再提交。"
	return ToolResult{Tool: tool.Spec().Name, Status: "draft", Summary: answer, Answer: answer, Data: draft, RequiresConfirmation: true}, nil
}

func stringArgument(arguments map[string]any, key string) string {
	if arguments == nil {
		return ""
	}
	value, ok := arguments[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}
