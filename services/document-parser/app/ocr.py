from __future__ import annotations

import hashlib
from pathlib import Path

import httpx

from .cleaning import clean_blocks
from .models import DocumentIR


class OCRServiceError(RuntimeError):
    pass


class PaddleOCRClient:
    """Adapter for a separately deployed PP-StructureV3 worker.

    Paddle dependencies and model memory stay outside the stateless format
    parser. The internal contract is Document IR, so another OCR engine can be
    substituted without changing ingestion or indexing code.
    """

    def __init__(self, base_url: str, *, shared_secret: str = "", timeout_seconds: float = 300) -> None:
        self.base_url = base_url.rstrip("/")
        self.shared_secret = shared_secret
        self.timeout_seconds = timeout_seconds

    async def parse_pdf(self, filename: str, content: bytes, content_type: str) -> DocumentIR:
        headers = {}
        if self.shared_secret:
            headers["Authorization"] = f"Bearer {self.shared_secret}"
        try:
            async with httpx.AsyncClient(timeout=self.timeout_seconds) as client:
                response = await client.post(
                    f"{self.base_url}/v1/parse",
                    files={"file": (Path(filename).name, content, content_type or "application/pdf")},
                    headers=headers,
                )
        except httpx.HTTPError as exc:
            raise OCRServiceError(f"PaddleOCR worker is unavailable: {exc}") from exc
        if response.status_code >= 400:
            raise OCRServiceError(f"PaddleOCR worker returned {response.status_code}: {response.text[:500]}")
        try:
            result = DocumentIR.model_validate(response.json())
        except Exception as exc:
            raise OCRServiceError(f"PaddleOCR worker returned an invalid Document IR: {exc}") from exc

        # Source identity is owned by the parser/ingestion boundary, not by the
        # worker's temporary file path.
        source_file = Path(filename).name
        normalized = []
        for block in result.blocks:
            provenance = block.provenance.model_copy(update={"source_file": source_file})
            normalized.append(block.model_copy(update={"provenance": provenance}))
        normalized, quality, warnings, cleaning_removals = clean_blocks(
            normalized,
            parser=result.quality.parser or "paddle-ppstructurev3",
            parser_version=result.quality.parser_version or "unknown",
            ocr_used=True,
        )
        if result.status != "ready" or not normalized:
            status = "ocr_required"
        elif quality.low_confidence_blocks:
            status = "review_required"
        else:
            status = "ready"
        return result.model_copy(
            update={
                # The parser boundary owns the externally visible IR version.
                # This also upgrades responses from an older compatible OCR
                # worker after the v4 cleaner deletion audit has been applied.
                "schema_version": "document-ir-v4",
                "status": status,
                "source_file": source_file,
                "mime_type": content_type or "application/pdf",
                "sha256": hashlib.sha256(content).hexdigest(),
                "blocks": normalized,
                "cleaning_removals": list(result.cleaning_removals) + cleaning_removals,
                "warnings": list(result.warnings) + warnings,
                "quality": quality,
            }
        )
