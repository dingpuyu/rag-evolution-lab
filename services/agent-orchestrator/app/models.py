from __future__ import annotations

from typing import Any, Literal, TypedDict

from pydantic import BaseModel, Field


ActionType = Literal[
    "knowledge_answer",
    "service_status",
    "account_access",
    "ticket_draft",
    "final",
    "clarify",
]


class Action(BaseModel):
    type: ActionType
    arguments: dict[str, Any] = Field(default_factory=dict)
    reason: str = ""
    message: str = ""


class AgentRequest(BaseModel):
    environment_id: str | None = None
    query: str = Field(min_length=1, max_length=4000)
    thread_id: str | None = None
    confirmation: bool | None = None


class Citation(BaseModel):
    chunk_id: str = ""
    document_id: str = ""
    document: str = ""
    excerpt: str = ""


class AgentStep(BaseModel):
    step: int
    action: dict[str, Any]
    observation: dict[str, Any] | None = None


class AgentResult(BaseModel):
    status: Literal["completed", "needs_confirmation", "needs_clarification"]
    answer: str
    answer_source: Literal["rag", "persona", "tool", "confirmation"] = "persona"
    citations: list[Citation] = Field(default_factory=list)
    needs_confirmation: bool = False
    confirmation_id: str | None = None
    steps: list[AgentStep] = Field(default_factory=list)
    tool_calls: list[str] = Field(default_factory=list)


class AgentResponse(BaseModel):
    app_id: str
    environment_id: str
    thread_id: str
    result: AgentResult


class Identity(BaseModel):
    subject: str = ""
    tenant_id: str = ""
    roles: list[str] = Field(default_factory=list)
    scopes: list[str] = Field(default_factory=list)
    application_id: str | None = None
    expires_at: int | None = None


class SearchHit(BaseModel):
    chunk_id: str = ""
    document_id: str = ""
    title: str = ""
    content: str = ""
    distance: float | None = None


class AgentState(TypedDict, total=False):
    """Runtime state stored by LangGraph; bearer tokens are kept out of it."""

    thread_id: str
    app_id: str
    environment_id: str
    query: str
    action: dict[str, Any]
    evidence: list[dict[str, Any]]
    gateway_error: str
    observations: list[dict[str, Any]]
    steps: list[dict[str, Any]]
    response: dict[str, Any]
