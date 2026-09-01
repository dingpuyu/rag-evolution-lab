from __future__ import annotations

from collections import defaultdict
import re

from .models import DocumentBlock, ParseQuality


ZERO_WIDTH = re.compile(r"[\u200b\u200c\u200d\u2060\ufeff]")
ASCII_LINE_HYPHEN = re.compile(r"(?<=[A-Za-z])-\s*\n\s*(?=[A-Za-z])")
INLINE_SPACE = re.compile(r"[\t\u00a0 ]+")
PAGE_NUMBER = re.compile(r"^(?:第\s*)?\d{1,4}(?:\s*页)?(?:\s*/\s*\d{1,4})?$")


def clean_blocks(
    blocks: list[DocumentBlock],
    *,
    parser: str = "native",
    parser_version: str = "document-parser-0.2",
    ocr_used: bool = False,
    low_confidence_threshold: float = 0.60,
) -> tuple[list[DocumentBlock], ParseQuality, list[str]]:
    """Clean layout blocks without destroying identifiers or provenance.

    The cleaner deliberately avoids broad Unicode compatibility conversion and
    fuzzy de-duplication. Both can silently alter model codes, error codes or
    version strings. Every removal rule is deterministic and auditable.
    """

    normalized_blocks = 0
    cleaned: list[DocumentBlock] = []
    for block in blocks:
        value = normalize_text(block.text, preserve_lines=block.block_type in {"table", "code"})
        if not value:
            continue
        if value != block.text:
            normalized_blocks += 1
        cleaned.append(block.model_copy(update={"text": value}))

    repeated_margin_indexes = _repeated_margin_indexes(cleaned)
    without_margins = [block for index, block in enumerate(cleaned) if index not in repeated_margin_indexes]

    deduplicated: list[DocumentBlock] = []
    overlapping_duplicates = 0
    for block in without_margins:
        if deduplicated and _same_overlapping_block(deduplicated[-1], block):
            overlapping_duplicates += 1
            continue
        deduplicated.append(block)

    confidences = [block.confidence for block in deduplicated if block.confidence is not None]
    low_confidence = sum(value < low_confidence_threshold for value in confidences)
    mean_confidence = round(sum(confidences) / len(confidences), 6) if confidences else None
    warnings: list[str] = []
    if repeated_margin_indexes:
        warnings.append(f"removed {len(repeated_margin_indexes)} repeated header/footer or page-number blocks")
    if overlapping_duplicates:
        warnings.append(f"removed {overlapping_duplicates} overlapping duplicate blocks")
    if low_confidence:
        warnings.append(
            f"{low_confidence} OCR blocks are below confidence threshold {low_confidence_threshold:.2f}; human review is required"
        )

    quality = ParseQuality(
        parser=parser,
        parser_version=parser_version,
        ocr_used=ocr_used,
        input_blocks=len(blocks),
        output_blocks=len(deduplicated),
        normalized_blocks=normalized_blocks,
        repeated_margin_blocks_removed=len(repeated_margin_indexes),
        overlapping_duplicates_removed=overlapping_duplicates,
        low_confidence_blocks=low_confidence,
        mean_confidence=mean_confidence,
    )
    return deduplicated, quality, warnings


def normalize_text(value: str, *, preserve_lines: bool = False) -> str:
    value = value.replace("\r\n", "\n").replace("\r", "\n")
    value = ZERO_WIDTH.sub("", value).replace("\u00ad", "")
    value = ASCII_LINE_HYPHEN.sub("", value)
    lines = [INLINE_SPACE.sub(" ", line).strip() for line in value.split("\n")]
    if preserve_lines:
        return "\n".join(line for line in lines if line).strip()
    return " ".join(line for line in lines if line).strip()


def _repeated_margin_indexes(blocks: list[DocumentBlock]) -> set[int]:
    candidates: dict[str, list[tuple[int, int]]] = defaultdict(list)
    remove: set[int] = set()
    for index, block in enumerate(blocks):
        provenance = block.provenance
        if provenance.page <= 0 or not _is_margin(block):
            continue
        key = _margin_key(block.text)
        if not key:
            continue
        if PAGE_NUMBER.fullmatch(key):
            remove.add(index)
            continue
        candidates[key].append((index, provenance.page))
    for occurrences in candidates.values():
        if len({page for _, page in occurrences}) >= 3:
            remove.update(index for index, _ in occurrences)
    return remove


def _margin_key(value: str) -> str:
    value = normalize_text(value).casefold()
    return value if 0 < len(value) <= 120 else ""


def _is_margin(block: DocumentBlock) -> bool:
    provenance = block.provenance
    if provenance.bbox is None or provenance.page_height <= 0:
        return False
    _, y0, _, y1 = provenance.bbox
    return y1 <= provenance.page_height * 0.12 or y0 >= provenance.page_height * 0.88


def _same_overlapping_block(left: DocumentBlock, right: DocumentBlock) -> bool:
    if left.provenance.page != right.provenance.page or left.text != right.text:
        return False
    if left.provenance.bbox is None or right.provenance.bbox is None:
        return False
    return _intersection_over_union(left.provenance.bbox, right.provenance.bbox) >= 0.80


def _intersection_over_union(
    left: tuple[float, float, float, float], right: tuple[float, float, float, float]
) -> float:
    x0, y0 = max(left[0], right[0]), max(left[1], right[1])
    x1, y1 = min(left[2], right[2]), min(left[3], right[3])
    intersection = max(0.0, x1 - x0) * max(0.0, y1 - y0)
    if intersection == 0:
        return 0.0
    left_area = max(0.0, left[2] - left[0]) * max(0.0, left[3] - left[1])
    right_area = max(0.0, right[2] - right[0]) * max(0.0, right[3] - right[1])
    union = left_area + right_area - intersection
    return intersection / union if union > 0 else 0.0
