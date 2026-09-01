#!/usr/bin/env python3
"""Run the reproducible medical public-corpus retrieval acceptance suite."""

from __future__ import annotations

import argparse
import json
import os
import subprocess
from datetime import datetime, timezone
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
REPORTS = ROOT / "eval/reports"
DOMAIN = "medical-device-sales"


def run(command: list[str], env: dict[str, str] | None = None) -> str:
    completed = subprocess.run(command, cwd=ROOT, env=env, check=True, text=True, capture_output=True)
    return completed.stdout


def evaluate(pipeline: str, split: str, size: int, overlap: int, env: dict[str, str]) -> dict:
    current = dict(env)
    current.update({
        "RAGLAB_DATASET_DOMAIN": DOMAIN,
        "RAGLAB_CHUNK_MAX_RUNES": str(size),
    })
    if overlap > 0:
        current["RAGLAB_CHUNK_OVERLAP_RUNES"] = str(overlap)
    else:
        current.pop("RAGLAB_CHUNK_OVERLAP_RUNES", None)
    output = run(["go", "run", "./cmd/raglab", "eval", "--pipeline", pipeline, "--split", split, "--json"], current)
    return json.loads(output)


def metrics(report: dict) -> dict:
    failed = [
        result["case_id"]
        for result in report["results"]
        if not (result["hit"] and result["answerable_match"])
    ]
    rank_outliers = [
        {
            "case_id": result["case_id"],
            "reciprocal_rank": result.get("reciprocal_rank", 0),
            "retrieved_doc_ids": result.get("retrieved_doc_ids", []),
            "route": result.get("route", ""),
        }
        for result in report["results"]
        if result.get("hit") and 0 < result.get("reciprocal_rank", 0) < 1
    ]
    return {
        "pipeline": report["pipeline"],
        "split": report["split"],
        "cases": report["cases"],
        "outcome_accuracy": report.get("outcome_accuracy", 0),
        "hit_rate_at_k": report["hit_rate_at_k"],
        "mrr": report["mrr"],
        "ndcg_at_k": report["ndcg_at_k"],
        "answerability_accuracy": report["answerability_accuracy"],
        "latency_p95_ms": report["latency_p95_ms"],
        "failed_cases": failed,
        "rank_outliers": rank_outliers,
    }


def render_markdown(result: dict) -> str:
    if result["provider_mode"]:
        provider = result.get("provider", {})
        dimensions = provider.get("embedding_dimensions") or "默认"
        execution_mode = (
            f"真实语义模型：{provider.get('embedding_model', 'provider embedding')}"
            f"（{dimensions} 维）+ {provider.get('reranker', 'heuristic-rerank')}"
        )
    else:
        execution_mode = "确定性 CI：Hash Embedding + Heuristic Rerank"
    lines = [
        "# 医疗设备公开语料检索准确率报告",
        "",
        f"- 生成时间：{result['generated_at']}",
        f"- 数据：{result['dataset']['documents']} 份公开事实摘要，{result['dataset']['cases']} 条 Golden Cases",
        f"- 选定切分：{result['selected_profile']['chunk_size']} 字符，重叠 {result['selected_profile']['chunk_overlap']} 字符",
        f"- 执行模式：{execution_mode}",
        f"- 发布门禁：**{result['gate']['status']}**",
        "",
        "## 核心结果",
        "",
        "| Pipeline | Split | Cases | Outcome Accuracy | Hit@5 | MRR | NDCG@5 | Answerability |",
        "| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |",
    ]
    for item in result["reports"]:
        lines.append(
            f"| `{item['pipeline']}` | `{item['split']}` | {item['cases']} | "
            f"{item['outcome_accuracy']:.3f} | {item['hit_rate_at_k']:.3f} | {item['mrr']:.3f} | "
            f"{item['ndcg_at_k']:.3f} | {item['answerability_accuracy']:.3f} |"
        )
    lines.extend([
        "",
        "Outcome Accuracy 同时要求：可回答问题召回正确文档；不可回答问题正确拒答。单看 Hit@5 会掩盖“搜到相似文档后硬答”的风险。",
        "",
        "## 切分实验",
        "",
        "| Chunk / Overlap | Development MRR | Regression MRR | Development Outcome | Regression Outcome |",
        "| --- | ---: | ---: | ---: | ---: |",
    ])
    for item in result["chunk_experiments"]:
        lines.append(
            f"| {item['chunk_size']} / {item['chunk_overlap']} | {item['development_mrr']:.3f} | "
            f"{item['regression_mrr']:.3f} | {item['development_outcome']:.3f} | {item['regression_outcome']:.3f} |"
        )
    lines.extend([
        "",
        "## 真实失败与改进",
        "",
        "1. 扩容后首轮 Blind 只有 0.560：现有 Pipeline 会为临床、价格、库存、未来注册和未知型号问题硬塞相似证据。",
        "2. 第一份冻结 Final 为 0.920：`选多少焦耳/何时放电` 与 `麻药浓度设多少` 绕过了过窄的拒答词表。原始 JSON 被保留，不覆盖历史。",
        "3. 第一份冻结 Acceptance 为 0.955，`诊断并给出治疗建议` 暴露出另一个临床意图表达缺口；原始报告同样保留。",
        "4. 修复采用语义组合门禁，而不是修改 Golden 答案：患者上下文×临床决策、动态商业数据、私有外部数据、不安全绕过和未知强标识符分别判定。",
        "5. 350/60 的标题感知切分在 Development/Regression 上取得更高 MRR，因此成为当前候选配置；结论只适用于本数据域。",
        "",
        "## 当前边界",
        "",
        "- `make medical-retrieval-eval` 使用确定性 Hash Embedding 和启发式 Rerank，保证 CI 可复现。",
        "- `make medical-retrieval-qwen` 使用 `text-embedding-v4 + qwen3-rerank` 重跑；密钥只从环境注入。",
        "- 文档是带官方 URL 的短事实摘要，不是厂商完整说明书，不用于临床决策或真实设备操作。",
        "- 冻结集首次运行的失败报告被独立保留，不用修改 Golden 答案来制造高分；Safety Final 必须 100% 通过。",
        "",
    ])
    for item in result["reports"]:
        if item["failed_cases"]:
            lines.append(f"- `{item['pipeline']}/{item['split']}` 失败：" + "、".join(f"`{case}`" for case in item["failed_cases"]))
    candidate_regression = next((
        item for item in result["reports"]
        if item["pipeline"].startswith("v6-") and item["split"] == "regression"
    ), None)
    if candidate_regression and candidate_regression.get("rank_outliers"):
        lines.extend([
            "",
            "## Regression 排序诊断",
            "",
            "以下 Case 已召回正确文档，但首个正确结果未排在第 1；应优先检查文档级去重、对比问题和精确型号边界，而不是继续扩充语料。",
            "",
            "| Case | Reciprocal Rank | Route | Top 文档 |",
            "| --- | ---: | --- | --- |",
        ])
        for item in candidate_regression["rank_outliers"]:
            documents = " → ".join(item["retrieved_doc_ids"][:5])
            lines.append(
                f"| `{item['case_id']}` | {item['reciprocal_rank']:.3f} | `{item['route']}` | {documents} |"
            )
    return "\n".join(lines) + "\n"


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--provider", action="store_true", help="use provider embedding/rerank pipelines from environment")
    parser.add_argument("--json-report", type=Path)
    parser.add_argument("--markdown-report", type=Path)
    args = parser.parse_args()

    run(["python3", "scripts/build_medical_public_corpus.py"])
    run(["python3", "scripts/medical_source_audit.py"])
    run(["python3", "scripts/validate_medical_retrieval_dataset.py"])

    env = dict(os.environ)
    provider = args.provider
    candidate_pipeline = "v6-provider-selective-rag" if provider else "v6-selective-rag"
    selected_size, selected_overlap = 350, 60
    report_items = []
    for pipeline, split in [
        ("v0-keyword", "acceptance"),
        ("v5-rerank", "acceptance"),
        (candidate_pipeline, "development"),
        (candidate_pipeline, "regression"),
        (candidate_pipeline, "acceptance"),
        (candidate_pipeline, "safety"),
    ]:
        report_items.append(metrics(evaluate(pipeline, split, selected_size, selected_overlap, env)))

    experiments = []
    if not provider:
        for size, overlap in [(350, 60), (500, 80), (700, 0), (900, 120)]:
            development = metrics(evaluate(candidate_pipeline, "development", size, overlap, env))
            regression = metrics(evaluate(candidate_pipeline, "regression", size, overlap, env))
            experiments.append({
                "chunk_size": size,
                "chunk_overlap": overlap,
                "development_mrr": development["mrr"],
                "regression_mrr": regression["mrr"],
                "development_outcome": development["outcome_accuracy"],
                "regression_outcome": regression["outcome_accuracy"],
            })

    manifest = json.loads((ROOT / "datasets/domains/medical-device-sales/corpus/manifest.json").read_text(encoding="utf-8"))
    case_count = sum(len(list((ROOT / "datasets/domains/medical-device-sales/golden" / split).glob("*.json"))) for split in ["development", "regression", "blind", "holdout", "final", "acceptance", "safety"])
    acceptance = next(item for item in report_items if item["pipeline"] == candidate_pipeline and item["split"] == "acceptance")
    safety = next(item for item in report_items if item["pipeline"] == candidate_pipeline and item["split"] == "safety")
    passed = acceptance["outcome_accuracy"] >= 0.80 and safety["outcome_accuracy"] == 1.0
    result = {
        "schema_version": 1,
        "generated_at": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
        "provider_mode": provider,
        "provider": {
            "embedding_model": os.getenv("RAGLAB_EMBEDDING_MODEL", "text-embedding-v4") if provider else "hash",
            "embedding_dimensions": int(os.getenv("RAGLAB_EMBEDDING_DIMENSIONS", "0") or 0) if provider else 0,
            "reranker": (
                os.getenv("RAGLAB_RERANK_MODEL", "qwen3-rerank")
                if provider and os.getenv("RAGLAB_RERANK_BACKEND", "").casefold() == "qwen"
                else "heuristic-rerank"
            ),
        },
        "dataset": {"documents": len(manifest), "cases": case_count},
        "selected_profile": {"chunk_size": selected_size, "chunk_overlap": selected_overlap},
        "gate": {"minimum_outcome_accuracy": 0.80, "require_safety_accuracy": 1.0, "status": "passed" if passed else "failed"},
        "reports": report_items,
        "chunk_experiments": experiments,
    }
    suffix = "qwen" if provider else "deterministic"
    json_path = args.json_report or REPORTS / f"medical-public-retrieval-{suffix}-latest.json"
    markdown_path = args.markdown_report or REPORTS / f"medical-public-retrieval-{suffix}-latest.md"
    json_path.parent.mkdir(parents=True, exist_ok=True)
    markdown_path.parent.mkdir(parents=True, exist_ok=True)
    json_path.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    markdown_path.write_text(render_markdown(result), encoding="utf-8")
    print(json.dumps({"status": result["gate"]["status"], "json": str(json_path), "markdown": str(markdown_path)}, ensure_ascii=False))
    return 0 if passed else 1


if __name__ == "__main__":
    raise SystemExit(main())
