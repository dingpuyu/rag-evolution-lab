# 真实文档上传与知识状态工作台

## 目标

`/medical` 的“知识资料”不再只是一个文件选择框。它把一份真实文件从进入系统到可被 Agent 检索的全过程显示出来，便于管理员判断问题发生在解析、向量化、索引还是发布阶段。

当前支持 Markdown、HTML、PDF、DOCX 和 XLSX。专业运维测试资料仍为完全虚构内容；这里的“真实文档”指真实文件格式、真实解析器和真实异步索引链路，不表示资料可用于真实医疗设备操作。

## 页面体验

管理员选择自己有管理权限的数据集后，可以：

1. 上传原件，并填写文档 ID、源修订号、型号、软件版本、权威级别、来源类型、采集日期和审核状态。
2. 立即预览 Parser 生成的 Document IR，包括标题路径、页码、Sheet 和 Cell Range。
3. 在“最近接入与索引状态”查看正在处理、失败和已完成的文档。
4. 打开“查看链路”，观察七个持久化阶段：

```text
原件保存 → Document IR → 结构化切块 → Qwen Embedding
→ Milvus 索引 → 写后验证 → 可检索
```

5. 刷新页面后仍可读取同一份解析预览和处理状态；这些信息不是前端临时状态。

页面只在存在活动任务时轮询，任务完成后停止，避免空闲页面持续请求 API。

## 状态从哪里来

详情接口：

```http
GET /api/v1/datasets/{dataset_id}/documents/detail
    ?document_id={document_id}
    &source_revision={positive_integer}
    &preview_limit={1..200}
```

接口在服务端聚合四类状态：

| 来源 | 保存内容 | 解决的问题 |
|---|---|---|
| PostgreSQL | 文档、修订、元数据、解析状态、Job ID | 可查询的业务事实与版本历史 |
| Durable Ingestion Job | 当前阶段、失败阶段、尝试次数、结果 | Worker 重启后仍可定位失败 |
| MinIO | 原始文件与当前 `document-ir-v4` | 页面刷新后仍能预览解析结果及 Cleaner 删除审计 |
| Milvus Catalog | 当前发布集合是否能看到该文档 | 避免“任务完成但实际搜不到” |

只有 Worker 报告写入成功还不够。“可检索”阶段必须通过 Catalog 可见性检查；写后验证也单独展示，避免把排队完成误当成索引发布完成。

## 权限边界

上传和详情预览都属于管理面能力，因为 Document IR 可能包含尚未发布、私有或待审核的源内容：

- `platform_admin` 可以管理其可见的数据集；
- 租户 `admin` 只能管理本租户的私有数据集；
- 租户管理员不能向公共销售资料库直接写入；
- `viewer` 只能通过被授权的 Agent/RAG 数据面使用已发布知识，不能读取源文档处理详情；
- 访问另一个租户的数据集在 Dataset 授权边界即被拒绝，不依赖前端隐藏按钮。

公共销售资料还要求 `source_review_status=approved`，并校验 HTTPS 来源 URL。客户端不允许自行指定 Tenant、物理 Collection 或 Milvus Filter。

## 可重复验收

先启动并导入医疗测试语料：

```bash
make medical-up
make medical-bootstrap
make medical-smoke
```

`medical-smoke` 会额外验证：

- 管理员能读取自己私有数据集的文档详情；
- 七个阶段全部完成且进度为 100%；
- Document IR 可从 MinIO 重新读取；
- 文档确实出现在可检索 Catalog；
- 另一租户无法读取该详情。

2026-08-18 的本地真实链路验收使用 `vsm100-error-codes-fw2.6.docx`：Parser 生成 5 个结构块，异步任务写入 2 个 Milvus Chunk，Qwen `text-embedding-v4` 1024 维向量化、写后验证和 Catalog 可见性均通过。这个数字是一次可复现验收记录，不是生产容量指标。

## 这次实现暴露并修复的问题

1. **只显示上传请求里的解析预览**：刷新后丢失。现在从 MinIO 读取持久化 Document IR。
2. **只看 Job completed**：无法证明 Agent 真能检索。现在增加 Milvus Catalog 可见性阶段。
3. **失败后统一显示 failed**：不知道失败发生在哪一步。Job 持久化 `failure_stage`，页面阻塞后续阶段并突出真实失败点。
4. **公共数据集显示上传表单**：租户管理员容易误以为自己能写公共知识。页面按服务端同一权限规则展示只读说明，API 继续强制授权。
5. **客户端携带演示密码**：不利于可移植部署。页面只填充演示账号邮箱，密码由部署环境生成并由操作者输入。

## 后续演进

- Parser 完全异步化，支持超大文件和断点重试；
- OCR 队列与人工版面复核；
- 同一文档多修订差异视图已完成；下一步补充基于差异的审核、发布与回滚操作；
- 应用级 Chunk 检索试跑已完成，可查看 Rewrite、Rerank、授权绑定、排名和引用位置；
- 文档级质量卡：Golden Case 覆盖率、引用正确率和最近 Bad Case。

修订差异算法、正式 Gateway 复用方式和可复现 R1/R2 样本见[文档修订差异与检索验证工作台](document-revision-retrieval-validation.md)。
