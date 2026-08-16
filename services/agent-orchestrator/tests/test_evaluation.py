from app.evaluation import EvaluationStore, evaluate_case, evaluate_retrieval_case, golden_device_context, load_medical_golden
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
    assert result["details"]["checks"] == {"decision": True, "reason": True, "citations": False}


def test_medical_golden_contains_development_and_regression_cases():
    cases = load_medical_golden()
    assert len(cases) == 40
    assert {case["split"] for case in cases} == {"development", "regression"}


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
