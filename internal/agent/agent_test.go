package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/dingpuyu/rag-evolution-lab/internal/auth"
	"github.com/dingpuyu/rag-evolution-lab/internal/generation"
)

type sequencePlanner struct {
	actions []Action
	index   int
}

func (planner *sequencePlanner) Plan(_ context.Context, _ PlanInput) (Action, error) {
	if planner.index >= len(planner.actions) {
		return Action{}, errors.New("planner sequence exhausted")
	}
	action := planner.actions[planner.index]
	planner.index++
	return action, nil
}

type terminalTool struct {
	name string
}

func (tool terminalTool) Spec() ToolSpec {
	return ToolSpec{Name: tool.name, Description: "test", ReadOnly: true}
}

func (tool terminalTool) Execute(context.Context, ToolContext, map[string]any) (ToolResult, error) {
	return ToolResult{Tool: tool.name, Status: "ok", Summary: "tool answer", Answer: "tool answer", Terminal: true}, nil
}

type observationTool struct{}

func (observationTool) Spec() ToolSpec {
	return ToolSpec{Name: "observe", Description: "test", ReadOnly: true}
}

func (observationTool) Execute(context.Context, ToolContext, map[string]any) (ToolResult, error) {
	return ToolResult{Tool: "observe", Status: "ok", Summary: "observed fact"}, nil
}

func TestServiceRunsToolAndReturnsTerminalEvidence(t *testing.T) {
	service, err := NewService(Config{
		Planner: &sequencePlanner{actions: []Action{{Type: ActionTool, Tool: "knowledge_answer"}}},
		Tools:   []Tool{terminalTool{name: "knowledge_answer"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := service.Run(context.Background(), ToolContext{Identity: auth.Identity{Subject: "user-1"}}, "如何配置 SSO？")
	if err != nil {
		t.Fatal(err)
	}
	if response.Status != StatusCompleted || response.Answer != "tool answer" || len(response.Steps) != 1 || len(response.ToolCalls) != 1 {
		t.Fatalf("unexpected terminal response: %#v", response)
	}
}

func TestServiceLoopsThroughObservationThenFinal(t *testing.T) {
	service, err := NewService(Config{
		Planner: &sequencePlanner{actions: []Action{
			{Type: ActionTool, Tool: "observe"},
			{Type: ActionFinal, Message: "根据状态，服务正常。"},
		}},
		Tools: []Tool{observationTool{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := service.Run(context.Background(), ToolContext{}, "服务状态")
	if err != nil {
		t.Fatal(err)
	}
	if response.Status != StatusCompleted || response.Answer != "根据状态，服务正常。" || len(response.Steps) != 2 {
		t.Fatalf("unexpected loop response: %#v", response)
	}
	if response.Steps[0].Observation == nil || response.Steps[0].Observation.Summary != "observed fact" {
		t.Fatalf("tool observation was not retained: %#v", response.Steps)
	}
}

func TestServiceStopsAtTicketConfirmation(t *testing.T) {
	service, err := NewService(Config{
		Planner: &sequencePlanner{actions: []Action{{Type: ActionTool, Tool: "ticket_draft", Arguments: map[string]any{"summary": "无法登录"}}}},
		Tools:   []Tool{TicketDraftTool{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := service.Run(context.Background(), ToolContext{Identity: auth.Identity{Subject: "user-1", TenantID: "tenant-a"}}, "帮我创建工单")
	if err != nil {
		t.Fatal(err)
	}
	if response.Status != StatusConfirmation || !response.NeedsConfirmation || len(response.ToolCalls) != 1 {
		t.Fatalf("write operation must stop for confirmation: %#v", response)
	}
}

type fakeStructuredGenerator struct {
	content string
}

func (generator fakeStructuredGenerator) GenerateStructured(context.Context, string, string) (generation.StructuredGeneration, error) {
	return generation.StructuredGeneration{Content: generator.content, Model: "fake-planner"}, nil
}

func TestDeepSeekPlannerValidatesStructuredAction(t *testing.T) {
	planner := DeepSeekPlanner{Generator: fakeStructuredGenerator{content: `{"type":"tool","tool":"service_status","arguments":{"service":"acmecloud"}}`}}
	action, err := planner.Plan(context.Background(), PlanInput{Query: "服务状态", Step: 1})
	if err != nil {
		t.Fatal(err)
	}
	if action.Type != ActionTool || action.Tool != "service_status" {
		t.Fatalf("unexpected parsed action: %#v", action)
	}
}

func TestDeepSeekPlannerRejectsInvalidAction(t *testing.T) {
	planner := DeepSeekPlanner{Generator: fakeStructuredGenerator{content: `{"type":"delete_database"}`}}
	if _, err := planner.Plan(context.Background(), PlanInput{Query: "删除数据库", Step: 1}); err == nil {
		t.Fatal("expected invalid planner action to be rejected")
	}
}
