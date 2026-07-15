"use client";

import { useState } from "react";

const metrics = [
  { label: "Hit Rate@5", before: 0.85, after: 0.9, delta: "+5.0%" },
  { label: "MRR", before: 0.762, after: 0.9, delta: "+13.8%" },
  { label: "Doc Recall@5", before: 0.85, after: 0.9, delta: "+5.0%" },
];

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
  const current = cases[activeCase];

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
            <div className="pipe-node"><small>RETRIEVER</small><strong>Vector</strong></div>
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
