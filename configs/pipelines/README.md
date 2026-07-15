# Pipeline 配置

本目录保存可复现的 RAG Pipeline 版本。

规则：

- 已发布配置不得原地改变语义。
- 参数调整需要新配置名或新 Git Tag。
- Evaluation Run 必须记录配置文件 SHA。
- 单变量实验从已有配置复制为 `experiments/` 下的临时配置，不覆盖基线。
- V6/V7 配置在对应设计完成时加入。

`v2-metadata.yaml` 已有对应运行时 Pipeline；其余高级版本配置仍主要表达目标结构，配置加载和校验尚未实现。
