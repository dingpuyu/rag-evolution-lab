from __future__ import annotations

import pytest

from app.graph import AgentRuntime
from app.gateway import GatewayError
from app.llm import RuleAnswerer, RulePlanner
from app.models import Identity


class FakeGateway:
    async def search(self, app_id: str, environment_id: str, query: str, authorization: str, device_context=None):
        return {"result": {"hits": [{"chunk_id": "c1", "document_id": "d1", "title": "SSO", "content": "请在身份中心申请 SSO。"}]}}

    def hits(self, payload):
        from app.models import SearchHit

        return [SearchHit.model_validate(hit) for hit in payload["result"]["hits"]]

    async def identity(self, authorization: str):
        return Identity(subject="alice", tenant_id="tenant_a", roles=["admin"])


class DeniedGateway(FakeGateway):
    async def search(self, app_id: str, environment_id: str, query: str, authorization: str, device_context=None):
        raise GatewayError("knowledge gateway returned 404: application was not found or is not accessible")


@pytest.mark.asyncio
async def test_graph_uses_gateway_for_knowledge_answer():
    runtime = AgentRuntime(FakeGateway(), RulePlanner(), RuleAnswerer())
    response = await runtime.run("tenant_a-support-agent", "tenant_a-support-agent-dev", "如何申请企业单点登录？", "Bearer test")
    assert response.result.status == "completed"
    assert response.result.answer_source == "rag"
    assert response.result.citations[0].chunk_id == "c1"
    assert response.result.tool_calls == ["knowledge_search"]


@pytest.mark.asyncio
async def test_graph_returns_persona_for_greeting():
    runtime = AgentRuntime(FakeGateway(), RulePlanner(), RuleAnswerer())
    response = await runtime.run("tenant_a-support-agent", "tenant_a-support-agent-dev", "你好，你是谁？", "Bearer test")
    assert response.result.status == "completed"
    assert response.result.answer_source == "persona"


@pytest.mark.asyncio
async def test_graph_returns_identity_from_gateway():
    runtime = AgentRuntime(FakeGateway(), RulePlanner(), RuleAnswerer())
    response = await runtime.run("tenant_a-support-agent", "tenant_a-support-agent-dev", "我的权限是什么？", "Bearer test")
    assert response.result.status == "completed"
    assert "tenant_a" in response.result.answer
    assert response.result.tool_calls == ["account_access"]


@pytest.mark.asyncio
async def test_ticket_requires_confirmation_then_resumes():
    runtime = AgentRuntime(FakeGateway(), RulePlanner(), RuleAnswerer())
    pending = await runtime.run("tenant_a-support-agent", "tenant_a-support-agent-dev", "帮我创建一个登录故障工单", "Bearer test")
    assert pending.result.status == "needs_confirmation"
    assert pending.result.confirmation_id
    confirmed = await runtime.run(
        "tenant_a-support-agent",
        "tenant_a-support-agent-dev",
        "帮我创建一个登录故障工单",
        "Bearer test",
        thread_id=pending.thread_id,
        confirmation=True,
    )
    assert confirmed.result.status == "completed"
    assert "未连接真实工单系统" in confirmed.result.answer


@pytest.mark.asyncio
async def test_gateway_denial_is_fail_closed():
    runtime = AgentRuntime(DeniedGateway(), RulePlanner(), RuleAnswerer())
    response = await runtime.run("tenant_b-support-agent", "tenant_b-support-agent-dev", "如何申请企业单点登录？", "Bearer test")
    assert response.result.status == "needs_clarification"
    assert "没有绕过权限" in response.result.answer


@pytest.mark.asyncio
async def test_medical_agent_clarifies_ambiguous_model():
    runtime = AgentRuntime(FakeGateway(), RulePlanner(), RuleAnswerer())
    response = await runtime.run(
        "tenant_a-medical-device-agent",
        "tenant_a-medical-device-agent-dev",
        "SYS-NET-042 是什么意思？",
        "Bearer test",
    )
    assert response.result.status == "needs_clarification"
    assert response.result.decision == "clarify"
    assert response.result.reason_code == "ambiguous_device_model"
    assert "VSM-100 Pro" in response.result.candidate_entities


@pytest.mark.asyncio
async def test_medical_agent_refuses_clinical_parameter_advice_without_search():
    runtime = AgentRuntime(FakeGateway(), RulePlanner(), RuleAnswerer())
    response = await runtime.run(
        "tenant_a-medical-device-agent",
        "tenant_a-medical-device-agent-dev",
        "患者的报警阈值应该设置多少？",
        "Bearer test",
    )
    assert response.result.status == "refused"
    assert response.result.decision == "refuse"
    assert response.result.reason_code == "clinical_boundary"
    assert response.result.citations == []


@pytest.mark.asyncio
async def test_medical_agent_uses_deterministic_field_correction_rule():
    from app.models import DeviceContext

    runtime = AgentRuntime(FakeGateway(), RulePlanner(), RuleAnswerer())
    response = await runtime.run(
        "tenant_a-medical-device-agent",
        "tenant_a-medical-device-agent-dev",
        "FC-2026-04 现场更正通知是否适用？",
        "Bearer test",
        device_context=DeviceContext(model_code="VSM-100 Pro", software_version="2.5.2", lot_or_batch="L26A03"),
    )
    assert response.result.status == "completed"
    assert response.result.reason_code == "field_correction_applies"
    assert "判定结果：适用" in response.result.answer
    assert "assess_field_correction" in response.result.tool_calls
