"use client";

import { useState } from "react";

const metrics = [
  { label: "Hit Rate@5", before: 0.85, after: 0.9, delta: "+5.0%" },
  { label: "MRR", before: 0.762, after: 0.9, delta: "+13.8%" },
  { label: "Doc Recall@5", before: 0.85, after: 0.9, delta: "+5.0%" },
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
          <a href="#experiment">效果对比</a>
          <a href="#harness">Harness</a>
          <a className="repo-link" href="https://github.com/dingpuyu/rag-evolution-lab" target="_blank" rel="noreferrer">
            GitHub <span aria-hidden="true">↗</span>
          </a>
        </div>
      </nav>

      <section className="hero shell" id="top">
        <div className="hero-copy">
          <div className="eyebrow"><span className="pulse" /> PHASE 2 · ACTIVE</div>
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
            <div className="terminal-line"><span>$</span> raglab compare --baseline v0 --candidate v2</div>
            <div className="run-row"><span>dataset</span><strong>development / 20 cases</strong></div>
            <div className="run-row"><span>candidate</span><strong>v2-metadata</strong></div>
            <div className="score-line">
              <div><small>MRR</small><strong>0.900</strong><em>+0.138</em></div>
              <div><small>HIT@5</small><strong>0.900</strong><em>+0.050</em></div>
              <div><small>VIOLATIONS</small><strong>0</strong><em>−41</em></div>
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
        <div><strong>20</strong><span>Golden Queries</span><small>8 failure categories</small></div>
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
          <article className="version-card active">
            <div className="version-top"><span>V2</span><small>CURRENT</small></div>
            <h3>Metadata Filter</h3>
            <p>在评分前过滤产品、版本和生命周期，同时保留显式历史版本查询。</p>
            <div className="version-metric"><span>MRR</span><strong>0.900</strong><em>+18.1%</em></div>
            <div className="version-state">● ACTIVE</div>
          </article>
          <article className="version-card future">
            <div className="version-top"><span>V3</span><small>NEXT</small></div>
            <h3>Hybrid + RRF</h3>
            <p>融合 Keyword 与 Vector 候选，兼顾精确标识符和语义召回。</p>
            <div className="version-metric"><span>TARGET</span><strong>Hybrid</strong></div>
            <div className="version-state">○ PLANNED</div>
          </article>
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
          <p>组件用小接口解耦，外部模型失败不会破坏离线测试和基线复现。</p>
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
        <div><span>NEXT ITERATION</span><h2>从“检索到相关内容”<br />走向“知道何时不该回答”</h2></div>
        <div className="next-list">
          <p><b>01</b><span>表格与代码块原子切分</span><em>STRUCTURE</em></p>
          <p><b>02</b><span>Parent / Child Chunk</span><em>CONTEXT</em></p>
          <p><b>03</b><span>Answerability Gate</span><em>REFUSAL</em></p>
          <p><b>04</b><span>Hybrid Retrieval + RRF</span><em>V3</em></p>
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
