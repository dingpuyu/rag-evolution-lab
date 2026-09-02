#!/usr/bin/env python3
"""Export real parser/cleaner/chunker artifacts through the authenticated API.

The fixtures are synthetic and are sent to the no-index evaluation endpoint.
No source file, access token, Document IR or chunk is persisted by RagLab.
"""

from __future__ import annotations

import argparse
import io
import json
import os
import tempfile
from pathlib import Path

import httpx
import pymupdf
from openpyxl import Workbook
from docx import Document


DEVELOPMENT_CASES = (
    ("dev-ocr-aed-critical-001", "aed-scan.pdf", "application/pdf"),
    ("dev-cleaning-margin-002", "vsm100-margins.pdf", "application/pdf"),
    ("dev-table-version-003", "vsm-compatibility.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"),
    ("dev-chunk-long-procedure-004", "long-procedure.md", "text/markdown"),
    ("dev-ocr-degraded-fallback-005", "monitor-degraded-development.pdf", "application/pdf"),
)

HOLDOUT_CASES = (
    ("holdout-rotated-manual-001", "pump-rotated.pdf", "application/pdf"),
    ("holdout-low-dpi-review-002", "monitor-low-contrast.pdf", "application/pdf"),
    ("holdout-docx-structure-003", "aed-maintenance.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"),
)

LONG_PROCEDURE = (
    "处理步骤：先断开 VSM-300 外部电源并记录 BAT-SVC-088，再确认备用电池已隔离；"
    "随后依次检查电池连接器、保险丝状态与电源管理板指示灯，禁止跳过绝缘检查；"
    "完成后恢复供电并观察十五分钟，确认错误码不再出现，最后把测量值、设备序列号、软件版本和复核人员写入服务记录。"
)


def cjk_font() -> str:
    configured = os.getenv("RAGLAB_CJK_FONT", "").strip()
    candidates = [
        configured,
        "/System/Library/Fonts/PingFang.ttc",
        "/System/Library/Fonts/STHeiti Light.ttc",
        "/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
        "/usr/share/fonts/truetype/wqy/wqy-zenhei.ttc",
    ]
    for candidate in candidates:
        if candidate and Path(candidate).is_file():
            return candidate
    raise RuntimeError("a CJK font is required; set RAGLAB_CJK_FONT to a local .ttf/.ttc file")


def add_text(
    page: pymupdf.Page,
    position: tuple[float, float],
    value: str,
    size: float,
    font_file: str,
    *,
    color: tuple[float, float, float] = (0, 0, 0),
) -> None:
    page.insert_font(fontname="raglab-cjk", fontfile=font_file)
    page.insert_text(position, value, fontsize=size, fontname="raglab-cjk", color=color)


def scanned_aed(font_file: str) -> bytes:
    source = pymupdf.open()
    page = source.new_page(width=800, height=1100)
    add_text(page, (70, 100), "AED 设备故障排查", 28, font_file)
    # Keep body typography consistent. A mixed 20/18pt layout repeatedly
    # caused PP-DocLayout-S to omit the fourth line even though OCR itself
    # recognized the identifiers; the controlled probe is retained as a real
    # layout-model Bad Case in the evaluation report.
    add_text(page, (70, 160), "型号：BeneHeart C2", 18, font_file)
    add_text(page, (70, 220), "错误码：BAT-LOW-021", 18, font_file)
    add_text(page, (70, 280), "BAT-LOW-021 处理：连接交流电源并检查电池状态", 18, font_file)
    add_text(page, (70, 340), "仅授权服务人员执行", 18, font_file)
    pixmap = page.get_pixmap(matrix=pymupdf.Matrix(2, 2), alpha=False)
    source.close()
    scanned = pymupdf.open()
    image_page = scanned.new_page(width=pixmap.width, height=pixmap.height)
    image_page.insert_image(image_page.rect, pixmap=pixmap)
    content = scanned.tobytes(deflate=True)
    scanned.close()
    return content


def repeated_margin_pdf(font_file: str) -> bytes:
    document = pymupdf.open()
    bodies = (
        "VSM-100 网络配置说明",
        "SYS-NET-042 请检查网络接口和网关配置",
        "执行变更前记录当前软件版本，禁止删除相似型号 VSM-100 Pro 的独立说明。",
    )
    for index, body in enumerate(bodies, start=1):
        page = document.new_page(width=595, height=842)
        add_text(page, (60, 45), "PulseCare Medical Devices", 10, font_file)
        add_text(page, (60, 160), body, 15, font_file)
        add_text(page, (270, 815), f"第 {index} 页", 10, font_file)
    content = document.tobytes(deflate=True)
    document.close()
    return content


def compatibility_xlsx(target: Path) -> bytes:
    workbook = Workbook()
    sheet = workbook.active
    sheet.title = "兼容矩阵"
    sheet.append(["型号", "软件版本", "硬件修订"])
    sheet.append(["VSM-100", "2.6", "HR-1"])
    sheet.append(["VSM-100 Pro", "2.6", "HR-2"])
    # Enough nearby rows to make embedding amplification visible when overlap
    # changes, while retaining exact model/version strings.
    for index in range(1, 26):
        sheet.append([f"VSM-200-{index:02d}", "3.1", f"HX-{index:02d}"])
    workbook.save(target)
    return target.read_bytes()


def long_procedure_markdown() -> bytes:
    filler_prefix = ("背景说明用于模拟真实服务手册中的连续长段落，当前句不包含操作结论。" * 10)[:270]
    filler_suffix = ("补充说明用于模拟审计、备件和交接要求，不能替代正式服务流程。" * 12)[:320]
    return f"# 长段落切分验证\n\n{filler_prefix}{LONG_PROCEDURE}{filler_suffix}\n".encode()


def rotated_pump_scan(font_file: str) -> bytes:
    source = pymupdf.open()
    page = source.new_page(width=800, height=1100)
    add_text(page, (70, 110), "BeneFusion uVP 输注泵服务手册", 25, font_file)
    add_text(page, (70, 190), "错误码 INF-OCC-017", 19, font_file)
    add_text(page, (70, 260), "INF-OCC-017 停止输注并检查管路", 19, font_file)
    add_text(page, (70, 330), "确认夹闭、折弯或堵塞已解除后再按规程复核。", 17, font_file)
    pixmap = page.get_pixmap(matrix=pymupdf.Matrix(2, 2), alpha=False)
    source.close()
    scanned = pymupdf.open()
    image_page = scanned.new_page(width=pixmap.width, height=pixmap.height)
    image_page.insert_image(image_page.rect, pixmap=pixmap)
    # Preserve a real 90-degree page-orientation probe. The OCR worker, rather
    # than the fixture generator, must prove that it can normalize the page.
    image_page.set_rotation(90)
    content = scanned.tobytes(deflate=True)
    scanned.close()
    return content


def low_contrast_monitor_scan(font_file: str) -> bytes:
    source = pymupdf.open()
    page = source.new_page(width=595, height=842)
    ink = (0.72, 0.72, 0.72)
    add_text(page, (55, 105), "BeneVision N1 现场服务记录", 20, font_file, color=ink)
    add_text(page, (55, 175), "设备序列号：N1-SYNTHETIC-204", 13, font_file, color=ink)
    add_text(page, (55, 230), "低对比度扫描件必须由人工确认后发布。", 13, font_file, color=ink)
    # 150 DPI is intentionally close to a common archive-scan boundary. No
    # expected status is hard-coded here; the parser confidence decides it.
    pixmap = page.get_pixmap(dpi=150, alpha=False)
    source.close()
    scanned = pymupdf.open()
    image_page = scanned.new_page(width=pixmap.width, height=pixmap.height)
    image_page.insert_image(image_page.rect, pixmap=pixmap)
    content = scanned.tobytes(deflate=True)
    scanned.close()
    return content


def degraded_monitor_development_scan(font_file: str) -> bytes:
    source = pymupdf.open()
    page = source.new_page(width=595, height=842)
    ink = (0.70, 0.70, 0.70)
    add_text(page, (55, 105), "BeneVision N12 维修接收单", 20, font_file, color=ink)
    add_text(page, (55, 175), "设备编号：N12-DEVELOPMENT-031", 13, font_file, color=ink)
    add_text(page, (55, 230), "结构识别降级结果必须人工复核。", 13, font_file, color=ink)
    pixmap = page.get_pixmap(dpi=180, alpha=False)
    source.close()
    scanned = pymupdf.open()
    image_page = scanned.new_page(width=pixmap.width, height=pixmap.height)
    image_page.insert_image(image_page.rect, pixmap=pixmap)
    content = scanned.tobytes(deflate=True)
    scanned.close()
    return content


def aed_maintenance_docx() -> bytes:
    document = Document()
    document.add_heading("维护前检查", level=1)
    document.add_paragraph("确认设备已退出临床使用", style="List Bullet")
    document.add_paragraph("记录设备型号、软件版本和维护工单号", style="List Bullet")
    table = document.add_table(rows=1, cols=2)
    table.rows[0].cells[0].text = "型号"
    table.rows[0].cells[1].text = "软件版本"
    row = table.add_row().cells
    row[0].text = "C2"
    row[1].text = "2.6"
    document.add_paragraph("MAINT-003 仅授权服务人员执行")
    output = io.BytesIO()
    document.save(output)
    return output.getvalue()


def login(client: httpx.Client, api: str) -> str:
    response = client.post(
        f"{api}/api/v1/auth/login",
        json={
            "email": os.getenv("RAGLAB_STACK_SMOKE_EMAIL", "admin@raglab.local"),
            "password": os.getenv("RAGLAB_PLATFORM_ADMIN_PASSWORD", "change-this-admin-password"),
        },
    )
    response.raise_for_status()
    return response.json()["access_token"]


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--api", default=os.getenv("RAGLAB_API_URL", f"http://127.0.0.1:{os.getenv('RAGLAB_API_PORT', '8080')}"))
    parser.add_argument("--dataset", default="public-medical-device")
    parser.add_argument("--max-runes", type=int, default=700)
    parser.add_argument("--overlap-runes", type=int, default=80)
    parser.add_argument("--split", choices=("development", "holdout"), default="development")
    parser.add_argument(
        "--pipeline-release",
        default=os.getenv("DOCUMENT_QUALITY_PIPELINE_RELEASE", ""),
    )
    parser.add_argument("--output", required=True)
    arguments = parser.parse_args()
    font_file = cjk_font()
    with tempfile.TemporaryDirectory(prefix="raglab-document-quality-") as directory:
        root = Path(directory)
        development_sources = {
            "dev-ocr-aed-critical-001": scanned_aed(font_file),
            "dev-cleaning-margin-002": repeated_margin_pdf(font_file),
            "dev-table-version-003": compatibility_xlsx(root / "vsm-compatibility.xlsx"),
            "dev-chunk-long-procedure-004": long_procedure_markdown(),
            "dev-ocr-degraded-fallback-005": degraded_monitor_development_scan(font_file),
        }
        holdout_sources = {
            "holdout-rotated-manual-001": rotated_pump_scan(font_file),
            "holdout-low-dpi-review-002": low_contrast_monitor_scan(font_file),
            "holdout-docx-structure-003": aed_maintenance_docx(),
        }
        cases = DEVELOPMENT_CASES if arguments.split == "development" else HOLDOUT_CASES
        sources = development_sources if arguments.split == "development" else holdout_sources
        artifacts = []
        with httpx.Client(timeout=420) as client:
            token = login(client, arguments.api.rstrip("/"))
            headers = {"Authorization": f"Bearer {token}"}
            for case_id, filename, content_type in cases:
                response = client.post(
                    f"{arguments.api.rstrip('/')}/api/v1/datasets/{arguments.dataset}/documents/evaluation-artifacts",
                    headers=headers,
                    data={
                        "case_id": case_id,
                        "max_runes": str(arguments.max_runes),
                        "overlap_runes": str(arguments.overlap_runes),
                    },
                    files={"file": (filename, sources[case_id], content_type)},
                )
                response.raise_for_status()
                artifact = response.json()
                assert artifact["indexed"] is False, artifact
                artifacts.append(artifact)
        output = Path(arguments.output)
        output.parent.mkdir(parents=True, exist_ok=True)
        observed_parsers = sorted({
            f"{item.get('document_ir', {}).get('quality', {}).get('parser', 'unknown')}@"
            f"{item.get('document_ir', {}).get('quality', {}).get('parser_version', 'unknown')}"
            for item in artifacts
        })
        pipeline_release = arguments.pipeline_release or f"document-ir-v4+chunker-v1+{'|'.join(observed_parsers)}"
        output.write_text(json.dumps({
            "schema": "agent-evaluation.document-quality.artifacts.v1",
            "source": f"rag-evolution-lab authenticated no-index {arguments.split} preview",
            "config": {
                "max_runes": arguments.max_runes,
                "overlap_runes": arguments.overlap_runes,
                "pipeline_release": pipeline_release,
            },
            "artifacts": artifacts,
        }, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        print(json.dumps({
            "status": "exported",
            "cases": len(artifacts),
            "output": str(output.resolve()),
            "indexed": False,
            "statuses": {item["case_id"]: item["status"] for item in artifacts},
            "split": arguments.split,
        }, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
