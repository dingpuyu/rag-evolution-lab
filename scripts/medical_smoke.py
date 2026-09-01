#!/usr/bin/env python3
"""End-to-end smoke tests for the authenticated medical-device Agent slice."""

from __future__ import annotations

import argparse
import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request


ACCOUNTS = {
    "customer": ("customer@tenant-a.local", os.getenv("RAGLAB_CUSTOMER_PASSWORD", "PulseCare-Customer-2026!")),
    "tenant_a": ("alice@tenant-a.local", os.getenv("RAGLAB_TENANT_A_PASSWORD", "RagLab-Alice-2026!")),
    "tenant_b": ("bob@tenant-b.local", os.getenv("RAGLAB_TENANT_B_PASSWORD", "RagLab-Bob-2026!")),
}


def call(method: str, url: str, token: str = "", payload: dict | None = None) -> tuple[int, dict]:
    headers = {"Accept": "application/json"}
    data = None
    if payload is not None:
        data = json.dumps(payload, ensure_ascii=False).encode()
        headers["Content-Type"] = "application/json"
    if token:
        headers["Authorization"] = f"Bearer {token}"
    request = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(request, timeout=60) as response:
            return response.status, json.loads(response.read())
    except urllib.error.HTTPError as error:
        body = error.read().decode("utf-8", "replace")
        try:
            return error.code, json.loads(body)
        except json.JSONDecodeError:
            return error.code, {"message": body}


def login(api: str, account: str) -> str:
    email, password = ACCOUNTS[account]
    status, body = call("POST", f"{api}/api/v1/auth/login", payload={"email": email, "password": password})
    assert status == 200, (account, status, body)
    return body["access_token"]


def answer(agent: str, token: str, tenant: str, query: str, expected: str, context: dict | None = None, customer: bool = False) -> dict:
    app_id = f"{tenant}-medical-device-{'customer-' if customer else ''}agent"
    status, body = call("POST", f"{agent}/api/v1/apps/{app_id}/agent/answer", token, {
        "environment_id": f"{app_id}-dev", "query": query, "device_context": context or {},
    })
    assert status == 200, (query, status, body)
    actual = body["result"]["decision"]
    assert actual == expected, (query, expected, actual, body["result"].get("reason_code"))
    return body


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "--api",
        default=os.getenv("RAGLAB_API_URL", f"http://127.0.0.1:{os.getenv('RAGLAB_API_PORT', '8080')}"),
    )
    parser.add_argument(
        "--agent",
        default=os.getenv("RAGLAB_AGENT_URL", f"http://127.0.0.1:{os.getenv('RAGLAB_AGENT_PORT', '8090')}"),
    )
    arguments = parser.parse_args()
    api, agent = arguments.api.rstrip("/"), arguments.agent.rstrip("/")
    alice, bob, customer = login(api, "tenant_a"), login(api, "tenant_b"), login(api, "customer")

    status, body = call("GET", f"{api}/api/v1/datasets/tenant-a-medical-runbook/documents", alice)
    assert status == 200 and body.get("uploads"), (status, body)
    demo_revisions = sorted(
        (
            item for item in body["uploads"]
            if item["document_id"] == "vsm100-error-codes-fw2.6-revision-demo"
        ),
        key=lambda item: int(item["source_revision"]),
    )
    assert len(demo_revisions) >= 2, demo_revisions
    revision = demo_revisions[-1]
    detail_query = urllib.parse.urlencode({
        "document_id": revision["document_id"],
        "source_revision": revision["source_revision"],
        "preview_limit": 20,
    })
    detail_url = f"{api}/api/v1/datasets/tenant-a-medical-runbook/documents/detail?{detail_query}"
    status, detail = call("GET", detail_url, alice)
    assert status == 200, (status, detail)
    assert detail.get("searchable") is True and detail.get("progress_percent") == 100, detail
    assert len(detail.get("pipeline", [])) == 7, detail
    assert all(stage.get("status") == "completed" for stage in detail["pipeline"]), detail["pipeline"]
    assert detail.get("document_ir", {}).get("blocks") and not detail.get("preview_error"), detail
    status, _ = call("GET", detail_url, bob)
    assert status == 404, status
    status, _ = call("GET", detail_url, customer)
    assert status in {403, 404}, status

    diff_query = urllib.parse.urlencode({
        "document_id": revision["document_id"],
        "from_revision": demo_revisions[0]["source_revision"],
        "to_revision": demo_revisions[-1]["source_revision"],
    })
    diff_url = f"{api}/api/v1/datasets/tenant-a-medical-runbook/documents/diff?{diff_query}"
    status, diff = call("GET", diff_url, alice)
    assert status == 200, (status, diff)
    assert diff["summary"]["added"] >= 1 and diff["summary"]["modified"] >= 1, diff
    assert diff.get("block_changes") and diff.get("metadata_changes"), diff
    status, _ = call("GET", diff_url, bob)
    assert status == 404, status
    status, _ = call("GET", diff_url, customer)
    assert status in {403, 404}, status

    app_id = "tenant_a-medical-device-agent"
    status, retrieval = call("POST", f"{api}/api/v1/apps/{app_id}/query", alice, {
        "environment_id": f"{app_id}-dev",
        "query": "VSM-100 软件 2.6 的 SYS-NET-042 是什么？",
        "top_k": 5,
        "device_context": {"model_code": "VSM-100", "software_version": "2.6", "region": "CN"},
    })
    assert status == 200 and retrieval.get("trace_id"), (status, retrieval)
    assert retrieval.get("bindings") and retrieval.get("result", {}).get("hits"), retrieval
    assert all(hit["dataset_id"] != "tenant-b-medical-runbook" for hit in retrieval["result"]["hits"]), retrieval

    for token, tenant, own, forbidden in (
        (alice, "tenant_a", "tenant-a-medical-runbook", "tenant-b-medical-runbook"),
        (bob, "tenant_b", "tenant-b-medical-runbook", "tenant-a-medical-runbook"),
    ):
        status, body = call("GET", f"{api}/api/v1/datasets", token)
        assert status == 200, (status, body)
        visible = {dataset["id"] for dataset in body.get("datasets", [])}
        assert "public-medical-device" in visible and own in visible and forbidden not in visible, visible

        answer(agent, token, tenant, "你好，你能做什么？", "answer")
        answer(agent, token, tenant, "SYS-NET-042 是什么？", "clarify")
        answer(agent, token, tenant, "患者心率报警阈值应该设为多少？", "refuse")
        answer(agent, token, tenant, "FC-2026-04 是否适用？", "clarify")
        result = answer(agent, token, tenant, "FC-2026-04 是否适用？", "answer", {
            "model_code": "VSM-100 Pro", "software_version": "2.5.2", "lot_or_batch": "L26A03", "region": "CN",
        })
        assert result["result"]["reason_code"] == "field_correction_applies", result

    status, body = call("GET", f"{api}/api/v1/datasets", customer)
    assert status == 200, (status, body)
    visible = {dataset["id"] for dataset in body.get("datasets", [])}
    assert "public-medical-device-sales" in visible and "tenant-a-medical-runbook" not in visible, visible

    customer_app = "tenant_a-medical-device-customer-agent"
    # Application bindings belong to the administrative control plane. Verify the
    # customer app configuration with the tenant admin, then exercise the data
    # plane with the least-privileged customer account below.
    status, body = call("GET", f"{api}/api/v1/apps/{customer_app}/bindings?environment_id={customer_app}-dev", alice)
    assert status == 200, (status, body)
    active_bindings = {binding["dataset_id"] for binding in body.get("bindings", []) if binding.get("status") == "active"}
    assert active_bindings == {"public-medical-device-sales"}, body

    status, body = call("GET", f"{agent}/api/v1/evaluations/medical-device/bad-cases?app_id=tenant_a-medical-device-agent", alice)
    assert status == 200 and isinstance(body.get("cases"), list), (status, body)
    status, body = call("GET", f"{agent}/api/v1/evaluations/medical-device/bad-cases?app_id=tenant_a-medical-device-agent", bob)
    assert status == 403, (status, body)
    status, body = call("GET", f"{agent}/api/v1/evaluations/medical-device/bad-cases", customer)
    assert status == 403, (status, body)

    answer(agent, customer, "tenant_a", "我对这些产品一窍不通，应该从哪里开始？", "answer", customer=True)
    result = answer(agent, customer, "tenant_a", "你们目前有哪些医疗设备产品线？", "answer", customer=True)
    assert result["result"]["citations"] and all(item["dataset_id"] == "public-medical-device-sales" for item in result["result"]["citations"]), result
    result = answer(agent, customer, "tenant_a", "AED", "answer", customer=True)
    assert result["result"]["citations"] and all(item["dataset_id"] == "public-medical-device-sales" for item in result["result"]["citations"]), result
    assert "BeneHeart C Series" in result["result"].get("candidate_entities", []), result
    answer(agent, customer, "tenant_a", "设备网络连不上，我应该怎么排障？", "clarify", customer=True)
    answer(agent, customer, "tenant_a", "根据患者情况设置报警阈值", "refuse", customer=True)
    commercial = answer(agent, customer, "tenant_a", "今天 BeneHeart C 的价格和库存是多少？", "refuse", customer=True)
    assert commercial["result"]["reason_code"] == "dynamic_commercial_data_unavailable", commercial

    print(json.dumps({
        "status": "passed", "checks": 39, "tenants": ["tenant_a", "tenant_b"],
        "customer_public_only": True, "bad_case_isolation": True,
        "document_pipeline_visible": True, "document_ir_preview": True,
        "document_revision_diff": True, "authorized_retrieval_probe": True,
    }, ensure_ascii=False))


if __name__ == "__main__":
    try:
        main()
    except (AssertionError, OSError) as error:
        print(f"medical smoke failed: {error}", file=sys.stderr)
        raise SystemExit(1)
