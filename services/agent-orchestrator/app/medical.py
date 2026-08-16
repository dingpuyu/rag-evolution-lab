from __future__ import annotations

import re
from dataclasses import dataclass

from .models import DeviceContext


MODEL_PATTERN = re.compile(r"\bVSM[\s-]?(100\s*PRO|100|200)\b", re.IGNORECASE)
VERSION_PATTERN = re.compile(r"(?:软件|固件|版本|FW)\s*[Vv]?\s*(\d+\.\d+(?:\.\d+)?)", re.IGNORECASE)
LOT_PATTERN = re.compile(r"\b(L26A\d{2})\b", re.IGNORECASE)


@dataclass(frozen=True)
class MedicalResolution:
    intent: str
    reason_code: str
    context: DeviceContext
    candidates: list[str]


def resolve_medical_query(query: str, supplied: DeviceContext | None = None) -> MedicalResolution:
    supplied = supplied or DeviceContext()
    text = query.strip()
    lower = text.lower()
    model = supplied.model_code.strip()
    version = supplied.software_version.strip()
    lot = supplied.lot_or_batch.strip().upper()

    if not model:
        match = MODEL_PATTERN.search(text)
        if match:
            suffix = re.sub(r"\s+", " ", match.group(1).upper())
            model = "VSM-" + suffix.title().replace("Pro", "Pro") if suffix.endswith("PRO") else "VSM-" + suffix
    if not version:
        match = VERSION_PATTERN.search(text)
        if match:
            version = match.group(1)
    if not lot:
        match = LOT_PATTERN.search(text)
        if match:
            lot = match.group(1).upper()

    context = DeviceContext(model_code=model, software_version=version, lot_or_batch=lot, region=supplied.region or "CN")

    clinical_terms = (
        "诊断", "治疗", "给药", "处方", "患者应该", "病情", "临床决策", "报警阈值", "参数设为多少", "血压阈值", "心率阈值",
    )
    if any(term in text for term in clinical_terms):
        return MedicalResolution("refuse", "clinical_boundary", context, [])

    if len(text) <= 20 and any(term in lower for term in ("你好", "您好", "你是谁", "能做什么", "help", "hello")):
        return MedicalResolution("persona", "capability_introduction", context, [])

    correction = "现场更正" in text or bool(re.search(r"\bFC[- ]?2026[- ]?04\b", text, re.IGNORECASE))
    if correction:
        missing = []
        if not model:
            missing.append("型号")
        if not version or version.count(".") < 2:
            missing.append("完整软件版本（例如 2.5.2）")
        if not lot:
            missing.append("批次")
        if missing:
            return MedicalResolution("clarify", "missing_applicability_context", context, missing)
        return MedicalResolution("field_correction", "deterministic_applicability", context, [])

    ambiguous_topics = ("SYS-NET-042", "日志最多", "日志上限", "菜单路径", "导出路径", "兼容", "配件")
    if not model and any(term.lower() in lower for term in ambiguous_topics):
        return MedicalResolution("clarify", "ambiguous_device_model", context, ["VSM-100", "VSM-100 Pro", "VSM-200"])

    return MedicalResolution("knowledge", "knowledge_required", context, [])


def assess_field_correction(context: DeviceContext) -> tuple[str, str]:
    """Evaluate the fully synthetic FC-2026-04 scope without delegating policy to an LLM."""
    if not context.model_code or not context.software_version or not context.lot_or_batch:
        return "needs_information", "型号、完整软件版本和批次必须同时提供。"
    try:
        version = tuple(int(part) for part in context.software_version.split("."))
    except ValueError:
        return "needs_information", "软件版本格式无法识别，请使用例如 2.5.2 的格式。"
    if len(version) != 3:
        return "needs_information", "需要补充补丁版本，例如 2.5.2。"
    model_applies = context.model_code.casefold() == "vsm-100 pro"
    version_applies = (2, 5, 0) <= version <= (2, 5, 3)
    lot_match = re.fullmatch(r"L26A(\d{2})", context.lot_or_batch.upper())
    lot_applies = bool(lot_match and 1 <= int(lot_match.group(1)) <= 7)
    if model_applies and version_applies and lot_applies:
        return "applies", "型号、软件版本和批次均落入 FC-2026-04 的虚构适用范围。"
    return "does_not_apply", "至少一个适用条件不匹配，因此不属于 FC-2026-04 的虚构范围。"


def verify_medical_evidence(context: DeviceContext, evidence: list[dict]) -> tuple[list[dict], str]:
    """Fail closed when retrieved evidence contradicts server-resolved scope."""
    verified: list[dict] = []
    requested_model = context.model_code.casefold().strip()
    requested_version = context.software_version.strip()
    for hit in evidence:
        if str(hit.get("status", "active")) not in {"", "active"}:
            continue
        models = [str(value).casefold().strip() for value in (hit.get("model_codes") or [])]
        if requested_model and models and requested_model not in models:
            continue
        version = str(hit.get("version", "")).strip()
        version_from = str(hit.get("software_version_from", "")).strip()
        version_to = str(hit.get("software_version_to", "")).strip()
        if requested_version:
            if version and not _version_compatible(requested_version, version):
                continue
            if (version_from or version_to) and not _version_in_range(requested_version, version_from, version_to):
                continue
        verified.append(hit)
    if not verified:
        return [], "no_evidence_matches_resolved_scope"
    if not requested_model:
        candidate_models = {model for hit in verified for model in (hit.get("model_codes") or []) if model}
        if len(candidate_models) > 1:
            return [], "conflicting_model_scope"
    return verified, "evidence_scope_verified"


def _version_tuple(value: str) -> tuple[int, ...] | None:
    value = value.strip().lower().removeprefix("v")
    if not re.fullmatch(r"\d+(?:\.\d+){1,3}", value):
        return None
    return tuple(int(part) for part in value.split("."))


def _version_compatible(requested: str, evidence: str) -> bool:
    requested_tuple, evidence_tuple = _version_tuple(requested), _version_tuple(evidence)
    if requested_tuple is None or evidence_tuple is None:
        return requested.casefold() == evidence.casefold()
    shortest = min(len(requested_tuple), len(evidence_tuple))
    return requested_tuple[:shortest] == evidence_tuple[:shortest]


def _version_in_range(requested: str, start: str, end: str) -> bool:
    value = _version_tuple(requested)
    lower, upper = _version_tuple(start) if start else None, _version_tuple(end) if end else None
    if value is None:
        return False
    width = max(len(value), len(lower or ()), len(upper or ()))
    normalize = lambda version: tuple(version or ()) + (0,) * (width - len(version or ()))
    normalized = normalize(value)
    return (lower is None or normalized >= normalize(lower)) and (upper is None or normalized <= normalize(upper))
