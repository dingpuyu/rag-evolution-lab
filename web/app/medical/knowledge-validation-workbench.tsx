"use client";

import { FormEvent, useEffect, useMemo, useState } from "react";

type Revision = {
  document_id: string;
  title: string;
  source_revision: number;
  document_version?: string;
  file_name: string;
};

type DeviceContext = {
  model_code: string;
  software_version: string;
  lot_or_batch: string;
  region: string;
};

type DiffValue = string | number | boolean | null | string[] | Record<string, unknown>;
type DocumentDiff = {
  document_id: string;
  from_revision: Revision & { source_hash: string; block_count: number };
  to_revision: Revision & { source_hash: string; block_count: number };
  metadata_changes: Array<{ field: string; from?: DiffValue; to?: DiffValue }>;
  summary: { added: number; removed: number; modified: number; unchanged: number };
  block_changes: Array<{
    change_type: "added" | "removed" | "modified";
    locator: string;
    block_type: string;
    from_text?: string;
    to_text?: string;
  }>;
  truncated: boolean;
};

type SearchHit = {
  chunk_id: string;
  document_id: string;
  dataset_id: string;
  title: string;
  content: string;
  version?: string;
  document_revision?: string;
  model_codes?: string[];
  source_file?: string;
  source_page?: number;
  source_sheet?: string;
  source_cell_range?: string;
  heading_path?: string[];
  distance?: number;
  fusion_score?: number;
  recall_sources?: string[];
  exact_matches?: string[];
};

type SearchTrial = {
  app_id: string;
  environment_id: string;
  decision: string;
  reason_code: string;
  trace_id?: string;
  rewritten_query?: string;
  bindings: Array<{
    dataset_id: string;
    dataset_name: string;
    hits: number;
    index_version?: string;
    index_collection?: string;
    rewrite: { applied?: boolean; query?: string; rewriter?: string; reason?: string };
    rerank: { applied: boolean; model?: string; candidates: number };
  }>;
  result: {
    query: string;
    collection: string;
    embedder: string;
    dimensions: number;
    metric: string;
    filter: string;
    embedding_latency_ms: number;
    search_latency_ms: number;
    total_latency_ms: number;
    hits: SearchHit[];
  };
};

type Props = {
  apiBase: string;
  token: string;
  datasetID: string;
  appID: string;
  environmentID: string;
  revisions: Revision[];
  selected: Revision | null;
  deviceContext: DeviceContext;
};

async function responseError(response: Response) {
  try {
    const body = await response.json();
    return body?.error?.message || body?.message || `请求失败（${response.status}）`;
  } catch {
    return `请求失败（${response.status}）`;
  }
}

function displayValue(value: DiffValue | undefined) {
  if (value == null || value === "") return "—";
  return typeof value === "string" ? value : JSON.stringify(value);
}

function sourceLocation(hit: SearchHit) {
  const parts = [hit.source_file];
  if (hit.source_page) parts.push(`p.${hit.source_page}`);
  if (hit.source_sheet)
    parts.push(`${hit.source_sheet}!${hit.source_cell_range || ""}`);
  if (hit.heading_path?.length) parts.push(hit.heading_path.join(" › "));
  return parts.filter(Boolean).join(" · ") || "无来源定位";
}

export default function KnowledgeValidationWorkbench({
  apiBase,
  token,
  datasetID,
  appID,
  environmentID,
  revisions,
  selected,
  deviceContext,
}: Props) {
  const documentRevisions = useMemo(
    () =>
      revisions
        .filter((item) => item.document_id === selected?.document_id)
        .sort((left, right) => right.source_revision - left.source_revision),
    [revisions, selected?.document_id],
  );
  const [fromRevision, setFromRevision] = useState("");
  const [toRevision, setToRevision] = useState("");
  const [diff, setDiff] = useState<DocumentDiff | null>(null);
  const [diffBusy, setDiffBusy] = useState(false);
  const [diffError, setDiffError] = useState("");
  const [query, setQuery] = useState(
    "VSM-100 软件 2.6 的 SYS-NET-042 是什么？",
  );
  const [topK, setTopK] = useState("5");
  const [trial, setTrial] = useState<SearchTrial | null>(null);
  const [trialBusy, setTrialBusy] = useState(false);
  const [trialError, setTrialError] = useState("");

  useEffect(() => {
    queueMicrotask(() => {
      setDiff(null);
      setDiffError("");
      if (documentRevisions.length >= 2) {
        setFromRevision(
          String(
            documentRevisions[documentRevisions.length - 1].source_revision,
          ),
        );
        setToRevision(String(documentRevisions[0].source_revision));
      } else {
        setFromRevision("");
        setToRevision("");
      }
    });
  }, [datasetID, selected?.document_id, documentRevisions]);

  async function compareRevisions() {
    if (!selected || !fromRevision || !toRevision) return;
    setDiffBusy(true);
    setDiffError("");
    try {
      const params = new URLSearchParams({
        document_id: selected.document_id,
        from_revision: fromRevision,
        to_revision: toRevision,
      });
      const response = await fetch(
        `${apiBase}/api/v1/datasets/${encodeURIComponent(datasetID)}/documents/diff?${params}`,
        { headers: { Authorization: `Bearer ${token}` } },
      );
      if (!response.ok) throw new Error(await responseError(response));
      setDiff((await response.json()) as DocumentDiff);
    } catch (error) {
      setDiffError(error instanceof Error ? error.message : "文档修订比较失败");
    } finally {
      setDiffBusy(false);
    }
  }

  async function runTrial(event: FormEvent) {
    event.preventDefault();
    if (!query.trim() || !appID || !environmentID) return;
    setTrialBusy(true);
    setTrialError("");
    try {
      const response = await fetch(
        `${apiBase}/api/v1/apps/${encodeURIComponent(appID)}/query`,
        {
          method: "POST",
          headers: {
            Authorization: `Bearer ${token}`,
            "Content-Type": "application/json",
          },
          body: JSON.stringify({
            environment_id: environmentID,
            query: query.trim(),
            top_k: Number(topK),
            device_context: deviceContext,
          }),
        },
      );
      if (!response.ok) throw new Error(await responseError(response));
      setTrial((await response.json()) as SearchTrial);
    } catch (error) {
      setTrialError(error instanceof Error ? error.message : "检索试跑失败");
    } finally {
      setTrialBusy(false);
    }
  }

  return (
    <div className="medical-content medical-status-section medical-validation-grid">
      <section className="medical-revision-diff">
        <div className="medical-workbench-title">
          <div>
            <p className="medical-kicker">REVISION DIFF</p>
            <h3>文档修订差异</h3>
            <small>
              比较 PostgreSQL 中同一 Document ID 的两个修订，并从 MinIO
              读取对应 Document IR。
            </small>
          </div>
          <span>{documentRevisions.length} revisions</span>
        </div>
        {selected ? (
          <>
            <div className="medical-diff-controls">
              <label>
                基线修订
                <select
                  value={fromRevision}
                  onChange={(event) => setFromRevision(event.target.value)}
                >
                  <option value="">请选择</option>
                  {documentRevisions.map((item) => (
                    <option
                      key={`from-${item.source_revision}`}
                      value={item.source_revision}
                    >
                      revision {item.source_revision} · {item.file_name}
                    </option>
                  ))}
                </select>
              </label>
              <label>
                目标修订
                <select
                  value={toRevision}
                  onChange={(event) => setToRevision(event.target.value)}
                >
                  <option value="">请选择</option>
                  {documentRevisions.map((item) => (
                    <option
                      key={`to-${item.source_revision}`}
                      value={item.source_revision}
                    >
                      revision {item.source_revision} · {item.file_name}
                    </option>
                  ))}
                </select>
              </label>
              <button
                type="button"
                disabled={
                  diffBusy ||
                  !fromRevision ||
                  !toRevision ||
                  fromRevision === toRevision
                }
                onClick={() => void compareRevisions()}
              >
                {diffBusy ? "比较中…" : "比较 Document IR"}
              </button>
            </div>
            {documentRevisions.length < 2 && (
              <div className="medical-workbench-empty">
                当前文档只有一个修订。使用相同 Document ID 上传 revision 2
                后即可比较元数据和结构块变化。
              </div>
            )}
          </>
        ) : (
          <div className="medical-workbench-empty">
            先在“最近接入与索引状态”中选择一份文档。
          </div>
        )}
        {diffError && <p className="medical-pipeline-error">{diffError}</p>}
        {diff && (
          <div className="medical-diff-result">
            <div className="medical-diff-metrics">
              <span className="added">
                <b>+{diff.summary.added}</b>新增
              </span>
              <span className="modified">
                <b>~{diff.summary.modified}</b>修改
              </span>
              <span className="removed">
                <b>-{diff.summary.removed}</b>删除
              </span>
              <span>
                <b>{diff.summary.unchanged}</b>未变化
              </span>
            </div>
            {diff.metadata_changes.length > 0 && (
              <details open>
                <summary>元数据变化 · {diff.metadata_changes.length}</summary>
                {diff.metadata_changes.map((change) => (
                  <p key={change.field}>
                    <code>{change.field}</code>
                    <del>{displayValue(change.from)}</del>
                    <ins>{displayValue(change.to)}</ins>
                  </p>
                ))}
              </details>
            )}
            <div className="medical-block-diffs">
              {diff.block_changes.map((change, index) => (
                <article
                  className={change.change_type}
                  key={`${change.change_type}-${change.locator}-${index}`}
                >
                  <header>
                    <b>{change.change_type}</b>
                    <span>{change.block_type}</span>
                    <code>{change.locator}</code>
                  </header>
                  {change.from_text && <del>{change.from_text}</del>}
                  {change.to_text && <ins>{change.to_text}</ins>}
                </article>
              ))}
              {!diff.block_changes.length && (
                <div className="medical-workbench-empty">
                  两个修订的 Document IR 内容一致。
                </div>
              )}
            </div>
            {diff.truncated && <small>变化过多，页面仅展示前 100 条。</small>}
          </div>
        )}
      </section>

      <section className="medical-retrieval-trial">
        <div className="medical-workbench-title">
          <div>
            <p className="medical-kicker">RETRIEVAL PROBE</p>
            <h3>应用级检索试跑</h3>
            <small>
              使用当前 Application/Environment 的真实绑定执行 Query Rewrite、混合召回和
              Rerank，不调用回答模型。
            </small>
          </div>
          <span>{selected?.document_id || "no target"}</span>
        </div>
        <form onSubmit={runTrial}>
          <label>
            测试问题
            <textarea
              rows={3}
              value={query}
              onChange={(event) => setQuery(event.target.value)}
            />
          </label>
          <label>
            Top K
            <select value={topK} onChange={(event) => setTopK(event.target.value)}>
              <option value="3">3</option>
              <option value="5">5</option>
              <option value="10">10</option>
            </select>
          </label>
          <button disabled={trialBusy || !appID || !environmentID || !query.trim()}>
            {trialBusy ? "检索中…" : "运行真实检索"}
          </button>
        </form>
        {trialError && <p className="medical-pipeline-error">{trialError}</p>}
        {trial && (
          <div className="medical-trial-result">
            <div className="medical-trial-summary">
              <span>
                <small>Decision</small>
                <b>{trial.decision}</b>
              </span>
              <span>
                <small>Hits</small>
                <b>{trial.result.hits.length}</b>
              </span>
              <span>
                <small>Total</small>
                <b>{trial.result.total_latency_ms.toFixed(1)} ms</b>
              </span>
              <span>
                <small>Embedding</small>
                <b>{trial.result.embedding_latency_ms.toFixed(1)} ms</b>
              </span>
            </div>
            <dl>
              <dt>Trace</dt>
              <dd>
                <code>{trial.trace_id || "—"}</code>
              </dd>
              <dt>Rewrite</dt>
              <dd>{trial.rewritten_query || trial.result.query}</dd>
              <dt>Retrieval</dt>
              <dd>
                {trial.result.metric} · {trial.result.embedder} · {trial.result.dimensions}d
              </dd>
              <dt>Server Filter</dt>
              <dd>
                <code>{trial.result.filter || "由各绑定独立执行"}</code>
              </dd>
            </dl>
            <div className="medical-binding-traces">
              {trial.bindings.map((binding) => (
                <article key={binding.dataset_id}>
                  <b>{binding.dataset_name || binding.dataset_id}</b>
                  <span>{binding.hits} hits</span>
                  <small>
                    rewrite {binding.rewrite.applied ? "on" : "off"} · rerank{" "}
                    {binding.rerank.applied ? binding.rerank.model || "on" : "off"}
                  </small>
                  <code>{binding.index_collection || "active alias"}</code>
                </article>
              ))}
            </div>
            <div className="medical-trial-hits">
              {trial.result.hits.map((hit, index) => (
                <article
                  className={
                    hit.document_id === selected?.document_id ? "target" : ""
                  }
                  key={`${hit.chunk_id}-${index}`}
                >
                  <header>
                    <i>#{index + 1}</i>
                    <div>
                      <b>{hit.title || hit.document_id}</b>
                      <small>
                        {hit.dataset_id} · {hit.document_revision || hit.version || "—"}
                      </small>
                    </div>
                    {hit.document_id === selected?.document_id && (
                      <span>选中文档命中</span>
                    )}
                  </header>
                  <p>{hit.content}</p>
                  <footer>
                    <code>{sourceLocation(hit)}</code>
                    <span>
                      {(hit.recall_sources ?? []).join(" + ") || trial.result.metric}
                      {hit.exact_matches?.length
                        ? ` · exact: ${hit.exact_matches.join(", ")}`
                        : ""}
                    </span>
                  </footer>
                </article>
              ))}
              {!trial.result.hits.length && (
                <div className="medical-workbench-empty">
                  当前授权范围内没有命中证据。
                </div>
              )}
            </div>
          </div>
        )}
      </section>
    </div>
  );
}
