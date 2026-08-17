from __future__ import annotations

import asyncio
import json
import uuid

from fastapi import FastAPI, Header, HTTPException, Path
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import JSONResponse, StreamingResponse
from pydantic import BaseModel, Field

from .config import get_settings
from .evaluation import BAD_CASE_ROOT_CAUSES, BAD_CASE_STATUSES, EvaluationStore, run_suite, verify_bad_case
from .gateway import KnowledgeGatewayClient
from .graph import AgentRuntime, build_runtime
from .llm import build_llm
from .models import AgentRequest, AgentResponse

settings = get_settings()
gateway = KnowledgeGatewayClient(settings.raglab_api_url)
runtime: AgentRuntime = build_runtime(
    gateway,
    build_llm(settings.model_api_key, settings.deepseek_base_url, settings.deepseek_model),
    max_steps=settings.agent_max_steps,
)
evaluation_store = EvaluationStore(settings.raglab_postgres_url)


class MedicalEvaluationRequest(BaseModel):
    app_id: str = ""
    environment_id: str = ""


class MedicalCaseReviewRequest(BaseModel):
    review_status: str = "bad_case"
    root_cause: str = "retrieval_or_decision_mismatch"
    human_note: str = ""


class MedicalBadCaseCreateRequest(BaseModel):
    root_cause: str = "other"
    resolution_note: str = ""


class MedicalBadCaseUpdateRequest(BaseModel):
    root_cause: str = "other"
    resolution_note: str = ""
    expected_document_ids: list[str] = Field(default_factory=list)
    expected_source_locations: list[dict] = Field(default_factory=list)
    device_context: dict[str, str] = Field(default_factory=dict)

app = FastAPI(title="RagLab LangGraph Agent", version="0.1.0")
app.add_middleware(
    CORSMiddleware,
    allow_origins=settings.cors_origins,
    allow_credentials=False,
    allow_methods=["GET", "POST", "PATCH", "OPTIONS"],
    allow_headers=["Authorization", "Content-Type", "Accept"],
)


async def evaluation_identity(authorization: str | None):
    if not authorization or not authorization.strip():
        raise HTTPException(status_code=401, detail="Authorization header is required")
    try:
        identity = await gateway.identity(authorization)
    except Exception as exc:
        raise HTTPException(status_code=401, detail="valid identity is required") from exc
    if not any(role in ("admin", "platform_admin") for role in identity.roles):
        raise HTTPException(status_code=403, detail="medical evaluation requires an administrator")
    return identity


@app.get("/healthz")
async def healthz() -> dict[str, str]:
    return {"status": "ok", "runtime": "langgraph", "model": settings.deepseek_model if settings.model_api_key else "rule-fallback"}


@app.post("/api/v1/evaluations/medical-device/runs")
async def create_medical_evaluation(
    request: MedicalEvaluationRequest,
    authorization: str | None = Header(default=None),
) -> JSONResponse:
    identity = await evaluation_identity(authorization)
    app_id = request.app_id.strip() or f"{identity.tenant_id}-medical-device-agent"
    environment_id = request.environment_id.strip() or f"{app_id}-dev"
    allowed_apps = {
        f"{identity.tenant_id}-medical-device-agent",
        f"{identity.tenant_id}-medical-device-customer-agent",
    }
    if "platform_admin" not in identity.roles and app_id not in allowed_apps:
        raise HTTPException(status_code=403, detail="evaluation app must belong to the authenticated tenant")
    run = await evaluation_store.create(identity.tenant_id, identity.subject, app_id, environment_id)
    # The bearer exists only in this in-process task and is never persisted.
    asyncio.create_task(run_suite(evaluation_store, run, runtime, authorization or ""))
    return JSONResponse(status_code=202, content=run)


@app.get("/api/v1/evaluations/medical-device/runs/latest")
async def get_latest_medical_evaluation(
    app_id: str = "",
    environment_id: str = "",
    authorization: str | None = Header(default=None),
) -> dict:
    identity = await evaluation_identity(authorization)
    allowed_apps = {
        f"{identity.tenant_id}-medical-device-agent",
        f"{identity.tenant_id}-medical-device-customer-agent",
    }
    if app_id and "platform_admin" not in identity.roles and app_id not in allowed_apps:
        raise HTTPException(status_code=403, detail="evaluation app must belong to the authenticated tenant")
    try:
        return await evaluation_store.latest(identity.tenant_id, app_id.strip(), environment_id.strip())
    except KeyError as exc:
        raise HTTPException(status_code=404, detail="evaluation run not found") from exc


@app.get("/api/v1/evaluations/runs/{run_id}")
async def get_medical_evaluation(run_id: str, authorization: str | None = Header(default=None)) -> dict:
    identity = await evaluation_identity(authorization)
    try:
        return await evaluation_store.get(run_id, identity.tenant_id, allow_any="platform_admin" in identity.roles)
    except KeyError as exc:
        raise HTTPException(status_code=404, detail="evaluation run not found") from exc


@app.get("/api/v1/evaluations/runs/{run_id}/cases")
async def get_medical_evaluation_cases(run_id: str, authorization: str | None = Header(default=None)) -> dict:
    await get_medical_evaluation(run_id, authorization)
    return {"run_id": run_id, "cases": await evaluation_store.list_cases(run_id)}


@app.get("/api/v1/evaluations/runs/{run_id}/events")
async def get_medical_evaluation_events(run_id: str, authorization: str | None = Header(default=None)) -> dict:
    await get_medical_evaluation(run_id, authorization)
    return {"run_id": run_id, "events": await evaluation_store.list_events(run_id)}


@app.patch("/api/v1/evaluations/runs/{run_id}/cases/{case_id}")
async def review_medical_evaluation_case(
    request: MedicalCaseReviewRequest,
    run_id: str,
    case_id: str,
    authorization: str | None = Header(default=None),
) -> dict:
    await get_medical_evaluation(run_id, authorization)
    if request.review_status not in {"unreviewed", "confirmed", "bad_case", "fixed"}:
        raise HTTPException(status_code=400, detail="unsupported review_status")
    try:
        return await evaluation_store.review_case(
            run_id, case_id, request.review_status, request.root_cause.strip()[:160], request.human_note.strip()[:2000],
        )
    except KeyError as exc:
        raise HTTPException(status_code=404, detail="evaluation case not found") from exc


@app.post("/api/v1/evaluations/runs/{run_id}/cases/{case_id}/bad-case")
async def create_medical_bad_case(
    request: MedicalBadCaseCreateRequest,
    run_id: str,
    case_id: str,
    authorization: str | None = Header(default=None),
) -> dict:
    identity = await evaluation_identity(authorization)
    try:
        run = await evaluation_store.get(run_id, identity.tenant_id, allow_any="platform_admin" in identity.roles)
    except KeyError as exc:
        raise HTTPException(status_code=404, detail="evaluation run not found") from exc
    cases = await evaluation_store.list_cases(run_id)
    case = next((item for item in cases if item["case_id"] == case_id), None)
    if case is None:
        raise HTTPException(status_code=404, detail="evaluation case not found")
    root_cause = request.root_cause.strip() or "other"
    if root_cause not in BAD_CASE_ROOT_CAUSES:
        raise HTTPException(status_code=400, detail="unsupported root_cause")
    return await evaluation_store.create_bad_case(
        run, case, identity.subject, root_cause, request.resolution_note.strip()[:4000],
    )


@app.get("/api/v1/evaluations/medical-device/bad-cases")
async def list_medical_bad_cases(
    app_id: str = "",
    status: str = "",
    authorization: str | None = Header(default=None),
) -> dict:
    identity = await evaluation_identity(authorization)
    if status and status not in BAD_CASE_STATUSES:
        raise HTTPException(status_code=400, detail="unsupported bad case status")
    allowed_apps = {
        f"{identity.tenant_id}-medical-device-agent",
        f"{identity.tenant_id}-medical-device-customer-agent",
    }
    if app_id and "platform_admin" not in identity.roles and app_id not in allowed_apps:
        raise HTTPException(status_code=403, detail="bad case app must belong to the authenticated tenant")
    cases = await evaluation_store.list_bad_cases(identity.tenant_id, app_id.strip(), status.strip())
    return {"cases": cases, "total": len(cases)}


@app.patch("/api/v1/evaluations/medical-device/bad-cases/{bad_case_id}")
async def update_medical_bad_case(
    request: MedicalBadCaseUpdateRequest,
    bad_case_id: str,
    authorization: str | None = Header(default=None),
) -> dict:
    identity = await evaluation_identity(authorization)
    root_cause = request.root_cause.strip() or "other"
    if root_cause not in BAD_CASE_ROOT_CAUSES:
        raise HTTPException(status_code=400, detail="unsupported root_cause")
    document_ids = list(dict.fromkeys(value.strip() for value in request.expected_document_ids if value.strip()))[:20]
    try:
        return await evaluation_store.update_bad_case(
            bad_case_id, identity.tenant_id, identity.subject, root_cause,
            request.resolution_note.strip()[:4000], document_ids,
            request.expected_source_locations[:20], request.device_context,
        )
    except KeyError as exc:
        raise HTTPException(status_code=404, detail="bad case not found") from exc


@app.post("/api/v1/evaluations/medical-device/bad-cases/{bad_case_id}/verify")
async def verify_medical_bad_case(
    bad_case_id: str,
    authorization: str | None = Header(default=None),
) -> dict:
    identity = await evaluation_identity(authorization)
    try:
        item = await evaluation_store.get_bad_case(bad_case_id, identity.tenant_id)
        result = await verify_bad_case(item, runtime, authorization or "")
        return await evaluation_store.record_bad_case_verification(bad_case_id, result, identity.subject)
    except KeyError as exc:
        raise HTTPException(status_code=404, detail="bad case not found") from exc
    except ValueError as exc:
        raise HTTPException(status_code=409, detail=str(exc)) from exc


@app.post("/api/v1/evaluations/medical-device/bad-cases/{bad_case_id}/promote")
async def promote_medical_bad_case(
    bad_case_id: str,
    authorization: str | None = Header(default=None),
) -> dict:
    identity = await evaluation_identity(authorization)
    try:
        return await evaluation_store.promote_bad_case(bad_case_id, identity.tenant_id, identity.subject)
    except KeyError as exc:
        raise HTTPException(status_code=404, detail="bad case not found") from exc
    except ValueError as exc:
        raise HTTPException(status_code=409, detail=str(exc)) from exc


@app.get("/api/v1/evaluations/medical-device/bad-cases/{bad_case_id}/attempts")
async def list_medical_bad_case_attempts(
    bad_case_id: str,
    authorization: str | None = Header(default=None),
) -> dict:
    identity = await evaluation_identity(authorization)
    try:
        attempts = await evaluation_store.list_bad_case_attempts(bad_case_id, identity.tenant_id)
        return {"bad_case_id": bad_case_id, "attempts": attempts}
    except KeyError as exc:
        raise HTTPException(status_code=404, detail="bad case not found") from exc


@app.post("/api/v1/apps/{app_id}/agent/answer", response_model=AgentResponse)
async def answer(
    request: AgentRequest,
    app_id: str = Path(min_length=1, max_length=160),
    authorization: str | None = Header(default=None),
) -> AgentResponse:
    if not authorization or not authorization.strip():
        raise HTTPException(status_code=401, detail="Authorization header is required")
    environment_id = (request.environment_id or f"{app_id}-dev").strip()
    if not environment_id:
        raise HTTPException(status_code=400, detail="environment_id must not be empty")
    try:
        return await runtime.run(
            app_id=app_id,
            environment_id=environment_id,
            query=request.query.strip(),
            authorization=authorization,
            thread_id=request.thread_id or f"thread-{uuid.uuid4().hex}",
            confirmation=request.confirmation,
            device_context=request.device_context,
        )
    except Exception as exc:
        raise HTTPException(status_code=422, detail=str(exc)) from exc


@app.post("/api/v1/apps/{app_id}/agent/answer/stream")
async def answer_stream(
    request: AgentRequest,
    app_id: str = Path(min_length=1, max_length=160),
    authorization: str | None = Header(default=None),
) -> StreamingResponse:
    if not authorization or not authorization.strip():
        raise HTTPException(status_code=401, detail="Authorization header is required")
    environment_id = (request.environment_id or f"{app_id}-dev").strip()

    def event(name: str, payload: dict) -> str:
        return f"event: {name}\ndata: {json.dumps(payload, ensure_ascii=False)}\n\n"

    async def generate():
        try:
            yield event("step", {"name": "classify_scope", "status": "running"})
            response = await runtime.run(
                app_id=app_id,
                environment_id=environment_id,
                query=request.query.strip(),
                authorization=authorization,
                thread_id=request.thread_id or f"thread-{uuid.uuid4().hex}",
                confirmation=request.confirmation,
                device_context=request.device_context,
            )
            result = response.result
            for step in result.steps:
                yield event("step", step.model_dump(exclude_none=True))
            if result.citations:
                yield event("retrieval", {"trace_id": result.trace_id, "hits": len(result.citations)})
            answer_text = result.answer
            for offset in range(0, len(answer_text), 24):
                yield event("token", {"text": answer_text[offset : offset + 24]})
                await asyncio.sleep(0)
            for citation in result.citations:
                yield event("citation", citation.model_dump())
            yield event("decision", {
                "decision": result.decision,
                "status": result.status,
                "reason_code": result.reason_code,
                "resolved_context": result.resolved_context.model_dump(),
                "candidate_entities": result.candidate_entities,
            })
            yield event("done", response.model_dump())
        except Exception as exc:
            yield event("error", {"message": str(exc)})

    return StreamingResponse(
        generate(),
        media_type="text/event-stream",
        headers={"Cache-Control": "no-cache, no-transform", "X-Accel-Buffering": "no"},
    )
