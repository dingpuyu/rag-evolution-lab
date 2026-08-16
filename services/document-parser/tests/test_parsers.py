from __future__ import annotations

import io

from docx import Document
import fitz
from openpyxl import Workbook

from app.parsers import parse_document


def test_markdown_preserves_heading_path_and_table():
    result = parse_document("manual.md", "# VSM-100\n\n## 错误码\n\n| 代码 | 含义 |\n| --- | --- |\n| SYS-NET-042 | 网络 |".encode())
    assert result.status == "ready"
    assert any(block.block_type == "table" for block in result.blocks)
    assert result.blocks[-1].heading_path == ["VSM-100", "错误码"]


def test_docx_parser_preserves_heading_and_table():
    stream = io.BytesIO()
    document = Document()
    document.add_heading("PulseCare", level=1)
    document.add_paragraph("虚构设备资料")
    table = document.add_table(rows=1, cols=2)
    table.rows[0].cells[0].text = "型号"
    table.rows[0].cells[1].text = "VSM-100"
    document.save(stream)
    result = parse_document("manual.docx", stream.getvalue())
    assert any(block.block_type == "table" and "VSM-100" in block.text for block in result.blocks)


def test_xlsx_parser_preserves_sheet_and_cell_range():
    stream = io.BytesIO()
    workbook = Workbook()
    sheet = workbook.active
    sheet.title = "兼容矩阵"
    sheet.append(["配件", "型号"])
    sheet.append(["WLM-2", "VSM-100 Pro"])
    workbook.save(stream)
    result = parse_document("matrix.xlsx", stream.getvalue())
    block = result.blocks[0]
    assert block.provenance.sheet == "兼容矩阵"
    assert block.provenance.cell_range == "A1:B2"


def test_digital_pdf_preserves_page_and_heading():
    document = fitz.open()
    page = document.new_page()
    page.insert_text((72, 72), "VSM-100 Error Codes", fontsize=18)
    page.insert_text((72, 110), "SYS-NET-042 means synthetic network failure.", fontsize=10)
    content = document.tobytes()
    document.close()
    result = parse_document("manual.pdf", content)
    assert result.status == "ready"
    assert all(block.provenance.page == 1 for block in result.blocks)
    assert any(block.block_type == "heading" and "VSM-100" in block.text for block in result.blocks)


def test_scanned_pdf_requires_ocr():
    document = fitz.open()
    document.new_page()
    content = document.tobytes()
    document.close()
    result = parse_document("scan.pdf", content)
    assert result.status == "ocr_required"
