#!/usr/bin/env python3
"""Wait until portable medical bootstrap jobs are durably indexed."""

from __future__ import annotations

import argparse
import json
import os
import time
import urllib.error
import urllib.request


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


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--api", default=os.getenv("RAGLAB_API_URL", "http://127.0.0.1:8080"))
    parser.add_argument("--timeout", type=int, default=900)
    args = parser.parse_args()
    base = args.api.rstrip("/")
    login = request(base + "/api/v1/auth/login", "POST", body={
        "email": "admin@raglab.local",
        "password": os.getenv("RAGLAB_PLATFORM_ADMIN_PASSWORD", "change-this-admin-password"),
    })
    token = login["access_token"]
    deadline = time.monotonic() + args.timeout
    last_summary = ""
    while time.monotonic() < deadline:
        records = [item for item in request(f"{base}/api/v1/ingestion/jobs", token=token).get("jobs", []) if item.get("dataset_id") in DATASETS]
        present = {str(item.get("dataset_id")) for item in records}
        missing = [dataset_id for dataset_id in DATASETS if dataset_id not in present]
        failed = [item for item in records if item.get("status") in {"failed", "cancelled"}]
        if failed:
            details = [{"dataset_id": item.get("dataset_id"), "job_id": item.get("job_id"), "status": item.get("status"), "error": item.get("error")} for item in failed]
            raise RuntimeError("ingestion failed: " + json.dumps(details, ensure_ascii=False))
        pending = [item for item in records if item.get("status") != "completed"]
        summary = f"documents={len(records)} pending={len(pending)} missing_datasets={len(missing)}"
        if summary != last_summary:
            print(f"ingestion_wait {summary}", flush=True)
            last_summary = summary
        if not missing and records and not pending:
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
