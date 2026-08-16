from __future__ import annotations

import json
import re
from typing import Any, Protocol

from langchain_core.messages import HumanMessage, SystemMessage
from langchain_openai import ChatOpenAI

from .models import Action, Citation


class Planner(Protocol):
    async def plan(self, query: str) -> Action: ...


class Answerer(Protocol):
    async def answer(self, query: str, evidence: list[dict[str, Any]]) -> str: ...


def build_llm(api_key: str, base_url: str, model: str) -> ChatOpenAI | None:
    if not api_key.strip():
        return None
    return ChatOpenAI(
        api_key=api_key,
        base_url=base_url,
        model=model,
        temperature=0,
        timeout=45,
        max_retries=2,
    )


class RulePlanner:
    async def plan(self, query: str) -> Action:
        text = query.lower()
        if any(word in text for word in ("工单", "报障", "提交问题", "创建故障")):
            return Action(type="ticket_draft", reason="用户要求创建或提交工单")
        if any(word in text for word in ("状态", "故障", "可用", "宕机", "正常吗")):
            return Action(type="service_status", reason="用户询问实时服务状态")
        if any(word in text for word in ("我的权限", "当前权限", "我的角色", "我是谁", "当前账号")):
            return Action(type="account_access", reason="用户询问当前身份或权限")
        if any(word in text for word in ("如何", "怎么", "申请", "配置", "流程", "规范", "文档")):
            return Action(type="knowledge_answer", arguments={"query": query}, reason="用户问题需要授权知识库证据")
        return Action(
            type="final",
            message="你好，我是企业 IT 服务台 Agent，可以帮你查询企业知识、服务状态、账号权限，并协助生成待确认工单。",
            reason="通用问候或能力咨询",
        )


class DeepSeekPlanner:
    system_prompt = """你是企业 IT 服务台 Agent 的规划器。只输出 JSON，不要输出 Markdown。
允许的 type 只有：knowledge_answer、service_status、account_access、ticket_draft、final、clarify。
知识问题必须使用 knowledge_answer；实时状态使用 service_status；用户权限使用 account_access；创建、提交、报障使用 ticket_draft。
如果只是问候或询问能力，使用 final 并填写 message。缺少产品、环境或错误码时使用 clarify。
JSON 格式：{"type":"...","arguments":{},"reason":"...","message":"..."}"""

    def __init__(self, llm: ChatOpenAI, fallback: Planner | None = None) -> None:
        self.llm = llm
        self.fallback = fallback or RulePlanner()
        self.structured = llm.with_structured_output(Action, method="json_mode")

    async def plan(self, query: str) -> Action:
        # Known business intents are fail-closed and deterministic. The model
        # is still used for ambiguous requests, but it cannot turn a clear
        # knowledge question into an identity lookup or a write operation.
        guarded = await self.fallback.plan(query)
        if guarded.type != "final":
            return guarded
        try:
            result = await self.structured.ainvoke([
                SystemMessage(content=self.system_prompt),
                HumanMessage(content=query),
            ])
            if isinstance(result, Action):
                return result
            return Action.model_validate(result)
        except Exception:
            return await self.fallback.plan(query)


class DeepSeekAnswerer:
    system_prompt = """你是企业 IT 服务台客服。基于给定证据回答问题，不能编造证据。
如果证据不足，要明确说明目前能确认的范围，同时给出下一步建议；不要简单地只回复“无法回答”。
回答简洁、可执行，引用编号由系统在外部展示。"""

    def __init__(self, llm: ChatOpenAI) -> None:
        self.llm = llm

    async def answer(self, query: str, evidence: list[dict[str, Any]]) -> str:
        context = "\n\n".join(
            f"[{index}] {item.get('title', '')}\n{item.get('content', '')}"
            for index, item in enumerate(evidence, start=1)
        )
        response = await self.llm.ainvoke([
            SystemMessage(content=self.system_prompt),
            HumanMessage(content=f"用户问题：{query}\n\n授权知识库证据：\n{context or '暂无命中证据'}"),
        ])
        content = response.content
        if isinstance(content, list):
            return "".join(str(part.get("text", part)) if isinstance(part, dict) else str(part) for part in content)
        return str(content)


class DeepSeekMedicalAnswerer(DeepSeekAnswerer):
    system_prompt = """你是面向医学工程师和设备运维人员的医疗设备知识助手。本项目中的 MediAxis/PulseCare 资料完全虚构。
只能依据给定的授权证据回答设备手册、型号、软件版本、错误码、兼容性和运维流程问题。
不得给出临床诊断、治疗、患者参数或真实设备操作建议；不得合并不同型号、版本或过期文档的结论。
回答应先说明适用型号与版本，再给出结论；证据不足或冲突时明确说明并要求补充信息。引用由系统在外部展示。"""


class RuleAnswerer:
    async def answer(self, query: str, evidence: list[dict[str, Any]]) -> str:
        if not evidence:
            return "我暂时没有检索到足够的企业资料。你可以补充产品、环境或错误码，我会继续帮你定位。"
        return "根据授权知识库，相关信息如下：\n\n" + "\n\n".join(
            f"- {item.get('content', '').strip()}" for item in evidence[:3] if item.get("content")
        )


def citations_from_hits(hits: list[dict[str, Any]]) -> list[Citation]:
    return [
        Citation(
            chunk_id=str(hit.get("chunk_id", "")),
            document_id=str(hit.get("document_id", "")),
            document=str(hit.get("title", "")),
            excerpt=str(hit.get("content", ""))[:240],
            dataset_id=str(hit.get("dataset_id", "")),
            version=str(hit.get("version", "")),
            document_revision=str(hit.get("document_revision", "")),
            source_file=str(hit.get("source_file", "")),
            source_page=int(hit.get("source_page", 0) or 0),
            source_sheet=str(hit.get("source_sheet", "")),
            source_cell_range=str(hit.get("source_cell_range", "")),
            heading_path=list(hit.get("heading_path") or []),
            model_codes=list(hit.get("model_codes") or []),
            software_version_from=str(hit.get("software_version_from", "")),
            software_version_to=str(hit.get("software_version_to", "")),
            effective_from=str(hit.get("effective_from", "")),
            effective_to=str(hit.get("effective_to", "")),
            authority_level=str(hit.get("authority_level", "")),
            supersedes=list(hit.get("supersedes") or []),
        )
        for hit in hits
    ]


def parse_json_object(value: Any) -> dict[str, Any]:
    if isinstance(value, dict):
        return value
    text = str(value)
    match = re.search(r"\{.*\}", text, re.DOTALL)
    if not match:
        raise ValueError("model response does not contain a JSON object")
    return json.loads(match.group(0))
