from __future__ import annotations

from typing import Literal

from pydantic import BaseModel, Field


class Provenance(BaseModel):
    source_file: str
    page: int = 0
    sheet: str = ""
    cell_range: str = ""
    bbox: tuple[float, float, float, float] | None = None
    page_width: float = 0
    page_height: float = 0


class DocumentBlock(BaseModel):
    block_type: Literal["heading", "paragraph", "list", "table", "code"]
    text: str
    heading_path: list[str] = Field(default_factory=list)
    provenance: Provenance
    confidence: float | None = None


class ParseQuality(BaseModel):
    parser: str = "native"
    parser_version: str = "document-parser-0.2"
    ocr_used: bool = False
    input_blocks: int = 0
    output_blocks: int = 0
    normalized_blocks: int = 0
    repeated_margin_blocks_removed: int = 0
    overlapping_duplicates_removed: int = 0
    low_confidence_blocks: int = 0
    mean_confidence: float | None = None


class CleaningRemoval(BaseModel):
    """One deterministic cleaner decision retained for quality evaluation."""

    reason: Literal["page_number", "repeated_margin", "overlapping_duplicate"]
    block: DocumentBlock


class DocumentIR(BaseModel):
    schema_version: str = "document-ir-v4"
    status: Literal["ready", "ocr_required", "review_required"] = "ready"
    source_file: str
    mime_type: str
    sha256: str
    blocks: list[DocumentBlock] = Field(default_factory=list)
    cleaning_removals: list[CleaningRemoval] = Field(default_factory=list)
    warnings: list[str] = Field(default_factory=list)
    quality: ParseQuality = Field(default_factory=ParseQuality)

    @property
    def text(self) -> str:
        return "\n\n".join(block.text for block in self.blocks if block.text.strip())
