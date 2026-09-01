#!/usr/bin/env python3
"""Export real parser/cleaner/chunker artifacts through the authenticated API.

The fixtures are synthetic and are sent to the no-index evaluation endpoint.
No source file, access token, Document IR or chunk is persisted by RagLab.
"""

from __future__ import annotations

import argparse
import json
import os
import tempfile
from pathlib import Path

import httpx
import pymupdf
from openpyxl import Workbook


CASES = (
    ("dev-ocr-aed-critical-001", "aed-scan.pdf", "application/pdf"),
    ("dev-cleaning-margin-002", "vsm100-margins.pdf", "application/pdf"),
    ("dev-table-version-003", "vsm-compatibility.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"),
    ("dev-chunk-long-procedure-004", "long-procedure.md", "text/markdown"),
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


def add_text(page: pymupdf.Page, position: tuple[float, float], value: str, size: float, font_file: str) -> None:
    page.insert_font(fontname="raglab-cjk", fontfile=font_file)
    page.insert_text(position, value, fontsize=size, fontname="raglab-cjk")


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
    parser.add_argument("--output", required=True)
    arguments = parser.parse_args()
    font_file = cjk_font()
    with tempfile.TemporaryDirectory(prefix="raglab-document-quality-") as directory:
        root = Path(directory)
        sources = {
            "dev-ocr-aed-critical-001": scanned_aed(font_file),
            "dev-cleaning-margin-002": repeated_margin_pdf(font_file),
            "dev-table-version-003": compatibility_xlsx(root / "vsm-compatibility.xlsx"),
            "dev-chunk-long-procedure-004": long_procedure_markdown(),
        }
        artifacts = []
        with httpx.Client(timeout=420) as client:
            token = login(client, arguments.api.rstrip("/"))
            headers = {"Authorization": f"Bearer {token}"}
            for case_id, filename, content_type in CASES:
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
        output.write_text(json.dumps({
            "schema": "agent-evaluation.document-quality.artifacts.v1",
            "source": "rag-evolution-lab authenticated no-index preview",
            "config": {"max_runes": arguments.max_runes, "overlap_runes": arguments.overlap_runes},
            "artifacts": artifacts,
        }, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        print(json.dumps({
            "status": "exported",
            "cases": len(artifacts),
            "output": str(output.resolve()),
            "indexed": False,
            "statuses": {item["case_id"]: item["status"] for item in artifacts},
        }, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
