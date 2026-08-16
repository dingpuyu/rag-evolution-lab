from __future__ import annotations

from fastapi import FastAPI, File, HTTPException, UploadFile

from .models import DocumentIR
from .parsers import UnsupportedDocument, parse_document


MAX_FILE_BYTES = 50 * 1024 * 1024
app = FastAPI(title="RagLab Document Parser", version="0.1.0")


@app.get("/healthz")
async def healthz() -> dict[str, str]:
    return {"status": "ok", "schema": "document-ir-v1"}


@app.post("/v1/parse", response_model=DocumentIR)
async def parse(file: UploadFile = File(...)) -> DocumentIR:
    content = await file.read(MAX_FILE_BYTES + 1)
    if len(content) > MAX_FILE_BYTES:
        raise HTTPException(status_code=413, detail="document exceeds 50 MiB limit")
    if not content:
        raise HTTPException(status_code=422, detail="document is empty")
    try:
        return parse_document(file.filename or "upload", content, file.content_type or "")
    except UnsupportedDocument as exc:
        raise HTTPException(status_code=415, detail=str(exc)) from exc
    except Exception as exc:
        raise HTTPException(status_code=422, detail=f"document parse failed: {exc}") from exc
