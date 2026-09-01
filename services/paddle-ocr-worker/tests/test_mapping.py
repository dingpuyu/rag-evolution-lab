from __future__ import annotations

from app.main import _to_blocks


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
