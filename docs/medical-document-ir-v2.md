# 医疗复杂文档解析与可验证引用：Document IR v4

## 1. 这次解决的不是“支持上传”，而是“证据能否定位”

生产知识库里，PDF、DOCX 和 XLSX 即使都能抽出文字，也不代表 RAG 可以可靠使用：

- DOCX 的段落和表格如果分两遍读取，表格会被统一挪到文末，继承错误的章节标题。
- XLSX 如果整张工作表只有一个 Chunk，相似型号、相似配件和多个版本会相互污染，引用也只能指向一个很大的范围。
- PDF 文本与表格来自不同解析接口，如果不按页面坐标重新排序，表格会失去它前面的章节语义。
- Document IR 转为索引文本时，如果相邻页或不同行的定位信息没有形成分块边界，最终 Citation 的页码或单元格范围可能是错的。
- 文件 SHA-256 没变不等于索引不需要重建；Parser 或 Chunker 升级后，旧索引必须能够识别并迁移。

因此这一层的目标是：**结构、定位、清洗决策和解析版本必须与文本一起贯穿 Parser → Chunk → Milvus → Knowledge Gateway → Agent Citation → Evaluation。**

## 2. 统一证据契约

Parser 当前输出 `document-ir-v4`。v4 在原有结构与来源字段之上增加 bbox、页面尺寸、OCR confidence，以及逐 Block 的清洗删除审计：

```json
{
  "block_type": "table",
  "text": "型号: VSM-100 Pro | 配件: WLM-2 | 最低固件: 3.4",
  "heading_path": ["兼容矩阵"],
  "provenance": {
    "source_file": "vsm100-compatibility-fw2.6.xlsx",
    "page": 0,
    "sheet": "兼容矩阵",
    "cell_range": "A1:E1,A4:E4"
  }
}
```

Cleaner 不再只报告“删了几块”，而是保留被删除 Block 的原文、页码、bbox 和确定性原因：

```json
{
  "cleaning_removals": [
    {
      "reason": "repeated_margin",
      "block": {
        "block_type": "paragraph",
        "text": "PulseCare Medical Devices",
        "provenance": {"page": 2}
      }
    }
  ]
}
```

允许的原因目前只有 `page_number`、`repeated_margin` 和 `overlapping_duplicate`。这样评测平台能够检查“噪声是否删掉”和“业务正文是否被误删”，而不是把 Cleaner 当成黑盒。

`A1:E1,A4:E4` 表示该证据同时依赖表头和第 4 行。只引用 `A4:E4` 会丢失列语义；引用 `A1:E4` 又会把无关的第 2、3 行错误包含进来。

索引中继续保留：

- `source_page`
- `source_sheet`
- `source_cell_range`
- `heading_path`

Agent 不自行拼装来源位置，只透传 Knowledge Gateway 返回的服务端证据。

## 3. 格式策略

### Markdown

连续表格行合并为同一个 Table Block，避免表头和数据行被拆成多个无语义片段。

### DOCX

直接遍历 Word Body XML 中的 Paragraph/Table 节点，保持真实文档顺序。表格在出现位置继承当时的完整 `heading_path`，不会被追加到文末。

### XLSX

- 按空行切分多个逻辑表格。
- 每个数据行生成一个可独立检索的 Table Block。
- 将表头转换成 `列名: 值`，让 Embedding 与 BM25 都能看到列语义。
- 使用“表头范围 + 当前数据行范围”形成精确引用。

这会增加 Chunk 数量，但能显著降低相似型号和相似版本串扰。对大表格，下一阶段还需要增加最大行数、宽表裁剪和数值列类型识别。

### PDF

- 文本块和表格分别解析后，按照页面 `y/x` 坐标重新排序。
- 表格继承它前面的章节标题。
- 标题上下文允许跨页延续。
- 每个 Block 保留真实页码。

扫描 PDF 在未配置 OCR Worker 时仍标记为 `ocr_required`，不允许静默发布空索引。可选的 PaddleOCR PP-StructureV3 Worker 已通过 Document IR 适配器接入；低置信度结果进入 `review_required` 并阻止发布。真实探针和洗料策略见 [扫描文档 OCR、洗料与 Chunk/Overlap 调参实验](ocr-cleaning-chunk-tuning.md)。

## 4. 防止定位信息在 Chunk 阶段丢失

Document IR v4 延续显式来源边界：

```text
<!-- source: page=0; sheet=兼容矩阵; range=A1:E1,A4:E4 -->
```

Chunker 遇到页码、工作表或单元格范围变化时，必须先关闭前一个 Parent Chunk，再切换来源位置。显式标记也会把旧的页码或工作表清空，避免“上一个 Block 的页码泄漏到下一个 Block”。

标题路径只输出发生变化的层级，不重复父标题；否则重复标题会污染关键词权重并制造空 Parent Chunk。

## 5. Parser 升级为什么必须触发重建

首次实现只使用源文件 SHA-256 判断是否重复。真实问题是：同一文件经过新 Parser 后，Block 顺序、表格粒度和定位信息都可能改变；同一文件的适用型号、版本或地区元数据变化后，pre-ANN filter 结果也会变化。

现在文档注册表保存：

```text
metadata.document_ir_schema_version = document-ir-v4
metadata.ingestion_metadata_sha256 = <影响检索的元数据指纹>
```

Bootstrap 只有在以下条件同时满足时才跳过：

1. 文件 SHA-256 相同；
2. 索引任务状态为 queued/running/completed；
3. `document_ir_schema_version` 与当前版本一致；
4. 标题、版本、型号、地区、权威等级等索引元数据指纹一致。

解析版本或检索范围元数据变化会创建新修订并重新 Embedding，避免“代码已经升级、线上仍使用旧 Chunk”的假升级，也避免内容不变时错误沿用旧的型号过滤范围。

## 6. 评测如何防止只验证“搜到文档”

新增三类跨格式 Golden Case：

- XLSX：要求命中兼容矩阵，并验证 `兼容矩阵!A1:E1,A4:E4`。
- DOCX：要求命中错误码资料，并验证完整标题路径。
- PDF：要求命中现场更正通知，并验证第 1 页。

评测将文档命中与来源位置分开判断。文档 ID 正确但页码、工作表、范围或标题路径错误时，结果是 `wrong_source_location`，不能通过发布门禁。

本轮首次真实回归虽然 49/49 功能用例通过，但 MRR 为 0.7838，低于原有 0.80 门禁。修复 XLSX 型号范围，并把经过审核的 Markdown/DOCX/PDF/HTML 同内容资料建成等价相关组后，第二次回归仍为 49/49，MRR 提升到 0.8394，来源定位准确率 1.0，发布门禁通过。这里没有降低指标阈值。

## 7. 面试讲法

可以按下面四步回答：

1. **问题**：多格式文档转成纯文本后能搜到，但表格行、章节和页码会错，引用不可审计。
2. **方案**：建立 Document IR，把文本、结构和 Provenance 一起传到 Milvus；XLSX 做表头感知的行级 Chunk，PDF 做坐标排序，DOCX 保持 XML 顺序。
3. **工程权衡**：行级表格增加向量数量，但换来更低的型号串扰和精确引用；扫描 PDF 通过独立 OCR Worker 接入，未配置或质量未过门禁时仍阻止发布。
4. **验证**：除了 Hit@5/MRR，还用 Golden Case 断言页码、工作表、单元格范围和标题路径；Parser 版本变化会强制重建索引。

这段经历的重点不是“调用了一个文档解析库”，而是识别并解决了 **解析正确性、索引一致性和引用可验证性** 三个生产问题。
