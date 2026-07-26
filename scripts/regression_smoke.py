#!/usr/bin/env python3
"""Run a small, repeatable live API regression against a selected lab profile.

The script deliberately accepts an API URL so the same checks can run against
the production-like portal profile or the isolated evaluation profile.
"""

from __future__ import annotations

import argparse
import json
import sys
import time
import urllib.error
import urllib.request
from typing import Any


DEMO_ACCOUNTS = {
    "alice": ("alice@tenant-a.local", "RagLab-Alice-2026!"),
    "bob": ("bob@tenant-b.local", "RagLab-Bob-2026!"),
    "platform": ("admin@raglab.local", "RagLab-Platform-2026!"),
}


def call(base_url: str, method: str, path: str, payload: Any = None, token: str | None = None) -> tuple[int, dict[str, Any]]:
    body = None if payload is None else json.dumps(payload, ensure_ascii=False).encode()
    request = urllib.request.Request(
        base_url.rstrip("/") + path,
        data=body,
        method=method,
        headers={"Content-Type": "application/json"},
    )
    if token:
        request.add_header("Authorization", f"Bearer {token}")
    try:
        with urllib.request.urlopen(request, timeout=60) as response:
            return response.status, json.loads(response.read())
    except urllib.error.HTTPError as error:
        return error.code, json.loads(error.read())


def login(base_url: str, account: str) -> str:
    email, password = DEMO_ACCOUNTS[account]
    status, body = call(base_url, "POST", "/api/v1/auth/login", {"email": email, "password": password})
    assert status == 200, (account, status, body)
    return body["access_token"]


def stream_answer(base_url: str, token: str) -> str:
    payload = json.dumps({"query": "企业单点登录如何配置？", "top_k": 5}).encode()
    request = urllib.request.Request(
        base_url.rstrip("/") + "/api/v1/datasets/public-identity/answer/stream",
        data=payload,
        method="POST",
        headers={"Content-Type": "application/json", "Authorization": f"Bearer {token}"},
    )
    with urllib.request.urlopen(request, timeout=90) as response:
        content = response.read().decode()
    for event in ("event: started", "event: retrieved", "event: completed", "event: done"):
        assert event in content, event
    return ",".join(event.removeprefix("event: ") for event in ("event: started", "event: retrieved", "event: completed", "event: done"))


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--api", default="http://127.0.0.1:8080", help="enterprise lab API base URL")
    args = parser.parse_args()
    base_url = args.api.rstrip("/")

    try:
        alice = login(base_url, "alice")
        bob = login(base_url, "bob")
        platform = login(base_url, "platform")

        status, body = call(base_url, "GET", "/api/v1/datasets", token=alice)
        assert status == 200
        alice_ids = {dataset["id"] for dataset in body["datasets"]}
        assert {"public-identity", "public-reports", "tenant-a-operations"}.issubset(alice_ids)
        assert "tenant-b-operations" not in alice_ids

        status, body = call(base_url, "GET", "/api/v1/datasets", token=bob)
        assert status == 200
        bob_ids = {dataset["id"] for dataset in body["datasets"]}
        assert {"public-identity", "public-reports", "tenant-b-operations"}.issubset(bob_ids)
        assert "tenant-a-operations" not in bob_ids

        status, body = call(base_url, "POST", "/api/v1/datasets/tenant-a-operations/search", {"query": "private queue", "top_k": 5}, alice)
        assert status == 200
        assert 'array_contains(allowed_tenants, "tenant_a")' in body["result"]["filter"]

        status, _ = call(base_url, "POST", "/api/v1/datasets/tenant-a-operations/search", {"query": "private queue", "top_k": 5}, bob)
        assert status == 404

        revision = int(time.time())
        status, body = call(
            base_url,
            "POST",
            "/api/v1/datasets/tenant-a-operations/documents",
            {
                "document_id": "regression-smoke",
                "title": "回归导入资料",
                "content": "这份资料验证资料导入、分块、Embedding、Milvus Upsert 和读回校验。",
                "version": "smoke",
                "source_revision": revision,
                "event_id": f"regression-smoke-{revision}",
            },
            alice,
        )
        assert status == 200 and body["result"]["verified"] is True

        status, _ = call(
            base_url,
            "POST",
            "/api/v1/datasets/tenant-a-operations/documents",
            {"document_id": "regression-forbidden", "title": "deny", "content": "deny"},
            bob,
        )
        assert status == 404

        events = stream_answer(base_url, alice)
        status, body = call(base_url, "GET", "/api/v1/audit/recent", token=platform)
        assert status == 200 and "events" in body
        status, _ = call(base_url, "GET", "/api/v1/audit/recent", token=alice)
        assert status == 403
    except (AssertionError, KeyError, urllib.error.URLError) as error:
        print(f"regression_smoke=failed error={error}", file=sys.stderr)
        return 1

    print("regression_smoke=passed")
    print(f"api={base_url}")
    print(f"alice_visible_datasets={len(alice_ids)} bob_visible_datasets={len(bob_ids)}")
    print(f"sse_events={events}")
    print("tenant_cross_access=404 document_ingest=verified")
    print("platform_audit=200 tenant_audit=403")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
