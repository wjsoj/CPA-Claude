# 02. GET https://api.anthropic.com/api/claude_cli/bootstrap

**阶段**：启动期 bootstrap **状态码**：200 **请求大小**：0 B **响应大小**：279 B

**用途**：CLI 引导：返回模型映射 + **完整 OAuth 账户信息**（account_uuid/email/organization_uuid/...）。即便客户端配置了三方 API，这条仍然走 OAuth 跑到 anthropic.com 拿账户元信息。

## 请求行

```
GET https://api.anthropic.com/api/claude_cli/bootstrap
```

## 请求头（共 8 个）

| Header | Value |
|---|---|
| accept | application/json, text/plain, */* |
| accept-encoding | gzip, br |
| authorization | Bearer sk-ant-oat01-REDACTED |
| content-type | application/json |
| user-agent | claude-code/2.1.126 |
| anthropic-beta | oauth-2025-04-20 |
| host | api.anthropic.com |
| connection | close |

## 请求体

_无_

## 响应头（共 17 个）

| Header | Value |
|---|---|
| date | Sun, 03 May 2026 15:53:52 GMT |
| content-type | application/json |
| transfer-encoding | chunked |
| connection | close |
| request-id | req_011CafuUjXaRYceXN9PWRSop |
| strict-transport-security | max-age=31536000; includeSubDomains; preload |
| anthropic-organization-id | 00000000-0000-0000-0000-000000000002 |
| server | cloudflare |
| x-envoy-upstream-service-time | 103 |
| content-encoding | gzip |
| vary | Accept-Encoding |
| server-timing | x-originResponse;dur=106 |
| cf-cache-status | DYNAMIC |
| set-cookie | _cfuvid=REDACTED; HttpOnly; SameSite=None; Secure; Path=/; Domain=api.anthropic.com |
| content-security-policy | default-src 'none'; frame-ancestors 'none' |
| x-robots-tag | none |
| cf-ray | 9f6070e8b8d1f7bf-LAX |

## 响应体

- **Content-Type**：`application/json`
- **解码后大小**：`478` B
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| client_data | object{1} | {kelp_forest_sonnet} |
| additional_model_options | null | null |
| additional_model_costs | null | null |
| oauth_account | object{8} | {account_uuid, account_email, organization_uuid...} |

## 字段深挖


**与 OAuth 模式 #04 的区别**：完全相同 —— **同样的 OAuth Bearer + 返回同一份 oauth_account**。说明即便 CLI 配置了三方 API key，本地 OAuth 凭据仍存在，会被 bootstrap 端点直接拿来认证。

---
_原始 JSON_：[`rows/02-GET-api.anthropic.com_api_claude_cli_bootstrap.json`](../rows/02-GET-api.anthropic.com_api_claude_cli_bootstrap.json)
