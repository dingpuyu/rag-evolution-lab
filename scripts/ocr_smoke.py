#!/usr/bin/env python3
from __future__ import annotations

import json
import os

import httpx
import pymupdf


def scanned_fixture() -> bytes:
    source = pymupdf.open()
    page = source.new_page(width=800, height=1100)
    page.insert_text((70, 100), "AED DEVICE TROUBLESHOOTING", fontsize=28)
    page.insert_text((70, 180), "MODEL: BeneHeart C2", fontsize=20)
    page.insert_text((70, 240), "ERROR CODE: BAT-LOW-021", fontsize=20)
    page.insert_text((70, 300), "Connect AC power and inspect battery status.", fontsize=18)
    pixmap = page.get_pixmap(matrix=pymupdf.Matrix(2, 2), alpha=False)
    source.close()

    scanned = pymupdf.open()
    image_page = scanned.new_page(width=pixmap.width, height=pixmap.height)
    image_page.insert_image(image_page.rect, pixmap=pixmap)
    content = scanned.tobytes()
    scanned.close()
    return content


def main() -> None:
    parser_url = os.getenv("RAGLAB_DOCUMENT_PARSER_URL", "http://127.0.0.1:8070").rstrip("/")
    response = httpx.post(
        f"{parser_url}/v1/parse",
        files={"file": ("ocr-smoke-aed.pdf", scanned_fixture(), "application/pdf")},
        timeout=360,
    )
    response.raise_for_status()
    result = response.json()
    texts = "\n".join(block["text"] for block in result.get("blocks", []))
    assert result["status"] == "ready", result
    assert result["quality"]["ocr_used"] is True, result
    assert "BeneHeart" in texts and "BAT-LOW-021" in texts, result
    print(json.dumps({
        "status": result["status"],
        "blocks": len(result["blocks"]),
        "quality": result["quality"],
        "verified_identifiers": ["BeneHeart C2", "BAT-LOW-021"],
    }, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
