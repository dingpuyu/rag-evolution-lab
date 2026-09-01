from __future__ import annotations

import re
from dataclasses import dataclass

from .models import DeviceContext


MODEL_PATTERN = re.compile(r"\bVSM[\s-]?(100\s*PRO|100|200)\b", re.IGNORECASE)
VERSION_PATTERN = re.compile(r"(?:软件|固件|版本|FW)\s*[Vv]?\s*(\d+\.\d+(?:\.\d+)?)", re.IGNORECASE)
LOT_PATTERN = re.compile(r"\b(L26A\d{2})\b", re.IGNORECASE)
SALES_MODEL_ALIASES = (
    ("BeneVision N22", ("benevision n22", "n22监护仪", "n22 监护仪")),
    ("BeneVision N19", ("benevision n19", "n19监护仪", "n19 监护仪")),
    ("IntelliVue MX850", ("intellivue mx850", "mx850")),
    ("IntelliVue MX750", ("intellivue mx750", "mx750")),
    ("IntelliVue MX550", ("intellivue mx550", "mx550")),
    ("IntelliVue MX500", ("intellivue mx500", "mx500")),
    ("IntelliVue MP5", ("intellivue mp5", "mp5监护仪", "mp5 监护仪")),
    ("BeneVision N1", ("benevision n1", "n1监护仪", "n1 监护仪")),
    ("BeneVision TMS30", ("benevision tms30", "tms30")),
    ("BeneVision CMS", ("benevision cms", "cms中央监护", "cms 中央监护")),
    ("BeneHeart C Series", ("beneheart c series", "beneheart c系列", "beneheart c 系列", "beneheart c")),
    ("BeneHeart D60", ("beneheart d60", "d60除颤", "d60 除颤")),
    ("BeneHeart D30", ("beneheart d30", "d30除颤", "d30 除颤")),
    ("BeneHeart DX", ("beneheart dx",)),
    ("HeartStart FRx", ("heartstart frx", "frx aed")),
    ("BeneFusion nVP", ("benefusion nvp", "nvp输液", "nvp 输液")),
    ("BeneFusion nSP", ("benefusion nsp", "nsp注射", "nsp 注射")),
    ("BeneFusion nDS", ("benefusion nds", "nds工作站", "nds 工作站")),
    ("BeneFusion tDS", ("benefusion tds", "tds工作站", "tds 工作站")),
    ("BeneFusion i/u", ("benefusion i/u", "benefusion i系列", "benefusion u系列", "benefusion i/u系列")),
    ("EPIQ Elite", ("epiq elite",)),
    ("Resona I9", ("resona i9", "resona i 9")),
    ("SV800", ("mindray sv800", "sv800呼吸机", "sv800 呼吸机")),
    ("SV600", ("mindray sv600", "sv600呼吸机", "sv600 呼吸机")),
    ("SV70", ("mindray sv70", "sv70呼吸机", "sv70 呼吸机")),
    ("SV60", ("mindray sv60", "sv60呼吸机", "sv60 呼吸机")),
    ("Evita V800", ("evita v800", "v800呼吸机", "v800 呼吸机")),
    ("Babylog VN800", ("babylog vn800", "vn800")),
    ("Babylog VN600", ("babylog vn600", "vn600")),
    ("Savina 300", ("savina 300",)),
    ("PulmoVista 500", ("pulmovista 500",)),
    ("Atlan A350 XL", ("atlan a350 xl",)),
    ("Atlan A350", ("atlan a350",)),
    ("Perseus A500", ("perseus a500",)),
    ("WATO EX-65", ("wato ex-65", "wato ex65")),
    ("BeneHeart R700", ("beneheart r700", "r700心电", "r700 心电")),
    ("BC-760 CS", ("bc-760 cs", "bc760 cs", "bc760cs")),
    ("Avalon FM50", ("avalon fm50", "fm50胎儿", "fm50 胎儿")),
    ("ePM 10M", ("epm 10m", "epm 10m/12m", "epm10m")),
    ("ePM 12M", ("epm 12m", "epm 10m/12m", "epm12m")),
    ("M9", ("mindray m9", "迈瑞 m9", "m9超声", "m9 超声")),
    ("Consona N9", ("consona n9",)),
    ("Consona N8", ("consona n8",)),
    ("Consona N7", ("consona n7",)),
    ("Consona N6", ("consona n6",)),
    ("Consona N5", ("consona n5",)),
    ("BeneVision V700 Neo", ("benevision v700 neo", "v700 neo")),
    ("BeneVision V500 Neo", ("benevision v500 neo", "v500 neo")),
    ("BeneVision V200 Neo", ("benevision v200 neo", "v200 neo")),
    ("BeneVision VMAX OR", ("benevision vmax or", "vmax or")),
    ("BeneVision V700 OR", ("benevision v700 or", "v700 or")),
    ("BeneVision V500 OR", ("benevision v500 or", "v500 or")),
    ("BeneVision V200 OR", ("benevision v200 or", "v200 or")),
)
SALES_MODEL_CANDIDATES = [name for name, _ in SALES_MODEL_ALIASES]
SALES_CATEGORY_ALIASES = (
    ("病人监护", ("监护仪", "病人监护", "患者监护", "生命体征监护"), ("BeneVision N1", "BeneVision N22", "BeneVision N19", "IntelliVue MX500", "IntelliVue MX550", "IntelliVue MX750", "IntelliVue MX850")),
    ("AED", ("aed", "自动体外除颤器", "自动除颤器", "除颤器"), ("BeneHeart C Series", "HeartStart FRx")),
    ("除颤监护", ("除颤监护仪", "监护除颤仪", "除颤器"), ("BeneHeart DX", "BeneHeart D60", "BeneHeart D30")),
    ("输注系统", ("输注系统", "输注泵", "输液泵", "注射泵", "输液工作站"), ("BeneFusion nVP", "BeneFusion nSP", "BeneFusion nDS", "BeneFusion tDS", "BeneFusion i/u")),
    ("超声", ("超声", "彩超"), ("Resona I9", "EPIQ Elite")),
    ("呼吸机", ("呼吸机", "通气设备"), ("Evita V800", "Savina 300", "SV800", "SV600", "SV70", "SV60", "Babylog VN800")),
    ("肺通气成像", ("肺通气成像", "电阻抗断层", "eit"), ("PulmoVista 500",)),
    ("遥测监护", ("遥测监护", "tms30"), ("BeneVision TMS30",)),
    ("中央监护", ("中央监护", "集中监护", "benevision cms"), ("BeneVision CMS",)),
    ("亚重症监护", ("epm 10m/12m", "亚重症监护"), ("ePM 10M", "ePM 12M")),
    (
        "新生儿监护",
        ("benevision v neo", "v neo系列", "v neo 系列", "新生儿监护"),
        ("BeneVision V700 Neo", "BeneVision V500 Neo", "BeneVision V200 Neo"),
    ),
    (
        "手术室监护",
        ("benevision v or", "v or系列", "v or 系列", "or系列", "or 系列", "手术室监护", "麻醉监护"),
        ("BeneVision VMAX OR", "BeneVision V700 OR", "BeneVision V500 OR", "BeneVision V200 OR"),
    ),
    ("麻醉工作站", ("麻醉工作站", "麻醉系统", "atlan", "perseus", "wato"), ("Atlan A350", "Atlan A350 XL", "Perseus A500", "WATO EX-65")),
    ("心电图机", ("心电图机", "心电图设备", "多道心电"), ("BeneHeart R700",)),
    ("血液分析", ("血液细胞分析", "血液分析仪", "血球仪"), ("BC-760 CS",)),
    ("胎儿监护", ("胎儿监护", "孕产妇监护", "产科监护"), ("Avalon FM50",)),
)
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
    matched: list[str] = []
    for model, aliases in SALES_MODEL_ALIASES:
        if not any(_alias_matches(normalized, alias) for alias in aliases):
            continue
        # Longer catalog names are ordered first. If "Atlan A350 XL" was
        # matched, do not also interpret its prefix as a second A350 model.
        if any(existing.casefold().startswith(model.casefold() + " ") for existing in matched):
            continue
        matched.append(model)
    return matched


def _alias_matches(normalized: str, alias: str) -> bool:
    normalized_alias = alias.casefold()
    if normalized_alias.isascii():
        return bool(re.search(rf"(?<![a-z0-9]){re.escape(normalized_alias)}(?![a-z0-9])", normalized))
    return normalized_alias in normalized


def mentioned_sales_categories(query: str) -> list[str]:
    """Resolve novice product-language to stable catalog categories.

    Short Latin names such as ``AED`` use token boundaries so a category is
    not accidentally inferred from a longer identifier. Chinese aliases use
    regular substring matching because they do not have whitespace boundaries.
    """
    normalized = query.casefold()
    categories: list[str] = []
    for category, aliases, _ in SALES_CATEGORY_ALIASES:
        matched = any(
            _alias_matches(normalized, alias)
            for alias in aliases
        )
        # “自动体外除颤器/AED”已经是明确产品类别，不能再因为其中
        # 包含通用词“除颤器”而误扩展成专业除颤监护仪产品线。
        if category == "除颤监护" and any(term in normalized for term in ("aed", "自动体外除颤器", "自动除颤器")):
            matched = False
        if matched:
            categories.append(category)
    return categories


def sales_models_for_categories(categories: list[str]) -> list[str]:
    """Return deduplicated model suggestions for the resolved categories."""
    requested = set(categories)
    models: list[str] = []
    for category, _, category_models in SALES_CATEGORY_ALIASES:
        if category not in requested:
            continue
        for model in category_models:
            if model not in models:
                models.append(model)
    return models


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
        "潮气量应设置多少", "潮气量应该设置多少", "潮气量设为多少", "氧浓度应设置多少", "麻药浓度应设置多少", "多少焦耳", "何时放电",
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
    mentioned_categories = mentioned_sales_categories(query)
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
    # Price, stock and delivery dates are transactional facts, not durable
    # knowledge. Even a relevant product brochure cannot prove their current
    # value, so fail closed before retrieval instead of letting the LLM turn a
    # disclaimer into a semantically misleading "answer" decision.
    dynamic_commercial_terms = ("价格", "报价", "多少钱", "库存", "现货", "交期", "到货时间", "实时库存")
    if any(term in lower for term in dynamic_commercial_terms):
        return MedicalResolution("refuse", "dynamic_commercial_data_unavailable", base.context, [])
    onboarding_terms = ("一窍不通", "完全不了解", "什么都不懂", "从零开始", "怎么开始了解", "第一次了解")
    if base.intent == "persona" or any(term in lower for term in onboarding_terms):
        return MedicalResolution("customer_onboarding", "customer_guided_onboarding", base.context, ["产品线概览", "认识型号", "开始排障"])

    product_terms = ("产品线", "有哪些产品", "有哪些型号", "产品介绍", "型号介绍", "特点", "特色", "区别", "对比", "哪款", "哪个好", "适合", "什么设备", "什么类型", "面向什么场景", "应用场景")
    troubleshooting_terms = ("故障", "排障", "连不上", "连接不上", "失败", "异常", "找不到", "没有菜单", "报错", "错误码", "网络问题")
    category_discovery = bool(mentioned_categories) and not any(term in lower for term in troubleshooting_terms)
    if len(mentioned_models) >= 2 or category_discovery or any(term in lower for term in product_terms):
        context = base.context
        if len(mentioned_models) >= 2 or (category_discovery and not mentioned_models) or any(term in lower for term in ("产品线", "有哪些产品", "有哪些型号", "区别", "对比", "哪款", "哪个好")):
            context = DeviceContext(region=base.context.region)
        candidates = mentioned_models or sales_models_for_categories(mentioned_categories) or SALES_MODEL_CANDIDATES
        return MedicalResolution("product_discovery", "customer_product_discovery", context, candidates)

    getting_started_terms = ("型号在哪里", "哪里看型号", "版本在哪里", "哪里看版本", "怎么提问", "如何提问", "第一次使用", "入门")
    if any(term in lower for term in getting_started_terms):
        return MedicalResolution("customer_getting_started", "customer_getting_started", base.context, [])

    if any(term in lower for term in troubleshooting_terms):
        if not base.context.model_code:
            candidates = sales_models_for_categories(mentioned_categories) or SALES_MODEL_CANDIDATES
            return MedicalResolution(
                "clarify", "customer_missing_model_for_troubleshooting", base.context,
                candidates + ["我不知道型号在哪里看"],
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
