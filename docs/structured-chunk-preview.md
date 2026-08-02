# 结构感知切分与父子 Chunk 预览

这一步借鉴课程第 15/16 章的工程化做法：先让切分结果可检查，再进入 Embedding 和 Milvus。目标不是增加一个“漂亮的分词器”，而是把召回粒度、上下文粒度和引用定位拆开验证。

## 为什么先做预览

- 子 Chunk 适合向量召回，避免一个页面过长导致相似度被稀释。
- Parent Chunk 保留完整逻辑段落，后续可以在召回后扩展上下文，减少回答被截断。
- `source_page` 和 `heading_path` 为引用展示提供可解释证据。
- 预览不调用 Embedding、不写 Milvus，先发现切分问题，再花模型和索引成本。

## 页面标记格式

当前核心入口仍支持 Markdown/纯文本。解析器适配 PDF、Docling 或 MinerU 时，只需要把页边界转换为下面任一种标记：

```text
第一页内容
\f
第二页内容
```

或：

```markdown
第一页内容

<!-- page: 7 -->

## 第二页标题

第二页内容
```

`\f` 默认按顺序计页；显式 `<!-- page: N -->` 用于保留原始 PDF 页码。标记不会进入 Chunk 内容。

## API

```bash
curl -s http://localhost:8080/api/v1/datasets/tenant-a-operations/documents/preview \
  -H "Authorization: Bearer $TOKEN" \
  -H 'content-type: application/json' \
  -d '{
    "title":"客服手册",
    "content":"# 登录\n\n第一页说明。\\f<!-- page: 7 -->\n## 配置\n\n第二页说明。",
    "max_runes":500,
    "overlap_runes":50
  }' | jq .
```

响应包含 `parent_count`、`child_count`、`pages` 和每个 child 的 `parent_id`、`source_page`、`heading_path`、`parent_content`。它是结构验证结果，不是已写入索引的承诺。

## 网页体验

打开 `/portal` → “导入资料”，编辑标题和正文后点击“预览分块”。页面会展示：

1. parent/child 数量、页数、`max_runes/overlap_runes`；
2. 每个 child 的页码、标题路径、父 Chunk ID；
3. child 内容与 parent 内容的对照。

点击“导入并验证”才会进入现有的 `VALIDATE → CHUNK → EMBED → INDEX → VERIFY` 异步任务。

## 当前边界与下一步

本轮不改已有 Milvus Collection schema，因此现有小规模索引和回归完全兼容；页码和父内容目前在切分/预览层验证，尚未持久化到 Milvus 字段。下一步应先接入 PDF 解析器，再设计新 Collection 或版本化字段迁移，并用真实页码引用回归验证。
