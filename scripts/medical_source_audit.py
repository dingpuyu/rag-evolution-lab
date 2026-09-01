#!/usr/bin/env python3
"""Audit the provenance and approved content of the medical sales corpus.

The lock file is an explicit human-review boundary: normal audits never mutate
it. A changed summary, URL or newly added document is reported as
``review_required`` and must be approved with ``--update-lock --reviewed-by``.
Online checks are advisory by default because vendor sites may rate-limit bots;
``--strict-online`` turns them into a release gate.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import sys
import urllib.error
import urllib.request
from datetime import datetime, timezone
from pathlib import Path
from urllib.parse import urlparse


ROOT = Path(__file__).resolve().parents[1]
CORPUS = ROOT / "datasets/domains/medical-device-sales/corpus"
DEFAULT_MANIFEST = CORPUS / "manifest.json"
DEFAULT_LOCK = CORPUS / "sources.lock.json"


def display_path(path: Path) -> str:
    """Keep committed reports portable while still accepting external paths."""
    try:
        return path.resolve().relative_to(ROOT).as_posix()
    except ValueError:
        return path.name
ALLOWED_SOURCE_DOMAINS = ("mindray.com", "philips.com", "philips.com.cn", "draeger.com", "nmpa.gov.cn")
MAX_REMOTE_BYTES = 2 * 1024 * 1024


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def allowed_host(host: str) -> bool:
    host = host.lower().rstrip(".")
    return any(host == domain or host.endswith("." + domain) for domain in ALLOWED_SOURCE_DOMAINS)


def validate_source_url(raw: str) -> str | None:
    parsed = urlparse(raw)
    if parsed.scheme != "https" or not parsed.hostname or parsed.username or parsed.password:
        return "source URL must be an absolute HTTPS URL without credentials"
    if not allowed_host(parsed.hostname):
        return f"source host is not allowlisted: {parsed.hostname}"
    return None


def fetch_source(raw: str, timeout: float) -> dict:
    request = urllib.request.Request(
        raw,
        headers={
            "User-Agent": "rag-evolution-lab-source-audit/1.0 (+human-reviewed RAG corpus)",
            "Accept": "text/html,application/xhtml+xml,application/json;q=0.8,*/*;q=0.5",
        },
    )
    started = datetime.now(timezone.utc)
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            body = response.read(MAX_REMOTE_BYTES + 1)
            truncated = len(body) > MAX_REMOTE_BYTES
            body = body[:MAX_REMOTE_BYTES]
            final_url = response.geturl()
            final_host = urlparse(final_url).hostname or ""
            return {
                "url": raw,
                "status": "healthy" if allowed_host(final_host) else "unsafe_redirect",
                "http_status": response.status,
                "final_url": final_url,
                "content_type": response.headers.get("Content-Type", ""),
                "etag": response.headers.get("ETag", ""),
                "last_modified": response.headers.get("Last-Modified", ""),
                "response_sha256": sha256(body),
                "bytes_hashed": len(body),
                "truncated": truncated,
                "latency_ms": round((datetime.now(timezone.utc) - started).total_seconds() * 1000),
            }
    except urllib.error.HTTPError as error:
        return {"url": raw, "status": "unavailable", "http_status": error.code, "error": str(error)}
    except Exception as error:  # Network/DNS/TLS failures are report data, not hidden exceptions.
        return {"url": raw, "status": "unavailable", "http_status": 0, "error": str(error)}


def markdown_report(report: dict) -> str:
    summary = report["summary"]
    lines = [
        "# 医疗公开资料来源审计",
        "",
        f"- 审计时间：{report['checked_at']}",
        f"- 结论：**{summary['overall_status']}**",
        f"- 文档：{summary['documents']}，已审核：{summary['approved']}，待复核：{summary['review_required']}",
        f"- 本地内容漂移：{summary['local_drift']}，在线异常：{summary['remote_issues']}",
        "",
        "| 文档 | 本地审核 | 内容指纹 | 在线来源 |",
        "| --- | --- | --- | --- |",
    ]
    for item in report["documents"]:
        remote = "、".join(source["status"] for source in item.get("online_sources", [])) or "未执行"
        lines.append(f"| `{item['doc_id']}` | {item['review_status']} | `{item['content_sha256'][:12]}` | {remote} |")
    lines.extend([
        "",
        "> 在线网页哈希变化只触发人工复核，不会自动覆盖已经发布的知识索引。",
        "",
    ])
    return "\n".join(lines)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--manifest", type=Path, default=DEFAULT_MANIFEST)
    parser.add_argument("--lock", type=Path, default=DEFAULT_LOCK)
    parser.add_argument("--online", action="store_true", help="check official URLs and record bounded response fingerprints")
    parser.add_argument("--strict-online", action="store_true", help="fail when a source is unavailable, redirects outside the allowlist, or changed")
    parser.add_argument("--timeout", type=float, default=12.0)
    parser.add_argument("--update-lock", action="store_true", help="approve the current local corpus after human review")
    parser.add_argument("--reviewed-by", default="")
    parser.add_argument("--json-report", type=Path)
    parser.add_argument("--markdown-report", type=Path)
    args = parser.parse_args()

    if args.update_lock and not args.reviewed_by.strip():
        parser.error("--reviewed-by is required with --update-lock")

    manifest = json.loads(args.manifest.read_text(encoding="utf-8"))
    existing_lock = json.loads(args.lock.read_text(encoding="utf-8")) if args.lock.exists() else {"documents": {}}
    locked_documents = existing_lock.get("documents", {})
    seen: set[str] = set()
    documents: list[dict] = []
    manifest_errors: list[str] = []

    for entry in manifest:
        doc_id = str(entry.get("doc_id", "")).strip()
        if not doc_id or doc_id in seen:
            manifest_errors.append(f"missing or duplicate doc_id: {doc_id!r}")
            continue
        seen.add(doc_id)
        relative = Path(str(entry.get("path", "")))
        target = (args.manifest.parent / relative).resolve()
        try:
            target.relative_to(args.manifest.parent.resolve())
        except ValueError:
            manifest_errors.append(f"{doc_id}: path escapes corpus root")
            continue
        if not target.is_file():
            manifest_errors.append(f"{doc_id}: file does not exist: {relative}")
            continue
        urls = [str(value).strip() for value in entry.get("source_urls", []) if str(value).strip()]
        url_errors = [f"{doc_id}: {error}" for raw in urls if (error := validate_source_url(raw))]
        if entry.get("source_type") != "company_service_policy" and not urls:
            url_errors.append(f"{doc_id}: externally sourced documents require source_urls")
        manifest_errors.extend(url_errors)

        content_hash = sha256(target.read_bytes())
        locked = locked_documents.get(doc_id, {})
        local_matches = (
            locked.get("path") == relative.as_posix()
            and locked.get("content_sha256") == content_hash
            and locked.get("source_urls") == urls
        )
        online_sources = [fetch_source(raw, args.timeout) for raw in urls] if args.online and not url_errors else []
        locked_remote = locked.get("remote_fingerprints", {})
        for source in online_sources:
            previous = locked_remote.get(source["url"])
            if source["status"] == "healthy" and previous and previous != source.get("response_sha256"):
                source["status"] = "changed"
            elif source["status"] == "healthy" and not previous:
                source["status"] = "healthy_unbaselined"
        documents.append({
            "doc_id": doc_id,
            "title": entry.get("title", ""),
            "path": relative.as_posix(),
            "content_sha256": content_hash,
            "source_type": entry.get("source_type", ""),
            "source_urls": urls,
            "collected_at": entry.get("collected_at", ""),
            "review_status": "approved" if local_matches else "review_required",
            "online_sources": online_sources,
        })

    if args.update_lock and not manifest_errors:
        approved_at = datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")
        new_lock = {
            "schema_version": 1,
            "dataset_id": "public-medical-device-sales",
            "approved_at": approved_at,
            "reviewed_by": args.reviewed_by.strip(),
            "documents": {},
        }
        for item in documents:
            remote_fingerprints = {
                source["url"]: source["response_sha256"]
                for source in item["online_sources"]
                if source["status"] in {"healthy", "healthy_unbaselined"} and source.get("response_sha256")
            }
            if not remote_fingerprints:
                remote_fingerprints = locked_documents.get(item["doc_id"], {}).get("remote_fingerprints", {})
            new_lock["documents"][item["doc_id"]] = {
                "path": item["path"],
                "content_sha256": item["content_sha256"],
                "source_urls": item["source_urls"],
                "reviewed_at": approved_at,
                "reviewed_by": args.reviewed_by.strip(),
                "remote_fingerprints": remote_fingerprints,
            }
            item["review_status"] = "approved"
        args.lock.write_text(json.dumps(new_lock, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

    locked_ids = set(locked_documents)
    orphaned_locks = sorted(locked_ids - seen)
    if orphaned_locks:
        manifest_errors.append("lock contains removed documents: " + ", ".join(orphaned_locks))
    remote_issues = sum(
        source["status"] in {"unavailable", "unsafe_redirect", "changed"}
        for item in documents for source in item["online_sources"]
    )
    review_required = sum(item["review_status"] != "approved" for item in documents)
    local_drift = review_required
    overall_status = "passed"
    if manifest_errors or review_required or (args.strict_online and remote_issues):
        overall_status = "failed"
    elif remote_issues:
        overall_status = "passed_with_online_warnings"
    report = {
        "schema_version": 1,
        "checked_at": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
        "manifest": display_path(args.manifest),
        "lock": display_path(args.lock),
        "summary": {
            "overall_status": overall_status,
            "documents": len(documents),
            "approved": len(documents) - review_required,
            "review_required": review_required,
            "local_drift": local_drift,
            "online_checked": sum(len(item["online_sources"]) for item in documents),
            "remote_issues": remote_issues,
        },
        "manifest_errors": manifest_errors,
        "documents": documents,
    }
    rendered = json.dumps(report, ensure_ascii=False, indent=2) + "\n"
    if args.json_report:
        args.json_report.parent.mkdir(parents=True, exist_ok=True)
        args.json_report.write_text(rendered, encoding="utf-8")
    if args.markdown_report:
        args.markdown_report.parent.mkdir(parents=True, exist_ok=True)
        args.markdown_report.write_text(markdown_report(report), encoding="utf-8")
    print(rendered, end="")
    return 0 if overall_status != "failed" else 1


if __name__ == "__main__":
    sys.exit(main())
