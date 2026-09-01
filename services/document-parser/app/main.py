from __future__ import annotations

import os

from fastapi import FastAPI, File, HTTPException, UploadFile

from .models import DocumentIR
from .ocr import OCRServiceError, PaddleOCRClient
from .parsers import UnsupportedDocument, parse_document


MAX_FILE_BYTES = 50 * 1024 * 1024
app = FastAPI(title="RagLab Document Parser", version="0.2.0")


def _ocr_client() -> PaddleOCRClient | None:
    base_url = os.getenv("RAGLAB_OCR_BACKEND_URL", "").strip()
    if not base_url:
        return None
    timeout = float(os.getenv("RAGLAB_OCR_TIMEOUT_SECONDS", "300"))
    return PaddleOCRClient(
        base_url,
        shared_secret=os.getenv("RAGLAB_OCR_SHARED_SECRET", "").strip(),
        timeout_seconds=timeout,
    )


@app.get("/healthz")
async def healthz() -> dict[str, str]:
    return {
        "status": "ok",
        "schema": "document-ir-v3",
        "ocr_backend": "configured" if _ocr_client() else "disabled",
    }


@app.post("/v1/parse", response_model=DocumentIR)
async def parse(file: UploadFile = File(...)) -> DocumentIR:
    content = await file.read(MAX_FILE_BYTES + 1)
    if len(content) > MAX_FILE_BYTES:
        raise HTTPException(status_code=413, detail="document exceeds 50 MiB limit")
    if not content:
        raise HTTPException(status_code=422, detail="document is empty")
    try:
        result = parse_document(file.filename or "upload", content, file.content_type or "")
        client = _ocr_client()
        if result.status == "ocr_required" and client is not None:
            try:
                return await client.parse_pdf(file.filename or "upload.pdf", content, file.content_type or "application/pdf")
            except OCRServiceError as exc:
                result.warnings.append(str(exc))
        return result
    except UnsupportedDocument as exc:
        raise HTTPException(status_code=415, detail=str(exc)) from exc
    except Exception as exc:
        raise HTTPException(status_code=422, detail=f"document parse failed: {exc}") from exc
