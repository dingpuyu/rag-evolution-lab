from __future__ import annotations

import uuid
from dataclasses import dataclass
from typing import Any

from langgraph.checkpoint.memory import MemorySaver
from langgraph.graph import END, START, StateGraph
from langgraph.types import interrupt

from .gateway import GatewayError, KnowledgeGatewayClient
from .llm import Answerer, Planner, RuleAnswerer, RulePlanner, citations_from_hits
from .medical import (
    assess_field_correction,
    resolve_customer_query,
    resolve_medical_query,
    sanitize_customer_retrieval_query,
    verify_customer_evidence,
    verify_field_correction_evidence,
    verify_medical_evidence,
)
from .models import Action, AgentResponse, AgentResult, AgentState, AgentStep, DeviceContext


@dataclass
class RequestContext:
    authorization: str
    app_id: str
    environment_id: str
    device_context: DeviceContext


class AgentRuntime:
    """LangGraph runtime with an intentionally small, explicit business graph."""

    def __init__(
        self,
        gateway: KnowledgeGatewayClient,
        planner: Planner,
        answerer: Answerer,
        medical_answerer: Answerer | None = None,
        medical_customer_answerer: Answerer | None = None,
        max_steps: int = 4,
    ) -> None:
        self.gateway = gateway
        self.planner = planner
        self.answerer = answerer
        self.medical_answerer = medical_answerer or answerer
        self.medical_customer_answerer = medical_customer_answerer or self.medical_answerer
        self.max_steps = max_steps
        self.contexts: dict[str, RequestContext] = {}
        builder = StateGraph(AgentState)
        builder.add_node("entry", self._entry)
        builder.add_node("plan", self._plan)
        builder.add_node("knowledge_search", self._knowledge_search)
        builder.add_node("knowledge_answer", self._knowledge_answer)
        builder.add_node("service_status", self._service_status)
        builder.add_node("account_access", self._account_access)
        builder.add_node("ticket_draft", self._ticket_draft)
        builder.add_node("final", self._final)
        builder.add_node("clarify", self._clarify)
        builder.add_node("medical_resolve", self._medical_resolve)
        builder.add_node("medical_persona", self._medical_persona)
        builder.add_node("medical_refuse", self._medical_refuse)
        builder.add_node("medical_clarify", self._medical_clarify)
        builder.add_node("medical_search", self._medical_search)
        builder.add_node("medical_verify", self._medical_verify)
        builder.add_node("medical_answer", self._medical_answer)
        builder.add_edge(START, "entry")
        builder.add_conditional_edges("entry", self._entry_route, {"plan": "plan", "medical_resolve": "medical_resolve"})
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
        builder.add_conditional_edges(
            "medical_resolve",
            self._medical_route,
            {
                "medical_persona": "medical_persona",
                "medical_refuse": "medical_refuse",
                "medical_clarify": "medical_clarify",
                "medical_search": "medical_search",
            },
        )
        builder.add_edge("medical_search", "medical_verify")
        builder.add_edge("medical_verify", "medical_answer")
        for node in ("medical_persona", "medical_refuse", "medical_clarify", "medical_answer"):
            builder.add_edge(node, END)
        self.graph = builder.compile(checkpointer=MemorySaver())

    async def _entry(self, state: AgentState) -> dict[str, Any]:
        context = self._context(state)
        return {
            "is_medical": "medical-device" in context.app_id and "agent" in context.app_id,
            "is_customer": "medical-device-customer-agent" in context.app_id,
        }

    @staticmethod
    def _entry_route(state: AgentState) -> str:
        return "medical_resolve" if state.get("is_medical") else "plan"

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

    async def _medical_resolve(self, state: AgentState) -> dict[str, Any]:
        resolver = resolve_customer_query if state.get("is_customer") else resolve_medical_query
        resolution = resolver(str(state["query"]), self._context(state).device_context)
        return {
            "medical_intent": resolution.intent,
            "reason_code": resolution.reason_code,
            "resolved_context": resolution.context.model_dump(),
            "candidate_entities": resolution.candidates,
        }

    @staticmethod
    def _medical_route(state: AgentState) -> str:
        intent = str(state.get("medical_intent", "knowledge"))
        if intent in {"persona", "customer_onboarding"}:
            return "medical_persona"
        if intent == "refuse":
            return "medical_refuse"
        if intent == "clarify":
            return "medical_clarify"
        return "medical_search"

    async def _medical_persona(self, state: AgentState) -> dict[str, Any]:
        if state.get("is_customer"):
            result = AgentResult(
                status="completed",
                decision="answer",
                reason_code="customer_guided_onboarding",
                answer_source="persona",
                answer=(
                    "你好，我是医疗设备销售顾问 Agent。你不需要先懂专业术语，我们可以从零开始："
                    "先认识病人监护、AED、输注系统、超声和呼吸机等产品线，再查看迈瑞、飞利浦、德尔格等厂商的公开型号资料，"
                    "最后学习报价前的配置与注册核验，或按设备铭牌和现场现象进行安全售后分诊。产品事实会给出公开资料引用。"
                ),
                resolved_context=DeviceContext.model_validate(state.get("resolved_context", {})),
                candidate_entities=list(state.get("candidate_entities", [])),
                suggested_questions=[
                    "你们目前有哪些医疗设备产品线？",
                    "BeneVision N1 和 IntelliVue MX550 都是什么设备？",
                    "购买医疗设备前需要核对哪些注册和配置信息？",
                    "设备网络连不上，但我不知道型号，应该怎么开始排查？",
                ],
            )
            return {"response": result.model_dump()}
        result = AgentResult(
            status="completed",
            decision="answer",
            reason_code="capability_introduction",
            answer_source="persona",
            answer="你好，我是 PulseCare 医疗设备运维知识助手，可以基于授权资料查询型号、软件版本、错误码、配件兼容性和现场更正通知。我不提供临床诊断、治疗或患者参数建议。",
            resolved_context=DeviceContext.model_validate(state.get("resolved_context", {})),
        )
        return {"response": result.model_dump()}

    async def _medical_refuse(self, state: AgentState) -> dict[str, Any]:
        result = AgentResult(
            status="refused",
            decision="refuse",
            reason_code=str(state.get("reason_code", "clinical_boundary")),
            answer_source="persona",
            answer="这个问题涉及临床诊断、治疗或患者参数设定，超出设备运维知识助手的安全边界。请遵循真实设备说明书、机构临床流程，并由具备资质的专业人员判断。",
            resolved_context=DeviceContext.model_validate(state.get("resolved_context", {})),
        )
        return {"response": result.model_dump()}

    async def _medical_clarify(self, state: AgentState) -> dict[str, Any]:
        candidates = list(state.get("candidate_entities", []))
        reason_code = str(state.get("reason_code"))
        if reason_code == "customer_missing_model_for_troubleshooting":
            answer = (
                "可以继续帮你分诊，不过不同厂商、型号和配置的错误含义可能不同。请先查看设备铭牌、"
                "采购合同或装箱单，告诉我厂商与完整型号；也请记录屏幕上的原始错误码和发生时间。"
                "在型号未确认前，不要拆机、屏蔽报警或进入受限维护菜单。"
            )
        elif reason_code == "missing_applicability_context":
            answer = "判断现场更正通知是否适用前，请补充：" + "、".join(candidates) + "。"
        else:
            answer = "该问题在不同型号上的答案可能不同，请先确认设备型号。可选型号：" + "、".join(candidates) + "。"
        result = AgentResult(
            status="needs_clarification",
            decision="clarify",
            reason_code=reason_code or "ambiguous_device_context",
            answer_source="tool",
            answer=answer,
            resolved_context=DeviceContext.model_validate(state.get("resolved_context", {})),
            candidate_entities=candidates,
            suggested_questions=["我不知道型号在哪里看", "先介绍你们的产品线"] if state.get("is_customer") else [],
            tool_calls=["resolve_device_context"],
        )
        return {"response": result.model_dump()}

    async def _medical_search(self, state: AgentState) -> dict[str, Any]:
        context = self._context(state)
        resolved = DeviceContext.model_validate(state.get("resolved_context", {}))
        gateway_context = resolved.model_dump()
        retrieval_query = sanitize_customer_retrieval_query(str(state["query"])) if state.get("is_customer") else str(state["query"])
        if state.get("medical_intent") == "field_correction":
            # Retrieve the governing notice first. Applicability is evaluated by
            # the deterministic tool after evidence identity/authority checks.
            gateway_context["model_code"] = ""
            gateway_context["software_version"] = ""
        try:
            payload = await self.gateway.search(
                context.app_id,
                context.environment_id,
                retrieval_query,
                context.authorization,
                device_context=gateway_context,
            )
            hits = [hit.model_dump() for hit in self.gateway.hits(payload)]
            trace_id = str(payload.get("trace_id", ""))
            observation = {"tool": "knowledge_search", "status": "ok", "summary": f"授权检索命中 {len(hits)} 条医疗设备证据"}
        except GatewayError as exc:
            hits, trace_id = [], ""
            observation = {"tool": "knowledge_search", "status": "error", "summary": str(exc)}
        action_arguments = {**gateway_context, "retrieval_query": retrieval_query}
        step = AgentStep(step=1, action={"type": "knowledge_search", "arguments": action_arguments}, observation=observation).model_dump(exclude_none=True)
        return {"evidence": hits, "trace_id": trace_id, "gateway_error": "" if observation["status"] == "ok" else observation["summary"], "steps": [step], "observations": [observation]}

    async def _medical_answer(self, state: AgentState) -> dict[str, Any]:
        evidence = list(state.get("evidence", []))
        resolved = DeviceContext.model_validate(state.get("resolved_context", {}))
        steps = [AgentStep.model_validate(step) for step in state.get("steps", [])]
        if state.get("gateway_error"):
            result = AgentResult(
                status="needs_clarification", decision="clarify", reason_code="knowledge_gateway_unavailable",
                answer="授权知识库当前不可用，系统不会绕过权限或使用模型记忆回答。请稍后重试。",
                answer_source="tool", resolved_context=resolved, steps=steps, tool_calls=["knowledge_search"],
            )
            return {"response": result.model_dump()}
        if not evidence:
            customer = bool(state.get("is_customer"))
            result = AgentResult(
                status="needs_clarification", decision="clarify", reason_code="insufficient_evidence",
                answer=(
                    "我在已采集的厂商官网和公开销售资料中没有找到能可靠回答这个问题的内容。请补充厂商、完整型号、"
                    "所在地区、配置或你看到的原始错误码；库存、价格、注册有效性和维修操作需要销售或授权服务人员再次核验。"
                    if customer else
                    "当前授权资料中没有找到与该型号和版本匹配的可靠证据。请检查型号、软件版本，或联系知识库管理员补充资料。"
                ),
                answer_source="rag", resolved_context=resolved, steps=steps, tool_calls=["knowledge_search"], trace_id=str(state.get("trace_id", "")),
                suggested_questions=["我在哪里查看完整型号？", "先介绍产品线"] if customer else [],
            )
            return {"response": result.model_dump()}
        citations = citations_from_hits(evidence)
        if state.get("medical_intent") == "field_correction":
            outcome, explanation = assess_field_correction(resolved)
            answer = {
                "applies": "判定结果：适用。",
                "does_not_apply": "判定结果：不适用。",
                "needs_information": "判定结果：信息不足。",
            }[outcome] + explanation + "该结论只针对本项目中的虚构通知 FC-2026-04。"
            decision = "clarify" if outcome == "needs_information" else "answer"
            result = AgentResult(
                status="needs_clarification" if decision == "clarify" else "completed",
                decision=decision, reason_code=f"field_correction_{outcome}", answer=answer,
                answer_source="tool", resolved_context=resolved, citations=citations, steps=steps,
                tool_calls=["knowledge_search", "assess_field_correction"], trace_id=str(state.get("trace_id", "")),
            )
            return {"response": result.model_dump()}
        customer = bool(state.get("is_customer"))
        answerer = self.medical_customer_answerer if customer else self.medical_answerer
        answer = await answerer.answer(str(state["query"]), evidence)
        result = AgentResult(
            status="completed", decision="answer", reason_code="grounded_customer_answer" if customer else "grounded_medical_answer", answer=answer,
            answer_source="rag", resolved_context=resolved, citations=citations, steps=steps,
            tool_calls=["resolve_device_context", "knowledge_search", "verify_evidence"], trace_id=str(state.get("trace_id", "")),
            suggested_questions=(
                ["继续对比同类型号", "购买前需要核验什么？", "按我的型号开始售后分诊"]
                if customer and state.get("medical_intent") == "product_discovery" else
                (["还有哪些安全检查？", "什么时候需要联系专业人员？"] if customer else [])
            ),
        )
        return {"response": result.model_dump()}

    async def _medical_verify(self, state: AgentState) -> dict[str, Any]:
        resolved = DeviceContext.model_validate(state.get("resolved_context", {}))
        if state.get("medical_intent") == "field_correction":
            evidence, reason = verify_field_correction_evidence("FC-2026-04", list(state.get("evidence", [])))
        elif state.get("is_customer"):
            evidence, reason = verify_customer_evidence(str(state.get("medical_intent", "knowledge")), resolved, list(state.get("evidence", [])))
        else:
            evidence, reason = verify_medical_evidence(resolved, list(state.get("evidence", [])))
        steps = list(state.get("steps", []))
        steps.append(AgentStep(
            step=len(steps) + 1,
            action={"type": "verify_evidence", "arguments": resolved.model_dump()},
            observation={"tool": "verify_evidence", "status": "ok" if evidence else "rejected", "summary": reason},
        ).model_dump(exclude_none=True))
        return {"evidence": evidence, "evidence_verification": reason, "steps": steps}

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
        device_context: DeviceContext | None = None,
    ) -> AgentResponse:
        thread_id = thread_id or f"thread-{uuid.uuid4().hex}"
        self.contexts[thread_id] = RequestContext(authorization=authorization, app_id=app_id, environment_id=environment_id, device_context=device_context or DeviceContext())
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
    medical_answerer: Answerer = answerer
    medical_customer_answerer: Answerer = answerer
    if llm is not None:
        from .llm import DeepSeekAnswerer, DeepSeekMedicalAnswerer, DeepSeekMedicalCustomerAnswerer, DeepSeekPlanner

        planner = DeepSeekPlanner(llm, fallback=planner)
        answerer = DeepSeekAnswerer(llm)
        medical_answerer = DeepSeekMedicalAnswerer(llm)
        medical_customer_answerer = DeepSeekMedicalCustomerAnswerer(llm)
    return AgentRuntime(
        gateway=gateway, planner=planner, answerer=answerer,
        medical_answerer=medical_answerer, medical_customer_answerer=medical_customer_answerer,
        max_steps=max_steps,
    )
