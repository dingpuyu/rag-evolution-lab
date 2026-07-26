"use client";

import { useRef, useState } from "react";

type SimilarityResult = {
  embedder: string;
  mode: string;
  dimensions: number;
  latency_ms: number;
  explanation: string;
  vector_a: { preview: number[]; l2_norm: number; minimum: number; maximum: number };
  vector_b: { preview: number[]; l2_norm: number; minimum: number; maximum: number };
  metrics: { cosine_similarity: number; dot_product: number; euclidean_distance: number };
};

type MilvusStatus = {
  connected: boolean;
  collection: string;
  collection_id: string;
  row_count: number;
  dimensions: number;
  metric: string;
  index_type: string;
  index_name: string;
  load_state: string;
  embedder: string;
};

type MilvusSearchResult = {
  query: string;
  collection: string;
  embedder: string;
  dimensions: number;
  metric: string;
  filter: string;
  embedding_latency_ms: number;
  search_latency_ms: number;
  total_latency_ms: number;
  hits: Array<{
    chunk_id: string;
    document_id: string;
    title: string;
    content: string;
    tenant_id: string;
    product: string;
    version: string;
    status: string;
    visibility: string;
    distance: number;
  }>;
};

type ScaleIndexStatus = {
  collection: string;
  rows: number;
  index_name: string;
  index_type: string;
  metric: string;
  state: string;
  indexed_rows: number;
  pending_rows: number;
  parameters: Record<string, string>;
};

type ScaleStatus = {
  connected: boolean;
  dataset: { chunks: number; dimensions: number; topics: number; tenants: number; profile: string };
  flat: ScaleIndexStatus;
  hnsw: ScaleIndexStatus;
  checked_at: string;
};

type ScaleSearchResult = {
  topic: number;
  scenario: string;
  tenant: string;
  top_k: number;
  ef: number;
  filter: string;
  query_vector_preview: number[];
  query_l2_norm: number;
  exact_recall_at_k: number;
  topic_hit_at_k: number;
  topic_precision_at_k: number;
  flat_latency_ms: number;
  hnsw_latency_ms: number;
  total_latency_ms: number;
  exact_top_k: string[];
  hits: Array<{
    rank: number;
    chunk_id: string;
    title: string;
    content: string;
    tenant_id: string;
    status: string;
    visibility: string;
    distance: number;
    in_exact_top_k: boolean;
    expected_topic: boolean;
  }>;
};

type AuthSession = {
  access_token: string;
  token_type: string;
  expires_at: number;
  identity: {
    subject: string;
    tenant_id: string;
    roles: string[];
    issuer: string;
    audience: string;
    expires_at: number;
  };
};

type DatasetResource = {
  id: string;
  name: string;
  description: string;
  visibility: "public" | "tenant";
  owner_tenant?: string;
  allowed_roles?: string[];
  status: string;
  created_by?: string;
};

type ControlPlaneStatus = {
  backend: string;
  connected: boolean;
  tenants: number;
  users: number;
  memberships: number;
  datasets: number;
};

type MembershipResource = {
  tenant_id: string;
  subject: string;
  role: string;
  status: string;
};

type DatasetSearchResponse = {
  dataset: DatasetResource;
  result: MilvusSearchResult;
};

type DatasetAnswerResult = {
  answerable: boolean;
  answer: string;
  refusal_reason?: string;
  citations: Array<{ chunk_id: string; document_id: string; document: string; excerpt: string }>;
  search: MilvusSearchResult;
  generation: {
    generator: string;
    model: string;
    prompt_version: string;
    finish_reason?: string;
    latency_ms: number;
    ttft_ms?: number;
    token_rate_tps?: number;
    prompt_tokens: number;
    output_tokens: number;
    safety_adjustments?: string[];
  };
};

type DatasetAnswerResponse = {
  dataset: DatasetResource;
  result: DatasetAnswerResult;
};

type AnswerStreamEvent = {
  type: string;
  elapsed_ms: number;
  delta?: string;
  search?: {
    hits: number;
    filter: string;
    embedding_latency_ms: number;
    search_latency_ms: number;
    total_latency_ms: number;
  };
  generation?: DatasetAnswerResult["generation"];
  response?: DatasetAnswerResult;
  error?: string;
};

type AuditEvent = {
  request_id: string;
  timestamp: string;
  subject?: string;
  tenant_id?: string;
  roles?: string[];
  method: string;
  path: string;
  decision: string;
  reason?: string;
  status: number;
  duration_ms: number;
};

type LifecycleStatus = {
  collection: string;
  alias: string;
  embedding_model: string;
  embedding_version: string;
  state_path: string;
  events: number;
  pending_events: number;
  documents: Record<string, {
    source_revision: number;
    deleted: boolean;
    document_version?: string;
    last_event_id: string;
    updated_at: string;
  }>;
};

type LifecycleResult = {
  event_id: string;
  operation: string;
  document_id: string;
  source_revision: number;
  collection: string;
  alias: string;
  embedding_model: string;
  embedding_version: string;
  previous_chunks: number;
  current_chunks: number;
  upserted_chunks: number;
  deleted_chunks: number;
  duplicate: boolean;
  verified: boolean;
  completed_at: string;
};

type IngestionJob = {
  job_id: string;
  idempotency_key: string;
  status: "queued" | "running" | "completed" | "failed" | "cancelled";
  stage: string;
  attempts: number;
  max_attempts: number;
  cancel_requested: boolean;
  last_error?: string;
  result?: LifecycleResult;
  created_at: string;
  updated_at: string;
};

type IngestionSummary = {
  total: number;
  queued: number;
  running: number;
  completed: number;
  failed: number;
  cancelled: number;
  jobs: IngestionJob[];
};

const metrics = [
  { label: "Hit Rate@5", before: 0.85, after: 0.9, delta: "+5.0%" },
  { label: "MRR", before: 0.762, after: 0.9, delta: "+13.8%" },
  { label: "Doc Recall@5", before: 0.85, after: 0.9, delta: "+5.0%" },
];

const ingestionStages = ["validating", "chunking", "embedding", "indexing", "verifying"];

const localModels = [
  { name: "Semantic Hash", detail: "deterministic · offline", hit: "0.900", mrr: "0.779", recall: "0.875" },
  { name: "mxbai-embed-large", detail: "Ollama · local", hit: "0.750", mrr: "0.692", recall: "0.700" },
  { name: "nomic-embed-text", detail: "Ollama · local", hit: "0.700", mrr: "0.675", recall: "0.650" },
  { name: "Qwen3-Embedding-4B", detail: "Q4_K_M · 2560d", hit: "0.900", mrr: "0.850", recall: "0.875", best: true },
  { name: "Qwen3 + Instruction", detail: "query-side only", hit: "0.900", mrr: "0.850", recall: "0.875" },
];

const hybridVariants = [
  { name: "V2 Metadata BM25", semantic: "0.750", access: "1.000", mrr: "0.900", violations: "0" },
  { name: "Qwen3 Hybrid + Metadata", semantic: "1.000", access: "0.500", mrr: "0.900", violations: "0", bestRecall: true },
  { name: "+ Consensus Gate", semantic: "0.750", access: "1.000", mrr: "0.900", violations: "0" },
];

const routeCards = [
  { intent: "EXACT", count: "11", strategy: "Metadata BM25", cue: "codes · headers · numbers" },
  { intent: "SEMANTIC", count: "4", strategy: "Hybrid Union", cue: "natural-language paraphrase" },
  { intent: "ACCESS", count: "4", strategy: "Tenant Gate + Consensus", cue: "tenant · privileged operation" },
  { intent: "RISK", count: "1", strategy: "Anchor Gate + Consensus", cue: "external status verification" },
];

const cases = {
  version: {
    id: "CASE / version_003",
    query: "当前稳定版的单点登录入口在哪里？",
    before: {
      tag: "STALE KNOWLEDGE",
      source: "AcmeCloud 2.1 SSO 配置指南",
      answer: "入口位于「安全设置 → SSO」",
      meta: "deprecated · version 2.1 · rank #1",
    },
    after: {
      tag: "VALID EVIDENCE",
      source: "AcmeCloud 2.3 SSO 配置指南",
      answer: "入口已迁移到「身份中心 → 企业登录」",
      meta: "active · version 2.3 · rank #1",
    },
  },
  access: {
    id: "CASE / access_002",
    query: "Tenant A 的 reports-priority-a 队列什么时候可以启用？",
    before: {
      tag: "NOISY ANSWER",
      source: "无关的公共报表导出文档",
      answer: "少于 50,000 行的任务可以同步导出……",
      meta: "ACL safe · irrelevant evidence · answered",
    },
    after: {
      tag: "SAFE REFUSAL",
      source: "No authorized & relevant evidence",
      answer: "知识库中没有找到足够证据。",
      meta: "0 results · 0 citations · refused",
    },
  },
};

export default function Home() {
  const [activeCase, setActiveCase] = useState<keyof typeof cases>("version");
  const [textA, setTextA] = useState("企业员工如何配置单点登录？");
  const [textB, setTextB] = useState("管理员可以在身份中心开启 SSO 企业登录。");
  const [embeddingMode, setEmbeddingMode] = useState("symmetric");
  const [apiBase, setAPIBase] = useState("http://127.0.0.1:8080");
  const [similarity, setSimilarity] = useState<SimilarityResult | null>(null);
  const [embeddingError, setEmbeddingError] = useState("");
  const [embeddingLoading, setEmbeddingLoading] = useState(false);
  const [milvusStatus, setMilvusStatus] = useState<MilvusStatus | null>(null);
  const [vectorQuery, setVectorQuery] = useState("当前版本如何配置企业单点登录？");
  const [vectorTenant, setVectorTenant] = useState("tenant_a");
  const [vectorRole, setVectorRole] = useState("admin");
  const [vectorProduct, setVectorProduct] = useState("");
  const [vectorTopK, setVectorTopK] = useState(5);
  const [vectorResult, setVectorResult] = useState<MilvusSearchResult | null>(null);
  const [milvusError, setMilvusError] = useState("");
  const [milvusLoading, setMilvusLoading] = useState(false);
  const [scaleStatus, setScaleStatus] = useState<ScaleStatus | null>(null);
  const [scaleResult, setScaleResult] = useState<ScaleSearchResult | null>(null);
  const [scaleTopic, setScaleTopic] = useState(137);
  const [scaleScenario, setScaleScenario] = useState("public_active");
  const [scaleTopK, setScaleTopK] = useState(10);
  const [scaleEF, setScaleEF] = useState(64);
  const [scaleError, setScaleError] = useState("");
  const [scaleLoading, setScaleLoading] = useState(false);
  const [scaleStatusLoading, setScaleStatusLoading] = useState(false);
  const [authPersona, setAuthPersona] = useState("tenant037_admin");
  const [authSession, setAuthSession] = useState<AuthSession | null>(null);
  const [authLoading, setAuthLoading] = useState(false);
  const [accountEmail, setAccountEmail] = useState("alice@tenant-a.local");
  const [accountPassword, setAccountPassword] = useState("RagLab-Alice-2026!");
  const [accountOrganization, setAccountOrganization] = useState("My New Organization");
  const [authError, setAuthError] = useState("");
  const [datasets, setDatasets] = useState<DatasetResource[]>([]);
  const [datasetID, setDatasetID] = useState("tenant-a-operations");
  const [datasetQuery, setDatasetQuery] = useState("专属应急队列是什么？");
  const [datasetResult, setDatasetResult] = useState<DatasetSearchResponse | null>(null);
  const [datasetError, setDatasetError] = useState("");
  const [datasetLoading, setDatasetLoading] = useState(false);
  const [answerResult, setAnswerResult] = useState<DatasetAnswerResponse | null>(null);
  const [answerEvents, setAnswerEvents] = useState<AnswerStreamEvent[]>([]);
  const [answerStreamPreview, setAnswerStreamPreview] = useState("");
  const [answerError, setAnswerError] = useState("");
  const [answerLoading, setAnswerLoading] = useState(false);
  const answerAbortRef = useRef<AbortController | null>(null);
  const [controlPlane, setControlPlane] = useState<ControlPlaneStatus | null>(null);
  const [memberships, setMemberships] = useState<MembershipResource[]>([]);
  const [newDatasetName, setNewDatasetName] = useState("产品支持知识库");
  const [newDatasetSlug, setNewDatasetSlug] = useState("product-support");
  const [newDatasetDescription, setNewDatasetDescription] = useState("由当前 Tenant Admin 创建并持久化的数据集");
  const [lastRequestID, setLastRequestID] = useState("");
  const [auditEvents, setAuditEvents] = useState<AuditEvent[]>([]);
  const [lifecycleStatus, setLifecycleStatus] = useState<LifecycleStatus | null>(null);
  const [lifecycleResult, setLifecycleResult] = useState<LifecycleResult | null>(null);
  const [lifecycleSearch, setLifecycleSearch] = useState<MilvusSearchResult | null>(null);
  const [lifecycleDocumentID, setLifecycleDocumentID] = useState("lifecycle-demo");
  const [lifecycleRevision, setLifecycleRevision] = useState(1);
  const [lifecycleVersion, setLifecycleVersion] = useState("1.0");
  const [lifecycleContent, setLifecycleContent] = useState("# 企业知识更新\n\n旧版入口位于安全设置页面。\n\n管理员需要配置单点登录。");
  const [lifecycleQuery, setLifecycleQuery] = useState("单点登录入口在哪里？");
  const [lifecycleError, setLifecycleError] = useState("");
  const [lifecycleLoading, setLifecycleLoading] = useState(false);
  const [ingestionSummary, setIngestionSummary] = useState<IngestionSummary | null>(null);
  const [ingestionError, setIngestionError] = useState("");
  const [ingestionLoading, setIngestionLoading] = useState(false);
  const current = cases[activeCase];

  async function compareEmbeddings() {
    setEmbeddingLoading(true);
    setEmbeddingError("");
    try {
      const response = await fetch(`${apiBase.replace(/\/$/, "")}/api/v1/embeddings/similarity`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ text_a: textA, text_b: textB, mode: embeddingMode, preview_dimensions: 12 }),
      });
      const body = await response.json();
      if (!response.ok) throw new Error(body?.error?.message || `HTTP ${response.status}`);
      setSimilarity(body);
    } catch (error) {
      setSimilarity(null);
      setEmbeddingError(error instanceof Error ? error.message : "无法连接 Embedding API");
    } finally {
      setEmbeddingLoading(false);
    }
  }

  async function refreshMilvus() {
    setMilvusError("");
    try {
      const response = await fetch(`${apiBase.replace(/\/$/, "")}/api/v1/milvus/status`);
      const body = await response.json();
      if (!response.ok) throw new Error(body?.error?.message || `HTTP ${response.status}`);
      setMilvusStatus(body);
    } catch (error) {
      setMilvusStatus(null);
      setMilvusError(error instanceof Error ? error.message : "无法连接 Milvus API");
    }
  }

  async function searchMilvus() {
    setMilvusLoading(true);
    setMilvusError("");
    try {
      const base = apiBase.replace(/\/$/, "");
      const [statusResponse, searchResponse] = await Promise.all([
        fetch(`${base}/api/v1/milvus/status`),
        fetch(`${base}/api/v1/milvus/search`, {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            ...(authSession ? { Authorization: `Bearer ${authSession.access_token}` } : {}),
          },
          body: JSON.stringify({ query: vectorQuery, tenant_id: vectorTenant, user_role: vectorRole, product: vectorProduct, status: "active", top_k: vectorTopK }),
        }),
      ]);
      const statusBody = await statusResponse.json();
      const searchBody = await searchResponse.json();
      if (!statusResponse.ok) throw new Error(statusBody?.error?.message || `HTTP ${statusResponse.status}`);
      if (!searchResponse.ok) throw new Error(searchBody?.error?.message || `HTTP ${searchResponse.status}`);
      setMilvusStatus(statusBody);
      setVectorResult(searchBody);
    } catch (error) {
      setVectorResult(null);
      setMilvusError(error instanceof Error ? error.message : "Milvus 向量检索失败");
    } finally {
      setMilvusLoading(false);
    }
  }

  async function refreshScaleStatus() {
    setScaleStatusLoading(true);
    setScaleError("");
    try {
      const response = await fetch(`${apiBase.replace(/\/$/, "")}/api/v1/milvus/scale/status`);
      const body = await response.json();
      if (!response.ok) throw new Error(body?.error?.message || `HTTP ${response.status}`);
      setScaleStatus(body);
    } catch (error) {
      setScaleStatus(null);
      setScaleError(error instanceof Error ? error.message : "无法连接 100K Milvus API");
    } finally {
      setScaleStatusLoading(false);
    }
  }

  async function searchScale() {
    setScaleLoading(true);
    setScaleError("");
    try {
      const response = await fetch(`${apiBase.replace(/\/$/, "")}/api/v1/milvus/scale/search`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          ...(authSession ? { Authorization: `Bearer ${authSession.access_token}` } : {}),
        },
        body: JSON.stringify({ topic: scaleTopic, scenario: scaleScenario, top_k: scaleTopK, ef: scaleEF }),
      });
      const body = await response.json();
      if (!response.ok) throw new Error(body?.error?.message || `HTTP ${response.status}`);
      setLastRequestID(response.headers.get("X-Request-ID") || "");
      setScaleResult(body);
      if (!scaleStatus) void refreshScaleStatus();
    } catch (error) {
      setScaleResult(null);
      setScaleError(error instanceof Error ? error.message : "100K 向量检索失败");
    } finally {
      setScaleLoading(false);
    }
  }

  async function issueDevIdentity() {
    setAuthLoading(true);
    setScaleError("");
    try {
      const response = await fetch(`${apiBase.replace(/\/$/, "")}/api/v1/auth/dev-token`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ persona: authPersona }),
      });
      const body = await response.json();
      if (!response.ok) throw new Error(body?.error?.message || `HTTP ${response.status}`);
      setAuthSession(body);
      setDatasets([]);
      setDatasetResult(null);
      await loadDatasets(body);
      setScaleResult(null);
      setAuditEvents([]);
    } catch (error) {
      setAuthSession(null);
      setScaleError(error instanceof Error ? error.message : "身份令牌签发失败");
    } finally {
      setAuthLoading(false);
    }
  }

  async function accountAuthentication(mode: "login" | "register") {
    setAuthLoading(true);
    setAuthError("");
    try {
      const response = await fetch(`${apiBase.replace(/\/$/, "")}/api/v1/auth/${mode}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(mode === "login"
          ? { email: accountEmail, password: accountPassword }
          : { email: accountEmail, password: accountPassword, organization: accountOrganization }),
      });
      const body = await response.json();
      if (!response.ok) throw new Error(body?.error?.message || `HTTP ${response.status}`);
      setAuthSession(body);
      setDatasetResult(null);
      await loadDatasets(body);
    } catch (error) {
      setAuthSession(null);
      setDatasets([]);
      setAuthError(error instanceof Error ? error.message : "账号认证失败");
    } finally {
      setAuthLoading(false);
    }
  }

  async function loadDatasets(session = authSession) {
    if (!session) {
      setDatasetError("请先登录或签发身份");
      return;
    }
    setDatasetError("");
    try {
      const response = await fetch(`${apiBase.replace(/\/$/, "")}/api/v1/datasets`, {
        headers: { Authorization: `Bearer ${session.access_token}` },
      });
      const body = await response.json();
      if (!response.ok) throw new Error(body?.error?.message || `HTTP ${response.status}`);
      setDatasets(body.datasets || []);
      const privateDataset = (body.datasets || []).find((item: DatasetResource) => item.visibility === "tenant");
      if (privateDataset) setDatasetID(privateDataset.id);
      setLastRequestID(response.headers.get("X-Request-ID") || "");
      await loadControlPlane(session);
    } catch (error) {
      setDatasets([]);
      setDatasetError(error instanceof Error ? error.message : "读取数据集失败");
    }
  }

  async function loadControlPlane(session = authSession) {
    if (!session) return;
    try {
      const headers = { Authorization: `Bearer ${session.access_token}` };
      const [statusResponse, memberResponse] = await Promise.all([
        fetch(`${apiBase.replace(/\/$/, "")}/api/v1/control-plane/status`, { headers }),
        fetch(`${apiBase.replace(/\/$/, "")}/api/v1/memberships`, { headers }),
      ]);
      const statusBody = await statusResponse.json();
      const memberBody = await memberResponse.json();
      if (!statusResponse.ok) throw new Error(statusBody?.error?.message || `HTTP ${statusResponse.status}`);
      setControlPlane(statusBody);
      setMemberships(memberResponse.ok ? memberBody.members || [] : []);
    } catch (error) {
      setControlPlane(null);
      setMemberships([]);
      setDatasetError(error instanceof Error ? error.message : "读取控制面失败");
    }
  }

  async function createDataset() {
    if (!authSession) {
      setDatasetError("请先以 Tenant Admin 登录");
      return;
    }
    setDatasetLoading(true);
    setDatasetError("");
    try {
      const response = await fetch(`${apiBase.replace(/\/$/, "")}/api/v1/datasets`, {
        method: "POST",
        headers: { "Content-Type": "application/json", Authorization: `Bearer ${authSession.access_token}` },
        body: JSON.stringify({
          name: newDatasetName,
          slug: newDatasetSlug,
          description: newDatasetDescription,
          visibility: "tenant",
        }),
      });
      const body = await response.json();
      if (!response.ok) throw new Error(body?.error?.message || `HTTP ${response.status}`);
      setDatasetID(body.id);
      await loadDatasets(authSession);
    } catch (error) {
      setDatasetError(error instanceof Error ? error.message : "创建数据集失败");
    } finally {
      setDatasetLoading(false);
    }
  }

  async function searchDataset(targetID = datasetID) {
    if (!authSession) {
      setDatasetError("请先登录或签发身份");
      return;
    }
    setDatasetLoading(true);
    setDatasetError("");
    setDatasetResult(null);
    try {
      const response = await fetch(`${apiBase.replace(/\/$/, "")}/api/v1/datasets/${encodeURIComponent(targetID)}/search`, {
        method: "POST",
        headers: { "Content-Type": "application/json", Authorization: `Bearer ${authSession.access_token}` },
        body: JSON.stringify({ query: datasetQuery, top_k: 5 }),
      });
      const body = await response.json();
      setLastRequestID(response.headers.get("X-Request-ID") || "");
      if (!response.ok) throw new Error(`${response.status} · ${body?.error?.message || "访问被拒绝"}`);
      setDatasetResult(body);
    } catch (error) {
      setDatasetError(error instanceof Error ? error.message : "数据集检索失败");
    } finally {
      setDatasetLoading(false);
    }
  }

  async function streamDatasetAnswer() {
    if (!authSession) {
      setAnswerError("请先登录或签发身份");
      return;
    }
    answerAbortRef.current?.abort();
    const controller = new AbortController();
    answerAbortRef.current = controller;
    setAnswerLoading(true);
    setAnswerError("");
    setAnswerResult(null);
    setAnswerEvents([]);
    setAnswerStreamPreview("");
    try {
      const response = await fetch(`${apiBase.replace(/\/$/, "")}/api/v1/datasets/${encodeURIComponent(datasetID)}/answer/stream`, {
        method: "POST",
        headers: { "Content-Type": "application/json", Authorization: `Bearer ${authSession.access_token}` },
        body: JSON.stringify({ query: datasetQuery, top_k: 5 }),
        signal: controller.signal,
      });
      if (!response.ok) {
        const body = await response.json().catch(() => null);
        throw new Error(`${response.status} · ${body?.error?.message || "回答请求被拒绝"}`);
      }
      const reader = response.body?.getReader();
      if (!reader) throw new Error("浏览器不支持 SSE 读取");
      const decoder = new TextDecoder();
      let buffer = "";
      const consumeFrame = (frame: string) => {
        const data = frame.split("\n").filter((line) => line.startsWith("data:")).map((line) => line.slice(5).trimStart()).join("\n");
        if (!data) return;
        const payload = JSON.parse(data) as { dataset?: DatasetResource; event?: AnswerStreamEvent };
        if (!payload.event) return;
        const event = payload.event;
        if (event.type === "token") {
          setAnswerStreamPreview((currentPreview) => currentPreview + (event.delta || ""));
        } else {
          setAnswerEvents((currentEvents) => [...currentEvents, event]);
        }
        if (event.type === "completed" && event.response && payload.dataset) {
          setAnswerResult({ dataset: payload.dataset, result: event.response });
          setAnswerStreamPreview(event.response.answer);
        }
        if (event.type === "error") setAnswerError(event.error || "流式回答失败");
      };
      while (true) {
        const read = await reader.read();
        buffer += decoder.decode(read.value || new Uint8Array(), { stream: !read.done });
        const frames = buffer.split("\n\n");
        buffer = frames.pop() || "";
        frames.forEach(consumeFrame);
        if (read.done) break;
      }
      if (buffer.trim()) consumeFrame(buffer);
    } catch (error) {
      if (error instanceof DOMException && error.name === "AbortError") {
        setAnswerError("已取消本次回答");
      } else {
        setAnswerError(error instanceof Error ? error.message : "流式回答失败");
      }
    } finally {
      if (answerAbortRef.current === controller) answerAbortRef.current = null;
      setAnswerLoading(false);
    }
  }

  function cancelDatasetAnswer() {
    answerAbortRef.current?.abort();
  }

  async function loadAuditEvents() {
    if (!authSession) return;
    setScaleError("");
    try {
      const response = await fetch(`${apiBase.replace(/\/$/, "")}/api/v1/audit/recent`, {
        headers: { Authorization: `Bearer ${authSession.access_token}` },
      });
      const body = await response.json();
      if (!response.ok) throw new Error(body?.error?.message || `HTTP ${response.status}`);
      setAuditEvents(body.events || []);
      setLastRequestID(response.headers.get("X-Request-ID") || "");
    } catch (error) {
      setAuditEvents([]);
      setScaleError(error instanceof Error ? error.message : "读取审计事件失败");
    }
  }

  async function refreshLifecycleStatus() {
    if (!authSession) {
      setLifecycleError("请先签发 Platform Admin 身份");
      return;
    }
    setLifecycleError("");
    try {
      const response = await fetch(`${apiBase.replace(/\/$/, "")}/api/v1/milvus/lifecycle/status`, {
        headers: { Authorization: `Bearer ${authSession.access_token}` },
      });
      const body = await response.json();
      if (!response.ok) throw new Error(body?.error?.message || `HTTP ${response.status}`);
      setLifecycleStatus(body);
      setLastRequestID(response.headers.get("X-Request-ID") || "");
    } catch (error) {
      setLifecycleError(error instanceof Error ? error.message : "读取生命周期状态失败");
    }
  }

  async function applyLifecycle(operation: "upsert" | "delete") {
    if (!authSession) {
      setLifecycleError("请先签发 Platform Admin 身份");
      return;
    }
    setLifecycleLoading(true);
    setLifecycleError("");
    try {
      const eventID = `${lifecycleDocumentID}-r${lifecycleRevision}-${operation}`;
      const payload = operation === "upsert" ? {
        event_id: eventID,
        operation,
        source_revision: lifecycleRevision,
        document: {
          document_id: lifecycleDocumentID,
          title: "企业知识生命周期演示",
          content: lifecycleContent,
          product: "identity",
          version: lifecycleVersion,
          status: "active",
          visibility: "public",
          allowed_tenants: [],
          allowed_roles: [],
        },
      } : {
        event_id: eventID,
        operation,
        source_revision: lifecycleRevision,
        document_id: lifecycleDocumentID,
      };
      const response = await fetch(`${apiBase.replace(/\/$/, "")}/api/v1/milvus/lifecycle/apply`, {
        method: "POST",
        headers: { "Content-Type": "application/json", Authorization: `Bearer ${authSession.access_token}` },
        body: JSON.stringify(payload),
      });
      const body = await response.json();
      if (!response.ok) throw new Error(body?.error?.message || `HTTP ${response.status}`);
      setLifecycleResult(body);
      setLifecycleRevision((value) => value + 1);
      setLastRequestID(response.headers.get("X-Request-ID") || "");
      await refreshLifecycleStatus();
    } catch (error) {
      setLifecycleError(error instanceof Error ? error.message : "生命周期变更失败");
    } finally {
      setLifecycleLoading(false);
    }
  }

  async function searchLifecycle() {
    if (!authSession) {
      setLifecycleError("请先签发身份");
      return;
    }
    setLifecycleLoading(true);
    setLifecycleError("");
    try {
      const response = await fetch(`${apiBase.replace(/\/$/, "")}/api/v1/milvus/lifecycle/search`, {
        method: "POST",
        headers: { "Content-Type": "application/json", Authorization: `Bearer ${authSession.access_token}` },
        body: JSON.stringify({ query: lifecycleQuery, status: "active", top_k: 5 }),
      });
      const body = await response.json();
      if (!response.ok) throw new Error(body?.error?.message || `HTTP ${response.status}`);
      setLifecycleSearch(body);
      setLastRequestID(response.headers.get("X-Request-ID") || "");
    } catch (error) {
      setLifecycleSearch(null);
      setLifecycleError(error instanceof Error ? error.message : "生命周期检索失败");
    } finally {
      setLifecycleLoading(false);
    }
  }

  async function refreshIngestionJobs() {
    if (!authSession) {
      setIngestionError("请先签发 Platform Admin 身份");
      return;
    }
    setIngestionError("");
    try {
      const response = await fetch(`${apiBase.replace(/\/$/, "")}/api/v1/ingestion/jobs`, {
        headers: { Authorization: `Bearer ${authSession.access_token}` },
      });
      const body = await response.json();
      if (!response.ok) throw new Error(body?.error?.message || `HTTP ${response.status}`);
      setIngestionSummary(body);
      setLastRequestID(response.headers.get("X-Request-ID") || "");
    } catch (error) {
      setIngestionError(error instanceof Error ? error.message : "读取导入任务失败");
    }
  }

  async function submitIngestionJob() {
    if (!authSession) {
      setIngestionError("请先签发 Platform Admin 身份");
      return;
    }
    setIngestionLoading(true);
    setIngestionError("");
    try {
      const eventID = `${lifecycleDocumentID}-r${lifecycleRevision}-async-upsert`;
      const response = await fetch(`${apiBase.replace(/\/$/, "")}/api/v1/ingestion/jobs`, {
        method: "POST",
        headers: { "Content-Type": "application/json", Authorization: `Bearer ${authSession.access_token}` },
        body: JSON.stringify({
          idempotency_key: `job-${eventID}`,
          change: {
            event_id: eventID,
            operation: "upsert",
            source_revision: lifecycleRevision,
            document: {
              document_id: lifecycleDocumentID,
              title: "企业异步知识导入演示",
              content: lifecycleContent,
              product: "identity",
              version: lifecycleVersion,
              status: "active",
              visibility: "public",
              allowed_tenants: [],
              allowed_roles: [],
            },
          },
        }),
      });
      const body = await response.json();
      if (!response.ok) throw new Error(body?.error?.message || `HTTP ${response.status}`);
      setLifecycleRevision((value) => value + 1);
      setLastRequestID(response.headers.get("X-Request-ID") || "");
      await refreshIngestionJobs();
    } catch (error) {
      setIngestionError(error instanceof Error ? error.message : "创建异步导入任务失败");
    } finally {
      setIngestionLoading(false);
    }
  }

  async function mutateIngestionJob(jobID: string, action: "retry" | "cancel") {
    if (!authSession) return;
    setIngestionLoading(true);
    setIngestionError("");
    try {
      const response = await fetch(`${apiBase.replace(/\/$/, "")}/api/v1/ingestion/jobs/${jobID}/${action}`, {
        method: "POST",
        headers: { Authorization: `Bearer ${authSession.access_token}` },
      });
      const body = await response.json();
      if (!response.ok) throw new Error(body?.error?.message || `HTTP ${response.status}`);
      setLastRequestID(response.headers.get("X-Request-ID") || "");
      await refreshIngestionJobs();
    } catch (error) {
      setIngestionError(error instanceof Error ? error.message : `任务${action}失败`);
    } finally {
      setIngestionLoading(false);
    }
  }

  function formatVector(vector: number[]) {
    return vector.map((value) => value.toFixed(5)).join(", ");
  }

  return (
    <main>
      <nav className="nav shell">
        <a className="brand" href="#top" aria-label="RAG Evolution Lab home">
          <span className="brand-mark">R/</span>
          <span>RAG Evolution Lab</span>
        </a>
        <div className="nav-links">
          <a href="#evolution">演进路径</a>
          <a href="#hybrid">Hybrid 实验</a>
          <a href="#routing">Query Routing</a>
          <a href="#embedding-lab">Embedding 实验</a>
          <a href="#milvus-lab">Milvus Lab</a>
          <a href="#scale-lab">100K Lab</a>
          <a href="#dataset-isolation">数据隔离</a>
          <a href="#answer-lab">Answer Lab</a>
          <a href="#lifecycle-lab">增量索引</a>
          <a href="#ingestion-jobs">导入任务</a>
          <a href="#experiment">效果对比</a>
          <a href="#harness">Harness</a>
          <a className="repo-link" href="https://github.com/dingpuyu/rag-evolution-lab" target="_blank" rel="noreferrer">
            GitHub <span aria-hidden="true">↗</span>
          </a>
        </div>
      </nav>

      <section className="hero shell" id="top">
        <div className="hero-copy">
          <div className="eyebrow"><span className="pulse" /> PHASE 4 · COMPLETE</div>
          <h1>把 RAG 优化<br />变成<span>可证明的工程实验</span></h1>
          <p className="hero-lead">
            不是只展示一个能回答问题的 Demo。每次演进都从失败案例出发，
            通过固定语料、Golden Dataset 与自动化评测证明改进有效。
          </p>
          <div className="hero-actions">
            <a className="primary-button" href="#experiment">查看优化效果 <span>↓</span></a>
            <a className="text-button" href="#architecture">浏览系统架构 <span>→</span></a>
            <a className="text-button" href="/portal">进入智能客服门户 <span>↗</span></a>
          </div>
        </div>

        <div className="hero-console" aria-label="Latest evaluation summary">
          <div className="console-top">
            <div className="console-dots"><i /><i /><i /></div>
            <span>eval / latest</span>
            <span className="console-status">PASSED</span>
          </div>
          <div className="console-body">
            <div className="terminal-line"><span>$</span> raglab eval --pipeline v4-ollama-router</div>
            <div className="run-row"><span>dataset</span><strong>development + challenge / 28 cases</strong></div>
            <div className="run-row"><span>routing</span><strong>4 intents / 3 strategies / 2 gates</strong></div>
            <div className="score-line">
              <div><small>MRR</small><strong>1.000</strong><em>+0.100</em></div>
              <div><small>RECALL</small><strong>1.000</strong><em>28 / 28</em></div>
              <div><small>VIOLATIONS</small><strong>0</strong><em>guarded</em></div>
            </div>
            <div className="checks">
              <span><b>✓</b> unit tests</span>
              <span><b>✓</b> race test</span>
              <span><b>✓</b> GitHub CI</span>
            </div>
          </div>
        </div>
      </section>

      <section className="facts shell" aria-label="Project scale">
        <div><strong>13</strong><span>知识文档</span><small>Synthetic enterprise corpus</small></div>
        <div><strong>38</strong><span>结构化 Chunks</span><small>Stable IDs + heading paths</small></div>
        <div><strong>28</strong><span>Golden Queries</span><small>development + challenge</small></div>
        <div><strong>0</strong><span>越权召回</span><small>Fail-closed ACL regression</small></div>
      </section>

      <section className="section shell" id="evolution">
        <div className="section-heading">
          <div><span className="section-index">01</span><p>EVOLUTION PATH</p></div>
          <h2>同一套 Harness，持续演进</h2>
          <p>不复制整套系统，只改变一个变量，让每次收益和退化都能被解释。</p>
        </div>
        <div className="evolution-grid">
          <article className="version-card complete">
            <div className="version-top"><span>V0</span><small>BASELINE</small></div>
            <h3>Keyword / BM25</h3>
            <p>建立低成本、可解释基线。精确标识符稳定，但语义改写和知识污染明显。</p>
            <div className="version-metric"><span>MRR</span><strong>0.762</strong></div>
            <div className="version-state">✓ REPRODUCED</div>
          </article>
          <article className="version-card complete">
            <div className="version-top"><span>V1</span><small>SEMANTIC</small></div>
            <h3>Vector Retrieval</h3>
            <p>确定性向量基线与 Ollama 适配器。真实模型不一定自动超过简单基线。</p>
            <div className="version-metric"><span>MRR</span><strong>0.779</strong></div>
            <div className="version-state">✓ BENCHMARKED</div>
          </article>
          <article className="version-card complete">
            <div className="version-top"><span>V2</span><small>FILTERED</small></div>
            <h3>Metadata Filter</h3>
            <p>在评分前过滤产品、版本和生命周期，同时保留显式历史版本查询。</p>
            <div className="version-metric"><span>MRR</span><strong>0.900</strong></div>
            <div className="version-state">✓ VALIDATED</div>
          </article>
          <article className="version-card complete">
            <div className="version-top"><span>V3</span><small>FUSED</small></div>
            <h3>Hybrid + RRF</h3>
            <p>并行融合 Keyword 与 Qwen3 Vector；分类收益与拒答退化都进入 Harness。</p>
            <div className="version-metric"><span>DOC RECALL</span><strong>0.900</strong><em>+2.5%</em></div>
            <div className="version-state">✓ EXPERIMENTED</div>
          </article>
          <article className="version-card active">
            <div className="version-top"><span>V4</span><small>CURRENT</small></div>
            <h3>Query Router</h3>
            <p>按 Query 特征和风险选择 BM25、Hybrid Union 或 Consensus，并在安全冲突前置拒绝。</p>
            <div className="version-metric"><span>MRR</span><strong>1.000</strong><em>28 cases</em></div>
            <div className="version-state">● VALIDATED</div>
          </article>
        </div>
      </section>

      <section className="hybrid-experiment shell" id="hybrid" aria-labelledby="hybrid-title">
        <div className="hybrid-heading">
          <span>V3 / FAILURE-DRIVEN FUSION</span>
          <h2 id="hybrid-title">总分一样，系统行为可能完全不同</h2>
          <p>RRF 并集找回了全部语义改写，却让一个应拒答的权限 Case 返回无关公共证据；Consensus 修复拒答，又牺牲了语义召回。</p>
        </div>
        <div className="hybrid-table">
          <div className="hybrid-row hybrid-head"><span>STRATEGY</span><span>SEMANTIC HIT</span><span>ACCESS HIT</span><span>MRR</span><span>POLLUTION</span></div>
          {hybridVariants.map((variant) => (
            <div className={`hybrid-row${variant.bestRecall ? " highlight" : ""}`} key={variant.name}>
              <strong>{variant.name}</strong><b>{variant.semantic}</b><b>{variant.access}</b><b>{variant.mrr}</b><b>{variant.violations}</b>
            </div>
          ))}
        </div>
        <div className="hybrid-notes">
          <span><b>UNION</b> recall 优先</span><span><b>CONSENSUS</b> precision 优先</span><span><b>V4</b> route by risk</span>
        </div>
      </section>

      <section className="routing-lab shell" id="routing" aria-labelledby="routing-title">
        <div className="routing-title">
          <span>V4 / QUERY ROUTING</span>
          <h2 id="routing-title">把策略选择也纳入评测</h2>
          <p>分类器只读取 Query，不读取 Golden 标签。Route、Reason 和 Strategy 全部写入 Trace。</p>
        </div>
        <div className="route-flow">
          <div className="route-input"><small>INPUT</small><strong>Query + Auth Context</strong><span>observable features only</span></div>
          <i>→</i>
          <div className="route-classifier"><small>CLASSIFY</small><strong>Intent + Risk</strong><span>deterministic · testable</span></div>
          <i>→</i>
          <div className="route-output"><small>TRACE</small><strong>Route + Reason</strong><span>strategy · result count</span></div>
        </div>
        <div className="route-grid">
          {routeCards.map((route) => (
            <article key={route.intent}><span>{route.intent}</span><b>{route.count}<small>/20</small></b><strong>{route.strategy}</strong><p>{route.cue}</p></article>
          ))}
        </div>
        <div className="challenge-strip"><span>CHALLENGE SPLIT</span><strong>8 / 8 passed</strong><i /> initial regression <b>0.875</b><i /> tenant scope gate <b>1.000</b><i /> vector calls <b>20 → 9</b></div>
      </section>

      <section className="model-benchmark shell" aria-labelledby="model-benchmark-title">
        <div className="benchmark-copy">
          <span>LOCAL EMBEDDING BENCHMARK</span>
          <h2 id="model-benchmark-title">模型更强，不等于每个配置都更好</h2>
          <p>同一语料与 Golden Dataset 下，Qwen3 4B 修复了中文语义改写短板；Query Instruction 改变了排序，却没有提升总分。</p>
          <div className="model-specs"><b>4.0B</b><span>Q4_K_M</span><span>2560 dimensions</span><span>Ollama / local</span></div>
        </div>
        <div className="benchmark-table">
          <div className="benchmark-row benchmark-head"><span>MODEL</span><span>HIT@5</span><span>MRR</span><span>RECALL</span></div>
          {localModels.map((model) => (
            <div className={`benchmark-row${model.best ? " best" : ""}`} key={model.name}>
              <span><strong>{model.name}</strong><small>{model.detail}</small></span>
              <b>{model.hit}</b><b>{model.mrr}</b><b>{model.recall}</b>
            </div>
          ))}
        </div>
      </section>

      <section className="embedding-lab shell" id="embedding-lab" aria-labelledby="embedding-lab-title">
        <div className="embedding-lab-heading">
          <div><span>LIVE EMBEDDING LAB</span><h2 id="embedding-lab-title">两段文字是怎样变成向量的？</h2></div>
          <p>调用本地 Go API，批量生成向量，再计算 cosine、dot product 和 Euclidean distance。默认 Hash 后端可离线运行，设置 Ollama 后切换到 Qwen3。</p>
        </div>
        <div className="embedding-workbench">
          <div className="embedding-inputs">
            <label><span>TEXT A / QUERY</span><textarea value={textA} onChange={(event) => setTextA(event.target.value)} /></label>
            <label><span>TEXT B / DOCUMENT</span><textarea value={textB} onChange={(event) => setTextB(event.target.value)} /></label>
            <div className="embedding-controls">
              <label><span>MODE</span><select value={embeddingMode} onChange={(event) => setEmbeddingMode(event.target.value)}><option value="symmetric">symmetric</option><option value="query_document">query_document</option></select></label>
              <label className="api-input"><span>API</span><input value={apiBase} onChange={(event) => setAPIBase(event.target.value)} /></label>
              <button onClick={compareEmbeddings} disabled={embeddingLoading}>{embeddingLoading ? "ENCODING…" : "生成向量并比较 →"}</button>
            </div>
            {embeddingError && <div className="embedding-error">连接失败：{embeddingError}<small>先运行：go run ./cmd/raglab serve-embedding</small></div>}
          </div>
          <div className="embedding-output">
            {similarity ? <>
              <div className="embedding-summary"><span>{similarity.embedder}</span><b>{similarity.dimensions} dimensions</b><em>{similarity.latency_ms.toFixed(2)} ms</em></div>
              <div className="similarity-score"><small>COSINE SIMILARITY</small><strong>{similarity.metrics.cosine_similarity.toFixed(6)}</strong><div><i style={{ width: `${Math.max(0, Math.min(1, similarity.metrics.cosine_similarity)) * 100}%` }} /></div></div>
              <div className="distance-grid"><div><span>DOT PRODUCT</span><b>{similarity.metrics.dot_product.toFixed(6)}</b></div><div><span>EUCLIDEAN</span><b>{similarity.metrics.euclidean_distance.toFixed(6)}</b></div></div>
              <div className="vector-preview"><span>VECTOR A · first {similarity.vector_a.preview.length}</span><code>[{formatVector(similarity.vector_a.preview)}]</code><small>L2 norm {similarity.vector_a.l2_norm.toFixed(6)} · range {similarity.vector_a.minimum.toFixed(5)}…{similarity.vector_a.maximum.toFixed(5)}</small></div>
              <div className="vector-preview"><span>VECTOR B · first {similarity.vector_b.preview.length}</span><code>[{formatVector(similarity.vector_b.preview)}]</code><small>L2 norm {similarity.vector_b.l2_norm.toFixed(6)} · range {similarity.vector_b.minimum.toFixed(5)}…{similarity.vector_b.maximum.toFixed(5)}</small></div>
              <p className="embedding-explanation">{similarity.explanation}</p>
            </> : <div className="embedding-placeholder"><span>01</span><p>输入两段文字</p><i /><span>02</span><p>Embedding → N 维浮点向量</p><i /><span>03</span><p>cos(A,B) = A·B / ‖A‖‖B‖</p></div>}
          </div>
        </div>
      </section>

      <section className="milvus-lab shell" id="milvus-lab" aria-labelledby="milvus-lab-title">
        <div className="milvus-heading">
          <div><span>LIVE VECTOR DATABASE</span><h2 id="milvus-lab-title">从 Query Embedding 到 Milvus Top-K</h2></div>
          <p>真实链路：Qwen3 将 Query 编码成 2560 维向量，Milvus 使用 HNSW + COSINE 做近似最近邻搜索，并在向量评分前执行元数据过滤。</p>
          <button className="status-button" onClick={refreshMilvus}>刷新服务状态</button>
        </div>
        <div className="milvus-pipeline" aria-label="Milvus vector search pipeline">
          <div><small>01 / QUERY</small><strong>自然语言问题</strong><span>{vectorQuery.slice(0, 24)}…</span></div><i>→</i>
          <div><small>02 / EMBED</small><strong>Qwen3 Embedding</strong><span>{milvusStatus?.dimensions || 2560} dimensions</span></div><i>→</i>
          <div className="active"><small>03 / ANN INDEX</small><strong>HNSW · COSINE</strong><span>M=16 · ef=64</span></div><i>→</i>
          <div><small>04 / FILTER</small><strong>Scalar Predicate</strong><span>tenant · role · product · status</span></div><i>→</i>
          <div><small>05 / OUTPUT</small><strong>Top-{vectorTopK} Chunks</strong><span>score + metadata + content</span></div>
        </div>
        <div className="milvus-proof-strip">
          <div><small>REPLACED</small><strong>O(N) in-memory scan</strong><span>→ Milvus Retriever Adapter</span></div>
          <div><small>REUSED</small><strong>RRF · Router · Rerank</strong><span>Context · Citation · Trace</span></div>
          <div><small>REGRESSION</small><strong>28 / 28 passed</strong><span>quality delta 0 · ACL violations 0</span></div>
          <div><small>PIPELINES</small><strong>v1 · v3 · v4 · v5</strong><span>same interface, real backend</span></div>
        </div>
        <div className="milvus-status-grid">
          <div><span>SERVICE</span><strong className={milvusStatus?.connected ? "online" : "muted"}>{milvusStatus?.connected ? "CONNECTED" : "NOT CHECKED"}</strong></div>
          <div><span>COLLECTION</span><strong>{milvusStatus?.collection || "raglab_chunks_qwen3"}</strong></div>
          <div><span>ROWS</span><strong>{milvusStatus?.row_count ?? "—"}</strong></div>
          <div><span>VECTOR</span><strong>{milvusStatus ? `${milvusStatus.dimensions}d` : "2560d"}</strong></div>
          <div><span>INDEX</span><strong>{milvusStatus ? `${milvusStatus.index_type} / ${milvusStatus.metric}` : "HNSW / COSINE"}</strong></div>
          <div><span>LOAD STATE</span><strong>{milvusStatus?.load_state || "—"}</strong></div>
        </div>
        <div className="milvus-workbench">
          <div className="milvus-query-panel">
            <label><span>QUERY</span><textarea value={vectorQuery} onChange={(event) => setVectorQuery(event.target.value)} /></label>
            <div className="milvus-filters">
              <label><span>TENANT</span><select value={vectorTenant} onChange={(event) => setVectorTenant(event.target.value)}><option value="public">public only</option><option value="tenant_a">tenant_a + public</option></select></label>
              <label><span>ROLE</span><select value={vectorRole} onChange={(event) => setVectorRole(event.target.value)}><option value="admin">admin</option><option value="viewer">viewer</option></select></label>
              <label><span>PRODUCT</span><select value={vectorProduct} onChange={(event) => setVectorProduct(event.target.value)}><option value="">all products</option><option value="identity">identity</option><option value="reports">reports</option><option value="storage">storage</option><option value="operations">operations</option><option value="api-gateway">api-gateway</option></select></label>
              <label><span>TOP-K</span><input type="number" min="1" max="20" value={vectorTopK} onChange={(event) => setVectorTopK(Number(event.target.value))} /></label>
            </div>
            <button className="vector-search-button" onClick={searchMilvus} disabled={milvusLoading}>{milvusLoading ? "EMBEDDING + SEARCHING…" : "执行真实向量检索 →"}</button>
            {milvusError && <div className="embedding-error">检索失败：{milvusError}<small>先执行 make milvus-up、make milvus-seed、make serve-lab</small></div>}
            <div className="milvus-lesson"><b>面试验证点</b><p>为什么要在 ANN 搜索前过滤？缺少 Role 为什么必须 fail closed？HNSW 的 M、efConstruction、ef 分别影响什么？如何用同一 Harness 证明替换 Retriever 没有质量退化？</p></div>
          </div>
          <div className="milvus-results">
            {vectorResult ? <>
              <div className="result-summary"><span>{vectorResult.hits.length} HITS</span><b>{vectorResult.total_latency_ms.toFixed(2)} ms total</b><em>embed {vectorResult.embedding_latency_ms.toFixed(2)} · search {vectorResult.search_latency_ms.toFixed(2)}</em></div>
              <code className="filter-code">{vectorResult.filter}</code>
              <div className="hit-list">
                {vectorResult.hits.map((hit, index) => <article key={hit.chunk_id}>
                  <div className="hit-rank"><span>#{index + 1}</span><strong>{hit.distance.toFixed(6)}</strong><small>COSINE</small></div>
                  <div className="hit-body"><div><strong>{hit.title}</strong><span>{hit.product} · v{hit.version} · {hit.tenant_id}</span></div><p>{hit.content}</p><code>{hit.chunk_id}</code></div>
                </article>)}
              </div>
            </> : <div className="milvus-placeholder"><span>VECTOR DATABASE CAPABILITIES</span><strong>建库 · 建索引 · Upsert · ANN Search · Scalar Filter</strong><p>点击“执行真实向量检索”后，这里会展示 Milvus 返回的原始相似度、Chunk 内容、元数据和分阶段耗时。</p></div>}
          </div>
        </div>
      </section>

      <section className="scale-lab shell" id="scale-lab" aria-labelledby="scale-lab-title">
        <div className="scale-heading">
          <div>
            <span>100K SCALE HARNESS · LIVE</span>
            <h2 id="scale-lab-title">亲手观察 HNSW 的“近似”意味着什么</h2>
          </div>
          <p>同一个查询分别访问 FLAT 精确索引与 HNSW 近似索引。调整 ef 和过滤范围，观察精确邻居重合、业务主题命中与延迟之间的真实取舍。</p>
          <button className="scale-status-button" onClick={refreshScaleStatus} disabled={scaleStatusLoading}>
            {scaleStatusLoading ? "CHECKING…" : "检查 100K 索引"}
          </button>
        </div>

        <div className="enterprise-auth">
          <div className="auth-story">
            <span>TRUSTED IDENTITY BOUNDARY</span>
            <strong>客户端不能决定自己是谁</strong>
            <p>本地演示由服务端签发HS256 JWT；生产环境应替换为企业OIDC/JWKS。检索接口只使用验签后的Claims，请求体里的Tenant与Role会被忽略。</p>
          </div>
          <div className="auth-flow" aria-label="Enterprise identity flow">
            <div className={authSession ? "done" : "active"}><small>01</small><b>Signed JWT</b><span>issuer · audience · exp</span></div>
            <i>→</i>
            <div className={authSession ? "done" : ""}><small>02</small><b>Verified Claims</b><span>subject · tenant · roles</span></div>
            <i>→</i>
            <div><small>03</small><b>Pre-ANN ACL</b><span>Milvus scalar filter</span></div>
            <i>→</i>
            <div><small>04</small><b>Audit Event</b><span>request · decision · latency</span></div>
          </div>
          <div className="persona-control">
            <label><span>DEMO PERSONA</span>
              <select value={authPersona} onChange={(event) => setAuthPersona(event.target.value)}>
                <option value="public_viewer">Public Viewer</option>
                <option value="tenant037_viewer">Tenant 037 · Viewer</option>
                <option value="tenant037_admin">Tenant 037 · Admin</option>
                <option value="platform_admin">Platform Admin</option>
              </select>
            </label>
            <button onClick={issueDevIdentity} disabled={authLoading}>{authLoading ? "SIGNING…" : authSession ? "切换并重新签发" : "签发演示 JWT"}</button>
          </div>
          <div className={authSession ? "verified-identity" : "verified-identity empty"}>
            {authSession ? <>
              <span>✓ SIGNATURE VERIFIED</span>
              <strong>{authSession.identity.subject}</strong>
              <code>tenant={authSession.identity.tenant_id}</code>
              <code>roles=[{authSession.identity.roles.join(", ")}]</code>
              <small>iss={authSession.identity.issuer} · aud={authSession.identity.audience} · exp={new Date(authSession.expires_at * 1000).toLocaleTimeString()}</small>
            </> : <>
              <span>NO TRUSTED IDENTITY</span>
              <strong>检索请求将返回 401</strong>
              <small>先选择一个服务端预定义身份并签发令牌。</small>
            </>}
          </div>
        </div>

        <div className="account-access-lab" id="dataset-isolation">
          <div className="account-panel">
            <span>LOCAL ACCOUNT EXPERIENCE</span>
            <h3>注册只创建新租户，不能自选已有租户</h3>
            <p>这一登录注册仅用于本地实验。密码只保存带盐派生值；生产环境关闭这两个接口，改由 OIDC/企业 IdP 完成账号、MFA 与组织邀请。</p>
            <label><span>EMAIL</span><input value={accountEmail} onChange={(event) => setAccountEmail(event.target.value)} /></label>
            <label><span>PASSWORD</span><input type="password" value={accountPassword} onChange={(event) => setAccountPassword(event.target.value)} /></label>
            <label><span>NEW ORGANIZATION</span><input value={accountOrganization} onChange={(event) => setAccountOrganization(event.target.value)} /></label>
            <div>
              <button onClick={() => accountAuthentication("login")} disabled={authLoading}>登录</button>
              <button className="secondary" onClick={() => accountAuthentication("register")} disabled={authLoading}>注册隔离租户</button>
            </div>
            <small>DEMO A · alice@tenant-a.local / RagLab-Alice-2026!</small>
            <small>DEMO B · bob@tenant-b.local / RagLab-Bob-2026!</small>
            {authError && <div className="dataset-denied">{authError}</div>}
          </div>

          <div className="dataset-panel">
            <div className="dataset-panel-head">
              <div><span>POSTGRESQL CONTROL PLANE</span><h3>当前身份能看到哪些数据集？</h3></div>
              <button onClick={() => loadDatasets()} disabled={!authSession}>刷新授权目录</button>
            </div>
            <div className="control-plane-metrics">
              <div><span>BACKEND</span><strong>{controlPlane?.backend || "—"}</strong><small>{controlPlane?.connected ? "CONNECTED" : "NOT CHECKED"}</small></div>
              <div><span>TENANTS</span><strong>{controlPlane?.tenants ?? "—"}</strong><small>control-plane rows</small></div>
              <div><span>MEMBERSHIPS</span><strong>{controlPlane?.memberships ?? "—"}</strong><small>{memberships.length} in current tenant</small></div>
              <div><span>DATASETS</span><strong>{controlPlane?.datasets ?? "—"}</strong><small>public + tenant</small></div>
            </div>
            <div className="membership-strip">
              <span>TRUSTED MEMBERSHIP</span>
              {memberships.length ? memberships.map((membership) => <code key={`${membership.tenant_id}:${membership.subject}`}>
                {membership.subject} → {membership.tenant_id} / {membership.role} / {membership.status}
              </code>) : <code>登录后由可信 Claims 首次登记，撤权后不会被请求自动恢复。</code>}
            </div>
            <div className="dataset-cards">
              {datasets.length ? datasets.map((dataset) => <button
                key={dataset.id}
                className={datasetID === dataset.id ? "selected" : ""}
                onClick={() => setDatasetID(dataset.id)}
              >
                <span>{dataset.visibility === "public" ? "PUBLIC" : "TENANT ONLY"}</span>
                <strong>{dataset.name}</strong>
                <p>{dataset.description}</p>
                <code>{dataset.id}</code>
              </button>) : <p>登录后，服务端只返回当前 Claims 可以访问的数据集目录。</p>}
            </div>
            <div className="dataset-create">
              <div><span>TENANT ADMIN MUTATION</span><strong>创建持久化数据集</strong><small>服务端强制 owner_tenant，客户端不能替换归属。</small></div>
              <label><span>NAME</span><input value={newDatasetName} onChange={(event) => setNewDatasetName(event.target.value)} /></label>
              <label><span>SLUG</span><input value={newDatasetSlug} onChange={(event) => setNewDatasetSlug(event.target.value)} /></label>
              <label><span>DESCRIPTION</span><input value={newDatasetDescription} onChange={(event) => setNewDatasetDescription(event.target.value)} /></label>
              <button onClick={createDataset} disabled={datasetLoading || !authSession}>创建并写入 PostgreSQL</button>
            </div>
            <div className="dataset-query">
              <label><span>DATASET RESOURCE ID</span><input value={datasetID} onChange={(event) => setDatasetID(event.target.value)} /></label>
              <label><span>QUERY</span><input value={datasetQuery} onChange={(event) => setDatasetQuery(event.target.value)} /></label>
              <button onClick={() => searchDataset()} disabled={datasetLoading}>{datasetLoading ? "AUTHORIZING…" : "授权并检索"}</button>
              <button className="danger" onClick={() => searchDataset(authSession?.identity.tenant_id === "tenant_a" ? "tenant-b-operations" : "tenant-a-operations")} disabled={datasetLoading || !authSession}>模拟跨租户越权</button>
            </div>
            {datasetError && <div className="dataset-denied"><b>DENIED / FAILED CLOSED</b><span>{datasetError}</span><small>不存在与无权限统一返回 404；被拒绝请求不会抵达 Milvus。</small></div>}
            {datasetResult && <div className="dataset-proof">
              <div><span>✓ RESOURCE GRANT</span><strong>{datasetResult.dataset.name}</strong><small>{datasetResult.dataset.visibility} · {datasetResult.result.hits.length} hits</small></div>
              <code>{datasetResult.result.filter}</code>
              {datasetResult.result.hits.map((hit) => <article key={hit.chunk_id}>
                <b>{hit.title}</b><p>{hit.content}</p><small>{hit.tenant_id} · {hit.visibility} · {hit.distance.toFixed(5)}</small>
              </article>)}
            </div>}
          </div>
        </div>

        <div className="answer-lab" id="answer-lab">
          <div className="answer-lab-heading">
            <div><span>GROUNDED ANSWER · SSE LIVE</span><h3>从检索证据到带引用的回答</h3></div>
            <p>同一个数据集授权边界下，服务端先检索，再执行安全门禁，最后生成结构化回答。事件时间线用于观察 TTFE、生成耗时、拒答原因和引用闭环。</p>
            <div className="answer-actions">
              <button onClick={streamDatasetAnswer} disabled={answerLoading || !authSession}>{answerLoading ? "STREAMING…" : "流式生成 Grounded Answer →"}</button>
              {answerLoading && <button className="secondary" onClick={cancelDatasetAnswer}>取消生成</button>}
            </div>
          </div>
          {answerError && <div className="answer-error"><b>ANSWER STREAM</b><span>{answerError}</span><small>跨租户资源会在进入流式响应前返回统一 404。</small></div>}
          <div className="answer-timeline">
            <div className="answer-event-list">
              <span>EVENT TIMELINE</span>
              {answerEvents.length ? answerEvents.map((event, index) => <article key={`${event.type}-${index}`} className={event.type === "error" ? "error" : event.type === "completed" ? "completed" : ""}>
                <b>{event.type.toUpperCase()}</b><code>{event.elapsed_ms.toFixed(1)} ms</code>
                {event.search && <small>{event.search.hits} hits · embed {event.search.embedding_latency_ms.toFixed(1)} · search {event.search.search_latency_ms.toFixed(1)} ms</small>}
                {event.generation && <small>{event.generation.model || event.generation.generator} · {event.generation.latency_ms.toFixed(1)} ms</small>}
                {event.error && <small>{event.error}</small>}
              </article>) : <p>登录后选择一个数据集，点击上方按钮观察 SSE 事件。</p>}
            </div>
            <div className="answer-output">
              {answerResult ? <>
                <div className="answer-result-head">
                  <div><span>{answerResult.result.answerable ? "✓ GROUNDED" : "○ REFUSED"}</span><strong>{answerResult.dataset.name}</strong><small>{answerResult.result.refusal_reason || "citations verified"}</small></div>
                  <div><span>TTFT / TOTAL</span><strong>{answerResult.result.generation.ttft_ms ? `${answerResult.result.generation.ttft_ms.toFixed(0)} / ${answerResult.result.generation.latency_ms.toFixed(0)} ms` : `${answerResult.result.generation.latency_ms.toFixed(0)} ms`}</strong><small>{answerResult.result.generation.model || answerResult.result.generation.generator}</small></div>
                  <div><span>TOKENS</span><strong>{answerResult.result.generation.prompt_tokens} / {answerResult.result.generation.output_tokens}</strong><small>prompt / output</small></div>
                </div>
                <div className="answer-copy"><p>{answerResult.result.answer}</p></div>
                {answerResult.result.generation.safety_adjustments?.length ? <div className="answer-safety"><b>SAFETY ADJUSTMENTS</b>{answerResult.result.generation.safety_adjustments.map((item) => <code key={item}>{item}</code>)}</div> : null}
                <div className="answer-citations">
                  <span>SERVER-VERIFIED CITATIONS · {answerResult.result.citations.length}</span>
                  {answerResult.result.citations.length ? answerResult.result.citations.map((citation) => <article key={citation.chunk_id}><b>{citation.document}</b><code>{citation.document_id} · {citation.chunk_id}</code><p>{citation.excerpt}</p></article>) : <small>拒答结果不携带证据引用。</small>}
                </div>
              </> : answerStreamPreview ? <div className="answer-stream-preview"><span>LIVE ANSWER DELTA · 已通过 SSE 收到模型增量</span><p>{answerStreamPreview}<i className={answerLoading ? "typing-caret" : ""} /></p><small>完整 JSON 收齐后，服务端才会提交 Citation 和安全契约校验。</small></div> : <div className="answer-placeholder"><span>SEARCH → SAFETY GATE → GENERATION → CITATION</span><strong>最终答案会显示在这里</strong><p>服务端只接受检索结果中的 Chunk 引用；模型输出的引用会再次与已选上下文比对，无法引用上下文外的文档。</p></div>}
            </div>
          </div>
        </div>

        <div className="lifecycle-lab" id="lifecycle-lab">
          <div className="lifecycle-intro">
            <span>INCREMENTAL KNOWLEDGE LIFECYCLE</span>
            <h3>写入、更新、删除都要能证明一致</h3>
            <p>使用稳定Chunk ID做幂等Upsert；新版本写入后删除陈旧Chunk；删除后再次Query确认零残留。Event ID防重复，Source Revision防乱序，Embedding Version防止不同向量空间混写。</p>
            <button onClick={refreshLifecycleStatus}>读取生命周期状态</button>
          </div>
          <div className="lifecycle-controls">
            <div className="lifecycle-fields">
              <label><span>DOCUMENT ID</span><input value={lifecycleDocumentID} onChange={(event) => setLifecycleDocumentID(event.target.value)} /></label>
              <label><span>SOURCE REVISION</span><input type="number" min="1" value={lifecycleRevision} onChange={(event) => setLifecycleRevision(Number(event.target.value))} /></label>
              <label><span>DOCUMENT VERSION</span><input value={lifecycleVersion} onChange={(event) => setLifecycleVersion(event.target.value)} /></label>
            </div>
            <label><span>MARKDOWN CONTENT</span><textarea value={lifecycleContent} onChange={(event) => setLifecycleContent(event.target.value)} /></label>
            <div className="lifecycle-actions">
              <button onClick={() => applyLifecycle("upsert")} disabled={lifecycleLoading}>UPSERT 当前版本</button>
              <button className="danger" onClick={() => applyLifecycle("delete")} disabled={lifecycleLoading}>DELETE 并验证</button>
            </div>
            <div className="lifecycle-search-control">
              <input value={lifecycleQuery} onChange={(event) => setLifecycleQuery(event.target.value)} />
              <button onClick={searchLifecycle} disabled={lifecycleLoading}>检索 Active Alias</button>
            </div>
            {lifecycleError && <div className="scale-error"><b>LIFECYCLE ERROR</b><span>{lifecycleError}</span><small>变更接口仅允许Platform Admin。发生Revision冲突时请使用更大的版本号。</small></div>}
          </div>
          <div className="lifecycle-output">
            <div className="lifecycle-metrics">
              <div><span>COLLECTION</span><strong>{lifecycleStatus?.collection || "raglab_lifecycle_v1"}</strong></div>
              <div><span>ACTIVE ALIAS</span><strong>{lifecycleStatus?.alias || "raglab_knowledge_active"}</strong></div>
              <div><span>EMBEDDING VERSION</span><strong>{lifecycleStatus?.embedding_version || "qwen3…v1"}</strong></div>
              <div><span>EVENTS / PENDING</span><strong>{lifecycleStatus ? `${lifecycleStatus.events} / ${lifecycleStatus.pending_events}` : "— / —"}</strong></div>
            </div>
            {lifecycleResult ? <article className="lifecycle-receipt">
              <span>{lifecycleResult.verified ? "✓ POST-MUTATION VERIFIED" : "NOT VERIFIED"}</span>
              <strong>{lifecycleResult.operation.toUpperCase()} · revision {lifecycleResult.source_revision}</strong>
              <code>{lifecycleResult.event_id}</code>
              <div><b>{lifecycleResult.previous_chunks}</b><small>BEFORE</small><i>→</i><b>{lifecycleResult.current_chunks}</b><small>AFTER</small><em>deleted {lifecycleResult.deleted_chunks}</em></div>
              <p>{lifecycleResult.duplicate ? "相同Event ID被识别为重复投递，没有重复写入。" : "变更已写入Milvus，并通过Strong Query完成结果核对。"}</p>
            </article> : <div className="lifecycle-placeholder">先使用Platform Admin签发JWT，再写入revision 1。修改正文后写入revision 2，观察旧Chunk被清除；最后Delete并检索，验证文档不会残留。</div>}
            {lifecycleSearch && <div className="lifecycle-hits">
              <span>ALIAS SEARCH · {lifecycleSearch.hits.length} HITS</span>
              <code>{lifecycleSearch.filter}</code>
              {lifecycleSearch.hits.map((hit) => <article key={hit.chunk_id}><b>{hit.title}</b><p>{hit.content}</p><small>{hit.chunk_id} · {hit.distance.toFixed(5)}</small></article>)}
            </div>}
          </div>
        </div>

        <div className="ingestion-board" id="ingestion-jobs">
          <div className="ingestion-heading">
            <div>
              <span>ASYNCHRONOUS INGESTION CONTROL PLANE</span>
              <h3>每次导入都必须可追踪、可重试、可取消</h3>
              <p>任务阶段由真实执行链路上报：Validating → Chunking → Embedding → Indexing → Verifying。相同幂等键不会重复执行，失败任务受最大尝试次数保护，服务重启后会恢复排队任务。</p>
            </div>
            <div>
              <button onClick={submitIngestionJob} disabled={ingestionLoading}>创建异步 UPSERT</button>
              <button className="secondary" onClick={refreshIngestionJobs} disabled={ingestionLoading}>刷新任务</button>
            </div>
          </div>
          <div className="ingestion-metrics">
            <div><span>TOTAL</span><strong>{ingestionSummary?.total ?? "—"}</strong></div>
            <div><span>QUEUED / RUNNING</span><strong>{ingestionSummary ? `${ingestionSummary.queued} / ${ingestionSummary.running}` : "— / —"}</strong></div>
            <div><span>COMPLETED</span><strong>{ingestionSummary?.completed ?? "—"}</strong></div>
            <div><span>FAILED / CANCELLED</span><strong>{ingestionSummary ? `${ingestionSummary.failed} / ${ingestionSummary.cancelled}` : "— / —"}</strong></div>
          </div>
          {ingestionError && <div className="scale-error"><b>INGESTION ERROR</b><span>{ingestionError}</span><small>任务管理接口仅允许 Platform Admin；同一个 Idempotency Key 不能绑定不同载荷。</small></div>}
          <div className="ingestion-jobs">
            {ingestionSummary?.jobs.length ? ingestionSummary.jobs.slice(0, 8).map((job) => <article key={job.job_id}>
              <div className="job-status">
                <span className={`job-dot ${job.status}`} />
                <div><strong>{job.status.toUpperCase()}</strong><small>{job.stage}</small></div>
                <code>{job.job_id}</code>
              </div>
              <div className="job-progress">
                {ingestionStages.map((stage) => <i key={stage} className={job.status === "completed" || ingestionStages.indexOf(stage) <= ingestionStages.indexOf(job.stage) ? "active" : ""} title={stage} />)}
              </div>
              <div className="job-meta">
                <span>attempt {job.attempts}/{job.max_attempts}</span>
                <span>{new Date(job.updated_at).toLocaleTimeString()}</span>
                {job.result && <span>{job.result.current_chunks} chunks · verified</span>}
              </div>
              {job.last_error && <p>{job.last_error}</p>}
              <div className="job-actions">
                {(job.status === "failed" || job.status === "cancelled") && job.attempts < job.max_attempts && <button onClick={() => mutateIngestionJob(job.job_id, "retry")}>RETRY</button>}
                {(job.status === "queued" || job.status === "running") && <button className="danger" onClick={() => mutateIngestionJob(job.job_id, "cancel")}>CANCEL</button>}
              </div>
            </article>) : <div className="ingestion-empty">使用上方相同文档内容创建异步任务，这里将展示任务阶段、尝试次数、结果与错误。刷新不会重新执行任务。</div>}
          </div>
        </div>

        <div className="scale-health">
          <div><small>DATASET</small><strong>{scaleStatus ? scaleStatus.dataset.chunks.toLocaleString() : "100,000"}</strong><span>chunks · {scaleStatus?.dataset.profile || "hard-v2"}</span></div>
          <div><small>DIMENSIONS</small><strong>{scaleStatus?.dataset.dimensions || 1024}</strong><span>normalized float vectors</span></div>
          <div><small>HNSW INDEXED</small><strong>{scaleStatus ? scaleStatus.hnsw.indexed_rows.toLocaleString() : "—"}</strong><span>pending {scaleStatus?.hnsw.pending_rows ?? "—"}</span></div>
          <div><small>INDEX STATE</small><strong className={scaleStatus?.hnsw.state === "Finished" ? "healthy" : ""}>{scaleStatus?.hnsw.state || "NOT CHECKED"}</strong><span>{scaleStatus ? `${scaleStatus.hnsw.index_type} / ${scaleStatus.hnsw.metric}` : "HNSW / COSINE"}</span></div>
          <div><small>BUILD PARAMS</small><strong>M={scaleStatus?.hnsw.parameters?.M || "8"}</strong><span>efConstruction={scaleStatus?.hnsw.parameters?.efConstruction || "160"}</span></div>
        </div>

        <div className="index-compare">
          <article>
            <div><span>GROUND TRUTH</span><b>FLAT</b></div>
            <strong>{scaleStatus ? scaleStatus.flat.rows.toLocaleString() : "100,000"} rows</strong>
            <p>穷举计算所有满足 Filter 的向量，得到精确 Top-K。实验对照使用，不建议作为大规模在线索引。</p>
          </article>
          <i>VERSUS</i>
          <article className="approximate">
            <div><span>ONLINE CANDIDATE</span><b>HNSW</b></div>
            <strong>{scaleStatus ? scaleStatus.hnsw.rows.toLocaleString() : "100,000"} rows</strong>
            <p>通过多层近邻图缩小搜索范围。ef 越大，访问节点更多，通常召回更高、计算成本也更高。</p>
          </article>
        </div>

        <div className="scale-workbench">
          <aside className="scale-controls">
            <div className="control-heading"><span>QUERY CONTROLS</span><b>固定 Seed，可重复实验</b></div>
            <label>
              <span>TOPIC / 0—999</span>
              <div className="topic-control">
                <input type="number" min="0" max="999" value={scaleTopic} onChange={(event) => setScaleTopic(Number(event.target.value))} />
                <button onClick={() => setScaleTopic((scaleTopic + 137) % 1000)}>换一个主题</button>
              </div>
            </label>
            <label>
              <span>FILTER SCENARIO</span>
              <select value={scaleScenario} onChange={(event) => setScaleScenario(event.target.value)}>
                <option value="active_all">Active · 全部租户</option>
                <option value="public_active">Public + Active</option>
                <option value="tenant_admin_active">Tenant Admin + Public</option>
              </select>
            </label>
            <label>
              <span>TOP-K</span>
              <input type="number" min="1" max="20" value={scaleTopK} onChange={(event) => setScaleTopK(Number(event.target.value))} />
            </label>
            <fieldset>
              <legend>SEARCH EF</legend>
              <div className="ef-options">
                {[16, 32, 64, 128].map((value) => (
                  <button className={scaleEF === value ? "selected" : ""} key={value} onClick={() => setScaleEF(value)}>
                    <span>ef</span>{value}
                  </button>
                ))}
              </div>
            </fieldset>
            <button className="scale-search-button" onClick={searchScale} disabled={scaleLoading}>
              {scaleLoading ? "COMPARING FLAT + HNSW…" : "运行双索引对照 →"}
            </button>
            {scaleError && <div className="scale-error"><b>实验暂不可用</b><span>{scaleError}</span><small>确认 Milvus 与 serve-lab 已启动，100K Collection 没有被删除。</small></div>}
            <div className="ef-explainer">
              <b>你正在改变什么？</b>
              <p><code>M / efConstruction</code> 是建库参数；这里的 <code>ef</code> 是查询参数。提高 ef 不会重建索引，适合按查询风险动态调整。</p>
            </div>
          </aside>

          <div className="scale-output">
            {scaleResult ? <>
              <div className="scale-result-head">
                <div><span>TOPIC</span><strong>{scaleResult.topic}</strong><small>{scaleResult.tenant}</small></div>
                <div><span>EXACT RECALL@{scaleResult.top_k}</span><strong>{scaleResult.exact_recall_at_k.toFixed(3)}</strong><small>HNSW ∩ FLAT</small></div>
                <div><span>TOPIC PRECISION</span><strong>{scaleResult.topic_precision_at_k.toFixed(3)}</strong><small>business relevance</small></div>
                <div><span>HNSW LATENCY</span><strong>{scaleResult.hnsw_latency_ms.toFixed(2)}</strong><small>ms · ef={scaleResult.ef}</small></div>
              </div>
              {lastRequestID && <div className="request-id"><span>AUDIT REQUEST ID</span><code>{lastRequestID}</code></div>}
              <div className="scale-query-proof">
                <div><span>QUERY VECTOR · first 8 / L2 {scaleResult.query_l2_norm.toFixed(4)}</span><code>[{formatVector(scaleResult.query_vector_preview)}]</code></div>
                <code>{scaleResult.filter}</code>
              </div>
              <div className="scale-legend">
                <span><i className="exact-dot" /> 同时属于 FLAT Top-K</span>
                <span><i className="topic-dot" /> 同主题但不是同一 Chunk</span>
                <b>FLAT {scaleResult.flat_latency_ms.toFixed(2)}ms · TOTAL {scaleResult.total_latency_ms.toFixed(2)}ms</b>
              </div>
              <div className="scale-hit-list">
                {scaleResult.hits.map((hit) => <article key={hit.chunk_id}>
                  <div className="scale-rank"><span>#{hit.rank}</span><strong>{hit.distance.toFixed(5)}</strong></div>
                  <div className="scale-hit-copy">
                    <div><strong>{hit.title}</strong><span>{hit.tenant_id} · {hit.visibility} · {hit.status}</span></div>
                    <p>{hit.content}</p>
                    <code>{hit.chunk_id}</code>
                  </div>
                  <div className="match-badges">
                    {hit.in_exact_top_k && <span className="exact">EXACT</span>}
                    {!hit.in_exact_top_k && hit.expected_topic && <span className="topic">SAME TOPIC</span>}
                  </div>
                </article>)}
              </div>
              <details className="ground-truth">
                <summary>查看 FLAT Ground Truth Top-{scaleResult.top_k}</summary>
                <div>{scaleResult.exact_top_k.map((id) => <code key={id}>{id}</code>)}</div>
              </details>
            </> : <div className="scale-placeholder">
              <span>FLAT GROUND TRUTH × HNSW APPROXIMATE SEARCH</span>
              <strong>选择同一个 Topic，先把 ef 从 16 调到 128。</strong>
              <p>观察 Exact Recall 是否提高、Topic Precision 是否已经足够，以及更大的搜索范围是否值得额外延迟。这就是向量索引选参的可视化实验。</p>
              <div><b>01</b><i /><b>16</b><i /><b>32</b><i /><b>64</b><i /><b>128</b></div>
            </div>}
          </div>
        </div>
        <div className="audit-console">
          <div>
            <span>SECURITY AUDIT TRAIL</span>
            <strong>每次认证决策都有 Request ID</strong>
            <p>只有Platform Admin能读取跨租户审计事件。Tenant级管理员无权查看全局日志。</p>
          </div>
          <button onClick={loadAuditEvents} disabled={!authSession}>读取最近审计事件</button>
          <div className="audit-list">
            {auditEvents.length > 0 ? auditEvents.slice(0, 8).map((event) => <article key={event.request_id}>
              <span className={event.decision}>{event.status} · {event.decision.toUpperCase()}</span>
              <code>{event.method} {event.path}</code>
              <b>{event.subject || "anonymous"} · {event.tenant_id || "no tenant"}</b>
              <small>{event.request_id} · {event.duration_ms.toFixed(2)}ms</small>
            </article>) : <p>使用Platform Admin身份签发JWT，然后执行检索并读取日志。</p>}
          </div>
        </div>
      </section>

      <section className="experiment-wrap" id="experiment">
        <div className="section shell">
          <div className="section-heading light">
            <div><span className="section-index">02</span><p>CONTROLLED EXPERIMENT</p></div>
            <h2>只增加 Metadata Filter，发生了什么？</h2>
            <p>语料、切块、BM25 参数、ACL 与评测协议保持不变。</p>
          </div>

          <div className="metric-panel">
            <div className="metric-legend"><span><i className="before-dot" /> V0 Keyword</span><span><i className="after-dot" /> V2 Metadata</span></div>
            <div className="metrics-list">
              {metrics.map((metric) => (
                <div className="metric-row" key={metric.label}>
                  <div className="metric-label"><span>{metric.label}</span><em>{metric.delta}</em></div>
                  <div className="bars">
                    <div className="bar before" style={{ width: `${metric.before * 100}%` }}><span>{metric.before.toFixed(3)}</span></div>
                    <div className="bar after" style={{ width: `${metric.after * 100}%` }}><span>{metric.after.toFixed(3)}</span></div>
                  </div>
                </div>
              ))}
              <div className="metric-row violation-row">
                <div className="metric-label"><span>Metadata 污染</span><em>−41</em></div>
                <div className="violation-visual"><div><span>41</span>{Array.from({ length: 16 }).map((_, i) => <i key={i} />)}</div><span className="arrow">→</span><div className="zero-ring">0<small>violations</small></div></div>
              </div>
            </div>
          </div>

          <div className="case-lab">
            <div className="case-header">
              <div><span>FAILURE LAB</span><strong>{current.id}</strong></div>
              <div className="case-tabs" role="tablist" aria-label="Failure cases">
                <button className={activeCase === "version" ? "selected" : ""} onClick={() => setActiveCase("version")}>版本污染</button>
                <button className={activeCase === "access" ? "selected" : ""} onClick={() => setActiveCase("access")}>权限与拒答</button>
              </div>
            </div>
            <div className="query-box"><span>QUERY</span><p>{current.query}</p></div>
            <div className="case-comparison">
              <article className="answer-card bad">
                <div className="answer-top"><span>BEFORE · V0</span><em>{current.before.tag}</em></div>
                <small>SOURCE</small><h3>{current.before.source}</h3>
                <blockquote>{current.before.answer}</blockquote>
                <div className="answer-meta">× {current.before.meta}</div>
              </article>
              <div className="transform-arrow"><span>filter</span>→</div>
              <article className="answer-card good">
                <div className="answer-top"><span>AFTER · V2</span><em>{current.after.tag}</em></div>
                <small>SOURCE</small><h3>{current.after.source}</h3>
                <blockquote>{current.after.answer}</blockquote>
                <div className="answer-meta">✓ {current.after.meta}</div>
              </article>
            </div>
          </div>
        </div>
      </section>

      <section className="section shell" id="architecture">
        <div className="section-heading">
          <div><span className="section-index">03</span><p>SYSTEM DESIGN</p></div>
          <h2>可替换、可追踪的 Pipeline</h2>
          <p>组件用小接口解耦，异构分数通过 RRF 排名融合，外部模型不影响离线基线复现。</p>
        </div>
        <div className="pipeline-map">
          <div className="pipe-node input"><small>INPUT</small><strong>Query + Context</strong><span>tenant · role · product · version</span></div>
          <i>→</i>
          <div className="pipe-node focus"><small>GUARD</small><strong>Metadata + ACL</strong><span>filter before scoring</span></div>
          <i>→</i>
          <div className="pipe-stack">
            <div className="pipe-node"><small>RETRIEVER</small><strong>BM25</strong></div>
            <div className="pipe-node"><small>RETRIEVER</small><strong>Milvus HNSW</strong></div>
          </div>
          <i>→</i>
          <div className="pipe-node"><small>CONTEXT</small><strong>Rank + Pack</strong><span>Top-K · citation map</span></div>
          <i>→</i>
          <div className="pipe-node output"><small>OUTPUT</small><strong>Answer + Trace</strong><span>evidence or refusal</span></div>
        </div>
        <div className="architecture-notes">
          <span><b>01</b> ACL fail closed</span>
          <span><b>02</b> Stable Chunk IDs</span>
          <span><b>03</b> Content-addressed cache</span>
          <span><b>04</b> External model timeout</span>
        </div>
      </section>

      <section className="harness-section" id="harness">
        <div className="section shell">
          <div className="section-heading light">
            <div><span className="section-index">04</span><p>EVALUATION HARNESS</p></div>
            <h2>优化必须通过回归门禁</h2>
            <p>总体分数之外，单独追踪失败类别、安全边界和知识有效性。</p>
          </div>
          <div className="harness-grid">
            <article><span>QUALITY</span><strong>Hit Rate · MRR · Recall</strong><p>正确证据是否出现，以及是否足够靠前。</p><small>3 deterministic metrics</small></article>
            <article><span>SAFETY</span><strong>Unauthorized Retrievals</strong><p>跨租户召回属于零容忍回归项。</p><small className="green">0 violations</small></article>
            <article><span>VALIDITY</span><strong>Metadata Violations</strong><p>权限合法不代表产品、版本和状态正确。</p><small className="green">41 → 0</small></article>
            <article><span>REPRODUCIBILITY</span><strong>Golden Dataset + CI</strong><p>固定语料、配置与查询分类，可重复运行。</p><small className="green">all checks passed</small></article>
          </div>
          <div className="test-strip"><span className="live-dot" /> <strong>main / CI</strong><i /> formatting <b>✓</b><i /> unit tests <b>✓</b><i /> dataset validation <b>✓</b><i /> baseline eval <b>✓</b><em>29s</em></div>
        </div>
      </section>

      <section className="next shell">
        <div><span>NEXT ITERATION</span><h2>从“检索策略正确”<br />走向“上下文精度可控”</h2></div>
        <div className="next-list">
          <p><b>01</b><span>Cross-encoder Reranker</span><em>V5</em></p>
          <p><b>02</b><span>Context Token Budget</span><em>PACKING</em></p>
          <p><b>03</b><span>Citation Coverage</span><em>GROUNDING</em></p>
          <p><b>04</b><span>60-query Golden Set</span><em>DATA</em></p>
        </div>
      </section>

      <footer className="footer shell">
        <div className="brand"><span className="brand-mark">R/</span><span>RAG Evolution Lab</span></div>
        <p>Build failures. Measure changes. Keep the evidence.</p>
        <a href="https://github.com/dingpuyu/rag-evolution-lab" target="_blank" rel="noreferrer">View source ↗</a>
      </footer>
    </main>
  );
}
