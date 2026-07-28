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
type AnswerResponse = {
  answerable: boolean;
  answer: string;
  refusal_reason?: string;
  citations: Array<{ chunk_id: string; document_id: string; document: string; excerpt: string }>;
  search: { hits: SearchHit[]; total_latency_ms: number; embedding_latency_ms: number; search_latency_ms: number; filter: string };
  generation: { generator: string; model: string; prompt_version: string; latency_ms: number; ttft_ms?: number; token_rate_tps?: number; prompt_tokens: number; output_tokens: number };
};
type ChatMessage = { id: string; role: "user" | "assistant"; text: string; response?: AnswerResponse; pending?: boolean };
type Membership = { tenant_id: string; subject: string; role: string; status: string };
type AuditEvent = { timestamp: string; subject?: string; tenant_id?: string; method: string; path: string; decision: string; status: number; reason?: string };

const API_BASE = process.env.NEXT_PUBLIC_API_BASE ?? "http://127.0.0.1:8080";
const DEMOS = [
  { label: "平台管理员", email: "admin@raglab.local", password: "RagLab-Platform-2026!" },
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
  const [view, setView] = useState<"chat" | "knowledge" | "ingest" | "access">("chat");
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
  const [ingestionSummary, setIngestionSummary] = useState<IngestionSummary | null>(null);
  const [ingestionError, setIngestionError] = useState("");
  const [ingestionRefreshing, setIngestionRefreshing] = useState(false);
  const [memberships, setMemberships] = useState<Membership[]>([]);
  const [audit, setAudit] = useState<AuditEvent[]>([]);
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const streamAbort = useRef<AbortController | null>(null);

  const isAdmin = Boolean(session?.identity.roles.some((role) => role === "admin" || role === "platform_admin"));
  const isPlatformAdmin = Boolean(session?.identity.roles.includes("platform_admin"));
  const currentDataset = useMemo(() => datasets.find((dataset) => dataset.id === selectedDataset), [datasets, selectedDataset]);

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
    } catch (error) {
      setNotice(error instanceof Error ? error.message : "无法加载知识库");
    }
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
    setSession(null); setMessages([]); setDatasets([]); setSelectedDataset("");
  }

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
          <button className={view === "knowledge" ? "active" : ""} onClick={() => { setView("knowledge"); setSidebarOpen(false); }}><span>▦</span>知识库</button>
          {isAdmin && <button className={view === "ingest" ? "active" : ""} onClick={() => { setView("ingest"); setSelectedDataset(datasets.find((dataset) => dataset.visibility === "tenant")?.id ?? datasets[0]?.id ?? ""); setSidebarOpen(false); }}><span>↑</span>导入资料</button>}
          {isAdmin && <button className={view === "access" ? "active" : ""} onClick={() => { setView("access"); setSidebarOpen(false); }}><span>◈</span>权限与审计</button>}
        </nav>
        <div className="sidebar-bottom"><a href="/" target="_blank" rel="noreferrer">打开工程实验室 ↗</a><button onClick={logout}>退出登录</button></div>
      </aside>
      <section className="portal-main">
        <header className="portal-header"><button className="mobile-menu" onClick={() => setSidebarOpen((open) => !open)}>☰</button><div><p className="eyebrow">CUSTOMER OPERATIONS / RAG</p><h1>{view === "chat" ? "智能客服" : view === "knowledge" ? "知识库" : view === "ingest" ? "导入资料" : "权限与审计"}</h1></div><div className="header-actions"><span className="api-status"><i />API 在线</span><button className="avatar" title={session.identity.subject}>{session.identity.subject.slice(-2).toUpperCase()}</button></div></header>
        {notice && <div className="portal-notice">{notice}<button onClick={() => setNotice("")}>×</button></div>}
        {view === "chat" && <ChatView datasets={datasets} selected={selectedDataset} setSelected={setSelectedDataset} current={currentDataset} query={query} setQuery={setQuery} messages={messages} streaming={streaming} send={sendQuestion} runSearch={() => void runSearch()} searching={searching} searchHits={searchHits} />}
        {view === "knowledge" && <KnowledgeView datasets={datasets} selected={selectedDataset} setSelected={setSelectedDataset} isAdmin={isAdmin} form={newDataset} setForm={setNewDataset} toggleRole={toggleRole} create={createKnowledgeBase} />}
        {view === "ingest" && <><IngestView datasets={datasets} selected={selectedDataset} setSelected={setSelectedDataset} isPlatformAdmin={isPlatformAdmin} document={document} setDocument={setDocument} submit={importDocument} importing={importing} /><IngestionTaskBoard summary={ingestionSummary} error={ingestionError} refreshing={ingestionRefreshing} refresh={() => void refreshDatasetJobs()} mutate={mutateDatasetJob} /></>}
        {view === "access" && <AccessView session={session} datasets={datasets} memberships={memberships} audit={audit} isPlatformAdmin={isPlatformAdmin} />}
      </section>
    </main>
  );
}

function LoginScreen(props: { mode: "login" | "register"; setMode: (mode: "login" | "register") => void; email: string; password: string; organization: string; setEmail: (value: string) => void; setPassword: (value: string) => void; setOrganization: (value: string) => void; error: string; submit: () => void; demo: (demo: typeof DEMOS[number]) => void }) {
  return <main className="login-page"><div className="login-art"><div className="grid-glow" /><div className="login-copy"><div className="portal-brand light"><div className="brand-mark">R</div><div><strong>RAG Desk</strong><span>Enterprise Knowledge</span></div></div><p className="eyebrow">GROUNDED CUSTOMER OPERATIONS</p><h1>让每一次回答，<br /><span>都有依据。</span></h1><p>面向企业客服的知识检索与回答工作台。身份、权限、引用、评测全部在一条真实链路上。</p><div className="proof-row"><span>⌁ Milvus 检索</span><span>✓ 权限隔离</span><span>◌ DeepSeek 生成</span></div></div></div><div className="login-card"><div className="login-heading"><p className="eyebrow">WELCOME BACK</p><h2>{props.mode === "login" ? "登录工作台" : "创建团队空间"}</h2><p>{props.mode === "login" ? "使用企业账号继续你的知识问答" : "注册后自动获得管理员身份"}</p></div><form onSubmit={(event) => { event.preventDefault(); props.submit(); }}><label>邮箱<input type="email" value={props.email} onChange={(event) => props.setEmail(event.target.value)} placeholder="you@company.com" required /></label><label>密码<input type="password" value={props.password} onChange={(event) => props.setPassword(event.target.value)} placeholder="至少 12 位" required /></label>{props.mode === "register" && <label>组织名称<input value={props.organization} onChange={(event) => props.setOrganization(event.target.value)} placeholder="例如：星河科技" required /></label>}{props.error && <div className="form-error">{props.error}</div>}<button className="primary-button" type="submit">{props.mode === "login" ? "进入客服工作台" : "创建并进入"}<span>→</span></button></form><div className="login-switch"><span>{props.mode === "login" ? "还没有团队空间？" : "已经有账号？"}</span><button onClick={() => props.setMode(props.mode === "login" ? "register" : "login")}>{props.mode === "login" ? "立即注册" : "返回登录"}</button></div><div className="demo-divider"><span>本地演示账号</span></div><div className="demo-list">{DEMOS.map((demo) => <button key={demo.email} onClick={() => props.demo(demo)}><span>{demo.label}</span><small>{demo.email}</small><b>↗</b></button>)}</div></div></main>;
}

function ChatView(props: { datasets: Dataset[]; selected: string; setSelected: (value: string) => void; current?: Dataset; query: string; setQuery: (value: string) => void; messages: ChatMessage[]; streaming: boolean; send: (event?: FormEvent) => void; runSearch: () => void; searching: boolean; searchHits: SearchHit[] }) {
  return <div className="chat-layout"><div className="chat-column"><div className="workspace-strip"><div><span className="section-kicker">KNOWLEDGE SCOPE</span><strong>{props.current?.name ?? "请选择知识库"}</strong><small>{props.current?.description ?? "当前身份没有可见知识库"}</small></div><select value={props.selected} onChange={(event) => props.setSelected(event.target.value)}>{props.datasets.map((dataset) => <option key={dataset.id} value={dataset.id}>{dataset.name}</option>)}</select></div><div className="conversation"><div className="welcome-card"><div className="spark">✦</div><h2>你好，我是你的知识助手</h2><p>我会先检索 <b>{props.current?.name ?? "当前知识库"}</b>，再基于命中的资料回答。每个结论都会保留来源引用。</p><div className="suggestions"><button onClick={() => props.setQuery("如何申请企业单点登录？")}>如何申请企业单点登录？</button><button onClick={() => props.setQuery("导出报表需要什么权限？")}>导出报表需要什么权限？</button><button onClick={() => props.setQuery("服务故障时的升级流程是什么？")}>服务故障时的升级流程？</button></div></div>{props.messages.map((message) => <div className={`message-row ${message.role}`} key={message.id}><div className="message-avatar">{message.role === "user" ? "我" : "R"}</div><div className="message-body"><div className="message-meta">{message.role === "user" ? "你" : "RAG Desk"}<span>{message.role === "assistant" && message.pending ? "实时生成中" : ""}</span></div><div className={`message-bubble ${message.pending ? "pending" : ""}`}>{message.text}{message.pending && <i className="typing" />}</div>{message.response && <AnswerMeta response={message.response} />}</div></div>)}</div><form className="composer" onSubmit={props.send}><textarea value={props.query} onChange={(event) => props.setQuery(event.target.value)} placeholder="向知识库提问…" rows={2} onKeyDown={(event) => { if (event.key === "Enter" && !event.shiftKey) { event.preventDefault(); props.send(); } }} /><div className="composer-bottom"><span>↗ Enter 发送 · Shift + Enter 换行</span><div><button type="button" className="ghost-button" onClick={props.runSearch} disabled={props.searching || !props.query.trim()}>只看检索</button><button className="send-button" disabled={props.streaming || !props.query.trim()}>发送 <span>↑</span></button></div></div></form></div><aside className="evidence-panel"><div className="panel-heading"><div><span className="section-kicker">RETRIEVAL PREVIEW</span><h3>检索证据</h3></div><span className="count-badge">{props.searchHits.length || "—"}</span></div>{props.searchHits.length ? props.searchHits.map((hit) => <EvidenceCard key={hit.chunk_id} hit={hit} />) : <div className="empty-evidence"><div>⌁</div><strong>等待一次检索</strong><p>点击“只看检索”，或发送问题后查看召回的 chunks、距离和租户过滤器。</p></div>}<div className="panel-footnote"><span>●</span> 只展示当前身份可见的证据</div></aside></div>;
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

function IngestView(props: { datasets: Dataset[]; selected: string; setSelected: (value: string) => void; isPlatformAdmin: boolean; document: { document_id: string; title: string; content: string; version: string; source_revision: string }; setDocument: (document: { document_id: string; title: string; content: string; version: string; source_revision: string }) => void; submit: (event: FormEvent) => void; importing: boolean }) {
  const writableDatasets = props.isPlatformAdmin ? props.datasets : props.datasets.filter((dataset) => dataset.visibility === "tenant");
  return <div className="content-page"><div className="page-intro"><div><span className="section-kicker">KNOWLEDGE LIFECYCLE</span><h2>导入一份新资料</h2><p>资料会经过分块、Embedding、Milvus Upsert 和读回校验；同一文档用 source revision 做幂等和版本保护。</p></div><span className="pipeline-pill"><i /> VALIDATE → EMBED → INDEX → VERIFY</span></div><form className="ingest-panel" onSubmit={props.submit}><div className="ingest-target"><div><span className="section-kicker">TARGET DATASET</span><strong>{writableDatasets.find((dataset) => dataset.id === props.selected)?.name ?? "选择知识库"}</strong></div><select value={props.selected} onChange={(event) => props.setSelected(event.target.value)} required>{writableDatasets.map((dataset) => <option key={dataset.id} value={dataset.id}>{dataset.name}</option>)}</select></div><div className="form-grid"><label>文档 ID<input value={props.document.document_id} onChange={(event) => props.setDocument({ ...props.document, document_id: event.target.value })} placeholder="support-sso-v1" required /></label><label>版本<input value={props.document.version} onChange={(event) => props.setDocument({ ...props.document, version: event.target.value })} placeholder="v1" /></label><label>源修订号<input type="number" min="1" value={props.document.source_revision} onChange={(event) => props.setDocument({ ...props.document, source_revision: event.target.value })} required /></label><label className="wide">标题<input value={props.document.title} onChange={(event) => props.setDocument({ ...props.document, title: event.target.value })} placeholder="企业单点登录接入指南" required /></label><label className="wide">正文<textarea value={props.document.content} onChange={(event) => props.setDocument({ ...props.document, content: event.target.value })} placeholder="粘贴 Markdown 或纯文本内容。建议包含清晰的小标题和步骤…" rows={12} required /></label></div><div className="ingest-actions"><span>最大 64 KB · 当前导入会写入当前知识库的 ACL</span><button className="primary-button" disabled={props.importing || writableDatasets.length === 0}>{props.importing ? "正在入库…" : "导入并验证"}<span>↑</span></button></div></form></div>;
}

function IngestionTaskBoard(props: { summary: IngestionSummary | null; error: string; refreshing: boolean; refresh: () => void; mutate: (jobID: string, action: "retry" | "cancel") => void }) {
  const jobs = props.summary?.jobs ?? [];
  const stages = ["validating", "chunking", "embedding", "indexing", "verifying"];
  return <section className="content-page task-page"><div className="page-intro"><div><span className="section-kicker">INGESTION OPERATIONS</span><h2>任务进度与人工控制</h2><p>导入已经进入异步队列。你可以观察 Worker 心跳、阶段进度和最终校验结果；失败或取消的任务可以在这里重新处理。</p></div><button type="button" className="ghost-button task-refresh" onClick={props.refresh} disabled={props.refreshing}>{props.refreshing ? "刷新中…" : "刷新任务"}</button></div>{props.error && <div className="form-error task-error" role="alert">{props.error}</div>}<div className="task-summary" aria-live="polite"><div><span>排队</span><strong>{props.summary?.queued ?? 0}</strong></div><div><span>处理中</span><strong>{props.summary?.running ?? 0}</strong></div><div><span>已完成</span><strong>{props.summary?.completed ?? 0}</strong></div><div><span>失败/取消</span><strong>{(props.summary?.failed ?? 0) + (props.summary?.cancelled ?? 0)}</strong></div></div><div className="task-list" aria-live="polite">{jobs.length ? jobs.slice(0, 12).map((job) => { const activeIndex = stages.indexOf(job.stage); return <article className="task-card" key={job.job_id}><div className="task-card-head"><div><span className={`task-status ${job.status}`}>{job.status === "running" ? "处理中" : job.status === "queued" ? "排队中" : job.status === "completed" ? "已完成" : job.status === "failed" ? "失败" : "已取消"}</span><strong title={job.idempotency_key}>{job.job_id}</strong></div><small>{new Date(job.updated_at).toLocaleTimeString()}</small></div><div className="task-stage-row">{stages.map((stage, index) => <div className={index <= activeIndex || job.status === "completed" ? "done" : ""} key={stage}><i /><span>{stage}</span></div>)}</div><div className="task-card-meta"><span>尝试 {job.attempts}/{job.max_attempts}</span><span>{job.worker_id ? `Worker ${job.worker_id}` : "等待 Worker"}</span>{job.last_heartbeat_at && <span>心跳 {new Date(job.last_heartbeat_at).toLocaleTimeString()}</span>}{job.result?.verified && <span className="verified">✓ 已校验 · {job.result.current_chunks} chunks</span>}</div>{job.last_error && <div className="task-error-text" role="alert">{job.last_error}</div>}{(job.status === "failed" || job.status === "cancelled" || job.status === "queued" || job.status === "running") && <div className="task-actions">{(job.status === "failed" || job.status === "cancelled") && job.attempts < job.max_attempts && <button type="button" onClick={() => props.mutate(job.job_id, "retry")}>重新处理</button>}{(job.status === "queued" || job.status === "running") && <button type="button" className="danger" onClick={() => { if (window.confirm(`确认取消任务 ${job.job_id}？正在执行的任务会请求 Worker 停止。`)) props.mutate(job.job_id, "cancel"); }}>取消任务</button>}</div>}</article>; }) : <div className="task-empty"><strong>还没有导入任务</strong><span>提交第一份资料后，这里会显示队列、Worker 和分阶段进度。</span></div>}</div></section>;
}

function AccessView(props: { session: Session; datasets: Dataset[]; memberships: Membership[]; audit: AuditEvent[]; isPlatformAdmin: boolean }) {
  return <div className="content-page"><div className="page-intro"><div><span className="section-kicker">TRUST BOUNDARY</span><h2>权限与审计</h2><p>你看到的检索结果由服务端身份 Claims、PostgreSQL 控制面和 Milvus scalar filter 共同决定。</p></div><span className="secure-pill">✓ POLICY ENFORCED</span></div><div className="access-grid"><div className="identity-card"><div className="identity-card-top"><div className="big-avatar">{props.session.identity.subject.slice(-2).toUpperCase()}</div><div><span className="section-kicker">CURRENT IDENTITY</span><h3>{props.session.identity.subject}</h3><p>{props.session.identity.tenant_id}</p></div></div><div className="claims"><div><span>Roles</span><strong>{props.session.identity.roles.join(" · ")}</strong></div><div><span>Token</span><strong>HS256 / local lab</strong></div><div><span>Boundary</span><strong>Tenant + role filter</strong></div></div></div><div className="membership-card"><div className="panel-heading"><div><span className="section-kicker">POSTGRESQL MEMBERSHIPS</span><h3>当前租户成员</h3></div><span className="count-badge">{props.memberships.length}</span></div>{props.memberships.length ? <div className="member-list">{props.memberships.map((member) => <div key={`${member.tenant_id}-${member.subject}`}><span className="member-dot" /><span>{member.subject}</span><b>{member.role}</b><small>{member.status}</small></div>)}</div> : <div className="loading-line">切换到此页加载成员数据…</div>}</div></div><div className="access-datasets"><div className="panel-heading"><div><span className="section-kicker">DATASET POLICY</span><h3>数据集授权快照</h3></div></div><div className="policy-table"><div className="policy-row policy-head"><span>知识库</span><span>范围</span><span>允许角色</span><span>状态</span></div>{props.datasets.map((dataset) => <div className="policy-row" key={dataset.id}><strong>{dataset.name}</strong><span>{dataset.visibility === "public" ? "public" : dataset.owner_tenant}</span><span>{dataset.allowed_roles?.join(" · ") || "viewer"}</span><b>ACTIVE</b></div>)}</div></div>{props.isPlatformAdmin && <div className="audit-card"><div className="panel-heading"><div><span className="section-kicker">SECURITY AUDIT TRAIL</span><h3>最近请求决策</h3></div></div>{props.audit.length ? props.audit.slice(-8).reverse().map((event, index) => <div className="audit-row" key={`${event.timestamp}-${index}`}><span className={event.decision === "allowed" ? "audit-ok" : "audit-deny"}>{event.decision === "allowed" ? "ALLOW" : "DENY"}</span><span>{event.method} {event.path}</span><small>{event.subject || "anonymous"} · {event.status}</small></div>) : <div className="loading-line">暂无审计事件</div>}</div>}</div>;
}
