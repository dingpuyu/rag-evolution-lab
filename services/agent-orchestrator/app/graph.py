from __future__ import annotations

import uuid
from dataclasses import dataclass
from typing import Any

from langgraph.checkpoint.memory import MemorySaver
from langgraph.graph import END, START, StateGraph
from langgraph.types import interrupt

from .gateway import GatewayError, KnowledgeGatewayClient
from .llm import Answerer, Planner, RuleAnswerer, RulePlanner, citations_from_hits
from .models import Action, AgentResponse, AgentResult, AgentState, AgentStep


@dataclass
class RequestContext:
    authorization: str
    app_id: str
    environment_id: str


class AgentRuntime:
    """LangGraph runtime with an intentionally small, explicit business graph."""

    def __init__(
        self,
        gateway: KnowledgeGatewayClient,
        planner: Planner,
        answerer: Answerer,
        max_steps: int = 4,
    ) -> None:
        self.gateway = gateway
        self.planner = planner
        self.answerer = answerer
        self.max_steps = max_steps
        self.contexts: dict[str, RequestContext] = {}
        builder = StateGraph(AgentState)
        builder.add_node("plan", self._plan)
        builder.add_node("knowledge_search", self._knowledge_search)
        builder.add_node("knowledge_answer", self._knowledge_answer)
        builder.add_node("service_status", self._service_status)
        builder.add_node("account_access", self._account_access)
        builder.add_node("ticket_draft", self._ticket_draft)
        builder.add_node("final", self._final)
        builder.add_node("clarify", self._clarify)
        builder.add_edge(START, "plan")
        builder.add_conditional_edges(
            "plan",
            self._route,
            {
                "knowledge_search": "knowledge_search",
                "service_status": "service_status",
                "account_access": "account_access",
                "ticket_draft": "ticket_draft",
                "final": "final",
                "clarify": "clarify",
            },
        )
        builder.add_edge("knowledge_search", "knowledge_answer")
        for node in ("knowledge_answer", "service_status", "account_access", "ticket_draft", "final", "clarify"):
            builder.add_edge(node, END)
        self.graph = builder.compile(checkpointer=MemorySaver())

    def _context(self, state: AgentState) -> RequestContext:
        thread_id = str(state["thread_id"])
        try:
            return self.contexts[thread_id]
        except KeyError as exc:
            raise RuntimeError("agent thread context expired; please retry") from exc

    @staticmethod
    def _step(action: Action, step: int, observation: dict[str, Any] | None = None) -> dict[str, Any]:
        return AgentStep(step=step, action=action.model_dump(exclude_none=True), observation=observation).model_dump(exclude_none=True)

    async def _plan(self, state: AgentState) -> dict[str, Any]:
        if len(state.get("steps", [])) >= self.max_steps:
            action = Action(type="clarify", message="本次请求已达到安全执行步数上限，请缩小问题范围后重试。", reason="bounded agent loop")
        else:
            action = await self.planner.plan(str(state["query"]))
        return {"action": action.model_dump(), "steps": state.get("steps", []) + [self._step(action, len(state.get("steps", [])) + 1)]}

    @staticmethod
    def _route(state: AgentState) -> str:
        action = Action.model_validate(state["action"])
        if action.type == "knowledge_answer":
            return "knowledge_search"
        return action.type

    async def _knowledge_search(self, state: AgentState) -> dict[str, Any]:
        context = self._context(state)
        try:
            payload = await self.gateway.search(context.app_id, context.environment_id, str(state["query"]), context.authorization)
            hits = [hit.model_dump() for hit in self.gateway.hits(payload)]
            observation = {"tool": "knowledge_search", "status": "ok", "summary": f"授权检索命中 {len(hits)} 条证据"}
        except GatewayError as exc:
            hits = []
            observation = {"tool": "knowledge_search", "status": "error", "summary": str(exc)}
        steps = list(state.get("steps", []))
        steps[-1]["observation"] = observation
        if observation["status"] == "error":
            return {"evidence": hits, "gateway_error": observation["summary"], "observations": list(state.get("observations", [])) + [observation], "steps": steps}
        return {"evidence": hits, "gateway_error": "", "observations": list(state.get("observations", [])) + [observation], "steps": steps}

    async def _knowledge_answer(self, state: AgentState) -> dict[str, Any]:
        evidence = list(state.get("evidence", []))
        gateway_error = str(state.get("gateway_error", ""))
        if gateway_error:
            result = AgentResult(
                status="needs_clarification",
                answer="当前应用的授权知识库暂时不可用，系统没有绕过权限继续回答。请检查应用绑定或联系管理员。",
                answer_source="tool",
                steps=[AgentStep.model_validate(step) for step in state.get("steps", [])],
                tool_calls=["knowledge_search"],
            )
            return {"response": result.model_dump()}
        answer = await self.answerer.answer(str(state["query"]), evidence)
        citations = citations_from_hits(evidence)
        result = AgentResult(
            status="completed",
            answer=answer,
            answer_source="rag",
            citations=citations,
            steps=[AgentStep.model_validate(step) for step in state.get("steps", [])],
            tool_calls=["knowledge_search"],
        )
        return {"response": result.model_dump()}

    async def _service_status(self, state: AgentState) -> dict[str, Any]:
        action = Action.model_validate(state["action"])
        observation = {"tool": "service_status", "status": "ok", "summary": "核心服务当前运行正常"}
        steps = list(state.get("steps", []))
        steps[-1]["observation"] = observation
        result = AgentResult(
            status="completed",
            answer="当前核心服务运行正常，暂未发现影响使用的服务异常。",
            answer_source="tool",
            steps=[AgentStep.model_validate(step) for step in steps],
            tool_calls=[action.type],
        )
        return {"response": result.model_dump()}

    async def _account_access(self, state: AgentState) -> dict[str, Any]:
        context = self._context(state)
        try:
            identity = await self.gateway.identity(context.authorization)
            answer = f"当前账号是 {identity.subject}，租户为 {identity.tenant_id}，角色为 {', '.join(identity.roles) or '未分配'}。"
            observation = {"tool": "account_access", "status": "ok", "summary": "已读取 Go 服务验证后的身份 Claims"}
        except GatewayError as exc:
            answer = "身份信息暂时无法读取，请重新登录后重试。"
            observation = {"tool": "account_access", "status": "error", "summary": str(exc)}
        steps = list(state.get("steps", []))
        steps[-1]["observation"] = observation
        result = AgentResult(
            status="completed" if observation["status"] == "ok" else "needs_clarification",
            answer=answer,
            answer_source="tool",
            steps=[AgentStep.model_validate(step) for step in steps],
            tool_calls=["account_access"],
        )
        return {"response": result.model_dump()}

    async def _ticket_draft(self, state: AgentState) -> dict[str, Any]:
        context = self._context(state)
        confirmation_id = f"ticket-{uuid.uuid4().hex[:12]}"
        draft = {
            "confirmation_id": confirmation_id,
            "title": str(state["query"])[:120],
            "tenant_id": context.app_id.split("-", 1)[0],
            "status": "draft",
        }
        steps = list(state.get("steps", []))
        steps[-1]["observation"] = {"tool": "ticket_draft", "status": "pending_confirmation", "summary": "已生成工单草稿，未写入业务系统"}
        approval = interrupt({"type": "ticket_confirmation", "draft": draft, "message": "是否确认提交该工单？"})
        approved = isinstance(approval, dict) and approval.get("approved") is True
        if approved:
            # There is deliberately no write backend in this first slice.
            result = AgentResult(
                status="completed",
                answer="已确认工单草稿，但当前演示环境未连接真实工单系统，因此没有执行外部写入。",
                answer_source="confirmation",
                confirmation_id=confirmation_id,
                steps=[AgentStep.model_validate(step) for step in steps],
                tool_calls=["ticket_draft"],
            )
        else:
            result = AgentResult(
                status="needs_confirmation",
                answer="工单草稿已准备好，确认后才会提交。",
                answer_source="confirmation",
                needs_confirmation=True,
                confirmation_id=confirmation_id,
                steps=[AgentStep.model_validate(step) for step in steps],
                tool_calls=["ticket_draft"],
            )
        return {"response": result.model_dump()}

    async def _final(self, state: AgentState) -> dict[str, Any]:
        action = Action.model_validate(state["action"])
        result = AgentResult(
            status="completed",
            answer=action.message or "你好，我是企业 IT 服务台 Agent，可以协助查询知识、服务状态、账号权限和工单流程。",
            answer_source="persona",
            steps=[AgentStep.model_validate(step) for step in state.get("steps", [])],
        )
        return {"response": result.model_dump()}

    async def _clarify(self, state: AgentState) -> dict[str, Any]:
        action = Action.model_validate(state["action"])
        result = AgentResult(
            status="needs_clarification",
            answer=action.message or "请补充产品、环境或错误码，我再帮你继续定位。",
            answer_source="persona",
            steps=[AgentStep.model_validate(step) for step in state.get("steps", [])],
        )
        return {"response": result.model_dump()}

    async def run(
        self,
        app_id: str,
        environment_id: str,
        query: str,
        authorization: str,
        thread_id: str | None = None,
        confirmation: bool | None = None,
    ) -> AgentResponse:
        thread_id = thread_id or f"thread-{uuid.uuid4().hex}"
        self.contexts[thread_id] = RequestContext(authorization=authorization, app_id=app_id, environment_id=environment_id)
        config = {"configurable": {"thread_id": thread_id}}
        if confirmation is True:
            from langgraph.types import Command

            result = await self.graph.ainvoke(Command(resume={"approved": True}), config=config)
        else:
            result = await self.graph.ainvoke(
                {"thread_id": thread_id, "app_id": app_id, "environment_id": environment_id, "query": query, "steps": [], "observations": []},
                config=config,
            )
        interrupts = result.get("__interrupt__") or []
        if interrupts:
            value = interrupts[0].value if hasattr(interrupts[0], "value") else interrupts[0]
            draft = value.get("draft", {}) if isinstance(value, dict) else {}
            pending = AgentResult(
                status="needs_confirmation",
                answer="工单草稿已准备好，确认后才会提交。",
                answer_source="confirmation",
                needs_confirmation=True,
                confirmation_id=draft.get("confirmation_id"),
                steps=[AgentStep.model_validate(step) for step in result.get("steps", [])],
                tool_calls=["ticket_draft"],
            )
            return AgentResponse(app_id=app_id, environment_id=environment_id, thread_id=thread_id, result=pending)
        self.contexts.pop(thread_id, None)
        response = AgentResult.model_validate(result.get("response", {"status": "needs_clarification", "answer": "Agent 没有返回结果。"}))
        return AgentResponse(app_id=app_id, environment_id=environment_id, thread_id=thread_id, result=response)


def build_runtime(gateway: KnowledgeGatewayClient, llm: Any, max_steps: int = 4) -> AgentRuntime:
    planner: Planner = RulePlanner()
    answerer: Answerer = RuleAnswerer()
    if llm is not None:
        from .llm import DeepSeekAnswerer, DeepSeekPlanner

        planner = DeepSeekPlanner(llm, fallback=planner)
        answerer = DeepSeekAnswerer(llm)
    return AgentRuntime(gateway=gateway, planner=planner, answerer=answerer, max_steps=max_steps)
