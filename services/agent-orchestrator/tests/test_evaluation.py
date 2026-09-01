from app.evaluation import EvaluationStore, agent_cases_for, bad_case_to_golden, evaluate_case, evaluate_retrieval_case, evaluation_request_interval_seconds, golden_cases_for, golden_device_context, load_medical_golden, load_medical_sales_golden
from app.models import AgentResponse, AgentResult, DeviceContext, SearchHit


async def test_memory_evaluation_store_is_tenant_scoped():
    store = EvaluationStore()
    run = await store.create("tenant_a", "alice", "tenant_a-medical-device-agent", "tenant_a-medical-device-agent-dev")
    fetched = await store.get(run["run_id"], "tenant_a")
    assert fetched["status"] == "queued"
    try:
        await store.get(run["run_id"], "tenant_b")
    except KeyError:
        pass
    else:
        raise AssertionError("cross-tenant evaluation run must not be readable")
    await store.add_case(run["run_id"], {"case_id": "rag:one", "query": "q", "expected_decision": "hit", "actual_decision": "miss", "passed": False})
    reviewed = await store.review_case(run["run_id"], "rag:one", "bad_case", "wrong_model", "人工复核")
    assert reviewed["review_status"] == "bad_case"
    assert reviewed["root_cause"] == "wrong_model"


async def test_latest_evaluation_is_tenant_and_environment_scoped():
    store = EvaluationStore()
    first = await store.create("tenant_a", "alice", "medical-a", "dev")
    second = await store.create("tenant_a", "alice", "medical-a", "staging")
    await store.create("tenant_b", "bob", "medical-b", "dev")

    latest = await store.latest("tenant_a")
    assert latest["run_id"] == second["run_id"]
    dev = await store.latest("tenant_a", "medical-a", "dev")
    assert dev["run_id"] == first["run_id"]
    try:
        await store.latest("tenant_a", "medical-b")
    except KeyError:
        pass
    else:
        raise AssertionError("latest evaluation must not cross tenant boundaries")


async def test_bad_case_lifecycle_requires_verification_before_regression_promotion():
    store = EvaluationStore()
    run = await store.create("tenant_a", "alice", "tenant_a-medical-device-agent", "tenant_a-medical-device-agent-dev")
    await store.add_case(run["run_id"], {
        "case_id": "rag:wrong-rank", "query": "VSM-100 Pro WLM-2 最低固件",
        "expected_decision": "hit", "actual_decision": "miss", "passed": False,
        "details": {
            "layer": "rag", "relevant_document_ids": ["compatibility-xlsx"],
            "retrieved_document_ids": ["noise"],
            "source_location_checks": [{"expected": {"document_id": "compatibility-xlsx", "source_sheet": "兼容矩阵"}}],
            "device_context": {"model_code": "VSM-100 Pro", "software_version": "2.6", "region": "CN"},
        },
    })
    case = (await store.list_cases(run["run_id"]))[0]
    bad_case = await store.create_bad_case(run, case, "alice", "rerank_order", "确认目标行应排第一")
    assert bad_case["status"] == "open"
    assert bad_case["expected_document_ids"] == ["compatibility-xlsx"]
    assert len(await store.list_bad_cases("tenant_a")) == 1
    assert await store.list_bad_cases("tenant_b") == []

    try:
        await store.promote_bad_case(bad_case["bad_case_id"], "tenant_a", "alice")
    except ValueError:
        pass
    else:
        raise AssertionError("an unverified bad case must not enter the regression suite")

    diagnosed = await store.update_bad_case(
        bad_case["bad_case_id"], "tenant_a", "alice", "rerank_order", "修复 XLSX 型号元数据",
        ["compatibility-xlsx"], bad_case["expected_source_locations"], bad_case["device_context"],
    )
    assert diagnosed["status"] == "diagnosed"
    verified = await store.record_bad_case_verification(
        bad_case["bad_case_id"], {"passed": True, "trace_id": "trace-fixed", "metrics": {"mrr": 1.0}}, "alice",
    )
    assert verified["status"] == "verified"
    attempts = await store.list_bad_case_attempts(bad_case["bad_case_id"], "tenant_a")
    assert attempts[0]["result"]["trace_id"] == "trace-fixed"
    try:
        await store.list_bad_case_attempts(bad_case["bad_case_id"], "tenant_b")
    except KeyError:
        pass
    else:
        raise AssertionError("verification attempts must remain tenant scoped")
    promoted = await store.promote_bad_case(bad_case["bad_case_id"], "tenant_a", "alice")
    assert promoted["status"] == "regression"
    golden = bad_case_to_golden(promoted)
    assert golden["split"] == "human_regression"
    assert golden["expected"]["source_locations"][0]["source_sheet"] == "兼容矩阵"
    assert len(await store.promoted_golden_cases("tenant_a", run["app_id"])) == 1

    next_run = await store.create("tenant_a", "alice", run["app_id"], run["environment_id"])
    static_total = len(agent_cases_for(run["app_id"])) + len(golden_cases_for(run["app_id"], "tenant_a"))
    assert next_run["total_cases"] == static_total + 1


async def test_editing_verified_bad_case_invalidates_verification_and_promotion():
    store = EvaluationStore()
    run = await store.create("tenant_a", "alice", "tenant_a-medical-device-agent", "dev")
    await store.add_case(run["run_id"], {
        "case_id": "rag:one", "query": "q", "expected_decision": "hit", "actual_decision": "miss",
        "passed": False, "details": {"layer": "rag", "relevant_document_ids": ["right"]},
    })
    item = await store.create_bad_case(run, (await store.list_cases(run["run_id"]))[0], "alice", "other", "")
    await store.record_bad_case_verification(item["bad_case_id"], {"passed": True}, "alice")
    edited = await store.update_bad_case(item["bad_case_id"], "tenant_a", "alice", "wrong_model", "changed", ["new-right"], [], {})
    assert edited["status"] == "diagnosed"
    assert edited["last_verification"] == {}
    assert edited["promoted_at"] is None


def test_evaluate_case_checks_decision_reason_and_citations():
    response = AgentResponse(
        app_id="tenant_a-medical-device-agent",
        environment_id="tenant_a-medical-device-agent-dev",
        thread_id="thread-1",
        result=AgentResult(
            status="completed", decision="answer", reason_code="field_correction_applies",
            answer="适用", answer_source="tool", resolved_context=DeviceContext(), citations=[],
        ),
    )
    result = evaluate_case(
        {"id": "notice", "query": "是否适用", "expected": "answer", "reason": "field_correction_applies", "citations": 1},
        response,
        12.0,
    )
    assert not result["passed"]
    assert result["details"]["layer"] == "agent"
    assert result["details"]["checks"] == {"decision": True, "reason": True, "citations": False, "public_only": True, "answer_boundary": True}


def test_evaluate_case_checks_customer_answer_boundary_language():
    response = AgentResponse(
        app_id="tenant_a-medical-device-customer-agent",
        environment_id="tenant_a-medical-device-customer-agent-dev",
        thread_id="thread-1",
        result=AgentResult(
            status="completed", decision="answer", reason_code="grounded_customer_answer",
            answer="所有型号都有，而且保证有现货。", answer_source="rag", resolved_context=DeviceContext(), citations=[],
        ),
    )
    result = evaluate_case(
        {"id": "unsafe-claim", "query": "直接承诺", "expected": "answer", "required_answer_any": ["不能", "需确认", "核验"]},
        response,
        3.0,
    )
    assert not result["passed"]
    assert result["details"]["checks"]["answer_boundary"] is False


def test_medical_golden_contains_development_and_regression_cases():
    cases = load_medical_golden()
    assert len(cases) == 43
    assert {case["split"] for case in cases} == {"development", "regression"}


def test_medical_sales_golden_is_an_independent_official_source_suite():
    cases = load_medical_sales_golden()
    assert len(cases) == 73
    assert {case["split"] for case in cases} == {"development", "regression"}
    assert any(case["id"] == "sales_nmpa_udi" for case in cases)


def test_retrieval_case_scores_rank_and_provenance():
    case = {
        "id": "exact", "query": "SYS-NET-042", "split": "regression", "category": "exact_match",
        "expected": {"answerable": True, "relevant_doc_ids": ["right"]},
    }
    item, score = evaluate_retrieval_case(case, {
        "trace_id": "trace-1", "bindings": [{"dataset_id": "public-medical-device"}],
        "result": {"hits": [{"document_id": "noise"}, {"document_id": "right"}]},
    }, 12.0)
    assert item["passed"]
    assert item["trace_id"] == "trace-1"
    assert score["hit5"] == 1
    assert score["mrr"] == 0.5


def test_retrieval_case_deduplicates_chunks_before_document_metrics():
    case = {
        "id": "duplicate-chunks", "query": "SYS-NET-042", "split": "regression", "category": "exact_match",
        "expected": {"answerable": True, "relevant_doc_ids": ["right"]},
    }
    item, score = evaluate_retrieval_case(case, {
        "result": {"hits": [
            {"chunk_id": "right-1", "document_id": "right"},
            {"chunk_id": "right-2", "document_id": "right"},
            {"chunk_id": "noise-1", "document_id": "noise"},
        ]},
    }, 4.0)
    assert item["details"]["retrieved_document_ids"] == ["right", "noise"]
    assert score == {"hit5": 1.0, "mrr": 1.0, "ndcg": 1.0}


def test_retrieval_case_requires_expected_source_location():
    case = {
        "id": "xlsx-location", "query": "最低固件", "split": "regression", "category": "cross_format_provenance",
        "expected": {
            "answerable": True,
            "relevant_doc_ids": ["matrix-xlsx"],
            "source_locations": [{
                "document_id": "matrix-xlsx", "source_sheet": "兼容矩阵",
                "source_cell_range": "A1:C1,A3:C3", "heading_path": ["兼容矩阵"],
            }],
        },
    }
    wrong, _ = evaluate_retrieval_case(case, {
        "result": {"hits": [{
            "document_id": "matrix-xlsx", "source_sheet": "兼容矩阵",
            "source_cell_range": "A1:C1,A2:C2", "heading_path": ["兼容矩阵"],
        }]},
    }, 2.0)
    assert not wrong["passed"]
    assert wrong["actual_decision"] == "wrong_source_location"

    right, _ = evaluate_retrieval_case(case, {
        "result": {"hits": [{
            "document_id": "matrix-xlsx", "source_sheet": "兼容矩阵",
            "source_cell_range": "A1:C1,A3:C3", "heading_path": ["兼容矩阵"],
        }]},
    }, 2.0)
    assert right["passed"]
    assert right["details"]["source_location_passed"] is True


def test_retrieval_case_treats_reviewed_cross_format_copy_as_equivalent():
    case = {
        "id": "docx-equivalent", "query": "SYS-NET-042", "split": "regression", "category": "exact_match",
        "expected": {"answerable": True, "relevant_doc_ids": ["vsm100-error-codes-fw2.6"]},
    }
    item, score = evaluate_retrieval_case(case, {
        "result": {"hits": [{"document_id": "vsm100-error-codes-fw2.6-docx"}]},
    }, 2.0)
    assert item["passed"]
    assert score["mrr"] == 1.0


def test_unanswerable_case_is_scored_by_agent_not_raw_retrieval():
    case = {
        "id": "clinical", "query": "给出治疗方案", "split": "regression", "category": "unanswerable",
        "expected": {"answerable": False, "relevant_doc_ids": []},
    }
    item, _ = evaluate_retrieval_case(case, {"result": {"hits": [{"document_id": "clinical-boundary"}]}}, 2.0)
    assert item["passed"]
    assert item["actual_decision"] == "not_scored"
    assert item["details"]["scored"] is False


def test_golden_context_maps_product_to_exact_model_code():
    assert golden_device_context({"context": {"product": "pulsecare-vsm100-pro", "version": "2.6"}}) == {
        "model_code": "VSM-100 Pro", "software_version": "2.6", "lot_or_batch": "", "region": "CN",
    }


def test_search_hit_accepts_null_optional_array_metadata():
    hit = SearchHit.model_validate({"document_id": "doc", "model_codes": None, "supersedes": None, "affected_lots": None})
    assert hit.model_codes == []
    assert hit.supersedes == []
    assert hit.affected_lots == []


def test_customer_evaluation_uses_public_only_suite():
    cases = agent_cases_for("tenant_a-medical-device-customer-agent")
    assert len(cases) == 17
    assert any(case["id"] == "customer-onboarding" for case in cases)
    assert any(case["id"] == "customer-savina-intro" for case in cases)
    assert any(case["id"] == "customer-ventilator-setting-boundary" for case in cases)
    commercial = next(case for case in cases if case["id"] == "customer-commercial-boundary")
    assert commercial["expected"] == "refuse"
    assert commercial["reason"] == "dynamic_commercial_data_unavailable"
    sales_golden = golden_cases_for("tenant_a-medical-device-customer-agent", "tenant_a")
    assert len(sales_golden) == 73
    assert all(case["id"].startswith(("sales_", "public_")) for case in sales_golden)


def test_evaluation_rate_is_paced_below_application_quota(monkeypatch):
    monkeypatch.setenv("MEDICAL_EVAL_REQUESTS_PER_MINUTE", "120")
    assert evaluation_request_interval_seconds() == 0.5
