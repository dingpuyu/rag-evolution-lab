from __future__ import annotations

import hashlib
import io
import mimetypes
import re
from pathlib import Path

from bs4 import BeautifulSoup
from docx import Document
import fitz
from openpyxl import load_workbook
from openpyxl.utils import get_column_letter

from .models import DocumentBlock, DocumentIR, Provenance


SUPPORTED_SUFFIXES = {".md", ".markdown", ".html", ".htm", ".pdf", ".docx", ".xlsx"}


class UnsupportedDocument(ValueError):
    pass


def parse_document(filename: str, content: bytes, content_type: str = "") -> DocumentIR:
    safe_name = Path(filename or "upload").name
    suffix = Path(safe_name).suffix.lower()
    if suffix not in SUPPORTED_SUFFIXES:
        raise UnsupportedDocument(f"unsupported document type: {suffix or 'unknown'}")
    mime = content_type or mimetypes.guess_type(safe_name)[0] or "application/octet-stream"
    digest = hashlib.sha256(content).hexdigest()
    if suffix in {".md", ".markdown"}:
        blocks = parse_markdown(safe_name, content.decode("utf-8-sig"))
    elif suffix in {".html", ".htm"}:
        blocks = parse_html(safe_name, content.decode("utf-8-sig"))
    elif suffix == ".pdf":
        blocks = parse_pdf(safe_name, content)
    elif suffix == ".docx":
        blocks = parse_docx(safe_name, content)
    else:
        blocks = parse_xlsx(safe_name, content)
    status = "ready"
    warnings: list[str] = []
    if suffix == ".pdf" and not any(block.text.strip() for block in blocks):
        status = "ocr_required"
        warnings.append("PDF contains no extractable text; OCR is required before indexing")
    return DocumentIR(status=status, source_file=safe_name, mime_type=mime, sha256=digest, blocks=blocks, warnings=warnings)


def parse_markdown(filename: str, text: str) -> list[DocumentBlock]:
    blocks: list[DocumentBlock] = []
    headings: list[str] = []
    paragraph: list[str] = []

    def flush() -> None:
        if paragraph:
            value = " ".join(line.strip() for line in paragraph if line.strip())
            if value:
                kind = "list" if all(re.match(r"^\s*(?:[-*+] |\d+[.)] )", line) for line in paragraph) else "paragraph"
                blocks.append(_block(kind, value, filename, headings))
            paragraph.clear()

    for line in text.splitlines():
        heading = re.match(r"^(#{1,6})\s+(.+?)\s*$", line)
        if heading:
            flush()
            level = len(heading.group(1))
            headings[:] = headings[: level - 1]
            headings.append(heading.group(2).strip())
            blocks.append(_block("heading", heading.group(2).strip(), filename, headings))
        elif line.strip().startswith("|") and line.strip().endswith("|"):
            flush()
            blocks.append(_block("table", line.strip(), filename, headings))
        elif not line.strip():
            flush()
        else:
            paragraph.append(line)
    flush()
    return blocks


def parse_html(filename: str, text: str) -> list[DocumentBlock]:
    soup = BeautifulSoup(text, "html.parser")
    headings: list[str] = []
    blocks: list[DocumentBlock] = []
    for element in soup.find_all(["h1", "h2", "h3", "h4", "h5", "h6", "p", "li", "pre", "table"]):
        value = element.get_text(" | " if element.name == "table" else " ", strip=True)
        if not value:
            continue
        if element.name.startswith("h"):
            level = int(element.name[1])
            headings[:] = headings[: level - 1]
            headings.append(value)
            kind = "heading"
        elif element.name == "table":
            kind = "table"
        elif element.name == "li":
            kind = "list"
        elif element.name == "pre":
            kind = "code"
        else:
            kind = "paragraph"
        blocks.append(_block(kind, value, filename, headings))
    return blocks


def parse_pdf(filename: str, content: bytes) -> list[DocumentBlock]:
    blocks: list[DocumentBlock] = []
    document = fitz.open(stream=content, filetype="pdf")
    try:
        for page_number, page in enumerate(document, start=1):
            table_boxes: list[fitz.Rect] = []
            try:
                finder = page.find_tables()
                for table in finder.tables:
                    rows = table.extract()
                    value = "\n".join(" | ".join("" if cell is None else str(cell).strip() for cell in row) for row in rows)
                    if value.strip(" |\n"):
                        blocks.append(DocumentBlock(
                            block_type="table", text=value, heading_path=[],
                            provenance=Provenance(source_file=filename, page=page_number),
                        ))
                        table_boxes.append(fitz.Rect(table.bbox))
            except Exception:
                # Some PDFs do not expose table geometry; text extraction still
                # provides a safe, page-addressable fallback.
                table_boxes = []
            headings: list[str] = []
            page_dict = page.get_text("dict")
            for raw in page_dict.get("blocks", []):
                if raw.get("type") != 0:
                    continue
                bbox = fitz.Rect(raw.get("bbox", (0, 0, 0, 0)))
                if any(bbox.intersects(table_box) and bbox.get_area() > 0 for table_box in table_boxes):
                    continue
                spans = [span for line in raw.get("lines", []) for span in line.get("spans", [])]
                value = "\n".join("".join(str(span.get("text", "")) for span in line.get("spans", [])).strip() for line in raw.get("lines", [])).strip()
                if not value:
                    continue
                max_size = max((float(span.get("size", 0)) for span in spans), default=0)
                bold = any(int(span.get("flags", 0)) & 16 for span in spans)
                is_heading = len(value) <= 120 and (max_size >= 14 or bold)
                if is_heading:
                    headings = [value]
                blocks.append(DocumentBlock(
                    block_type="heading" if is_heading else "paragraph",
                    text=value, heading_path=list(headings),
                    provenance=Provenance(source_file=filename, page=page_number),
                ))
    finally:
        document.close()
    return blocks


def parse_docx(filename: str, content: bytes) -> list[DocumentBlock]:
    document = Document(io.BytesIO(content))
    blocks: list[DocumentBlock] = []
    headings: list[str] = []
    for paragraph in document.paragraphs:
        value = paragraph.text.strip()
        if not value:
            continue
        style = paragraph.style.name if paragraph.style else ""
        match = re.match(r"Heading\s+(\d+)", style, re.IGNORECASE)
        if match:
            level = int(match.group(1))
            headings[:] = headings[: level - 1]
            headings.append(value)
            kind = "heading"
        else:
            kind = "list" if "List" in style else "paragraph"
        blocks.append(_block(kind, value, filename, headings))
    for table in document.tables:
        rows = [" | ".join(cell.text.strip() for cell in row.cells) for row in table.rows]
        value = "\n".join(row for row in rows if row.strip(" |"))
        if value:
            blocks.append(_block("table", value, filename, headings))
    return blocks


def parse_xlsx(filename: str, content: bytes) -> list[DocumentBlock]:
    workbook = load_workbook(io.BytesIO(content), read_only=True, data_only=True)
    blocks: list[DocumentBlock] = []
    try:
        for sheet in workbook.worksheets:
            rows: list[str] = []
            min_row = min_col = None
            max_row = max_col = 0
            for row in sheet.iter_rows():
                values = ["" if cell.value is None else str(cell.value).strip() for cell in row]
                if not any(values):
                    continue
                rows.append(" | ".join(values))
                nonempty = [cell for cell in row if cell.value is not None]
                if nonempty:
                    min_row = min(min_row or row[0].row, row[0].row)
                    min_col = min(min_col or nonempty[0].column, nonempty[0].column)
                    max_row = max(max_row, row[0].row)
                    max_col = max(max_col, nonempty[-1].column)
            if rows:
                cell_range = f"{get_column_letter(min_col)}{min_row}:{get_column_letter(max_col)}{max_row}"
                blocks.append(DocumentBlock(
                    block_type="table",
                    text="\n".join(rows),
                    heading_path=[sheet.title],
                    provenance=Provenance(source_file=filename, sheet=sheet.title, cell_range=cell_range),
                ))
    finally:
        workbook.close()
    return blocks


def _block(kind: str, text: str, filename: str, headings: list[str]) -> DocumentBlock:
    return DocumentBlock(block_type=kind, text=text, heading_path=list(headings), provenance=Provenance(source_file=filename))
