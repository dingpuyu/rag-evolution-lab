# SSO 故障排查

## 回调失败

收到 `redirect_uri_mismatch` 时，先核对身份提供商中登记的回调地址、协议、域名、端口和路径。回调地址必须与身份中心配置完全一致，并且使用 HTTPS。

## 用户无法登录

先确认用户邮箱域名已经通过验证，组织管理员已启用该域名，并检查用户是否被身份提供商分配到 AcmeCloud 应用。不要通过修改用户角色绕过域名校验。

## 断言与时钟

SAML 断言的 `Audience` 必须是当前组织的 Service Provider ID，`NotBefore` 和 `NotOnOrAfter` 由双方时钟决定。服务器时钟偏差超过 120 秒时应先同步 NTP。

## 安全处理

排查时只记录 trace_id、错误码和脱敏后的 subject，不要保存 SAML 断言、OIDC refresh token 或完整 Authorization 响应。
