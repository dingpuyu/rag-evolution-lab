# 服务状态确认

当多个用户同时出现相同故障时，先检查状态页 `status.acmecloud.example`。状态页按 Identity、Reports、Storage 和 API Gateway 分模块显示。

## 故障判断

状态为 Degraded 表示部分请求失败；Outage 表示服务不可用。若状态页正常且仅单个租户受影响，应继续检查租户配置、套餐和权限。

## 事件编号

已确认的平台事件会生成 `INC-` 开头的编号。没有事件编号时不能直接断言平台发生全局故障。
