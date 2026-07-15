# API 限流

默认 API 配额为每个租户每分钟 600 次请求。企业版可以申请提升至每分钟 3,000 次。

## 响应头

每个响应都会返回 `X-RateLimit-Limit`、`X-RateLimit-Remaining` 和 `X-RateLimit-Reset`。其中 `X-RateLimit-Reset` 表示下一次配额重置的 Unix 时间戳。

## 重试

收到 HTTP 429 后应使用指数退避。首次等待 1 秒，之后依次等待 2、4、8 秒，单次等待不超过 30 秒，并加入随机抖动。
