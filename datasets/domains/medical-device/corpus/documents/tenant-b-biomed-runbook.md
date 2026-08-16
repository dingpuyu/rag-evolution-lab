# Tenant B 生物医学工程测试 Runbook

> 合成租户私有资料，只用于权限隔离测试。

Tenant B 的测试资产标签前缀为 `SH-TRAIN-LAB`，资产同步连接器为 `biomed-stage-b`。只有 Tenant B 管理员可以读取该连接器名称。

测试资产命中 FC-2026-04 时，把内部工单路由到队列 `tenant-b-inspection`。不得向其他租户返回资产标签、连接器或队列名称。
