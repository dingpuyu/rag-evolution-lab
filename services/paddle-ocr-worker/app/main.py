from __future__ import annotations

from functools import lru_cache
import hashlib
import os
from pathlib import Path
import tempfile
from typing import Any

from fastapi import FastAPI, File, Header, HTTPException, UploadFile


MAX_FILE_BYTES = 50 * 1024 * 1024
app = FastAPI(title="RagLab PaddleOCR Worker", version="0.1.0")


def _enabled(name: str, default: bool) -> bool:
    value = os.getenv(name)
    if value is None:
        return default
    return value.strip().lower() in {"1", "true", "yes", "on"}


def _authorize(authorization: str | None) -> None:
    expected = os.getenv("RAGLAB_OCR_SHARED_SECRET", "").strip()
    if expected and authorization != f"Bearer {expected}":
        raise HTTPException(status_code=401, detail="invalid OCR worker credential")


@lru_cache(maxsize=1)
def _pipeline():
    # Import lazily so health checks remain cheap and model initialization only
    # happens when OCR is actually requested.
    from paddleocr import PPStructureV3

    return PPStructureV3(
        layout_detection_model_name=os.getenv("PADDLEOCR_LAYOUT_MODEL", "PP-DocLayout-S"),
        text_detection_model_name=os.getenv("PADDLEOCR_TEXT_DET_MODEL", "PP-OCRv5_mobile_det"),
        text_recognition_model_name=os.getenv("PADDLEOCR_TEXT_REC_MODEL", "PP-OCRv5_mobile_rec"),
        use_doc_orientation_classify=_enabled("PADDLEOCR_USE_ORIENTATION", True),
        use_doc_unwarping=_enabled("PADDLEOCR_USE_UNWARPING", False),
        use_textline_orientation=_enabled("PADDLEOCR_USE_TEXTLINE_ORIENTATION", False),
        use_table_recognition=_enabled("PADDLEOCR_USE_TABLE", True),
        use_seal_recognition=_enabled("PADDLEOCR_USE_SEAL", False),
        use_formula_recognition=_enabled("PADDLEOCR_USE_FORMULA", False),
        use_chart_recognition=_enabled("PADDLEOCR_USE_CHART", False),
        use_region_detection=_enabled("PADDLEOCR_USE_REGION", False),
    )


@app.get("/healthz")
async def healthz() -> dict[str, str]:
    return {"status": "ok", "engine": "PaddleOCR", "pipeline": "PP-StructureV3"}


@app.post("/v1/parse")
async def parse(
    file: UploadFile = File(...), authorization: str | None = Header(default=None)
) -> dict[str, Any]:
    _authorize(authorization)
    filename = Path(file.filename or "scan.pdf").name
    suffix = Path(filename).suffix.lower()
    if suffix not in {".pdf", ".png", ".jpg", ".jpeg", ".tif", ".tiff"}:
        raise HTTPException(status_code=415, detail="OCR worker supports PDF and image files only")
    content = await file.read(MAX_FILE_BYTES + 1)
    if len(content) > MAX_FILE_BYTES:
        raise HTTPException(status_code=413, detail="document exceeds 50 MiB limit")
    if not content:
        raise HTTPException(status_code=422, detail="document is empty")

    temporary_path = ""
    try:
        with tempfile.NamedTemporaryFile(suffix=suffix, delete=False) as temporary:
            temporary.write(content)
            temporary_path = temporary.name
        pages = list(_pipeline().predict(temporary_path))
        blocks, warnings = _to_blocks(filename, pages)
    except Exception as exc:
        raise HTTPException(status_code=422, detail=f"PP-StructureV3 parse failed: {exc}") from exc
    finally:
        if temporary_path:
            Path(temporary_path).unlink(missing_ok=True)

    if not blocks:
        warnings.append("PP-StructureV3 returned no indexable blocks")
    return {
        "schema_version": "document-ir-v4",
        "status": "ready" if blocks else "ocr_required",
        "source_file": filename,
        "mime_type": file.content_type or "application/octet-stream",
        "sha256": hashlib.sha256(content).hexdigest(),
        "blocks": blocks,
        "cleaning_removals": [],
        "warnings": warnings,
        "quality": {
            "parser": "paddle-ppstructurev3",
            "parser_version": _package_version(),
            "ocr_used": True,
            "input_blocks": len(blocks),
            "output_blocks": len(blocks),
        },
    }


def _to_blocks(filename: str, results: list[Any]) -> tuple[list[dict[str, Any]], list[str]]:
    blocks: list[dict[str, Any]] = []
    warnings: list[str] = []
    headings: list[str] = []
    for fallback_page, result in enumerate(results, start=1):
        payload = result.json
        page = payload.get("res", payload)
        page_number = int(page.get("page_index", fallback_page - 1)) + 1
        width, height = float(page.get("width", 0)), float(page.get("height", 0))
        scores, page_mean_confidence = _ocr_scores(page.get("overall_ocr_res") or {})
        ordered = sorted(page.get("parsing_res_list") or [], key=lambda item: item.get("block_order", 0))
        for item in ordered:
            text = str(item.get("block_content") or "").strip()
            if not text:
                continue
            label = str(item.get("block_label") or "text")
            block_type = _block_type(label)
            if block_type == "heading":
                headings = [text]
            bbox = item.get("block_bbox")
            blocks.append({
                "block_type": block_type,
                "text": text,
                "heading_path": list(headings),
                "provenance": {
                    "source_file": filename,
                    "page": page_number,
                    "sheet": "",
                    "cell_range": "",
                    "bbox": bbox if isinstance(bbox, list) and len(bbox) == 4 else None,
                    "page_width": width,
                    "page_height": height,
                },
                # Table/formula blocks may not have a one-to-one OCR text row;
                # use the page mean instead of silently omitting quality data.
                "confidence": scores.get(_score_key(text), page_mean_confidence),
            })
    return blocks, warnings


def _block_type(label: str) -> str:
    normalized = label.casefold()
    if normalized in {"doc_title", "paragraph_title", "title", "section_title"}:
        return "heading"
    if "table" in normalized:
        return "table"
    if "list" in normalized:
        return "list"
    if "formula" in normalized or "code" in normalized:
        return "code"
    return "paragraph"


def _ocr_scores(result: dict[str, Any]) -> tuple[dict[str, float], float | None]:
    texts = result.get("rec_texts") or []
    scores = result.get("rec_scores") or []
    mapped = {_score_key(str(text)): float(score) for text, score in zip(texts, scores)}
    mean = sum(mapped.values()) / len(mapped) if mapped else None
    return mapped, mean


def _score_key(value: str) -> str:
    return "".join(value.split()).casefold()


def _package_version() -> str:
    try:
        import paddleocr

        return str(paddleocr.__version__)
    except Exception:
        return "unknown"
