import assert from "node:assert/strict";
import { access, readFile } from "node:fs/promises";
import test from "node:test";

const projectRoot = new URL("../", import.meta.url);

async function render(path = "/") {
  const workerUrl = new URL("../dist/server/index.js", import.meta.url);
  workerUrl.searchParams.set("test", `${process.pid}-${Date.now()}`);
  const { default: worker } = await import(workerUrl.href);
  return worker.fetch(
    new Request(`http://localhost${path}`, { headers: { accept: "text/html" } }),
    { ASSETS: { fetch: async () => new Response("Not found", { status: 404 }) } },
    { waitUntil() {}, passThroughOnException() {} },
  );
}

test("server-renders the customer portal entrypoint", async () => {
  const response = await render("/portal");
  assert.equal(response.status, 200);
  const html = await response.text();
  assert.match(html, /<title>RAG Desk/);
  assert.match(html, /正在连接 RAG 服务/);
  assert.match(html, /portal-loading/);
});

test("server-renders the RAG experiment dashboard", async () => {
  const response = await render();
  assert.equal(response.status, 200);
  assert.match(response.headers.get("content-type") ?? "", /^text\/html\b/i);

  const html = await response.text();
  assert.match(html, /<title>RAG Evolution Lab/);
  assert.match(html, /可证明的工程实验/);
  assert.match(html, /0\.762/);
  assert.match(html, /0\.900/);
  assert.match(html, /Metadata Filter/);
  assert.match(html, /Qwen3-Embedding-4B/);
  assert.match(html, /0\.850/);
  assert.match(html, /Qwen3 Hybrid \+ Metadata/);
  assert.match(html, /Consensus Gate/);
  assert.match(html, /SEMANTIC/);
  assert.match(html, /v4-ollama-router/);
  assert.match(html, /Tenant Gate \+ Consensus/);
  assert.match(html, /8 \/ 8 passed/);
  assert.match(html, /20 → 9/);
  assert.match(html, /从 Query Embedding 到 Milvus Top-K/);
  assert.match(html, /HNSW · COSINE/);
  assert.match(html, /Scalar Predicate/);
  assert.match(html, /Unauthorized Retrievals/);
  assert.match(html, /亲手观察 HNSW 的“近似”意味着什么/);
  assert.match(html, /FLAT GROUND TRUTH/i);
  assert.match(html, /100,000/);
  assert.match(html, /SEARCH EF/);
  assert.match(html, /TRUSTED IDENTITY BOUNDARY/);
  assert.match(html, /签发演示 JWT/);
  assert.match(html, /SECURITY AUDIT TRAIL/);
  assert.match(html, /POSTGRESQL CONTROL PLANE/);
  assert.match(html, /创建并写入 PostgreSQL/);
  assert.match(html, /TRUSTED MEMBERSHIP/);
  assert.match(html, /INCREMENTAL KNOWLEDGE LIFECYCLE/);
  assert.match(html, /UPSERT 当前版本/);
  assert.match(html, /DELETE 并验证/);
  assert.match(html, /EMBEDDING VERSION/);
  assert.doesNotMatch(html, /codex-preview|Your site is taking shape/);
});

test("removes starter preview assets and includes social preview", async () => {
  const [page, layout, packageJson] = await Promise.all([
    readFile(new URL("../app/page.tsx", import.meta.url), "utf8"),
    readFile(new URL("../app/layout.tsx", import.meta.url), "utf8"),
    readFile(new URL("../package.json", import.meta.url), "utf8"),
  ]);

  assert.match(page, /version_003/);
  assert.match(page, /access_002/);
  assert.match(layout, /openGraph/);
  assert.doesNotMatch(packageJson, /react-loading-skeleton/);
  await access(new URL("../public/og.png", import.meta.url));
  await assert.rejects(access(new URL("app/_sites-preview", projectRoot)));
});
