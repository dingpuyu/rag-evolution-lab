#!/usr/bin/env python3
"""Audit the medical retrieval corpus and Golden Cases before evaluation.

The retrieval score is only meaningful when the corpus and evaluation set are
internally consistent.  This script therefore treats case leakage, missing
evidence and malformed refusal cases as release-blocking errors.  Similar but
non-identical questions and uncovered documents are reported as review items so
they can be inspected without silently rewriting a frozen evaluation split.
"""

from __future__ import annotations

import argparse
import json
import re
from collections import Counter, defaultdict
from datetime import datetime, timezone
from pathlib import Path
from typing import Any


ROOT = Path(__file__).resolve().parents[1]
DOMAIN = ROOT / "datasets/domains/medical-device-sales"
MANIFEST = DOMAIN / "corpus/manifest.json"
DOCUMENTS = DOMAIN / "corpus/documents"
GOLDEN = DOMAIN / "golden"
REPORTS = ROOT / "eval/reports"
SPLITS = ("development", "regression", "blind", "holdout", "final", "acceptance", "safety")
MINIMUM_SPLIT_COUNTS = {
    "development": 40,
    "regression": 25,
    "blind": 30,
    "holdout": 20,
    "final": 20,
    "acceptance": 20,
    "safety": 10,
}


def normalized_text(value: str) -> str:
    return re.sub(r"[^0-9a-z\u3400-\u9fff]+", "", value.casefold())


def similarity_features(value: str) -> set[str]:
    normalized = normalized_text(value)
    if len(normalized) < 2:
        return {normalized} if normalized else set()
    return {normalized[index : index + 2] for index in range(len(normalized) - 1)}


def jaccard(left: set[str], right: set[str]) -> float:
    if not left or not right:
        return 0.0
    return len(left & right) / len(left | right)


def load_json(path: Path) -> Any:
    return json.loads(path.read_text(encoding="utf-8"))


def load_cases() -> tuple[list[dict[str, Any]], list[str]]:
    cases: list[dict[str, Any]] = []
    errors: list[str] = []
    for split in SPLITS:
        directory = GOLDEN / split
        if not directory.is_dir():
            errors.append(f"missing split directory: {directory.relative_to(ROOT)}")
            continue
        for path in sorted(directory.glob("*.json")):
            try:
                payload = load_json(path)
            except (OSError, json.JSONDecodeError) as exc:
                errors.append(f"invalid JSON {path.relative_to(ROOT)}: {exc}")
                continue
            items = payload if isinstance(payload, list) else [payload]
            for item in items:
                if not isinstance(item, dict):
                    errors.append(f"case must be an object: {path.relative_to(ROOT)}")
                    continue
                copied = dict(item)
                copied["_path"] = str(path.relative_to(ROOT))
                copied["_directory_split"] = split
                cases.append(copied)
    return cases, errors


def corpus_text(document: dict[str, Any]) -> str:
    path = DOCUMENTS.parent / str(document.get("path", ""))
    if not path.is_file():
        return ""
    return path.read_text(encoding="utf-8")


def audit() -> dict[str, Any]:
    errors: list[str] = []
    warnings: list[str] = []
    manifest = load_json(MANIFEST)
    if not isinstance(manifest, list):
        raise ValueError("manifest must be a JSON array")

    documents: dict[str, dict[str, Any]] = {}
    document_texts: dict[str, str] = {}
    required_document_fields = (
        "doc_id", "title", "doc_type", "manufacturer", "product_family",
        "path", "status", "visibility",
    )
    for item in manifest:
        doc_id = str(item.get("doc_id", "")).strip()
        if not doc_id:
            errors.append("manifest document without doc_id")
            continue
        if doc_id in documents:
            errors.append(f"duplicate document id: {doc_id}")
            continue
        missing = [field for field in required_document_fields if not item.get(field)]
        if missing:
            errors.append(f"document {doc_id} missing fields: {', '.join(missing)}")
        if item.get("doc_type") in {"official_product_summary", "official_product_portfolio"}:
            if not item.get("model_codes"):
                errors.append(f"product document {doc_id} must declare model_codes")
            if not item.get("source_urls"):
                errors.append(f"product document {doc_id} must declare source_urls")
        content = corpus_text(item)
        if not content.strip():
            errors.append(f"document {doc_id} is missing or empty: {item.get('path', '')}")
        documents[doc_id] = item
        document_texts[doc_id] = content

    cases, case_load_errors = load_cases()
    errors.extend(case_load_errors)
    split_counts: Counter[str] = Counter()
    category_counts: Counter[str] = Counter()
    answerability_counts: Counter[str] = Counter()
    referenced_by_split: dict[str, Counter[str]] = defaultdict(Counter)
    ids: dict[str, str] = {}
    exact_queries: dict[str, list[dict[str, Any]]] = defaultdict(list)

    for case in cases:
        case_id = str(case.get("id", "")).strip()
        query = str(case.get("query", "")).strip()
        split = str(case.get("split", "")).strip()
        directory_split = case["_directory_split"]
        path = case["_path"]
        expected = case.get("expected")

        if not case_id:
            errors.append(f"case without id: {path}")
        elif case_id in ids:
            errors.append(f"duplicate case id {case_id}: {ids[case_id]} and {path}")
        else:
            ids[case_id] = path
        if not query:
            errors.append(f"case {case_id or path} has empty query")
        if split != directory_split:
            errors.append(f"case {case_id or path} declares split={split!r}, stored in {directory_split!r}")
        if not isinstance(expected, dict):
            errors.append(f"case {case_id or path} has no expected object")
            continue

        answerable = expected.get("answerable")
        relevant = expected.get("relevant_doc_ids", [])
        facts = expected.get("required_facts", [])
        minimum_citations = expected.get("minimum_citations")
        refusal_reason = expected.get("expected_refusal_reason")
        if not isinstance(answerable, bool):
            errors.append(f"case {case_id} expected.answerable must be boolean")
            continue
        if not isinstance(relevant, list) or not all(isinstance(value, str) and value for value in relevant):
            errors.append(f"case {case_id} relevant_doc_ids must be a string array")
            continue
        if not isinstance(facts, list) or not all(isinstance(value, str) and value for value in facts):
            errors.append(f"case {case_id} required_facts must be a string array")
            continue

        split_counts[split] += 1
        category_counts[str(case.get("category", "unknown"))] += 1
        answerability_counts["answerable" if answerable else "refusal"] += 1
        exact_queries[normalized_text(query)].append(case)

        if answerable:
            if not relevant:
                errors.append(f"answerable case {case_id} has no relevant documents")
            if not facts:
                errors.append(f"answerable case {case_id} has no required facts")
            if not isinstance(minimum_citations, int) or minimum_citations < 1:
                errors.append(f"answerable case {case_id} requires minimum_citations >= 1")
            if refusal_reason not in (None, ""):
                errors.append(f"answerable case {case_id} must not declare a refusal reason")
        else:
            if relevant:
                errors.append(f"refusal case {case_id} must not declare relevant documents")
            if facts:
                errors.append(f"refusal case {case_id} must not declare required facts")
            if minimum_citations != 0:
                errors.append(f"refusal case {case_id} must require zero citations")
            if not isinstance(refusal_reason, str) or not refusal_reason.strip():
                errors.append(f"refusal case {case_id} requires expected_refusal_reason")
        if split == "safety" and answerable:
            errors.append(f"safety case {case_id} must be a refusal case")

        missing_docs = [doc_id for doc_id in relevant if doc_id not in documents]
        if missing_docs:
            errors.append(f"case {case_id} references missing documents: {', '.join(missing_docs)}")
            continue
        for doc_id in relevant:
            referenced_by_split[split][doc_id] += 1

        if answerable and relevant:
            evidence = normalized_text("\n".join(document_texts[doc_id] for doc_id in relevant))
            missing_facts = [fact for fact in facts if normalized_text(fact) not in evidence]
            if missing_facts:
                errors.append(
                    f"case {case_id} required facts absent from its labelled evidence: "
                    + ", ".join(missing_facts)
                )

    for split, minimum in MINIMUM_SPLIT_COUNTS.items():
        if split_counts[split] < minimum:
            errors.append(f"split {split} has {split_counts[split]} cases; minimum is {minimum}")
    if sum(split_counts.values()) < 180:
        errors.append(f"only {sum(split_counts.values())} cases; minimum is 180")
    if category_counts["hard_negative"] < 20:
        errors.append(f"only {category_counts['hard_negative']} hard-negative cases; minimum is 20")
    if answerability_counts["refusal"] < 30:
        errors.append(f"only {answerability_counts['refusal']} refusal cases; minimum is 30")

    exact_leakage: list[dict[str, Any]] = []
    for normalized, duplicates in exact_queries.items():
        if not normalized or len(duplicates) < 2:
            continue
        locations = sorted({str(item["split"]) for item in duplicates})
        case_ids = [str(item.get("id", "")) for item in duplicates]
        exact_leakage.append({"query": duplicates[0]["query"], "case_ids": case_ids, "splits": locations})
        errors.append(f"exact query leakage across cases: {', '.join(case_ids)}")

    near_duplicates: list[dict[str, Any]] = []
    features = [(case, similarity_features(str(case.get("query", "")))) for case in cases]
    for left_index, (left, left_features) in enumerate(features):
        for right, right_features in features[left_index + 1 :]:
            if left["split"] == right["split"]:
                continue
            score = jaccard(left_features, right_features)
            if score >= 0.90:
                near_duplicates.append({
                    "left_case_id": left.get("id"),
                    "left_split": left.get("split"),
                    "right_case_id": right.get("id"),
                    "right_split": right.get("split"),
                    "similarity": round(score, 3),
                })
    if near_duplicates:
        warnings.append(f"{len(near_duplicates)} near-duplicate cross-split query pairs require review")

    all_referenced = set().union(*(set(counter) for counter in referenced_by_split.values()))
    uncovered_documents = sorted(set(documents) - all_referenced)
    if uncovered_documents:
        warnings.append(f"{len(uncovered_documents)} documents have no answerable Golden Case")

    manufacturer_counts = Counter(str(item.get("manufacturer", "unknown")) for item in manifest)
    family_counts = Counter(str(item.get("product_family", "unknown")) for item in manifest)
    return {
        "schema_version": 1,
        "generated_at": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
        "status": "passed" if not errors else "failed",
        "summary": {
            "documents": len(documents),
            "cases": len(cases),
            "answerable_cases": answerability_counts["answerable"],
            "refusal_cases": answerability_counts["refusal"],
            "hard_negative_cases": category_counts["hard_negative"],
            "exact_query_leaks": len(exact_leakage),
            "near_duplicate_pairs": len(near_duplicates),
            "uncovered_documents": len(uncovered_documents),
        },
        "split_counts": dict(sorted(split_counts.items())),
        "category_counts": dict(sorted(category_counts.items())),
        "manufacturer_counts": dict(sorted(manufacturer_counts.items())),
        "product_family_counts": dict(sorted(family_counts.items())),
        "uncovered_document_ids": uncovered_documents,
        "near_duplicate_pairs": near_duplicates,
        "errors": errors,
        "warnings": warnings,
    }


def render_markdown(report: dict[str, Any]) -> str:
    summary = report["summary"]
    lines = [
        "# 医疗设备检索数据集质量审计",
        "",
        f"- 生成时间：{report['generated_at']}",
        f"- 状态：**{report['status']}**",
        f"- 语料：{summary['documents']} 份",
        f"- 用例：{summary['cases']} 条（可回答 {summary['answerable_cases']}，拒答 {summary['refusal_cases']}）",
        f"- Hard Negative：{summary['hard_negative_cases']} 条",
        f"- 精确问题泄漏：{summary['exact_query_leaks']} 组",
        f"- 高相似跨集合问题：{summary['near_duplicate_pairs']} 组（人工复核项，不自动判错）",
        "",
        "## 分层覆盖",
        "",
        "| Split | Cases | Minimum |",
        "| --- | ---: | ---: |",
    ]
    for split in SPLITS:
        lines.append(f"| `{split}` | {report['split_counts'].get(split, 0)} | {MINIMUM_SPLIT_COUNTS[split]} |")
    lines.extend(["", "## 厂商覆盖", "", "| Manufacturer | Documents |", "| --- | ---: |"])
    for manufacturer, count in report["manufacturer_counts"].items():
        lines.append(f"| {manufacturer} | {count} |")
    lines.extend(["", "## 门禁检查", ""])
    if report["errors"]:
        lines.extend(f"- ❌ {item}" for item in report["errors"])
    else:
        lines.append("- ✅ ID、Split、证据引用、答案事实、拒答结构和最低覆盖全部通过。")
    if report["warnings"]:
        lines.extend(["", "## 人工复核项", ""])
        lines.extend(f"- ⚠️ {item}" for item in report["warnings"])
    if report["uncovered_document_ids"]:
        lines.extend(["", "### 尚未覆盖的文档", ""])
        lines.extend(f"- `{doc_id}`" for doc_id in report["uncovered_document_ids"])
    if report["near_duplicate_pairs"]:
        lines.extend(["", "### 高相似跨集合问题", "", "| Left | Split | Right | Split | Similarity |", "| --- | --- | --- | --- | ---: |"])
        for item in report["near_duplicate_pairs"]:
            lines.append(
                f"| `{item['left_case_id']}` | `{item['left_split']}` | `{item['right_case_id']}` | "
                f"`{item['right_split']}` | {item['similarity']:.3f} |"
            )
    lines.extend([
        "",
        "## 审计原则",
        "",
        "- 可回答题必须引用存在的文档，且 `required_facts` 必须能够在标注证据中找到。",
        "- 拒答题不能偷偷携带相关文档或引用，Safety 集必须全部拒答。",
        "- 完全相同的问题不得跨集合出现；高相似问题保留给人工判断，因为同一事实的自然改写本身是有效鲁棒性测试。",
        "- 审计只证明数据集内部一致性，不代替人工事实核验和真实 Provider 端到端评测。",
        "",
    ])
    return "\n".join(lines)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--json-report", type=Path, default=REPORTS / "medical-dataset-quality-latest.json")
    parser.add_argument("--markdown-report", type=Path, default=REPORTS / "medical-dataset-quality-latest.md")
    args = parser.parse_args()
    report = audit()
    args.json_report.parent.mkdir(parents=True, exist_ok=True)
    args.markdown_report.parent.mkdir(parents=True, exist_ok=True)
    args.json_report.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    args.markdown_report.write_text(render_markdown(report), encoding="utf-8")
    print(json.dumps({
        "status": report["status"],
        "documents": report["summary"]["documents"],
        "cases": report["summary"]["cases"],
        "errors": len(report["errors"]),
        "warnings": len(report["warnings"]),
        "json": str(args.json_report),
        "markdown": str(args.markdown_report),
    }, ensure_ascii=False))
    return 0 if report["status"] == "passed" else 1


if __name__ == "__main__":
    raise SystemExit(main())
