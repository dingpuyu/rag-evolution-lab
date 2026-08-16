#!/usr/bin/env python3
"""Generate fictional multi-format fixtures from the medical-device corpus."""

from __future__ import annotations

from pathlib import Path

import fitz
from docx import Document
from openpyxl import Workbook


ROOT = Path(__file__).resolve().parents[1]
SOURCE = ROOT / "datasets/domains/medical-device/corpus/documents"
OUTPUT = ROOT / "data/medical/generated"


def docx_fixture() -> None:
    document = Document()
    document.add_heading("VSM-100 软件 2.6 系统错误码（DOCX 测试副本）", level=1)
    document.add_paragraph("本文件由项目脚本从虚构测试资料派生，仅用于解析与跨格式检索验证。")
    document.add_heading("SYS-NET-042", level=2)
    document.add_paragraph("VSM-100 软件 2.6 中表示设备网络接口无法取得有效地址。")
    table = document.add_table(rows=1, cols=3)
    for cell, value in zip(table.rows[0].cells, ("错误码", "适用型号", "排查入口"), strict=True):
        cell.text = value
    row = table.add_row().cells
    for cell, value in zip(row, ("SYS-NET-042", "VSM-100", "设置 > 网络 > 接口状态"), strict=True):
        cell.text = value
    document.save(OUTPUT / "vsm100-error-codes-fw2.6.docx")


def xlsx_fixture() -> None:
    workbook = Workbook()
    sheet = workbook.active
    sheet.title = "兼容矩阵"
    sheet.append(["型号", "软件版本", "配件", "最低固件", "结论"])
    sheet.append(["VSM-100", "2.6", "HUB-4", "1.8", "兼容"])
    sheet.append(["VSM-100", "2.6", "WLM-2", "3.2", "兼容"])
    sheet.append(["VSM-100 Pro", "2.6", "WLM-2", "3.4", "兼容；不得沿用 VSM-100 的 3.2 结论"])
    note = workbook.create_sheet("说明")
    note.append(["范围", "完全虚构的跨格式检索测试数据"])
    workbook.save(OUTPUT / "vsm100-compatibility-fw2.6.xlsx")


def pdf_fixture() -> None:
    document = fitz.open()
    page = document.new_page()
    page.insert_text((60, 72), "PulseCare Field Correction Test Notice FC-2026-04", fontsize=15)
    lines = [
        "Synthetic data only. Not for real device operation.",
        "Affected model: VSM-100 Pro",
        "Affected software: 2.5.0 through 2.5.3",
        "Affected lots: L26A01 through L26A07",
        "Target software after correction: 2.5.4",
        "The applicability result must be computed by the deterministic policy tool.",
    ]
    for index, line in enumerate(lines):
        page.insert_text((60, 112 + index * 24), line, fontsize=11)
    document.set_metadata({"title": "FC-2026-04 synthetic PDF fixture"})
    document.save(OUTPUT / "field-correction-fc-2026-04.pdf")


def html_fixture() -> None:
    body = (SOURCE / "vsm100-release-notes-fw2.6.md").read_text(encoding="utf-8")
    escaped = body.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")
    html = f"""<!doctype html><html lang=\"zh-CN\"><head><meta charset=\"utf-8\"><title>VSM-100 软件 2.6 发布说明</title></head><body><h1>VSM-100 软件 2.6 发布说明（HTML 测试副本）</h1><p>完全虚构，仅用于解析验证。</p><pre>{escaped}</pre></body></html>"""
    (OUTPUT / "vsm100-release-notes-fw2.6.html").write_text(html, encoding="utf-8")


def main() -> None:
    OUTPUT.mkdir(parents=True, exist_ok=True)
    docx_fixture()
    xlsx_fixture()
    pdf_fixture()
    html_fixture()
    for path in sorted(OUTPUT.iterdir()):
        print(path.relative_to(ROOT))


if __name__ == "__main__":
    main()
