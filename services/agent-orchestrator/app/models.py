from __future__ import annotations

from typing import Any, Literal, TypedDict

from pydantic import BaseModel, Field, field_validator


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
    device_context: DeviceContext = Field(default_factory=lambda: DeviceContext())


class DeviceContext(BaseModel):
    model_code: str = ""
    software_version: str = ""
    lot_or_batch: str = ""
    region: str = "CN"


class Citation(BaseModel):
    chunk_id: str = ""
    document_id: str = ""
    document: str = ""
    excerpt: str = ""
    dataset_id: str = ""
    version: str = ""
    document_revision: str = ""
    source_file: str = ""
    source_page: int = 0
    source_sheet: str = ""
    source_cell_range: str = ""
    heading_path: list[str] = Field(default_factory=list)
    model_codes: list[str] = Field(default_factory=list)
    software_version_from: str = ""
    software_version_to: str = ""
    effective_from: str = ""
    effective_to: str = ""
    authority_level: str = ""
    supersedes: list[str] = Field(default_factory=list)


class AgentStep(BaseModel):
    step: int
    action: dict[str, Any]
    observation: dict[str, Any] | None = None


class ContextUsage(BaseModel):
    candidate_chunks: int = 0
    selected_chunks: int = 0
    estimated_tokens: int = 0
    token_budget: int = 0
    truncated: bool = False


class AgentResult(BaseModel):
    status: Literal["completed", "needs_confirmation", "needs_clarification", "refused"]
    answer: str
    answer_source: Literal["rag", "persona", "tool", "confirmation"] = "persona"
    citations: list[Citation] = Field(default_factory=list)
    needs_confirmation: bool = False
    confirmation_id: str | None = None
    steps: list[AgentStep] = Field(default_factory=list)
    tool_calls: list[str] = Field(default_factory=list)
    decision: Literal["answer", "clarify", "refuse"] = "answer"
    reason_code: str = ""
    resolved_context: DeviceContext = Field(default_factory=lambda: DeviceContext())
    candidate_entities: list[str] = Field(default_factory=list)
    suggested_questions: list[str] = Field(default_factory=list)
    trace_id: str = ""
    context_usage: ContextUsage | None = None


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
    dataset_id: str = ""
    version: str = ""
    status: str = ""
    model_codes: list[str] = Field(default_factory=list)
    software_version_from: str = ""
    software_version_to: str = ""
    effective_from: str = ""
    effective_to: str = ""
    authority_level: str = ""
    supersedes: list[str] = Field(default_factory=list)
    document_revision: str = ""
    source_file: str = ""
    source_page: int = 0
    source_sheet: str = ""
    source_cell_range: str = ""
    heading_path: list[str] = Field(default_factory=list)
    affected_lots: list[str] = Field(default_factory=list)

    @field_validator("model_codes", "supersedes", "heading_path", "affected_lots", mode="before")
    @classmethod
    def normalize_optional_lists(cls, value: Any) -> Any:
        # Milvus dynamic JSON fields can be returned as null when an older or
        # non-applicable document has no value. Treat null as an empty list so
        # valid evidence is not discarded at the Agent boundary.
        return [] if value is None else value


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
    is_medical: bool
    is_customer: bool
    medical_intent: str
    reason_code: str
    resolved_context: dict[str, Any]
    candidate_entities: list[str]
    trace_id: str
    evidence_verification: str
    context_token_budget: int
    context_usage: dict[str, Any]
