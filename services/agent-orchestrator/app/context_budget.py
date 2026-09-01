from __future__ import annotations

import unicodedata
from typing import Any, Iterable

from .models import ContextUsage


DEFAULT_CONTEXT_TOKEN_BUDGET = 4_000
DEFAULT_CONTEXT_MAX_CHUNKS = 6


def estimate_tokens(value: str) -> int:
    """Conservatively estimate mixed Chinese/ASCII context tokens.

    This intentionally mirrors the Go Knowledge Gateway approximation. It is
    deterministic and suitable for enforcing an application budget; provider
    usage remains the source of truth for billing.
    """

    cjk = 0
    other = 0
    for current in value:
        name = unicodedata.name(current, "")
        if name.startswith("CJK UNIFIED IDEOGRAPH") or name.startswith("CJK COMPATIBILITY IDEOGRAPH"):
            cjk += 1
        elif not current.isspace():
            other += 1
    return cjk + (other + 3) // 4


def _truncate_to_estimated_tokens(value: str, budget: int) -> str:
    if budget <= 0:
        return ""
    selected: list[str] = []
    cjk = 0
    other = 0
    for current in value:
        next_cjk, next_other = cjk, other
        name = unicodedata.name(current, "")
        if name.startswith("CJK UNIFIED IDEOGRAPH") or name.startswith("CJK COMPATIBILITY IDEOGRAPH"):
            next_cjk += 1
        elif not current.isspace():
            next_other += 1
        if next_cjk + (next_other + 3) // 4 > budget:
            break
        selected.append(current)
        cjk, other = next_cjk, next_other
    return "".join(selected).strip()


def token_budget_from_gateway(payloads: Iterable[dict[str, Any]], default: int = DEFAULT_CONTEXT_TOKEN_BUDGET) -> int:
    """Resolve the strictest positive budget from every authorized binding."""

    budgets: list[int] = []
    for payload in payloads:
        for binding in payload.get("bindings") or []:
            policy = binding.get("policy") or {}
            try:
                budget = int(policy.get("token_budget", 0))
            except (TypeError, ValueError):
                continue
            if budget > 0:
                budgets.append(budget)
    return min(budgets) if budgets else default


def pack_evidence(
    candidates: list[dict[str, Any]],
    token_budget: int,
    max_chunks: int = DEFAULT_CONTEXT_MAX_CHUNKS,
) -> tuple[list[dict[str, Any]], ContextUsage]:
    """Pack verified evidence into a bounded, provenance-preserving context."""

    budget = token_budget if token_budget > 0 else DEFAULT_CONTEXT_TOKEN_BUDGET
    limit = max_chunks if max_chunks > 0 else DEFAULT_CONTEXT_MAX_CHUNKS
    selected: list[dict[str, Any]] = []
    seen: set[str] = set()
    estimated = 0
    truncated = False

    for index, candidate in enumerate(candidates):
        if len(selected) >= limit or estimated >= budget:
            truncated = True
            break
        identity = str(candidate.get("chunk_id") or f"{candidate.get('document_id', '')}:{index}")
        if identity in seen:
            continue
        seen.add(identity)
        title = str(candidate.get("title", ""))
        content = str(candidate.get("content", ""))
        cost = estimate_tokens(title + "\n" + content)
        remaining = budget - estimated
        if cost > remaining:
            title_cost = estimate_tokens(title + "\n")
            content_budget = remaining - title_cost
            # Keeping one partial top-ranked passage is more useful than
            # producing an empty answer when a single chunk exceeds budget.
            if not selected and content_budget > 0:
                packed = dict(candidate)
                packed["content"] = _truncate_to_estimated_tokens(content, content_budget)
                selected.append(packed)
                estimated += estimate_tokens(title + "\n" + str(packed["content"]))
            truncated = True
            break
        selected.append(dict(candidate))
        estimated += cost

    usage = ContextUsage(
        candidate_chunks=len(candidates),
        selected_chunks=len(selected),
        estimated_tokens=estimated,
        token_budget=budget,
        truncated=truncated or len(selected) < len({str(item.get("chunk_id") or index) for index, item in enumerate(candidates)}),
    )
    return selected, usage
