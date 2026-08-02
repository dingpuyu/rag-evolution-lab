from __future__ import annotations

import uuid

from fastapi import FastAPI, Header, HTTPException, Path
from fastapi.middleware.cors import CORSMiddleware

from .config import get_settings
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

app = FastAPI(title="RagLab LangGraph Agent", version="0.1.0")
app.add_middleware(
    CORSMiddleware,
    allow_origins=settings.cors_origins,
    allow_credentials=False,
    allow_methods=["GET", "POST", "OPTIONS"],
    allow_headers=["Authorization", "Content-Type", "Accept"],
)


@app.get("/healthz")
async def healthz() -> dict[str, str]:
    return {"status": "ok", "runtime": "langgraph", "model": settings.deepseek_model if settings.model_api_key else "rule-fallback"}


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
        )
    except Exception as exc:
        raise HTTPException(status_code=422, detail=str(exc)) from exc

