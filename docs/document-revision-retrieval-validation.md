# 文档修订差异与检索验证工作台

## 为什么需要这一层

异步任务显示 `completed` 只能证明数据写入成功，不能证明知识更新正确。生产环境还要回答两个问题：

1. 新修订到底改了哪些元数据、段落、表格或来源位置？
2. 目标 Query 是否在当前应用授权、索引版本和检索策略下命中了正确 Chunk？

`/medical` 的专业模式把这两个问题放在同一个知识管理页面，但仍保持职责分离：左侧是文档治理，右侧是在线检索诊断。它不是离线评测平台；批量 Golden Case、Prompt 实验和版本比较继续由独立 `agent-evaluation` 项目负责。

## 文档修订差异

选择“最近接入与索引状态”中的一份文档后，页面按相同 `document_id` 列出 PostgreSQL 中的全部源修订，并调用：

```http
GET /api/v1/datasets/{dataset_id}/documents/diff
    ?document_id={document_id}
    &from_revision={positive_integer}
    &to_revision={positive_integer}
```

服务端不会比较前端缓存或最终 Chunk，而是从 MinIO 读取两个持久化 `document-ir-v2`，返回：

- 标题、业务版本、文件名、内容指纹、解析状态和医疗适用范围的变化；
- `added / removed / modified / unchanged` 结构块数量；
- 每个变化块的标题路径、页码或 Sheet/Cell Range；
- 修改前后的文本，最多展示 100 条变化。

差异算法先按 Block 类型与来源位置分组，再在组内匹配完全相同文本。剩余块才按顺序配成修改，避免在段落中间插入一条内容时，把后续所有段落误判为修改。

该接口与文档解析详情一样属于管理面：平台管理员可管理其可见数据集，租户管理员只能管理本租户私有数据集，普通 Viewer 不能读取未发布 Document IR。

## 应用级检索试跑

页面复用正式数据面的接口：

```http
POST /api/v1/apps/{app_id}/query
```

它不是绕过权限直接查 Milvus，也不接受客户端传入物理 Collection 或任意 Filter。服务端根据当前身份、Application、Environment 和 Knowledge Binding 解析允许的数据集，然后执行：

```text
Query Rewrite
→ 精确标识符 + Dense + BM25 多路召回
→ RRF 融合
→ Qwen Rerank
→ Top-K 证据
```

诊断结果显示 Trace ID、改写后的 Query、Embedding 模型与维度、服务端 Filter、每个绑定的索引 Collection、Rewrite/Rerank 是否生效，以及每个 Hit 的排名、召回来源、精确命中项和原文位置。该试跑不调用 DeepSeek 回答模型，因此可以独立判断问题发生在检索还是生成阶段，并减少一次无关的模型成本。

## 可复现的修订样本

`make medical-bootstrap` 会在 Tenant A 私有运维库导入同一文档 ID 的两个 DOCX 修订：

```text
vsm100-error-codes-fw2.6-revision-demo / revision 1
vsm100-error-codes-fw2.6-revision-demo / revision 2
```

R2 修改 `SYS-NET-042` 的解释，新增两个排查前置条件，并给表格增加升级条件。这样新电脑部署后不需要手工制造第二版文件，就能立即验证修订差异和目标 Query 命中。

Office 测试文件会固定文档属性和 ZIP Entry 时间戳，PDF 也固定元数据与文件 ID；连续运行生成脚本得到相同 SHA-256。Bootstrap 还按不可变业务修订 `R1/R2` 识别已存在样本，避免仅因容器重启或文件打包时间变化而产生伪修订和重复向量成本。

推荐验收步骤：

1. 以 Tenant A 管理员登录 `/medical`，切换“专业运维”和“知识资料”。
2. 选择 Tenant A 私有运维库，在最近任务中打开修订演示文档。
3. 比较 revision 1 与 revision 2，确认新增和修改块均可定位。
4. 使用 `VSM-100 软件 2.6 的 SYS-NET-042 是什么？` 运行真实检索。
5. 确认页面显示授权绑定、Qwen Embedding、Rerank、Top-K 排名，并命中选中文档。
6. 用 Tenant B 身份验证其看不到 Tenant A 文档修订，也不能通过检索命中该私有证据。

## 这次实现得到的工程经验

- **写入成功不等于知识正确**：必须把来源差异和 Query 命中都变成可观察事实。
- **版本差异应在结构化中间层完成**：直接比较二进制文件不可解释，比较最终 Chunk 又会混入切块策略噪声，Document IR 是更稳定的诊断边界。
- **检索试跑必须复用正式 Gateway**：单独写一个“调试搜索”很容易漏掉租户、角色、绑定和索引版本过滤，产生与线上不一致的结论。
- **检索和生成要分开定位**：先证明正确证据进入 Top-K，再讨论 DeepSeek 的引用、表达和拒答策略。
- **人工操作应能沉淀为回归**：单题试跑发现的问题可进入现有 Bad Case 工作台，再晋升为 `agent-evaluation` 的回归门禁。
