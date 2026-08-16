# Medical Device Synthetic Domain

这是面向复杂知识检索测试的**合成医疗设备知识域**。

- 制造商 `MediAxis`、产品系列 `PulseCare`、型号、设备标识、固件版本、错误码、批次和操作路径全部虚构；
- 资料只用于验证 Parser、元数据过滤、混合检索、Rerank、权限和版本冲突；
- 资料不是医疗建议、真实设备说明书或临床操作指南，不得用于真实设备、患者照护或采购决策；
- 测试语料不包含真实患者信息、真实机构信息或真实设备序列号。

## 设计的检索难点

1. `VSM-100`、`VSM-100 Pro`、`VSM-200` 名称高度相似；
2. VSM-100 的 2.4 与 2.6 菜单路径不同；
3. 相同错误码 `SYS-NET-042` 在不同型号上的含义不同；
4. 配件名称相似，但兼容型号和最低版本不同；
5. 现场更正通知只覆盖特定型号、固件区间和虚构批次；
6. 历史工单包含过期建议和 Prompt Injection；
7. 两个租户的内部生物医学工程 Runbook 只能由本租户管理员检索；
8. 未提供型号的问题应当澄清，而不是在多个型号之间猜测。

## 运行

```bash
RAGLAB_DATASET_DOMAIN=medical-device go run ./cmd/raglab validate --split development
RAGLAB_DATASET_DOMAIN=medical-device go run ./cmd/raglab eval --pipeline v0-keyword --split development
RAGLAB_DATASET_DOMAIN=medical-device go run ./cmd/raglab eval --pipeline v5-rerank --split development
RAGLAB_DATASET_DOMAIN=medical-device go run ./cmd/raglab eval --pipeline v5-rerank --split regression
# 或一次验证并运行 21 条 Development + 19 条 Regression：
make medical-eval-all
```

首轮结果与两个刻意保留的 Bad Case 见 [Development Baseline](eval/baseline-2026-08-16.md)。
