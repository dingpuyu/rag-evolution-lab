from __future__ import annotations

from app.main import _document_status, _to_blocks


class FakeResult:
    def __init__(self, payload):
        self.json = {"res": payload}


def test_ppstructure_mapping_preserves_order_heading_bbox_and_quality():
    blocks, warnings = _to_blocks("scan.pdf", [FakeResult({
        "page_index": 0,
        "width": 1200,
        "height": 1600,
        "parsing_res_list": [
            {"block_label": "table", "block_content": "型号 | 版本\nVSM-100 | 2.6", "block_bbox": [40, 300, 1100, 700], "block_order": 2},
            {"block_label": "doc_title", "block_content": "兼容矩阵", "block_bbox": [40, 60, 500, 140], "block_order": 1},
        ],
        "overall_ocr_res": {"rec_texts": ["兼容矩阵", "VSM-100", "2.6"], "rec_scores": [0.99, 0.96, 0.93]},
    })])

    assert warnings == []
    assert [block["block_type"] for block in blocks] == ["heading", "table"]
    assert blocks[1]["heading_path"] == ["兼容矩阵"]
    assert blocks[1]["provenance"]["bbox"] == [40, 300, 1100, 700]
    assert blocks[1]["confidence"] == 0.96


def test_degraded_text_fallback_requires_review_instead_of_silent_publish():
    blocks = [{"block_type": "paragraph", "text": "degraded OCR"}]
    assert _document_status(blocks, degraded=True) == "review_required"
    assert _document_status(blocks, degraded=False) == "ready"
    assert _document_status([], degraded=True) == "ocr_required"


def test_null_block_order_uses_visual_fallback_without_crashing_page():
    blocks, _ = _to_blocks("low-contrast.pdf", [FakeResult({
        "page_index": 0,
        "width": 1200,
        "height": 1600,
        "parsing_res_list": [
            {"block_label": "text", "block_content": "第二行", "block_bbox": [40, 300, 500, 380], "block_order": None},
            {"block_label": "text", "block_content": "第一行", "block_bbox": [40, 200, 500, 280], "block_order": 1},
        ],
        "overall_ocr_res": {"rec_texts": ["第一行", "第二行"], "rec_scores": [0.91, 0.88]},
    })])
    assert [item["text"] for item in blocks] == ["第一行", "第二行"]
