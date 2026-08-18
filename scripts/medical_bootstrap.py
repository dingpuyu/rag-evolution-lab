#!/usr/bin/env python3
"""Upload the synthetic engineering and official-source sales corpora."""

from __future__ import annotations

import argparse
import hashlib
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
SALES_CORPUS = ROOT / "datasets/domains/medical-device-sales/corpus"
GENERATED = ROOT / "data/medical/generated"
DOCUMENT_IR_SCHEMA_VERSION = "document-ir-v2"
INGESTION_METADATA_FIELDS = (
    "title", "version", "domain", "manufacturer", "product_family", "model_codes",
    "software_version_from", "software_version_to", "hardware_revision", "region", "language",
    "effective_from", "effective_to", "authority_level", "document_revision", "supersedes",
    "device_identifiers", "affected_lots",
)
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


def get_json(url: str, token: str) -> dict:
    request = urllib.request.Request(url, headers={"Authorization": f"Bearer {token}"})
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            return json.loads(response.read())
    except urllib.error.HTTPError as error:
        payload = error.read().decode("utf-8", "replace")
        try:
            body = json.loads(payload)
        except json.JSONDecodeError:
            body = {"message": payload}
        detail = body.get("error") if isinstance(body.get("error"), dict) else body
        message = str(detail.get("message", ""))
        # A genuinely empty deployment has no lifecycle collection yet. The
        # first upload is responsible for creating it, so absence is equivalent
        # to an empty revision history during bootstrap—not a reason to abort.
        if error.code == 503 and "can't find collection" in message:
            return {"uploads": [], "cold_start": True}
        raise RuntimeError(f"GET {url} failed ({error.code}): {body}") from error


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


def sales_metadata(entry: dict, source_revision: int = 1, review: dict | None = None) -> dict:
    """Map the reviewed sales manifest to the production ingestion contract."""
    collected_at = str(entry.get("collected_at") or "")[:10]
    return {
        "document_id": entry["doc_id"],
        "title": entry["title"],
        "version": entry.get("version", collected_at),
        "source_revision": source_revision,
        "domain": "medical-device-sales",
        "manufacturer": entry.get("manufacturer", ""),
        "product_family": entry.get("product_family", ""),
        "model_codes": entry.get("model_codes", []),
        "software_version_from": "",
        "software_version_to": "",
        "hardware_revision": "",
        "region": entry.get("region", "CN"),
        "language": entry.get("language", "zh-CN"),
        "effective_from": collected_at,
        "effective_to": "",
        "authority_level": entry.get("authority_level", "official_source_summary"),
        "document_revision": f"R{source_revision}",
        "supersedes": [],
        "device_identifiers": [],
        "affected_lots": [],
        "source_type": entry.get("source_type", ""),
        "source_urls": entry.get("source_urls", []),
        "collected_at": collected_at,
        "source_review_status": "approved",
        "source_reviewed_at": (review or {}).get("reviewed_at", entry.get("source_reviewed_at", "2026-08-17T08:00:00Z")),
    }


def select_revision(path: Path, meta: dict, existing: dict[str, list[dict]]) -> dict:
    """Reuse a revision for identical bytes; advance it for changed bytes."""
    prepared = dict(meta)
    digest = hashlib.sha256(path.read_bytes()).hexdigest()
    records = existing.get(str(meta["document_id"]), [])
    identical = [
        int(record.get("source_revision", 0)) for record in records
        if record.get("source_hash") == digest
        and record.get("index_status") not in {"failed", "cancelled"}
        and record_parser_version(record) == DOCUMENT_IR_SCHEMA_VERSION
        and record_metadata_matches(record, meta)
    ]
    if identical:
        prepared["source_revision"] = max(identical)
    elif records:
        prepared["source_revision"] = max(int(meta.get("source_revision", 1)), max(int(record.get("source_revision", 0)) for record in records) + 1)
    return prepared


def upload(api: str, token: str, dataset: str, path: Path, meta: dict, revisions: dict[str, dict[str, list[dict]]]) -> dict:
    digest = hashlib.sha256(path.read_bytes()).hexdigest()
    identical = [
        record for record in revisions.get(dataset, {}).get(str(meta["document_id"]), [])
        if record.get("source_hash") == digest
        and record.get("index_status") in {"queued", "running", "completed"}
        and record_parser_version(record) == DOCUMENT_IR_SCHEMA_VERSION
        and record_metadata_matches(record, meta)
    ]
    if identical:
        current = max(identical, key=lambda record: int(record.get("source_revision", 0)))
        return {"job_id": current.get("job_id"), "status": current.get("index_status"), "duplicate": True}
    meta = select_revision(path, meta, revisions.get(dataset, {}))
    body, boundary = multipart(path, meta)
    status, response = call(
        f"{api}/api/v1/datasets/{dataset}/documents/uploads",
        body,
        {"Authorization": f"Bearer {token}", "Content-Type": f"multipart/form-data; boundary={boundary}"},
    )
    if status not in (200, 202, 409):
        raise RuntimeError(f"upload {path.name} failed ({status}): {response}")
    return response


def record_parser_version(record: dict) -> str:
    metadata_value = record_metadata(record)
    return str(metadata_value.get("document_ir_schema_version") or "")


def record_metadata(record: dict) -> dict:
    metadata_value = record.get("metadata") or {}
    if isinstance(metadata_value, str):
        try:
            metadata_value = json.loads(metadata_value)
        except json.JSONDecodeError:
            return {}
    return metadata_value


def ingestion_metadata_hash(metadata_value: dict) -> str:
    payload = {field: metadata_value.get(field, [] if field in {"model_codes", "supersedes", "device_identifiers", "affected_lots"} else "") for field in INGESTION_METADATA_FIELDS}
    encoded = json.dumps(payload, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode()
    return hashlib.sha256(encoded).hexdigest()


def record_metadata_matches(record: dict, desired: dict) -> bool:
    current = record_metadata(record)
    expected_hash = ingestion_metadata_hash(desired)
    if current.get("ingestion_metadata_sha256"):
        return current["ingestion_metadata_sha256"] == expected_hash
    # Compatibility for records created before the metadata fingerprint was
    # introduced. This prevents a parser upgrade from re-embedding every
    # unchanged document while still detecting model/version scope changes.
    current = dict(current)
    current["title"] = record.get("title", "")
    current["version"] = record.get("document_version", "")
    return all(current.get(field, [] if field in {"model_codes", "supersedes", "device_identifiers", "affected_lots"} else "") == desired.get(field, [] if field in {"model_codes", "supersedes", "device_identifiers", "affected_lots"} else "") for field in INGESTION_METADATA_FIELDS)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--api", default=os.getenv("RAGLAB_API_URL", "http://127.0.0.1:8080"))
    parser.add_argument("--skip-derived", action="store_true")
    parser.add_argument("--source-revision", type=int, default=int(os.getenv("MEDICAL_SOURCE_REVISION", "1")))
    arguments = parser.parse_args()
    api = arguments.api.rstrip("/")
    tokens = {name: login(api, name) for name in ACCOUNTS}
    manifest = json.loads((CORPUS / "manifest.json").read_text(encoding="utf-8"))
    sales_manifest = json.loads((SALES_CORPUS / "manifest.json").read_text(encoding="utf-8"))
    sales_lock = json.loads((SALES_CORPUS / "sources.lock.json").read_text(encoding="utf-8"))["documents"]
    datasets = ["public-medical-device", "tenant-a-medical-runbook", "tenant-b-medical-runbook", "public-medical-device-sales"]
    revisions: dict[str, dict[str, list[dict]]] = {}
    for dataset_id in datasets:
        records = get_json(f"{api}/api/v1/datasets/{dataset_id}/documents", tokens["platform"]).get("uploads", [])
        revisions[dataset_id] = {}
        for record in records:
            revisions[dataset_id].setdefault(record["document_id"], []).append(record)
    submitted: list[dict] = []
    for entry in manifest:
        tenant = (entry.get("allowed_tenants") or ["platform"])[0]
        dataset = {"platform": "public-medical-device", "tenant_a": "tenant-a-medical-runbook", "tenant_b": "tenant-b-medical-runbook"}[tenant]
        result = upload(api, tokens[tenant], dataset, CORPUS / entry["path"], metadata(entry, source_revision=arguments.source_revision), revisions)
        submitted.append({"document_id": entry["doc_id"], "dataset_id": dataset, "job_id": result.get("job_id"), "status": result.get("status")})

    for entry in sales_manifest:
        result = upload(
            api,
            tokens["platform"],
            "public-medical-device-sales",
            SALES_CORPUS / entry["path"],
            sales_metadata(entry, arguments.source_revision, sales_lock.get(entry["doc_id"])),
            revisions,
        )
        submitted.append({
            "document_id": entry["doc_id"], "dataset_id": "public-medical-device-sales",
            "job_id": result.get("job_id"), "status": result.get("status"),
        })

    if not arguments.skip_derived and GENERATED.exists():
        by_name = {
            "vsm100-error-codes-fw2.6.docx": next(item for item in manifest if item["doc_id"] == "vsm100-error-codes-fw2.6"),
            "vsm100-compatibility-fw2.6.xlsx": next(item for item in manifest if item["doc_id"] == "vsm100-compatibility-fw2.6"),
            "field-correction-fc-2026-04.pdf": next(item for item in manifest if item["doc_id"] == "field-correction-fc-2026-04"),
            "vsm100-release-notes-fw2.6.html": next(item for item in manifest if item["doc_id"] == "vsm100-family-release-fw2.6"),
        }
        for name, entry in by_name.items():
            suffix = "-" + Path(name).suffix.removeprefix(".")
            derived_metadata = metadata(entry, suffix, arguments.source_revision)
            if name == "vsm100-compatibility-fw2.6.xlsx":
                # The workbook contains both the standard and Pro rows. Scope
                # metadata must describe the file, not only its source fixture.
                derived_metadata["model_codes"] = ["VSM-100", "VSM-100 Pro"]
                derived_metadata["product_family"] = "pulsecare-vsm100-family"
            result = upload(api, tokens["platform"], "public-medical-device", GENERATED / name, derived_metadata, revisions)
            submitted.append({"document_id": entry["doc_id"] + suffix, "dataset_id": "public-medical-device", "job_id": result.get("job_id"), "status": result.get("status")})
    print(json.dumps({"submitted": len(submitted), "documents": submitted}, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        print(f"medical bootstrap failed: {error}", file=sys.stderr)
        raise SystemExit(1)
