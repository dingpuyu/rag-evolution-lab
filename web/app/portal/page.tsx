"use client";

import { FormEvent, useEffect, useMemo, useRef, useState } from "react";

type Identity = {
  subject: string;
  tenant_id: string;
  roles: string[];
  expires_at?: number;
};
type Session = { access_token: string; token_type: string; expires_at: number; identity: Identity };
type Dataset = {
  id: string;
  name: string;
  description: string;
  visibility: "public" | "tenant";
  owner_tenant?: string;
  allowed_roles?: string[];
  status: string;
};
type AgentApplication = { app_id: string; tenant_id: string; name: string; slug: string; description: string; status: string };
type AppEnvironment = { environment_id: string; app_id: string; name: string; config_version: string; status: string };
type GatewayBinding = { dataset_id: string; dataset_name: string; purpose?: string; priority: number; hits: number; index_version?: string; index_collection?: string; rewrite?: { applied: boolean; query: string; rewriter?: string }; rerank?: { applied: boolean; model?: string; candidates: number }; policy: { top_k: number; candidate_k: number; rerank: boolean; query_rewrite: boolean; token_budget: number } };
type IngestionJob = {
  job_id: string;
  idempotency_key: string;
  tenant_id?: string;
  dataset_id?: string;
  status: "queued" | "running" | "completed" | "failed" | "cancelled";
  stage: string;
  attempts: number;
  max_attempts: number;
  cancel_requested: boolean;
  last_error?: string;
  created_at: string;
  updated_at: string;
  started_at?: string;
  completed_at?: string;
  worker_id?: string;
  lease_expires_at?: string;
  last_heartbeat_at?: string;
  result?: { current_chunks: number; verified: boolean; embedding_version?: string };
};
type IngestionSummary = { total: number; queued: number; running: number; completed: number; failed: number; cancelled: number; jobs: IngestionJob[] };
type SearchHit = {
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
};
type ChunkPreview = { id: string; parent_id: string; parent_sequence: number; source_page: number; sequence: number; heading_path?: string[]; content: string; parent_content: string };
type ChunkPreviewResponse = { chunker_version: string; max_runes: number; overlap_runes: number; parent_count: number; child_count: number; pages: number[]; chunks: ChunkPreview[] };
type AnswerResponse = {
  answerable: boolean;
  answer: string;
  refusal_reason?: string;
  citations: Array<{ chunk_id: string; document_id: string; document: string; excerpt: string }>;
  search: { hits: SearchHit[]; total_latency_ms: number; embedding_latency_ms: number; search_latency_ms: number; filter: string };
  generation: { generator: string; model: string; prompt_version: string; latency_ms: number; ttft_ms?: number; token_rate_tps?: number; prompt_tokens: number; output_tokens: number };
};
type GatewayAnswerResponse = { app_id: string; environment_id: string; trace_id?: string; bindings: GatewayBinding[]; result: AnswerResponse };
type ChatMessage = { id: string; role: "user" | "assistant"; text: string; response?: AnswerResponse; pending?: boolean };
type Membership = { tenant_id: string; subject: string; role: string; status: string };
type AuditEvent = { timestamp: string; subject?: string; tenant_id?: string; method: string; path: string; decision: string; status: number; reason?: string };
type IndexBuild = { build_id: string; idempotency_key: string; app_id: string; environment_id: string; version: string; collection: string; status: string; stage: string; attempts: number; last_error?: string; manifest?: { row_count: number; dimensions: number; schema_hash: string; manifest_hash: string; embedding_model: string; embedding_version: string; validated_at: string } };
type IndexRelease = { release_id: string; version: string; collection: string; state: string; channel: string; rollout_percent: number; published_at: string };
type ApplicationCredential = { credential_id: string; app_id: string; name: string; scopes: string[]; status: string; created_by: string; created_at: string; last_used_at?: string };
type RuntimeTrace = { trace_id: string; app_id: string; environment_id: string; status: string; index_version?: string; index_collection?: string; embedding_model?: string; generator?: string; model?: string; hit_count: number; candidate_count: number; rerank_applied: boolean; rewrite_applied: boolean; total_ms: number; prompt_tokens: number; output_tokens: number; total_cost_usd: number; started_at: string };

const API_BASE = process.env.NEXT_PUBLIC_API_BASE ?? "http://127.0.0.1:8080";
const DEMOS = [
  { label: "平台管理员", email: "admin@raglab.local", password: "change-this-admin-password" },
  { label: "Tenant A 管理员", email: "alice@tenant-a.local", password: "RagLab-Alice-2026!" },
  { label: "Tenant B 管理员", email: "bob@tenant-b.local", password: "RagLab-Bob-2026!" },
];

function uid(prefix: string) {
  return `${prefix}-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
}

async function parseError(response: Response) {
  try {
    const body = await response.json();
    return body?.error?.message || body?.message || `请求失败（${response.status}）`;
  } catch {
    return `请求失败（${response.status}）`;
  }
}

export default function CustomerPortal() {
  const [session, setSession] = useState<Session | null>(null);
  const [booting, setBooting] = useState(true);
  const [authMode, setAuthMode] = useState<"login" | "register">("login");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [organization, setOrganization] = useState("我的团队");
  const [authError, setAuthError] = useState("");
  const [view, setView] = useState<"chat" | "knowledge" | "ingest" | "access" | "apps" | "runtime">("chat");
  const [datasets, setDatasets] = useState<Dataset[]>([]);
  const [selectedDataset, setSelectedDataset] = useState("");
  const [query, setQuery] = useState("");
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [streaming, setStreaming] = useState(false);
  const [searching, setSearching] = useState(false);
  const [searchHits, setSearchHits] = useState<SearchHit[]>([]);
  const [notice, setNotice] = useState("");
  const [newDataset, setNewDataset] = useState({ name: "", slug: "", description: "", visibility: "tenant", allowed_roles: ["admin"] });
  const [document, setDocument] = useState({ document_id: "", title: "", content: "", version: "v1", source_revision: "1" });
  const [importing, setImporting] = useState(false);
  const [previewBusy, setPreviewBusy] = useState(false);
  const [chunkPreview, setChunkPreview] = useState<ChunkPreviewResponse | null>(null);
  const [ingestionSummary, setIngestionSummary] = useState<IngestionSummary | null>(null);
  const [ingestionError, setIngestionError] = useState("");
  const [ingestionRefreshing, setIngestionRefreshing] = useState(false);
  const [memberships, setMemberships] = useState<Membership[]>([]);
  const [audit, setAudit] = useState<AuditEvent[]>([]);
  const [applications, setApplications] = useState<AgentApplication[]>([]);
  const [selectedApplication, setSelectedApplication] = useState("");
  const [environments, setEnvironments] = useState<AppEnvironment[]>([]);
  const [selectedEnvironment, setSelectedEnvironment] = useState("");
  const [appQuery, setAppQuery] = useState("");
  const [appAnswer, setAppAnswer] = useState<GatewayAnswerResponse | null>(null);
  const [appBusy, setAppBusy] = useState(false);
  const [indexBuilds, setIndexBuilds] = useState<IndexBuild[]>([]);
  const [indexReleases, setIndexReleases] = useState<IndexRelease[]>([]);
  const [credentials, setCredentials] = useState<ApplicationCredential[]>([]);
  const [runtimeTrace, setRuntimeTrace] = useState<RuntimeTrace | null>(null);
  const [runtimeLoading, setRuntimeLoading] = useState(false);
  const [runtimeError, setRuntimeError] = useState("");
  const [buildForm, setBuildForm] = useState({ version: "", collection: "raglab_lifecycle_v1", embedding_model: "", embedding_version: "", chunker_version: "" });
  const [credentialName, setCredentialName] = useState("portal-integration");
  const [credentialScopes, setCredentialScopes] = useState<string[]>(["rag:query", "rag:answer"]);
  const [credentialSecret, setCredentialSecret] = useState("");
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const streamAbort = useRef<AbortController | null>(null);

  const isAdmin = Boolean(session?.identity.roles.some((role) => role === "admin" || role === "platform_admin"));
  const isPlatformAdmin = Boolean(session?.identity.roles.includes("platform_admin"));
  const currentDataset = useMemo(() => datasets.find((dataset) => dataset.id === selectedDataset), [datasets, selectedDataset]);
  const currentApplication = useMemo(() => applications.find((application) => application.app_id === selectedApplication), [applications, selectedApplication]);

  useEffect(() => {
    const raw = window.localStorage.getItem("raglab-portal-session");
    if (raw) {
      try {
        const restored = JSON.parse(raw) as Session;
        if (!restored.expires_at || restored.expires_at * 1000 > Date.now()) setSession(restored);
      } catch {
        window.localStorage.removeItem("raglab-portal-session");
      }
    }
    setBooting(false);
  }, []);

  useEffect(() => {
    if (session) void loadWorkspace(session);
    // Workspace loading intentionally runs when the authenticated identity changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [session]);

  useEffect(() => {
    if (datasets.length && !datasets.some((dataset) => dataset.id === selectedDataset)) setSelectedDataset(datasets[0].id);
  }, [datasets, selectedDataset]);

  async function api(path: string, init: RequestInit = {}, token = session?.access_token) {
    const headers = new Headers(init.headers);
    headers.set("Content-Type", "application/json");
    if (token) headers.set("Authorization", `Bearer ${token}`);
    const response = await fetch(`${API_BASE}${path}`, { ...init, headers });
    if (!response.ok) throw new Error(await parseError(response));
    return response;
  }

  async function authenticate(nextEmail = email, nextPassword = password) {
    setAuthError("");
    try {
      const path = authMode === "login" ? "/api/v1/auth/login" : "/api/v1/auth/register";
      const response = await fetch(`${API_BASE}${path}`, {
        method: "POST", headers: { "Content-Type": "application/json" },
        body: JSON.stringify(authMode === "login" ? { email: nextEmail, password: nextPassword } : { email: nextEmail, password: nextPassword, organization }),
      });
      if (!response.ok) throw new Error(await parseError(response));
      const nextSession = (await response.json()) as Session;
      window.localStorage.setItem("raglab-portal-session", JSON.stringify(nextSession));
      setSession(nextSession);
      setPassword("");
    } catch (error) {
      setAuthError(error instanceof Error ? error.message : "登录失败，请检查 API 服务是否启动");
    }
  }

  async function loadWorkspace(active: Session) {
    try {
      const response = await api("/api/v1/datasets", {}, active.access_token);
      const body = (await response.json()) as { datasets: Dataset[] };
      setDatasets(body.datasets ?? []);
      if (body.datasets?.length) setSelectedDataset((current) => current || body.datasets[0].id);
      if (active.identity.roles.some((role) => role === "admin" || role === "platform_admin")) await loadApplications(active.access_token);
    } catch (error) {
      setNotice(error instanceof Error ? error.message : "无法加载知识库");
    }
  }

  async function loadApplications(token = session?.access_token) {
    if (!token) return;
    try {
      const response = await api("/api/v1/apps", {}, token);
      const body = (await response.json()) as { applications: AgentApplication[] };
      setApplications(body.applications ?? []);
      const first = body.applications?.[0];
      if (first) {
        setSelectedApplication((current) => current || first.app_id);
        await loadApplicationEnvironments(first.app_id, token);
      }
    } catch (error) {
      setNotice(error instanceof Error ? error.message : "应用空间加载失败");
    }
  }

  async function loadApplicationEnvironments(applicationID: string, token = session?.access_token) {
    if (!token || !applicationID) return;
    try {
      const response = await api(`/api/v1/apps/${encodeURIComponent(applicationID)}/environments`, {}, token);
      const body = (await response.json()) as { environments: AppEnvironment[] };
      setEnvironments(body.environments ?? []);
      if (body.environments?.length) setSelectedEnvironment((current) => current || body.environments[0].environment_id);
    } catch (error) {
      setNotice(error instanceof Error ? error.message : "应用环境加载失败");
    }
  }

  async function loadRuntimeControlPlane(applicationID = selectedApplication, environmentID = selectedEnvironment) {
    if (!session || !applicationID || !environmentID) return;
    setRuntimeLoading(true); setRuntimeError("");
    try {
      const prefix = `/api/v1/apps/${encodeURIComponent(applicationID)}`;
      const [buildResponse, releaseResponse, credentialResponse] = await Promise.all([
        api(`${prefix}/environments/${encodeURIComponent(environmentID)}/index-builds`),
        api(`${prefix}/environments/${encodeURIComponent(environmentID)}/indexes`),
        api(`${prefix}/credentials`),
      ]);
      setIndexBuilds(((await buildResponse.json()) as { builds: IndexBuild[] }).builds ?? []);
      setIndexReleases(((await releaseResponse.json()) as { releases: IndexRelease[] }).releases ?? []);
      setCredentials(((await credentialResponse.json()) as { credentials: ApplicationCredential[] }).credentials ?? []);
    } catch (error) {
      setRuntimeError(error instanceof Error ? error.message : "运行控制面加载失败");
    } finally { setRuntimeLoading(false); }
  }

  async function loadRuntimeTrace(traceID: string, applicationID = selectedApplication) {
    if (!traceID || !applicationID) return;
    try {
      const response = await api(`/api/v1/apps/${encodeURIComponent(applicationID)}/traces/${encodeURIComponent(traceID)}`);
      setRuntimeTrace((await response.json()) as RuntimeTrace);
    } catch (error) {
      setNotice(error instanceof Error ? error.message : "Trace 读取失败");
    }
  }

  async function submitIndexBuild(event: FormEvent) {
    event.preventDefault();
    if (!selectedApplication || !selectedEnvironment) return;
    const version = buildForm.version.trim() || `portal-${new Date().toISOString().slice(0, 19).replace(/[-:T]/g, "")}`;
    try {
      const response = await api(`/api/v1/apps/${encodeURIComponent(selectedApplication)}/environments/${encodeURIComponent(selectedEnvironment)}/index-builds`, {
        method: "POST", body: JSON.stringify({
          idempotency_key: `portal-${selectedApplication}-${version}`,
          version, collection: buildForm.collection.trim(), embedding_model: buildForm.embedding_model.trim(),
          embedding_version: buildForm.embedding_version.trim(), chunker_version: buildForm.chunker_version.trim(),
        }),
      });
      const body = (await response.json()) as { build: IndexBuild; idempotent: boolean };
      setNotice(body.idempotent ? `构建 ${body.build.build_id} 已存在，恢复原任务。` : `异步索引构建已提交：${body.build.build_id}`);
      setBuildForm((current) => ({ ...current, version }));
      await loadRuntimeControlPlane();
    } catch (error) { setRuntimeError(error instanceof Error ? error.message : "索引构建提交失败"); }
  }

  async function publishIndex(channel: "stable" | "canary") {
    if (!selectedApplication || !selectedEnvironment) return;
    const completed = indexBuilds.find((build) => build.status === "completed" && build.manifest);
    if (!completed) { setRuntimeError("没有可发布的 completed Manifest，请先提交并等待构建完成。"); return; }
    try {
      await api(`/api/v1/apps/${encodeURIComponent(selectedApplication)}/environments/${encodeURIComponent(selectedEnvironment)}/indexes/publish`, {
        method: "POST", body: JSON.stringify({ environment_id: selectedEnvironment, version: completed.version, collection: completed.collection, channel, rollout_percent: channel === "canary" ? 10 : 100 }),
      });
      setNotice(`${channel === "canary" ? "Canary 10%" : "Stable 100%"} 发布完成。`);
      await loadRuntimeControlPlane();
    } catch (error) { setRuntimeError(error instanceof Error ? error.message : "索引发布失败"); }
  }

  async function rollbackIndex(releaseID: string) {
    if (!selectedApplication || !selectedEnvironment) return;
    try {
      await api(`/api/v1/apps/${encodeURIComponent(selectedApplication)}/environments/${encodeURIComponent(selectedEnvironment)}/indexes/rollback`, { method: "POST", body: JSON.stringify({ release_id: releaseID }) });
      setNotice(`索引发布 ${releaseID} 已回滚。`);
      await loadRuntimeControlPlane();
    } catch (error) { setRuntimeError(error instanceof Error ? error.message : "索引回滚失败"); }
  }

  async function createCredential(event: FormEvent) {
    event.preventDefault();
    if (!selectedApplication || !credentialName.trim()) return;
    try {
      const response = await api(`/api/v1/apps/${encodeURIComponent(selectedApplication)}/credentials`, { method: "POST", body: JSON.stringify({ name: credentialName.trim(), scopes: credentialScopes }) });
      const body = (await response.json()) as { credential: ApplicationCredential; secret: string };
      setCredentialSecret(body.secret);
      setNotice("Credential 已创建。Secret 只在这次响应显示，请立即保存。");
      await loadRuntimeControlPlane();
    } catch (error) { setRuntimeError(error instanceof Error ? error.message : "Credential 创建失败"); }
  }

  async function revokeCredential(credentialID: string) {
    if (!selectedApplication) return;
    try {
      await api(`/api/v1/apps/${encodeURIComponent(selectedApplication)}/credentials/${encodeURIComponent(credentialID)}/revoke`, { method: "POST" });
      setNotice(`Credential ${credentialID} 已撤销，后续请求会被拒绝。`);
      await loadRuntimeControlPlane();
    } catch (error) { setRuntimeError(error instanceof Error ? error.message : "Credential 撤销失败"); }
  }

  function toggleCredentialScope(scope: string) {
    setCredentialScopes((current) => current.includes(scope) ? current.filter((item) => item !== scope) : [...current, scope]);
  }

  async function loadAccessData() {
    try {
      const [membersResponse, auditResponse] = await Promise.all([
        api("/api/v1/memberships"),
        isPlatformAdmin ? api("/api/v1/audit/recent") : Promise.resolve(null),
      ]);
      setMemberships(((await membersResponse.json()) as { members: Membership[] }).members ?? []);
      if (auditResponse) setAudit(((await auditResponse.json()) as { events: AuditEvent[] }).events ?? []);
    } catch (error) {
      setNotice(error instanceof Error ? error.message : "权限信息加载失败");
    }
  }

  function logout() {
    streamAbort.current?.abort();
    window.localStorage.removeItem("raglab-portal-session");
    setSession(null); setMessages([]); setDatasets([]); setSelectedDataset(""); setApplications([]); setSelectedApplication(""); setEnvironments([]); setSelectedEnvironment(""); setAppAnswer(null);
  }

  async function askApplication() {
    const text = appQuery.trim();
    if (!text || !selectedApplication || appBusy) return;
    setAppBusy(true); setNotice(""); setAppAnswer(null);
    try {
      const response = await api(`/api/v1/apps/${encodeURIComponent(selectedApplication)}/answer/stream`, {
        method: "POST", body: JSON.stringify({ environment_id: selectedEnvironment, query: text, top_k: 5 }),
      });
      if (!response.body) throw new Error("服务端没有返回应用级流式响应");
      const reader = response.body.getReader();
      const decoder = new TextDecoder();
      let buffer = "";
      while (true) {
        const chunk = await reader.read();
        if (chunk.done) break;
        buffer += decoder.decode(chunk.value, { stream: true });
        const blocks = buffer.split("\n\n");
        buffer = blocks.pop() ?? "";
        for (const block of blocks) {
          const dataLine = block.split("\n").find((line) => line.startsWith("data: "));
          if (!dataLine) continue;
          const payload = JSON.parse(dataLine.slice(6)) as { result?: GatewayAnswerResponse };
          if (payload.result) {
            setAppAnswer(payload.result);
            if (payload.result.trace_id) void loadRuntimeTrace(payload.result.trace_id, selectedApplication);
          }
        }
      }
    } catch (error) {
      setNotice(error instanceof Error ? error.message : "应用级回答失败");
    } finally { setAppBusy(false); }
  }

  useEffect(() => {
    if (!selectedApplication || !session) return;
    setSelectedEnvironment("");
    void loadApplicationEnvironments(selectedApplication);
    // Application changes intentionally reload server-side environments.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedApplication, session]);

  async function sendQuestion(event?: FormEvent) {
    event?.preventDefault();
    const text = query.trim();
    if (!text || !selectedDataset || streaming) return;
    const userId = uid("user");
    const assistantId = uid("assistant");
    setQuery("");
    setSearchHits([]);
    setMessages((current) => [...current, { id: userId, role: "user", text }, { id: assistantId, role: "assistant", text: "正在检索知识库…", pending: true }]);
    setStreaming(true);
    const controller = new AbortController();
    streamAbort.current = controller;
    try {
      const response = await api(`/api/v1/datasets/${selectedDataset}/answer/stream`, { method: "POST", body: JSON.stringify({ query: text, top_k: 5 }), signal: controller.signal });
      if (!response.body) throw new Error("服务端没有返回流式响应");
      const reader = response.body.getReader();
      const decoder = new TextDecoder();
      let buffer = "";
      let answer = "";
      let finalResponse: AnswerResponse | undefined;
      while (true) {
        const chunk = await reader.read();
        if (chunk.done) break;
        buffer += decoder.decode(chunk.value, { stream: true });
        const blocks = buffer.split("\n\n");
        buffer = blocks.pop() ?? "";
        for (const block of blocks) {
          const dataLine = block.split("\n").find((line) => line.startsWith("data: "));
          if (!dataLine) continue;
          try {
            const payload = JSON.parse(dataLine.slice(6)) as { event?: { type: string; delta?: string; response?: AnswerResponse; error?: string; search?: unknown } };
            const progress = payload.event;
            if (!progress) continue;
            if (progress.type === "retrieved") {
              setMessages((current) => current.map((message) => message.id === assistantId ? { ...message, text: "已完成召回，正在生成可信回答…" } : message));
            } else if (progress.type === "token" && progress.delta) {
              answer += progress.delta;
              setMessages((current) => current.map((message) => message.id === assistantId ? { ...message, text: answer } : message));
            } else if (progress.type === "completed" && progress.response) {
              finalResponse = progress.response;
              setSearchHits(progress.response.search.hits ?? []);
            } else if (progress.type === "error") {
              throw new Error(progress.error || "回答生成失败");
            }
          } catch (parseError) {
            if (parseError instanceof Error && parseError.message !== "Unexpected end of JSON input") throw parseError;
          }
        }
      }
      setMessages((current) => current.map((message) => message.id === assistantId ? { ...message, text: finalResponse?.answer || answer || "没有找到足够依据。", response: finalResponse, pending: false } : message));
    } catch (error) {
      if (!(error instanceof DOMException && error.name === "AbortError")) {
        setMessages((current) => current.map((message) => message.id === assistantId ? { ...message, text: error instanceof Error ? error.message : "回答失败", pending: false } : message));
      }
    } finally {
      setStreaming(false); streamAbort.current = null;
    }
  }

  async function runSearch() {
    if (!query.trim() || !selectedDataset || searching) return;
    setSearching(true); setNotice("");
    try {
      const response = await api(`/api/v1/datasets/${selectedDataset}/search`, { method: "POST", body: JSON.stringify({ query: query.trim(), top_k: 8 }) });
      setSearchHits(((await response.json()) as { result: { hits: SearchHit[] } }).result.hits ?? []);
    } catch (error) { setNotice(error instanceof Error ? error.message : "检索失败"); }
    finally { setSearching(false); }
  }

  async function createKnowledgeBase(event: FormEvent) {
    event.preventDefault(); setNotice("");
    try {
      const response = await api("/api/v1/datasets", { method: "POST", body: JSON.stringify(newDataset) });
      const created = (await response.json()) as Dataset;
      setDatasets((current) => [...current, created]); setSelectedDataset(created.id); setView("ingest");
      setNewDataset({ name: "", slug: "", description: "", visibility: "tenant", allowed_roles: ["admin"] });
      setNotice("知识库已创建，可以立即导入第一份资料");
    } catch (error) { setNotice(error instanceof Error ? error.message : "知识库创建失败"); }
  }

  async function previewDocument() {
    if (!selectedDataset || !document.title.trim() || !document.content.trim() || previewBusy) return;
    setPreviewBusy(true); setNotice("");
    try {
      const response = await api(`/api/v1/datasets/${selectedDataset}/documents/preview`, {
        method: "POST",
        body: JSON.stringify({ title: document.title, content: document.content, max_runes: 500, overlap_runes: 50 }),
      });
      setChunkPreview((await response.json()) as ChunkPreviewResponse);
      setNotice("结构化分块预览完成；确认页码、标题和父子关系后再导入。此次预览不会写入 Milvus。");
    } catch (error) { setNotice(error instanceof Error ? error.message : "分块预览失败"); }
    finally { setPreviewBusy(false); }
  }

  async function importDocument(event: FormEvent) {
    event.preventDefault();
    if (!selectedDataset) return;
    setImporting(true); setNotice("");
    try {
      const eventID = `${document.document_id}-r${Number(document.source_revision) || 1}-portal`;
      const response = await api(`/api/v1/datasets/${selectedDataset}/ingestion/jobs`, { method: "POST", body: JSON.stringify({
        idempotency_key: `portal-${selectedDataset}-${document.document_id}-${Number(document.source_revision) || 1}`,
        change: {
          event_id: eventID,
          operation: "upsert",
          source_revision: Number(document.source_revision) || 1,
          document: { document_id: document.document_id, title: document.title, content: document.content, version: document.version, status: "active" },
        },
      }) });
      const body = (await response.json()) as { duplicate: boolean; job: IngestionJob };
      setNotice(body.duplicate ? "相同资料任务已存在，已恢复显示原任务状态。" : `导入任务已创建：${body.job.job_id}，页面会实时刷新阶段进度。`);
      setDocument({ document_id: "", title: "", content: "", version: "v1", source_revision: "1" });
      await refreshDatasetJobs(selectedDataset);
    } catch (error) { setNotice(error instanceof Error ? error.message : "资料导入失败"); }
    finally { setImporting(false); }
  }

  async function loadDocumentFile(file: File) {
    setNotice("");
    if (file.size > 64 * 1024) {
      setNotice("当前演示版单份资料限制为 64 KB，请先拆分或压缩内容。");
      return;
    }
    try {
      const content = await file.text();
      const baseName = file.name.replace(/\.(markdown|md|txt)$/i, "");
      const documentID = baseName.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 120) || `document-${Date.now()}`;
      setDocument((current) => ({
        ...current,
        document_id: current.document_id || documentID,
        title: current.title || baseName,
        content,
      }));
      setNotice(`已读取 ${file.name}，请确认标题和版本后导入。`);
    } catch (error) {
      setNotice(error instanceof Error ? error.message : "无法读取资料文件");
    }
  }

  async function refreshDatasetJobs(datasetID = selectedDataset) {
    if (!session || !datasetID) return;
    setIngestionRefreshing(true);
    try {
      const response = await api(`/api/v1/datasets/${datasetID}/ingestion/jobs`);
      setIngestionSummary((await response.json()) as IngestionSummary);
      setIngestionError("");
    } catch (error) {
      setIngestionError(error instanceof Error ? error.message : "读取导入任务失败");
    } finally { setIngestionRefreshing(false); }
  }

  async function mutateDatasetJob(jobID: string, action: "retry" | "cancel") {
    if (!selectedDataset) return;
    try {
      await api(`/api/v1/datasets/${selectedDataset}/ingestion/jobs/${jobID}/${action}`, { method: "POST" });
      setNotice(action === "retry" ? "任务已重新排队，正在继续处理。" : "已发送取消请求，等待 Worker 确认。" );
      await refreshDatasetJobs(selectedDataset);
    } catch (error) { setIngestionError(error instanceof Error ? error.message : `任务${action}失败`); }
  }

  function toggleRole(role: string) {
    setNewDataset((current) => ({ ...current, allowed_roles: current.allowed_roles.includes(role) ? current.allowed_roles.filter((item) => item !== role) : [...current.allowed_roles, role] }));
  }

  useEffect(() => {
    if (view === "access" && session) void loadAccessData();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [view, session]);

  useEffect(() => {
    if (view === "runtime" && session && selectedApplication && selectedEnvironment) void loadRuntimeControlPlane();
    // Runtime data is scoped to the visible application environment.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [view, session, selectedApplication, selectedEnvironment]);

  useEffect(() => {
    if (view !== "ingest" || !session || !selectedDataset) return;
    void refreshDatasetJobs(selectedDataset);
    const timer = window.setInterval(() => void refreshDatasetJobs(selectedDataset), 1800);
    return () => window.clearInterval(timer);
    // Polling is intentionally scoped to the visible import workspace.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [view, session, selectedDataset]);

  if (booting) return <main className="portal-loading"><div className="portal-loader" /><span>正在连接 RAG 服务…</span></main>;
  if (!session) return <LoginScreen mode={authMode} setMode={setAuthMode} email={email} password={password} organization={organization} setEmail={setEmail} setPassword={setPassword} setOrganization={setOrganization} error={authError} submit={() => void authenticate()} demo={(demo) => { setEmail(demo.email); setPassword(demo.password); setAuthMode("login"); void authenticate(demo.email, demo.password); }} />;

  return (
    <main className="portal-shell">
      <aside className={`portal-sidebar ${sidebarOpen ? "open" : ""}`}>
        <div className="portal-brand"><div className="brand-mark">R</div><div><strong>RAG Desk</strong><span>Enterprise Knowledge</span></div></div>
        <div className="tenant-chip"><span className="status-dot" />{session.identity.tenant_id}<small>{session.identity.roles.join(" · ")}</small></div>
        <nav className="portal-nav">
          <button className={view === "chat" ? "active" : ""} onClick={() => { setView("chat"); setSidebarOpen(false); }}><span>⌁</span>智能客服<em>LIVE</em></button>
          {isAdmin && <button className={view === "apps" ? "active" : ""} onClick={() => { setView("apps"); setSidebarOpen(false); }}><span>◇</span>Agent 应用<em>GATEWAY</em></button>}
          {isAdmin && <button className={view === "runtime" ? "active" : ""} onClick={() => { setView("runtime"); setSidebarOpen(false); }}><span>⌘</span>运行控制面<em>OPS</em></button>}
          <button className={view === "knowledge" ? "active" : ""} onClick={() => { setView("knowledge"); setSidebarOpen(false); }}><span>▦</span>知识库</button>
          {isAdmin && <button className={view === "ingest" ? "active" : ""} onClick={() => { setView("ingest"); setSelectedDataset(datasets.find((dataset) => dataset.visibility === "tenant")?.id ?? datasets[0]?.id ?? ""); setSidebarOpen(false); }}><span>↑</span>导入资料</button>}
          {isAdmin && <button className={view === "access" ? "active" : ""} onClick={() => { setView("access"); setSidebarOpen(false); }}><span>◈</span>权限与审计</button>}
        </nav>
        <div className="sidebar-bottom"><a href="/" target="_blank" rel="noreferrer">打开工程实验室 ↗</a><button onClick={logout}>退出登录</button></div>
      </aside>
      <section className="portal-main">
        <header className="portal-header"><button className="mobile-menu" onClick={() => setSidebarOpen((open) => !open)}>☰</button><div><p className="eyebrow">CUSTOMER OPERATIONS / RAG</p><h1>{view === "chat" ? "智能客服" : view === "apps" ? "Agent 应用" : view === "runtime" ? "运行控制面" : view === "knowledge" ? "知识库" : view === "ingest" ? "导入资料" : "权限与审计"}</h1></div><div className="header-actions"><span className="api-status"><i />API 在线</span><button className="avatar" title={session.identity.subject}>{session.identity.subject.slice(-2).toUpperCase()}</button></div></header>
        {notice && <div className="portal-notice">{notice}<button onClick={() => setNotice("")}>×</button></div>}
        {view === "chat" && <ChatView datasets={datasets} selected={selectedDataset} setSelected={setSelectedDataset} current={currentDataset} query={query} setQuery={setQuery} messages={messages} streaming={streaming} send={sendQuestion} runSearch={() => void runSearch()} searching={searching} searchHits={searchHits} />}
        {view === "apps" && <ApplicationView applications={applications} environments={environments} selectedApplication={selectedApplication} setSelectedApplication={setSelectedApplication} selectedEnvironment={selectedEnvironment} setSelectedEnvironment={setSelectedEnvironment} current={currentApplication} query={appQuery} setQuery={setAppQuery} answer={appAnswer} busy={appBusy} ask={() => void askApplication()} />}
        {view === "runtime" && <RuntimeView applications={applications} environments={environments} selectedApplication={selectedApplication} setSelectedApplication={setSelectedApplication} selectedEnvironment={selectedEnvironment} setSelectedEnvironment={setSelectedEnvironment} builds={indexBuilds} releases={indexReleases} credentials={credentials} trace={runtimeTrace} loading={runtimeLoading} error={runtimeError} buildForm={buildForm} setBuildForm={setBuildForm} submitBuild={submitIndexBuild} publish={publishIndex} rollback={rollbackIndex} credentialName={credentialName} setCredentialName={setCredentialName} credentialScopes={credentialScopes} toggleCredentialScope={toggleCredentialScope} createCredential={createCredential} secret={credentialSecret} revokeCredential={revokeCredential} />}
        {view === "knowledge" && <KnowledgeView datasets={datasets} selected={selectedDataset} setSelected={setSelectedDataset} isAdmin={isAdmin} form={newDataset} setForm={setNewDataset} toggleRole={toggleRole} create={createKnowledgeBase} />}
        {view === "ingest" && <><IngestView datasets={datasets} selected={selectedDataset} setSelected={setSelectedDataset} isPlatformAdmin={isPlatformAdmin} document={document} setDocument={(next) => { setDocument(next); setChunkPreview(null); }} loadFile={(file) => void loadDocumentFile(file)} submit={importDocument} importing={importing} preview={previewDocument} previewBusy={previewBusy} chunkPreview={chunkPreview} /><IngestionTaskBoard summary={ingestionSummary} error={ingestionError} refreshing={ingestionRefreshing} refresh={() => void refreshDatasetJobs()} mutate={mutateDatasetJob} /></>}
        {view === "access" && <AccessView session={session} datasets={datasets} memberships={memberships} audit={audit} isPlatformAdmin={isPlatformAdmin} />}
      </section>
    </main>
  );
}

function LoginScreen(props: { mode: "login" | "register"; setMode: (mode: "login" | "register") => void; email: string; password: string; organization: string; setEmail: (value: string) => void; setPassword: (value: string) => void; setOrganization: (value: string) => void; error: string; submit: () => void; demo: (demo: typeof DEMOS[number]) => void }) {
  return <main className="login-page"><div className="login-art"><div className="grid-glow" /><div className="login-copy"><div className="portal-brand light"><div className="brand-mark">R</div><div><strong>RAG Desk</strong><span>Enterprise Knowledge</span></div></div><p className="eyebrow">GROUNDED CUSTOMER OPERATIONS</p><h1>让每一次回答，<br /><span>都有依据。</span></h1><p>面向企业客服的知识检索与回答工作台。身份、权限、引用、评测全部在一条真实链路上。</p><div className="proof-row"><span>⌁ Milvus 检索</span><span>✓ 权限隔离</span><span>◌ DeepSeek 生成</span></div></div></div><div className="login-card"><div className="login-heading"><p className="eyebrow">WELCOME BACK</p><h2>{props.mode === "login" ? "登录工作台" : "创建团队空间"}</h2><p>{props.mode === "login" ? "使用企业账号继续你的知识问答" : "注册后自动获得管理员身份"}</p></div><form onSubmit={(event) => { event.preventDefault(); props.submit(); }}><label>邮箱<input type="email" value={props.email} onChange={(event) => props.setEmail(event.target.value)} placeholder="you@company.com" required /></label><label>密码<input type="password" value={props.password} onChange={(event) => props.setPassword(event.target.value)} placeholder="至少 12 位" required /></label>{props.mode === "register" && <label>组织名称<input value={props.organization} onChange={(event) => props.setOrganization(event.target.value)} placeholder="例如：星河科技" required /></label>}{props.error && <div className="form-error">{props.error}</div>}<button className="primary-button" type="submit">{props.mode === "login" ? "进入客服工作台" : "创建并进入"}<span>→</span></button></form><div className="login-switch"><span>{props.mode === "login" ? "还没有团队空间？" : "已经有账号？"}</span><button onClick={() => props.setMode(props.mode === "login" ? "register" : "login")}>{props.mode === "login" ? "立即注册" : "返回登录"}</button></div><div className="demo-divider"><span>本地演示账号</span></div><div className="demo-list">{DEMOS.map((demo) => <button key={demo.email} onClick={() => props.demo(demo)}><span>{demo.label}</span><small>{demo.email}</small><b>↗</b></button>)}</div></div></main>;
}

function ChatView(props: { datasets: Dataset[]; selected: string; setSelected: (value: string) => void; current?: Dataset; query: string; setQuery: (value: string) => void; messages: ChatMessage[]; streaming: boolean; send: (event?: FormEvent) => void; runSearch: () => void; searching: boolean; searchHits: SearchHit[] }) {
  return <div className="chat-layout"><div className="chat-column"><div className="workspace-strip"><div><span className="section-kicker">KNOWLEDGE SCOPE</span><strong>{props.current?.name ?? "请选择知识库"}</strong><small>{props.current?.description ?? "当前身份没有可见知识库"}</small></div><select value={props.selected} onChange={(event) => props.setSelected(event.target.value)}>{props.datasets.map((dataset) => <option key={dataset.id} value={dataset.id}>{dataset.name}</option>)}</select></div><div className="conversation"><div className="welcome-card"><div className="spark">✦</div><h2>你好，我是你的知识助手</h2><p>我会先检索 <b>{props.current?.name ?? "当前知识库"}</b>，再基于命中的资料回答。每个结论都会保留来源引用。</p><div className="suggestions"><button onClick={() => props.setQuery("如何申请企业单点登录？")}>如何申请企业单点登录？</button><button onClick={() => props.setQuery("导出报表需要什么权限？")}>导出报表需要什么权限？</button><button onClick={() => props.setQuery("服务故障时的升级流程是什么？")}>服务故障时的升级流程？</button></div></div>{props.messages.map((message) => <div className={`message-row ${message.role}`} key={message.id}><div className="message-avatar">{message.role === "user" ? "我" : "R"}</div><div className="message-body"><div className="message-meta">{message.role === "user" ? "你" : "RAG Desk"}<span>{message.role === "assistant" && message.pending ? "实时生成中" : ""}</span></div><div className={`message-bubble ${message.pending ? "pending" : ""}`}>{message.text}{message.pending && <i className="typing" />}</div>{message.response && <AnswerMeta response={message.response} />}</div></div>)}</div><form className="composer" onSubmit={props.send}><textarea value={props.query} onChange={(event) => props.setQuery(event.target.value)} placeholder="向知识库提问…" rows={2} onKeyDown={(event) => { if (event.key === "Enter" && !event.shiftKey) { event.preventDefault(); props.send(); } }} /><div className="composer-bottom"><span>↗ Enter 发送 · Shift + Enter 换行</span><div><button type="button" className="ghost-button" onClick={props.runSearch} disabled={props.searching || !props.query.trim()}>只看检索</button><button className="send-button" disabled={props.streaming || !props.query.trim()}>发送 <span>↑</span></button></div></div></form></div><aside className="evidence-panel"><div className="panel-heading"><div><span className="section-kicker">RETRIEVAL PREVIEW</span><h3>检索证据</h3></div><span className="count-badge">{props.searchHits.length || "—"}</span></div>{props.searchHits.length ? props.searchHits.map((hit) => <EvidenceCard key={hit.chunk_id} hit={hit} />) : <div className="empty-evidence"><div>⌁</div><strong>等待一次检索</strong><p>点击“只看检索”，或发送问题后查看召回的 chunks、距离和租户过滤器。</p></div>}<div className="panel-footnote"><span>●</span> 回答完成后会自动展示本次检索证据，仅包含当前身份可见内容</div></aside></div>;
}

function ApplicationView(props: { applications: AgentApplication[]; environments: AppEnvironment[]; selectedApplication: string; setSelectedApplication: (value: string) => void; selectedEnvironment: string; setSelectedEnvironment: (value: string) => void; current?: AgentApplication; query: string; setQuery: (value: string) => void; answer: GatewayAnswerResponse | null; busy: boolean; ask: () => void }) {
  return <div className="content-page application-page"><div className="page-intro"><div><span className="section-kicker">APPLICATION KNOWLEDGE GATEWAY</span><h2>Agent 应用知识入口</h2><p>上层 Agent 只选择应用和环境。服务端会解析绑定、租户权限与检索策略，不把 Milvus Filter 暴露给调用方。</p></div><span className="secure-pill">✓ SERVER POLICY</span></div>{props.applications.length ? <><div className="create-panel app-selector"><div className="form-grid"><label>应用<select value={props.selectedApplication} onChange={(event) => props.setSelectedApplication(event.target.value)}>{props.applications.map((application) => <option key={application.app_id} value={application.app_id}>{application.name} · {application.tenant_id}</option>)}</select></label><label>环境<select value={props.selectedEnvironment} onChange={(event) => props.setSelectedEnvironment(event.target.value)}>{props.environments.map((environment) => <option key={environment.environment_id} value={environment.environment_id}>{environment.name} · {environment.config_version}</option>)}</select></label></div><small className="gateway-contract">POST /api/v1/apps/{props.selectedApplication || "{app_id}"}/answer/stream</small></div><div className="create-panel gateway-query"><div className="panel-heading"><div><span className="section-kicker">GATEWAY ANSWER</span><h3>{props.current?.name ?? "选择一个应用"}</h3></div><span className="lock-badge">BINDING ACL</span></div><textarea value={props.query} onChange={(event) => props.setQuery(event.target.value)} placeholder="向当前 Agent 应用提问…" rows={3} /><div className="ingest-actions"><span>应用级多知识库聚合 · Citation Allowlist · Environment 隔离</span><button className="primary-button compact" onClick={props.ask} disabled={props.busy || !props.query.trim() || !props.selectedEnvironment}>{props.busy ? "检索与生成中…" : "调用 Gateway →"}</button></div></div>{props.answer && <div className="gateway-result"><div className="result-answer"><div className="section-kicker">GROUNDED ANSWER</div><h3>{props.answer.result.answer}</h3><div className="answer-stats"><span>召回 {props.answer.result.search.hits.length} 条</span><span>{Math.round(props.answer.result.search.total_latency_ms)} ms</span><span>{props.answer.result.generation.model || props.answer.result.generation.generator}</span></div>{props.answer.trace_id && <small className="gateway-trace">Trace {props.answer.trace_id}</small>}</div><div className="binding-proof"><div className="section-kicker">RESOLVED BINDINGS</div>{props.answer.bindings.map((binding) => <div className="binding-proof-row" key={binding.dataset_id}><strong>{binding.dataset_name || binding.dataset_id}</strong><span>{binding.hits} hits · top_k {binding.policy.top_k} · candidate {binding.policy.candidate_k} · index {binding.index_version || "default"}</span><small>{binding.rewrite?.applied ? `rewrite ${binding.rewrite.rewriter || "on"}` : "rewrite off"} · {binding.rerank?.applied ? `rerank ${binding.rerank.model || "on"}` : "rerank off"}</small></div>)}<code>{props.answer.result.search.filter}</code></div></div>}</> : <div className="empty-evidence app-empty"><div>◇</div><strong>还没有可用的 Agent 应用</strong><p>先通过控制面创建 Application 并绑定一个或多个知识库，门户会在这里展示可体验的 Gateway。</p></div>}</div>;
}

function RuntimeView(props: {
  applications: AgentApplication[]; environments: AppEnvironment[]; selectedApplication: string; setSelectedApplication: (value: string) => void; selectedEnvironment: string; setSelectedEnvironment: (value: string) => void;
  builds: IndexBuild[]; releases: IndexRelease[]; credentials: ApplicationCredential[]; trace: RuntimeTrace | null; loading: boolean; error: string;
  buildForm: { version: string; collection: string; embedding_model: string; embedding_version: string; chunker_version: string }; setBuildForm: (value: { version: string; collection: string; embedding_model: string; embedding_version: string; chunker_version: string }) => void; submitBuild: (event: FormEvent) => void; publish: (channel: "stable" | "canary") => void; rollback: (releaseID: string) => void;
  credentialName: string; setCredentialName: (value: string) => void; credentialScopes: string[]; toggleCredentialScope: (scope: string) => void; createCredential: (event: FormEvent) => void; secret: string; revokeCredential: (credentialID: string) => void;
}) {
  const latestBuild = props.builds[0];
  return <div className="content-page runtime-page"><div className="page-intro"><div><span className="section-kicker">ENTERPRISE RUNTIME / CONTROL PLANE</span><h2>运行控制面</h2><p>把索引、发布、应用凭证和 Query Trace 放在同一个可审计的应用边界内。这里的每个动作都通过服务端权限校验。</p></div><span className="secure-pill">{props.loading ? "↻ LOADING" : "✓ POLICY ENFORCED"}</span></div>{props.applications.length ? <><div className="create-panel app-selector"><div className="form-grid"><label>应用<select value={props.selectedApplication} onChange={(event) => props.setSelectedApplication(event.target.value)}>{props.applications.map((application) => <option key={application.app_id} value={application.app_id}>{application.name} · {application.tenant_id}</option>)}</select></label><label>环境<select value={props.selectedEnvironment} onChange={(event) => props.setSelectedEnvironment(event.target.value)}>{props.environments.map((environment) => <option key={environment.environment_id} value={environment.environment_id}>{environment.name} · {environment.config_version}</option>)}</select></label></div><small className="gateway-contract">Control plane is scoped to {props.selectedApplication || "{app_id}"} / {props.selectedEnvironment || "{environment_id}"}</small></div>{props.error && <div className="form-error">{props.error}</div>}<div className="runtime-grid"><section className="create-panel"><div className="panel-heading"><div><span className="section-kicker">ASYNC INDEX BUILD</span><h3>构建 Manifest</h3></div><span className="lock-badge">ADMIN ONLY</span></div><form onSubmit={props.submitBuild}><div className="form-grid"><label>版本<input value={props.buildForm.version} onChange={(event) => props.setBuildForm({ ...props.buildForm, version: event.target.value })} placeholder="例如：v2026-07-29" /></label><label>Collection<input value={props.buildForm.collection} onChange={(event) => props.setBuildForm({ ...props.buildForm, collection: event.target.value })} required /></label><label>Embedding Model<input value={props.buildForm.embedding_model} onChange={(event) => props.setBuildForm({ ...props.buildForm, embedding_model: event.target.value })} placeholder="可选断言" /></label><label>Embedding Version<input value={props.buildForm.embedding_version} onChange={(event) => props.setBuildForm({ ...props.buildForm, embedding_version: event.target.value })} placeholder="可选断言" /></label><label className="wide">Chunker Version<input value={props.buildForm.chunker_version} onChange={(event) => props.setBuildForm({ ...props.buildForm, chunker_version: event.target.value })} placeholder="记录可复现的切分版本" /></label></div><button className="primary-button compact" type="submit">提交异步构建 →</button></form>{latestBuild && <div className="runtime-list"><div className="runtime-list-head"><span>最近构建</span><b className={`task-status ${latestBuild.status}`}>{latestBuild.status}</b></div><strong>{latestBuild.version}</strong><small>{latestBuild.stage} · attempts {latestBuild.attempts} · {latestBuild.collection}</small>{latestBuild.manifest && <code>rows {latestBuild.manifest.row_count} · dim {latestBuild.manifest.dimensions} · manifest {latestBuild.manifest.manifest_hash.slice(0, 16)}…</code>}</div>}</section><section className="create-panel"><div className="panel-heading"><div><span className="section-kicker">INDEX RELEASE</span><h3>灰度与回滚</h3></div><span className="lock-badge">STABLE / CANARY</span></div><div className="ingest-actions"><span>已完成构建可发布</span><div><button className="ghost-button" onClick={() => props.publish("canary")} disabled={!latestBuild?.manifest}>Canary 10%</button><button className="primary-button compact" onClick={() => props.publish("stable")} disabled={!latestBuild?.manifest}>Stable 100%</button></div></div><div className="runtime-list">{props.releases.length ? props.releases.slice(0, 6).map((release) => <div className="runtime-row" key={release.release_id}><div><strong>{release.version}</strong><small>{release.channel} · {release.state} · {release.rollout_percent}% · {release.collection}</small></div><button className="ghost-button" onClick={() => props.rollback(release.release_id)}>回滚</button></div>) : <div className="loading-line">暂无发布版本</div>}</div></section></div><div className="runtime-grid"><section className="create-panel"><div className="panel-heading"><div><span className="section-kicker">APPLICATION CREDENTIAL</span><h3>创建访问凭证</h3></div><span className="lock-badge">ONE-TIME SECRET</span></div><form onSubmit={props.createCredential}><div className="form-grid"><label className="wide">名称<input value={props.credentialName} onChange={(event) => props.setCredentialName(event.target.value)} required /></label></div><div className="role-picker"><span>Scopes</span><div><button type="button" className={props.credentialScopes.includes("rag:query") ? "chosen" : ""} onClick={() => props.toggleCredentialScope("rag:query")}>rag:query</button><button type="button" className={props.credentialScopes.includes("rag:answer") ? "chosen" : ""} onClick={() => props.toggleCredentialScope("rag:answer")}>rag:answer</button></div></div><button className="primary-button compact" type="submit">生成 Credential →</button></form>{props.secret && <div className="credential-secret"><span>立即保存 Secret（只显示一次）</span><code>{props.secret}</code></div>}<div className="runtime-list">{props.credentials.length ? props.credentials.slice(0, 6).map((credential) => <div className="runtime-row" key={credential.credential_id}><div><strong>{credential.name}</strong><small>{credential.status} · {credential.scopes.join(" · ")}</small></div><button className="ghost-button" onClick={() => props.revokeCredential(credential.credential_id)} disabled={credential.status !== "active"}>撤销</button></div>) : <div className="loading-line">暂无应用凭证</div>}</div></section><section className="create-panel"><div className="panel-heading"><div><span className="section-kicker">QUERY TRACE / COST</span><h3>最近一次应用回答</h3></div><span className="lock-badge">OTEL READY</span></div>{props.trace ? <div className="trace-card"><code>{props.trace.trace_id}</code><div className="answer-stats"><span>{props.trace.status}</span><span>{Math.round(props.trace.total_ms)} ms</span><span>{props.trace.hit_count} hits / {props.trace.candidate_count} candidates</span></div><div className="trace-facts"><span>index {props.trace.index_version || "default"}</span><span>{props.trace.rerank_applied ? "rerank on" : "rerank off"}</span><span>{props.trace.rewrite_applied ? "rewrite on" : "rewrite off"}</span><span>cost ${props.trace.total_cost_usd.toFixed(6)}</span></div><small>{props.trace.generator || props.trace.model || "generator pending"} · {props.trace.embedding_model || "embedding pending"}</small></div> : <div className="empty-evidence"><div>◌</div><strong>还没有 Trace</strong><p>在 Agent 应用页调用一次 Gateway，返回的 trace_id 会在这里读取持久化记录。</p></div>}</section></div></> : <div className="empty-evidence app-empty"><div>⌘</div><strong>还没有可用的 Agent 应用</strong><p>请先创建 Application，并为环境绑定知识库。</p></div>}</div>;
}

function AnswerMeta({ response }: { response: AnswerResponse }) {
  return <div className="answer-meta"><div className="answer-stats"><span>召回 {response.search.hits.length} 条</span><span>{Math.round(response.search.total_latency_ms)} ms</span><span>{response.generation.model || response.generation.generator}</span></div>{response.citations?.length > 0 && <div className="citation-list"><span>引用来源</span>{response.citations.slice(0, 3).map((citation) => <button key={citation.chunk_id} title={citation.excerpt}>⌕ {citation.document}</button>)}</div>}</div>;
}

function EvidenceCard({ hit }: { hit: SearchHit }) {
  return <article className="evidence-card"><div className="evidence-top"><span>{hit.title}</span><b>{hit.distance.toFixed(3)}</b></div><p>{hit.content.slice(0, 170)}{hit.content.length > 170 ? "…" : ""}</p><small><span>{hit.visibility === "public" ? "PUBLIC" : hit.tenant_id}</span><span>{hit.version || "active"}</span></small></article>;
}

function KnowledgeView(props: { datasets: Dataset[]; selected: string; setSelected: (value: string) => void; isAdmin: boolean; form: { name: string; slug: string; description: string; visibility: string; allowed_roles: string[] }; setForm: (form: { name: string; slug: string; description: string; visibility: string; allowed_roles: string[] }) => void; toggleRole: (role: string) => void; create: (event: FormEvent) => void }) {
  return <div className="content-page"><div className="page-intro"><div><span className="section-kicker">AUTHORIZED DATASETS</span><h2>知识库目录</h2><p>每次搜索都会在服务端重新校验数据集、租户和角色，前端不会持有“可见全部数据”的权限。</p></div><span className="metric-pill">{props.datasets.length} 个可见空间</span></div><div className="dataset-grid">{props.datasets.map((dataset) => <button className={`dataset-card ${props.selected === dataset.id ? "selected" : ""}`} key={dataset.id} onClick={() => props.setSelected(dataset.id)}><div className="dataset-icon">{dataset.visibility === "public" ? "◌" : "⌂"}</div><div className="dataset-card-main"><div className="dataset-title"><h3>{dataset.name}</h3><span className={dataset.visibility === "public" ? "public-tag" : "tenant-tag"}>{dataset.visibility === "public" ? "公开" : "租户隔离"}</span></div><p>{dataset.description}</p><small>{dataset.id} · {dataset.allowed_roles?.join(" / ") || "viewer"}</small></div><span className="arrow">→</span></button>)}</div>{props.isAdmin && <form className="create-panel" onSubmit={props.create}><div className="panel-heading"><div><span className="section-kicker">CONTROL PLANE</span><h3>创建一个知识空间</h3></div><span className="lock-badge">ADMIN ONLY</span></div><div className="form-grid"><label>名称<input value={props.form.name} onChange={(event) => props.setForm({ ...props.form, name: event.target.value })} placeholder="例如：售后服务手册" required /></label><label>Slug<input value={props.form.slug} onChange={(event) => props.setForm({ ...props.form, slug: event.target.value })} placeholder="after-sales" pattern="[a-z0-9]+(?:-[a-z0-9]+)*" required /></label><label className="wide">描述<input value={props.form.description} onChange={(event) => props.setForm({ ...props.form, description: event.target.value })} placeholder="描述这个知识库服务的业务范围" /></label><label>可见范围<select value={props.form.visibility} onChange={(event) => props.setForm({ ...props.form, visibility: event.target.value })}><option value="tenant">当前租户</option><option value="public">公开（平台管理员）</option></select></label><div className="role-picker"><span>允许角色</span><div><button type="button" className={props.form.allowed_roles.includes("viewer") ? "chosen" : ""} onClick={() => props.toggleRole("viewer")}>Viewer</button><button type="button" className={props.form.allowed_roles.includes("admin") ? "chosen" : ""} onClick={() => props.toggleRole("admin")}>Admin</button></div></div></div><button className="primary-button compact" type="submit">创建知识库 <span>→</span></button></form>}</div>;
}

function IngestView(props: { datasets: Dataset[]; selected: string; setSelected: (value: string) => void; isPlatformAdmin: boolean; document: { document_id: string; title: string; content: string; version: string; source_revision: string }; setDocument: (document: { document_id: string; title: string; content: string; version: string; source_revision: string }) => void; loadFile: (file: File) => void; submit: (event: FormEvent) => void; importing: boolean; preview: () => void; previewBusy: boolean; chunkPreview: ChunkPreviewResponse | null }) {
  const writableDatasets = props.isPlatformAdmin ? props.datasets : props.datasets.filter((dataset) => dataset.visibility === "tenant");
  return <div className="content-page"><div className="page-intro"><div><span className="section-kicker">KNOWLEDGE LIFECYCLE</span><h2>导入一份新资料</h2><p>先用结构化预览检查标题、页码、父子 Chunk 和重叠范围，再执行 Embedding、Milvus Upsert 与读回校验。</p></div><span className="pipeline-pill"><i /> PREVIEW → VALIDATE → EMBED → INDEX</span></div><form className="ingest-panel" onSubmit={props.submit}><div className="ingest-target"><div><span className="section-kicker">TARGET DATASET</span><strong>{writableDatasets.find((dataset) => dataset.id === props.selected)?.name ?? "选择知识库"}</strong></div><select value={props.selected} onChange={(event) => props.setSelected(event.target.value)} required>{writableDatasets.map((dataset) => <option key={dataset.id} value={dataset.id}>{dataset.name}</option>)}</select></div><div className="form-grid"><label className="wide">导入 Markdown / 文本文件<input type="file" accept=".md,.markdown,.txt,text/markdown,text/plain" onChange={(event) => { const file = event.target.files?.[0]; if (file) props.loadFile(file); event.currentTarget.value = ""; }} /><small>浏览器本地读取；演示版单份资料最大 64 KB。PDF 页码接入将在解析器适配层统一转换为 page marker。</small></label><label>文档 ID<input value={props.document.document_id} onChange={(event) => props.setDocument({ ...props.document, document_id: event.target.value })} placeholder="support-sso-v1" required /></label><label>版本<input value={props.document.version} onChange={(event) => props.setDocument({ ...props.document, version: event.target.value })} placeholder="v1" /></label><label>源修订号<input type="number" min="1" value={props.document.source_revision} onChange={(event) => props.setDocument({ ...props.document, source_revision: event.target.value })} required /></label><label className="wide">标题<input value={props.document.title} onChange={(event) => props.setDocument({ ...props.document, title: event.target.value })} placeholder="企业单点登录接入指南" required /></label><label className="wide">正文<textarea value={props.document.content} onChange={(event) => props.setDocument({ ...props.document, content: event.target.value })} placeholder="粘贴 Markdown 或纯文本内容。建议包含清晰的小标题和步骤…" rows={12} required /></label></div><div className="ingest-actions"><span>预览默认 500 字符 Chunk / 50 字符 overlap · 不写入 Milvus</span><div><button type="button" className="ghost-button" onClick={props.preview} disabled={props.previewBusy || !props.document.title.trim() || !props.document.content.trim()}>{props.previewBusy ? "预览中…" : "预览分块"}</button><button className="primary-button" disabled={props.importing || writableDatasets.length === 0}>{props.importing ? "正在入库…" : "导入并验证"}<span>↑</span></button></div></div></form>{props.chunkPreview && <ChunkPreviewPanel preview={props.chunkPreview} />}</div>;
}

function ChunkPreviewPanel({ preview }: { preview: ChunkPreviewResponse }) {
  return <section className="chunk-preview-panel"><div className="panel-heading"><div><span className="section-kicker">STRUCTURED CHUNK INSPECTOR</span><h3>分块与溯源预览</h3></div><span className="secure-pill">{preview.chunker_version}</span></div><div className="chunk-preview-metrics"><span><b>{preview.parent_count}</b> parents</span><span><b>{preview.child_count}</b> children</span><span><b>{preview.pages.length || 1}</b> pages</span><span><b>{preview.max_runes}/{preview.overlap_runes}</b> max / overlap</span></div><p className="chunk-preview-note">父 Chunk 保留完整逻辑段落，子 Chunk 用于召回；page marker 与标题路径用于后续引用定位。预览结果只在内存中生成。</p><div className="chunk-preview-list">{preview.chunks.slice(0, 12).map((chunk) => <article className="chunk-preview-card" key={chunk.id}><div className="chunk-preview-card-head"><strong>{chunk.id}</strong><span>page {chunk.source_page || "—"} · parent {chunk.parent_sequence}</span></div><small>{chunk.heading_path?.join(" / ") || "无标题路径"} · {chunk.content.length} chars</small><p>{chunk.content.slice(0, 260)}{chunk.content.length > 260 ? "…" : ""}</p><code>{chunk.parent_id}</code></article>)}</div>{preview.chunks.length > 12 && <small className="chunk-preview-more">仅展示前 12 个 child Chunk，完整结果由 API 返回。</small>}</section>;
}

function IngestionTaskBoard(props: { summary: IngestionSummary | null; error: string; refreshing: boolean; refresh: () => void; mutate: (jobID: string, action: "retry" | "cancel") => void }) {
  const jobs = props.summary?.jobs ?? [];
  const stages = ["validating", "chunking", "embedding", "indexing", "verifying"];
  return <section className="content-page task-page"><div className="page-intro"><div><span className="section-kicker">INGESTION OPERATIONS</span><h2>任务进度与人工控制</h2><p>导入已经进入异步队列。你可以观察 Worker 心跳、阶段进度和最终校验结果；失败或取消的任务可以在这里重新处理。</p></div><button type="button" className="ghost-button task-refresh" onClick={props.refresh} disabled={props.refreshing}>{props.refreshing ? "刷新中…" : "刷新任务"}</button></div>{props.error && <div className="form-error task-error" role="alert">{props.error}</div>}<div className="task-summary" aria-live="polite"><div><span>排队</span><strong>{props.summary?.queued ?? 0}</strong></div><div><span>处理中</span><strong>{props.summary?.running ?? 0}</strong></div><div><span>已完成</span><strong>{props.summary?.completed ?? 0}</strong></div><div><span>失败/取消</span><strong>{(props.summary?.failed ?? 0) + (props.summary?.cancelled ?? 0)}</strong></div></div><div className="task-list" aria-live="polite">{jobs.length ? jobs.slice(0, 12).map((job) => { const activeIndex = stages.indexOf(job.stage); return <article className="task-card" key={job.job_id}><div className="task-card-head"><div><span className={`task-status ${job.status}`}>{job.status === "running" ? "处理中" : job.status === "queued" ? "排队中" : job.status === "completed" ? "已完成" : job.status === "failed" ? "失败" : "已取消"}</span><strong title={job.idempotency_key}>{job.job_id}</strong></div><small>{new Date(job.updated_at).toLocaleTimeString()}</small></div><div className="task-stage-row">{stages.map((stage, index) => <div className={index <= activeIndex || job.status === "completed" ? "done" : ""} key={stage}><i /><span>{stage}</span></div>)}</div><div className="task-card-meta"><span>尝试 {job.attempts}/{job.max_attempts}</span><span>{job.worker_id ? `Worker ${job.worker_id}` : "等待 Worker"}</span>{job.last_heartbeat_at && <span>心跳 {new Date(job.last_heartbeat_at).toLocaleTimeString()}</span>}{job.result?.verified && <span className="verified">✓ 已校验 · {job.result.current_chunks} chunks</span>}</div>{job.last_error && <div className="task-error-text" role="alert">{job.last_error}</div>}{(job.status === "failed" || job.status === "cancelled" || job.status === "queued" || job.status === "running") && <div className="task-actions">{(job.status === "failed" || job.status === "cancelled") && job.attempts < job.max_attempts && <button type="button" onClick={() => props.mutate(job.job_id, "retry")}>重新处理</button>}{(job.status === "queued" || job.status === "running") && <button type="button" className="danger" onClick={() => { if (window.confirm(`确认取消任务 ${job.job_id}？正在执行的任务会请求 Worker 停止。`)) props.mutate(job.job_id, "cancel"); }}>取消任务</button>}</div>}</article>; }) : <div className="task-empty"><strong>还没有导入任务</strong><span>提交第一份资料后，这里会显示队列、Worker 和分阶段进度。</span></div>}</div></section>;
}

function AccessView(props: { session: Session; datasets: Dataset[]; memberships: Membership[]; audit: AuditEvent[]; isPlatformAdmin: boolean }) {
  return <div className="content-page"><div className="page-intro"><div><span className="section-kicker">TRUST BOUNDARY</span><h2>权限与审计</h2><p>你看到的检索结果由服务端身份 Claims、PostgreSQL 控制面和 Milvus scalar filter 共同决定。</p></div><span className="secure-pill">✓ POLICY ENFORCED</span></div><div className="access-grid"><div className="identity-card"><div className="identity-card-top"><div className="big-avatar">{props.session.identity.subject.slice(-2).toUpperCase()}</div><div><span className="section-kicker">CURRENT IDENTITY</span><h3>{props.session.identity.subject}</h3><p>{props.session.identity.tenant_id}</p></div></div><div className="claims"><div><span>Roles</span><strong>{props.session.identity.roles.join(" · ")}</strong></div><div><span>Token</span><strong>HS256 / local lab</strong></div><div><span>Boundary</span><strong>Tenant + role filter</strong></div></div></div><div className="membership-card"><div className="panel-heading"><div><span className="section-kicker">POSTGRESQL MEMBERSHIPS</span><h3>当前租户成员</h3></div><span className="count-badge">{props.memberships.length}</span></div>{props.memberships.length ? <div className="member-list">{props.memberships.map((member) => <div key={`${member.tenant_id}-${member.subject}`}><span className="member-dot" /><span>{member.subject}</span><b>{member.role}</b><small>{member.status}</small></div>)}</div> : <div className="loading-line">切换到此页加载成员数据…</div>}</div></div><div className="access-datasets"><div className="panel-heading"><div><span className="section-kicker">DATASET POLICY</span><h3>数据集授权快照</h3></div></div><div className="policy-table"><div className="policy-row policy-head"><span>知识库</span><span>范围</span><span>允许角色</span><span>状态</span></div>{props.datasets.map((dataset) => <div className="policy-row" key={dataset.id}><strong>{dataset.name}</strong><span>{dataset.visibility === "public" ? "public" : dataset.owner_tenant}</span><span>{dataset.allowed_roles?.join(" · ") || "viewer"}</span><b>ACTIVE</b></div>)}</div></div>{props.isPlatformAdmin && <div className="audit-card"><div className="panel-heading"><div><span className="section-kicker">SECURITY AUDIT TRAIL</span><h3>最近请求决策</h3></div></div>{props.audit.length ? props.audit.slice(-8).reverse().map((event, index) => <div className="audit-row" key={`${event.timestamp}-${index}`}><span className={event.decision === "allowed" ? "audit-ok" : "audit-deny"}>{event.decision === "allowed" ? "ALLOW" : "DENY"}</span><span>{event.method} {event.path}</span><small>{event.subject || "anonymous"} · {event.status}</small></div>) : <div className="loading-line">暂无审计事件</div>}</div>}</div>;
}
