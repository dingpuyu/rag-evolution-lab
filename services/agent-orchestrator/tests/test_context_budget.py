from app.context_budget import estimate_tokens, pack_evidence, token_budget_from_gateway


def test_token_budget_uses_strictest_authorized_binding():
    payloads = [
        {"bindings": [{"policy": {"token_budget": 5000}}]},
        {"bindings": [{"policy": {"token_budget": 4500}}, {"policy": {"token_budget": 0}}]},
    ]
    assert token_budget_from_gateway(payloads) == 4500


def test_pack_evidence_truncates_top_ranked_passage_and_preserves_provenance():
    candidates = [
        {
            "chunk_id": "chunk-1", "document_id": "doc-1", "title": "公开产品资料",
            "content": "这是用于验证预算执行的中文证据。" * 20, "source_page": 3,
        },
        {"chunk_id": "chunk-2", "document_id": "doc-2", "title": "第二份资料", "content": "不会被选中。"},
    ]

    selected, usage = pack_evidence(candidates, token_budget=30)

    assert len(selected) == 1
    assert selected[0]["chunk_id"] == "chunk-1"
    assert selected[0]["source_page"] == 3
    assert selected[0]["content"] != candidates[0]["content"]
    assert usage.candidate_chunks == 2
    assert usage.selected_chunks == 1
    assert usage.estimated_tokens <= usage.token_budget == 30
    assert usage.truncated is True
    assert estimate_tokens(selected[0]["title"] + "\n" + selected[0]["content"]) == usage.estimated_tokens
