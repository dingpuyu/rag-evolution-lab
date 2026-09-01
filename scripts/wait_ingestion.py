#!/usr/bin/env python3
"""Wait until portable medical bootstrap jobs are durably indexed."""

from __future__ import annotations

import argparse
import json
import os
import time
import urllib.error
import urllib.request
from pathlib import Path


DATASETS = (
    "public-medical-device",
    "tenant-a-medical-runbook",
    "tenant-b-medical-runbook",
    "public-medical-device-sales",
)


def request(url: str, method: str = "GET", token: str = "", body: dict | None = None) -> dict:
    payload = json.dumps(body).encode() if body is not None else None
    headers = {"Accept": "application/json"}
    if payload is not None:
        headers["Content-Type"] = "application/json"
    if token:
        headers["Authorization"] = f"Bearer {token}"
    try:
        with urllib.request.urlopen(urllib.request.Request(url, data=payload, headers=headers, method=method), timeout=60) as response:
            return json.load(response)
    except urllib.error.HTTPError as error:
        raise RuntimeError(f"{method} {url} returned {error.code}: {error.read().decode(errors='replace')[:800]}") from error


def expected_job_ids(path: Path | None) -> set[str]:
    if path is None:
        return set()
    report = json.loads(path.read_text(encoding="utf-8"))
    job_ids = {
        str(item.get("job_id", "")).strip()
        for item in report.get("documents", [])
        if str(item.get("job_id", "")).strip()
    }
    if not job_ids:
        raise RuntimeError(f"job report contains no job IDs: {path}")
    return job_ids


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--api",
        default=os.getenv("RAGLAB_API_URL", f"http://127.0.0.1:{os.getenv('RAGLAB_API_PORT', '8080')}"),
    )
    parser.add_argument("--timeout", type=int, default=900)
    parser.add_argument(
        "--job-report",
        type=Path,
        help="wait only for the exact jobs emitted by medical_bootstrap.py; avoids historical failure pollution",
    )
    args = parser.parse_args()
    base = args.api.rstrip("/")
    expected = expected_job_ids(args.job_report)
    login = request(base + "/api/v1/auth/login", "POST", body={
        "email": "admin@raglab.local",
        "password": os.getenv("RAGLAB_PLATFORM_ADMIN_PASSWORD", "change-this-admin-password"),
    })
    token = login["access_token"]
    deadline = time.monotonic() + args.timeout
    last_summary = ""
    while time.monotonic() < deadline:
        all_records = request(f"{base}/api/v1/ingestion/jobs", token=token).get("jobs", [])
        records = [
            item for item in all_records
            if item.get("job_id") in expected
        ] if expected else [item for item in all_records if item.get("dataset_id") in DATASETS]
        missing_jobs = sorted(expected - {str(item.get("job_id", "")) for item in records})
        present = {str(item.get("dataset_id")) for item in records}
        missing = [dataset_id for dataset_id in DATASETS if dataset_id not in present]
        failed = [item for item in records if item.get("status") in {"failed", "cancelled"}]
        if failed:
            details = [{"dataset_id": item.get("dataset_id"), "job_id": item.get("job_id"), "status": item.get("status"), "error": item.get("error")} for item in failed]
            raise RuntimeError("ingestion failed: " + json.dumps(details, ensure_ascii=False))
        pending = [item for item in records if item.get("status") != "completed"]
        summary = f"jobs={len(records)} pending={len(pending)} missing_jobs={len(missing_jobs)} missing_datasets={len(missing)}"
        if summary != last_summary:
            print(f"ingestion_wait {summary}", flush=True)
            last_summary = summary
        if not missing_jobs and not missing and records and not pending:
            # Once the first collection exists, the document catalog must also
            # be readable for every dataset; this catches a completed job whose
            # metadata never became visible to the control plane.
            for dataset_id in DATASETS:
                uploads = request(f"{base}/api/v1/datasets/{dataset_id}/documents", token=token).get("uploads", [])
                if not uploads:
                    raise RuntimeError(f"no indexed document is visible for {dataset_id}")
            print(f"ingestion_wait=passed documents={len(records)} datasets={len(DATASETS)}", flush=True)
            return
        time.sleep(3)
    raise TimeoutError(f"ingestion did not complete within {args.timeout}s ({last_summary})")


if __name__ == "__main__":
    main()
