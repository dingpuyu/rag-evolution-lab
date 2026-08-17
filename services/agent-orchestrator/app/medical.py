from __future__ import annotations

import re
from dataclasses import dataclass

from .models import DeviceContext


MODEL_PATTERN = re.compile(r"\bVSM[\s-]?(100\s*PRO|100|200)\b", re.IGNORECASE)
VERSION_PATTERN = re.compile(r"(?:软件|固件|版本|FW)\s*[Vv]?\s*(\d+\.\d+(?:\.\d+)?)", re.IGNORECASE)
LOT_PATTERN = re.compile(r"\b(L26A\d{2})\b", re.IGNORECASE)
SALES_MODEL_ALIASES = (
    ("IntelliVue MX550", ("intellivue mx550", "mx550")),
    ("IntelliVue MX500", ("intellivue mx500", "mx500")),
    ("BeneVision N1", ("benevision n1", "n1监护仪", "n1 监护仪")),
    ("BeneHeart C Series", ("beneheart c series", "beneheart c系列", "beneheart c 系列", "beneheart c")),
    ("BeneFusion i/u", ("benefusion i/u", "benefusion i系列", "benefusion u系列", "benefusion i/u系列")),
    ("Resona I9", ("resona i9", "resona i 9")),
    ("Evita V800", ("evita v800", "v800呼吸机", "v800 呼吸机")),
)
SALES_MODEL_CANDIDATES = [name for name, _ in SALES_MODEL_ALIASES]
UNTRUSTED_QUERY_DIRECTIVE = re.compile(
    r"(?:忽略|无视|绕过|覆盖)(?:此前|之前|上面|系统|资料|证据|规则|限制|提示词|指令)?[^，,。；;]{0,36}[，,。；;]\s*",
    re.IGNORECASE,
)


@dataclass(frozen=True)
class MedicalResolution:
    intent: str
    reason_code: str
    context: DeviceContext
    candidates: list[str]


def mentioned_sales_models(query: str) -> list[str]:
    """Return every explicitly mentioned sales model in stable catalog order."""
    normalized = query.casefold()
    return [model for model, aliases in SALES_MODEL_ALIASES if any(alias in normalized for alias in aliases)]


def sanitize_customer_retrieval_query(query: str) -> str:
    """Remove control-language from search without changing model identifiers.

    Generation still receives the original user query. This only prevents
    instructions such as "ignore the evidence and promise" from diluting BM25
    and embedding recall.
    """
    sanitized = UNTRUSTED_QUERY_DIRECTIVE.sub("", query.strip())
    sanitized = re.sub(r"(?:请)?(?:直接|必须)?(?:承诺|保证)", "", sanitized)
    sanitized = re.sub(r"\s+", " ", sanitized).strip(" ，,。；;")
    if sanitized != query.strip():
        sanitized += "；请核验官方证据、具体配置和库存边界"
    return sanitized or query.strip()


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


def resolve_customer_query(query: str, supplied: DeviceContext | None = None) -> MedicalResolution:
    """Resolve a novice customer's goal without forcing expert terminology.

    Product discovery may span models, while model-specific troubleshooting is
    fail-closed until the customer can identify the complete model.
    """
    supplied = supplied or DeviceContext()
    mentioned_models = mentioned_sales_models(query)
    if not supplied.model_code.strip() and mentioned_models:
        supplied = DeviceContext(
            model_code=mentioned_models[0],
            software_version=supplied.software_version,
            lot_or_batch=supplied.lot_or_batch,
            region=supplied.region,
        )
    base = resolve_medical_query(query, supplied)
    if base.intent in {"refuse", "field_correction"}:
        return base
    text = query.strip()
    lower = text.casefold()
    onboarding_terms = ("一窍不通", "完全不了解", "什么都不懂", "从零开始", "怎么开始了解", "第一次了解")
    if base.intent == "persona" or any(term in lower for term in onboarding_terms):
        return MedicalResolution("customer_onboarding", "customer_guided_onboarding", base.context, ["产品线概览", "认识型号", "开始排障"])

    product_terms = ("产品线", "有哪些产品", "有哪些型号", "产品介绍", "型号介绍", "特点", "特色", "区别", "对比", "哪款", "哪个好", "适合")
    if len(mentioned_models) >= 2 or any(term in lower for term in product_terms):
        context = base.context
        if len(mentioned_models) >= 2 or any(term in lower for term in ("产品线", "有哪些产品", "有哪些型号", "区别", "对比", "哪款", "哪个好")):
            context = DeviceContext(region=base.context.region)
        return MedicalResolution("product_discovery", "customer_product_discovery", context, mentioned_models or SALES_MODEL_CANDIDATES)

    getting_started_terms = ("型号在哪里", "哪里看型号", "版本在哪里", "哪里看版本", "怎么提问", "如何提问", "第一次使用", "入门")
    if any(term in lower for term in getting_started_terms):
        return MedicalResolution("customer_getting_started", "customer_getting_started", base.context, [])

    troubleshooting_terms = ("故障", "排障", "连不上", "连接不上", "失败", "异常", "找不到", "没有菜单", "报错", "错误码", "网络问题")
    if any(term in lower for term in troubleshooting_terms):
        if not base.context.model_code:
            return MedicalResolution(
                "clarify", "customer_missing_model_for_troubleshooting", base.context,
                SALES_MODEL_CANDIDATES + ["我不知道型号在哪里看"],
            )
        return MedicalResolution("customer_troubleshooting", "customer_guided_troubleshooting", base.context, [])

    if base.intent == "clarify":
        return base
    return MedicalResolution("knowledge", "customer_public_knowledge", base.context, [])


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


def verify_field_correction_evidence(notice_id: str, evidence: list[dict]) -> tuple[list[dict], str]:
    """Verify notice identity and authority before applicability is evaluated.

    Model/version/lot mismatches are deliberately not rejected here: those are
    inputs to the deterministic applicability rule and may legitimately produce
    a "does not apply" answer.
    """
    expected = notice_id.casefold().strip()
    verified: list[dict] = []
    for hit in evidence:
        if str(hit.get("status", "active")) not in {"", "active"}:
            continue
        authority = str(hit.get("authority_level", "")).casefold().strip()
        haystack = " ".join(str(hit.get(field, "")) for field in ("document_id", "title", "content", "source_file")).casefold()
        if expected not in haystack:
            continue
        if authority and authority not in {"field_correction", "manufacturer"}:
            continue
        verified.append(hit)
    if not verified:
        return [], "field_correction_notice_not_verified"
    return verified, "field_correction_notice_verified"


def verify_customer_evidence(intent: str, context: DeviceContext, evidence: list[dict]) -> tuple[list[dict], str]:
    """Defense-in-depth filter for the public customer application."""
    public = [
        hit for hit in evidence
        if str(hit.get("dataset_id", "")) in {"", "public-medical-device-sales"}
        and str(hit.get("status", "active")) in {"", "active"}
        and str(hit.get("authority_level", "")).casefold() not in {"tenant_runbook", "internal", "unverified"}
        and not str(hit.get("document_id", "")).casefold().startswith(("legacy-", "archived-"))
    ]
    if not public:
        return [], "no_public_customer_evidence"
    if intent in {"product_discovery", "customer_getting_started"}:
        return public, "public_product_evidence_verified"
    verified, reason = verify_medical_evidence(context, public)
    return verified, reason


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
