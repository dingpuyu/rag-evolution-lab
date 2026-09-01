from __future__ import annotations

import httpx
import pymupdf
import pytest

from app.ocr import PaddleOCRClient


@pytest.mark.asyncio
async def test_paddle_adapter_normalizes_worker_document_ir(monkeypatch):
    payload = {
        "schema_version": "document-ir-v3",
        "status": "ready",
        "source_file": "temporary.pdf",
        "mime_type": "application/pdf",
        "sha256": "worker-hash",
        "blocks": [{
            "block_type": "heading",
            "text": "AED\u200b 设备故障排查",
            "heading_path": ["AED 设备故障排查"],
            "provenance": {
                "source_file": "temporary.pdf",
                "page": 1,
                "sheet": "",
                "cell_range": "",
                "bbox": [100, 100, 600, 180],
                "page_width": 1600,
                "page_height": 2200,
            },
            "confidence": 0.99,
        }],
        "warnings": [],
        "quality": {"parser": "paddle-ppstructurev3", "parser_version": "3.7.0", "ocr_used": True},
    }

    class FakeAsyncClient:
        def __init__(self, *args, **kwargs):
            pass

        async def __aenter__(self):
            return self

        async def __aexit__(self, *args):
            return None

        async def post(self, *args, **kwargs):
            return httpx.Response(200, json=payload)

    monkeypatch.setattr(httpx, "AsyncClient", FakeAsyncClient)
    result = await PaddleOCRClient("http://paddle-ocr:8071").parse_pdf(
        "customer-scan.pdf", b"pdf-content", "application/pdf"
    )

    assert result.status == "ready"
    assert result.source_file == "customer-scan.pdf"
    assert result.blocks[0].text == "AED 设备故障排查"
    assert result.blocks[0].provenance.source_file == "customer-scan.pdf"
    assert result.quality.ocr_used is True
    assert result.quality.mean_confidence == 0.99


def test_blank_scanned_pdf_stays_fail_closed_without_worker():
    document = pymupdf.open()
    document.new_page()
    content = document.tobytes()
    document.close()
    from app.parsers import parse_document

    result = parse_document("scan.pdf", content)
    assert result.status == "ocr_required"
    assert result.blocks == []
