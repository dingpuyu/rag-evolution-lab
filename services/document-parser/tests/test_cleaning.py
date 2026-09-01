from __future__ import annotations

from app.cleaning import clean_blocks, normalize_text
from app.models import DocumentBlock, Provenance


def _block(text: str, page: int, y0: float, y1: float, *, confidence: float | None = None) -> DocumentBlock:
    return DocumentBlock(
        block_type="paragraph",
        text=text,
        provenance=Provenance(
            source_file="manual.pdf",
            page=page,
            bbox=(20, y0, 580, y1),
            page_width=600,
            page_height=800,
        ),
        confidence=confidence,
    )


def test_normalize_text_removes_ocr_artifacts_but_preserves_identifiers():
    assert normalize_text("SYS-\u200bNET-042\t  版本 2.6") == "SYS-NET-042 版本 2.6"
    assert normalize_text("inter-\nface") == "interface"


def test_cleaner_removes_repeated_margin_noise_and_page_numbers():
    blocks = []
    for page in range(1, 4):
        blocks.extend([
            _block("PulseCare Medical Devices", page, 10, 40),
            _block(f"第 {page} 页", page, 760, 790),
            _block(f"第 {page} 页的有效正文 SYS-NET-042", page, 160, 220),
        ])

    cleaned, quality, warnings, removals = clean_blocks(blocks)

    assert [block.text for block in cleaned] == [
        "第 1 页的有效正文 SYS-NET-042",
        "第 2 页的有效正文 SYS-NET-042",
        "第 3 页的有效正文 SYS-NET-042",
    ]
    assert quality.repeated_margin_blocks_removed == 6
    assert {removal.reason for removal in removals} == {"page_number", "repeated_margin"}
    assert {removal.block.text for removal in removals} >= {"PulseCare Medical Devices", "第 1 页"}
    assert any("header/footer" in warning for warning in warnings)


def test_cleaner_reports_low_ocr_confidence_without_silent_deletion():
    blocks = [_block("BAT-LOW-021", 1, 120, 160, confidence=0.52)]
    cleaned, quality, warnings, removals = clean_blocks(blocks, parser="paddle-ppstructurev3", ocr_used=True)
    assert cleaned[0].text == "BAT-LOW-021"
    assert quality.low_confidence_blocks == 1
    assert quality.mean_confidence == 0.52
    assert "human review" in warnings[0]
    assert removals == []


def test_cleaner_only_deduplicates_exact_text_at_the_same_visual_position():
    blocks = [
        _block("同一型号说明 VSM-100", 1, 120, 160),
        _block("同一型号说明 VSM-100", 1, 121, 161),
        _block("同一型号说明 VSM-100", 1, 420, 460),
        _block("同一型号说明 VSM-100", 2, 120, 160),
    ]

    cleaned, quality, _, removals = clean_blocks(blocks)

    assert len(cleaned) == 3
    assert quality.overlapping_duplicates_removed == 1
    assert removals[0].reason == "overlapping_duplicate"


def test_table_cleaning_preserves_rows_and_exact_identifiers():
    table = DocumentBlock(
        block_type="table",
        text="型号  |  版本\nVSM-100\t| 2.6\nBAT-LOW-021 | 保留",
        provenance=Provenance(source_file="manual.pdf", page=3),
    )

    cleaned, _, _, _ = clean_blocks([table])

    assert cleaned[0].text == "型号 | 版本\nVSM-100 | 2.6\nBAT-LOW-021 | 保留"
