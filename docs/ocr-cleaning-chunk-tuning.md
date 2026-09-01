# 扫描文档 OCR、洗料与 Chunk/Overlap 调参实验

## 1. 目标

复杂知识库的难点不是“把 PDF 转成字符串”，而是保证以下链路可以被验证：

```text
扫描件 → 版面/OCR → Document IR → 洗料 → 结构化 Chunk
→ Embedding/Milvus → Golden Cases → Bad Case 修复 → 新索引发布
```

本阶段选择百度开源 PaddleOCR 的 PP-StructureV3 作为首个 OCR 实现。它不只做文字识别，还提供版面区域、阅读顺序、表格等结构信息；项目通过独立 Worker 接入，主 Parser 和索引服务只依赖 Document IR，不绑定具体 OCR 引擎。官方能力与配置参见 [PP-StructureV3 使用文档](https://github.com/PaddlePaddle/PaddleOCR/blob/main/docs/version3.x/pipeline_usage/PP-StructureV3.en.md)。

## 2. 已完成的真实 OCR 与版面实验

测试环境：Apple M1 Pro、32 GB，隔离 Python 3.12 环境；`PaddlePaddle 3.3.1 + PaddleOCR 3.7.0`。Linux ARM64 容器中的 `PaddlePaddle 3.2.2` 在 PPStructureV3 初始化阶段实际发生 SIGSEGV，因此这套组合没有被标记为可发布。当前 Mac 验收让容器内 Parser 通过 `host.docker.internal` 调用宿主机 Worker；生产部署必须在目标 x86_64 镜像验证新版 wheel，或切换到云 OCR/独立 OCR 节点，不能把本地成功等同于容器可移植。

轻量探针配置：

```text
layout       PP-DocLayout-S
text detect  PP-OCRv5_mobile_det
text rec     PP-OCRv5_mobile_rec
table        off（本次只验证主 OCR 链路）
formula      off
chart        off
```

输入是一页由图片构成的合成扫描 PDF，不含可提取文本。原文包含：

```text
AED 设备故障排查
型号：BeneHeart C2
错误码：BAT-LOW-021
处理：连接交流电源并检查电池状态。
如报警持续，请联系授权服务人员。
```

第一次生成页混用了 20/18pt 字号且首行间距不同。OCR 的字符识别能看到关键标识符，但 PP-DocLayout-S 没有把完整处理行输出为版面 Block，整页 CER 为 `0.3649`。逐块对照后只改变测试输入的排版，将正文统一为 18pt、行距统一为 60pt；同一 OCR 模型恢复 5/5 个 Block，整页 CER 降为 `0`，平均 confidence 为 `0.977630`，页码、bbox、页面宽高进入 Document IR。独立的三段式 `medical-ocr-smoke` 探针平均 confidence 为 `0.988292`，两者输入不同，报告不混用。

这个实验说明：OCR 问题必须拆成字符识别与 Layout 输出两层。高 confidence 不能证明没有漏行，单纯调回答 Prompt 更不可能补回缺失证据。后续数据集需要同时校验型号、错误码、版本、批次、整页 CER、Block 数和阅读顺序。

## 3. 工程边界

### 为什么独立 OCR Worker

- Paddle 运行时、模型和 OpenCV 依赖较重，不进入常驻 Parser 镜像。
- Worker 可以单独扩容、限并发和使用 GPU，Parser 保持无状态。
- 内部接口返回 Document IR；以后替换为 PaddleOCR-VL、云 OCR 或其他引擎，不改上传、MinIO、Milvus 和 Agent。
- OCR 未配置或调用失败时仍返回 `ocr_required`，不允许空文档进入索引。
- 任一 OCR Block 低于 `0.60` 时返回 `review_required`；当前上传链路对所有非 `ready` 状态阻止发布。

启动可选 OCR Profile：

```bash
make medical-ocr-up
make medical-ocr-smoke
```

首次请求会下载官方模型并写入独立缓存。`PADDLEOCR_USE_TABLE=true` 会启用表格结构识别，模型与内存开销明显高于轻量探针，应在目标部署机器上单独压测。Apple Silicon 上的 Docker 组合目前有上述运行时崩溃，`make medical-ocr-smoke` 只有在 Worker 真正可用时才算通过，不能只看容器 `running`。

如果国内机器构建镜像时 Debian 官方源过慢，可仅在本机 `.env` 配置 `PADDLEOCR_DEBIAN_MIRROR=mirrors.aliyun.com`；这只是镜像构建源，不涉及模型密钥，默认仍保留 Debian 官方源。

## 4. 已实现洗料规则

洗料不是删除得越多越好。当前只启用可解释、可回归的规则：

1. 清除 zero-width、BOM、soft hyphen、NBSP 和重复空白。
2. 只对 ASCII 单词处理跨行连字符，例如 `inter-\nface → interface`；不改中文、型号、错误码和版本。
3. 同一短文本在至少 3 个不同页面的顶部/底部重复时，判定为页眉页脚并移除。
4. 页边缘的纯页码单独移除。
5. 文本、页面和 bbox 都相同且 IoU ≥ 0.80 的相邻 Block 才去重；不做模糊文本去重，避免误删相似型号细则。
6. 表格和代码保留换行；段落才合并视觉折行。
7. 洗料报告记录输入/输出 Block、标准化数量、页眉页脚移除量、重叠去重量、低置信度数量和平均 confidence。

明确未做：自动改正型号、自动合并相似段落、LLM 改写原文。这些操作会破坏证据可审计性，只能作为带原文对照的人工修订层。

## 5. Chunk Size 与 Overlap 到底怎么设

`overlap` 是相邻 Child Chunk 重复的字符数，目的是减少答案跨切分边界时的语义丢失。它不是越大越好：Overlap 会增加 Embedding 数量、索引体积、重复召回和 Rerank 成本，还可能导致同一文档占满 Top-K。

### 当前真实结论

| 使用位置 | 参数 | 已有证据 | 结论边界 |
| --- | --- | --- | --- |
| 39 份短销售摘要离线实验 | `350/60` | Regression MRR `0.950`，优于 `500/80、700/0、900/120` | 只适用于短摘要 |
| 在线 Document IR | `700/80` | 165 Chunk，Customer Agent v5 `90/90` | 当前在线受控语料已回归 |
| 长操作单元 Development | `700/80` 候选 | 同一实际 Artifact 上 `400/100` 为 3/4，`700/80` 为 4/4 | 仅 4 条开发集晋级，不是生产最优值 |

`700/80` 的 Overlap Ratio 是 `11.4%`；`350/60` 是 `17.1%`。项目网页现在允许修改 `max_runes/overlap_runes` 并预览：

- Parent/Child 数量；
- 每个 Child 的标题、页码和来源范围；
- Overlap Ratio；
- `indexed runes / unique parent runes`，用于观察重复文本造成的 Embedding 放大倍数。

### 长说明书下一轮候选矩阵

这组值是待验证候选，不是生产结论：

| 文档类型 | 候选 Child / Overlap | 结构策略 |
| --- | --- | --- |
| 中文说明书段落 | `400/60、600/80、800/100、1000/120` | 标题为 Parent，Child 不跨章节/页码 |
| 错误码/故障步骤 | `300/40、500/60` | 错误码和完整步骤不得被拆开 |
| 表格 | 不使用普通字符 overlap | 按语义行切，重复表头；合并单元格保留坐标 |
| 版本兼容矩阵 | 一行或一组同型号记录一个 Chunk | 型号、版本上下限作为 Metadata 与正文双锚点 |
| OCR 扫描页 | `400/60、600/80` | 先按版面和标题分块，不跨页重叠 |

10%–15% 只能作为初始搜索区间。首轮受控单变量实验固定 OCR、Cleaner 与数据快照，`400/100` 因长步骤跨边界导致 Answer Span Containment `0.75`，`700/80` 达到 `1.00`；Embedding 放大倍数同时由 `1.0734` 降至 `1.0299`。这只是 Development 候选晋级证据，仍需 Holdout、Regression 和真实检索验证；安全、租户隔离、错型号、错版本和引用越界继续是零容忍门禁。

## 6. 无索引 Artifact 导出契约

为了让独立 `agent-evaluation` 使用真实 Parser，而不把测试文档污染生产知识库，RAG API 新增：

```http
POST /api/v1/datasets/{dataset_id}/documents/evaluation-artifacts
Content-Type: multipart/form-data

case_id=<golden case id>
max_runes=700
overlap_runes=80
file=<synthetic or authorized test file>
```

接口复用服务端 Dataset/Tenant/Role 授权，只允许所属租户管理员或平台管理员访问。它运行 Parser、Cleaner 和 Chunker，返回服务端确认的 `dataset_id`、`Document IR v4`、删除审计、Chunk、耗时与配置 SHA-256；响应固定 `indexed=false`，不会保存原件、创建异步 Job、调用 Embedding、写 PostgreSQL/MinIO/Milvus，也不会伪造 Retrieval 结果。

本地真实导出：

```bash
OUTPUT=../agent-evaluation/data/document-quality/artifacts-700-80.json \
MAX_RUNES=700 OVERLAP_RUNES=80 make document-quality-export
```

脚本动态生成扫描 PDF、重复页眉页脚 PDF、XLSX 兼容矩阵和长 Markdown。账号密码只从运行环境读取，Artifact 目录在评测仓库中默认忽略，避免把访问令牌或目标系统文档提交到 Git。

## 7. 每次实验必须记录什么

质量：

- Hit@5、MRR、NDCG@5；
- CorrectModel@5、CorrectVersion@5、WrongModelRate；
- 页码/工作表/标题路径引用准确率；
- OCR 关键字段 Exact Match；
- Answer/Clarify/Refuse 决策准确率。

成本与运行：

- 文档数、Block 数、Parent/Child 数；
- 总 Embedding 字符/Token、Embedding 放大倍数；
- 同文档占用 Top-K 的比例；
- Parser/OCR/Embedding/Rerank P50/P95；
- 模型与参数指纹。

一次只改一类变量。Chunk 参数变化会改变 Chunk ID 和向量，不允许原地覆盖当前 Collection；应新建版本化 Collection，回归通过后再切 Alias，并保留回滚版本。

## 8. 下一轮数据计划

1. 准备 10～20 份公开长说明书样本，保留完全扫描、数字文本、混合页三种类型。
2. 人工标注至少 80 条跨页、表格、相似型号、版本和错误码 Case。
3. 加入旋转、倾斜、低 DPI、印章、水印、双栏和跨页表格扰动。
4. 单独评测 OCR 字段准确率与 RAG 检索准确率，避免把两层故障混成一个分数。
5. 将真实 Bad Case 归因到 `OCR / layout / cleaning / chunk / retrieval / rerank / answer` 后再选择修复点。

这样面试时可以诚实地说明：已经跑通开源 OCR、清洗审计与受鉴权的无索引评测链路；发现过版面漏行和 ARM64 容器崩溃，且用分层指标区分算法质量与运行故障。`700/80` 目前只通过 Development，不把小样本候选冒充生产最优参数。

配套 `agent-evaluation` 已能读取实际 Artifact，显式选择 `ocr,layout,cleaning,chunk` 层并对同一 Snapshot 做 Baseline/Candidate 晋级决策；没有执行的 Retrieval/Safety 不计作通过。第一轮完整结果见同级项目 `agent-evaluation/docs/document-quality-phase-b-report.md`。下一阶段才接真实临时 Collection，继续验证 Retrieval、Holdout 与 Regression，并始终只给出人工发布建议。
