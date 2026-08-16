#!/usr/bin/env python3
"""Upload the fictional medical corpus through the real authenticated pipeline."""

from __future__ import annotations

import argparse
import json
import mimetypes
import os
import secrets
import sys
import urllib.error
import urllib.request
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
CORPUS = ROOT / "datasets/domains/medical-device/corpus"
GENERATED = ROOT / "data/medical/generated"
ACCOUNTS = {
    "platform": ("admin@raglab.local", os.getenv("RAGLAB_PLATFORM_ADMIN_PASSWORD", "change-this-admin-password")),
    "tenant_a": ("alice@tenant-a.local", os.getenv("RAGLAB_TENANT_A_PASSWORD", "RagLab-Alice-2026!")),
    "tenant_b": ("bob@tenant-b.local", os.getenv("RAGLAB_TENANT_B_PASSWORD", "RagLab-Bob-2026!")),
}


def call(url: str, data: bytes, headers: dict[str, str]) -> tuple[int, dict]:
    request = urllib.request.Request(url, data=data, headers=headers, method="POST")
    try:
        with urllib.request.urlopen(request, timeout=120) as response:
            return response.status, json.loads(response.read())
    except urllib.error.HTTPError as error:
        payload = error.read().decode("utf-8", "replace")
        try:
            return error.code, json.loads(payload)
        except json.JSONDecodeError:
            return error.code, {"message": payload}


def login(api: str, account: str) -> str:
    email, password = ACCOUNTS[account]
    status, body = call(
        f"{api}/api/v1/auth/login",
        json.dumps({"email": email, "password": password}).encode(),
        {"Content-Type": "application/json"},
    )
    if status != 200:
        raise RuntimeError(f"login {account} failed ({status}): {body}")
    return body["access_token"]


def multipart(file_path: Path, metadata: dict) -> tuple[bytes, str]:
    boundary = "----RagLab" + secrets.token_hex(12)
    parts: list[bytes] = []
    for name, content, content_type, filename in (
        ("metadata", json.dumps(metadata, ensure_ascii=False).encode(), "application/json", None),
        ("file", file_path.read_bytes(), mimetypes.guess_type(file_path.name)[0] or "application/octet-stream", file_path.name),
    ):
        parts.append(f"--{boundary}\r\n".encode())
        disposition = f'Content-Disposition: form-data; name="{name}"'
        if filename:
            disposition += f'; filename="{filename}"'
        parts.append((disposition + "\r\n").encode())
        parts.append(f"Content-Type: {content_type}\r\n\r\n".encode())
        parts.append(content)
        parts.append(b"\r\n")
    parts.append(f"--{boundary}--\r\n".encode())
    return b"".join(parts), boundary


def models(product: str) -> list[str]:
    if "vsm100-pro" in product:
        return ["VSM-100 Pro"]
    if "vsm100-family" in product:
        return ["VSM-100", "VSM-100 Pro"]
    if "vsm100" in product:
        return ["VSM-100"]
    if "vsm200" in product:
        return ["VSM-200"]
    return []


def metadata(entry: dict, suffix: str = "", source_revision: int = 1) -> dict:
    is_notice = entry["doc_id"].startswith("field-correction")
    version_from = "2.5.0" if is_notice else entry["version"]
    version_to = "2.5.3" if is_notice else entry["version"]
    return {
        "document_id": entry["doc_id"] + suffix,
        "title": entry["title"] + ("（跨格式测试副本）" if suffix else ""),
        "version": entry["version"],
        "source_revision": source_revision,
        "domain": "medical-device",
        "manufacturer": "PulseCare",
        "product_family": entry["product"],
        "model_codes": models(entry["product"]),
        "software_version_from": version_from,
        "software_version_to": version_to,
        "hardware_revision": "",
        "region": "CN",
        "language": "zh-CN",
        "effective_from": str(entry.get("effective_at") or "")[:10],
        "effective_to": str(entry.get("expires_at") or "")[:10],
        "authority_level": "field_correction" if is_notice else ("manufacturer" if entry["quality"] == "authoritative" else "reviewed"),
        "document_revision": f"R{source_revision}",
        "supersedes": ["FC-2026-04-DRAFT"] if is_notice else [],
        "device_identifiers": ["MDX-V100P-A"] if is_notice else [],
        "affected_lots": [f"L26A{index:02d}" for index in range(1, 8)] if is_notice else [],
    }


def upload(api: str, token: str, dataset: str, path: Path, meta: dict) -> dict:
    body, boundary = multipart(path, meta)
    status, response = call(
        f"{api}/api/v1/datasets/{dataset}/documents/uploads",
        body,
        {"Authorization": f"Bearer {token}", "Content-Type": f"multipart/form-data; boundary={boundary}"},
    )
    if status not in (200, 202, 409):
        raise RuntimeError(f"upload {path.name} failed ({status}): {response}")
    return response


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--api", default=os.getenv("RAGLAB_API_URL", "http://127.0.0.1:8080"))
    parser.add_argument("--skip-derived", action="store_true")
    parser.add_argument("--source-revision", type=int, default=int(os.getenv("MEDICAL_SOURCE_REVISION", "1")))
    arguments = parser.parse_args()
    api = arguments.api.rstrip("/")
    tokens = {name: login(api, name) for name in ACCOUNTS}
    manifest = json.loads((CORPUS / "manifest.json").read_text(encoding="utf-8"))
    submitted: list[dict] = []
    for entry in manifest:
        tenant = (entry.get("allowed_tenants") or ["platform"])[0]
        dataset = {"platform": "public-medical-device", "tenant_a": "tenant-a-medical-runbook", "tenant_b": "tenant-b-medical-runbook"}[tenant]
        result = upload(api, tokens[tenant], dataset, CORPUS / entry["path"], metadata(entry, source_revision=arguments.source_revision))
        submitted.append({"document_id": entry["doc_id"], "dataset_id": dataset, "job_id": result.get("job_id"), "status": result.get("status")})

    if not arguments.skip_derived and GENERATED.exists():
        by_name = {
            "vsm100-error-codes-fw2.6.docx": next(item for item in manifest if item["doc_id"] == "vsm100-error-codes-fw2.6"),
            "vsm100-compatibility-fw2.6.xlsx": next(item for item in manifest if item["doc_id"] == "vsm100-compatibility-fw2.6"),
            "field-correction-fc-2026-04.pdf": next(item for item in manifest if item["doc_id"] == "field-correction-fc-2026-04"),
            "vsm100-release-notes-fw2.6.html": next(item for item in manifest if item["doc_id"] == "vsm100-family-release-fw2.6"),
        }
        for name, entry in by_name.items():
            suffix = "-" + Path(name).suffix.removeprefix(".")
            result = upload(api, tokens["platform"], "public-medical-device", GENERATED / name, metadata(entry, suffix, arguments.source_revision))
            submitted.append({"document_id": entry["doc_id"] + suffix, "dataset_id": "public-medical-device", "job_id": result.get("job_id"), "status": result.get("status")})
    print(json.dumps({"submitted": len(submitted), "documents": submitted}, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        print(f"medical bootstrap failed: {error}", file=sys.stderr)
        raise SystemExit(1)
