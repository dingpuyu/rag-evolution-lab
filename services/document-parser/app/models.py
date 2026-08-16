from __future__ import annotations

from typing import Literal

from pydantic import BaseModel, Field


class Provenance(BaseModel):
    source_file: str
    page: int = 0
    sheet: str = ""
    cell_range: str = ""


class DocumentBlock(BaseModel):
    block_type: Literal["heading", "paragraph", "list", "table", "code"]
    text: str
    heading_path: list[str] = Field(default_factory=list)
    provenance: Provenance


class DocumentIR(BaseModel):
    schema_version: str = "document-ir-v1"
    status: Literal["ready", "ocr_required"] = "ready"
    source_file: str
    mime_type: str
    sha256: str
    blocks: list[DocumentBlock] = Field(default_factory=list)
    warnings: list[str] = Field(default_factory=list)

    @property
    def text(self) -> str:
        return "\n\n".join(block.text for block in self.blocks if block.text.strip())
