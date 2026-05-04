# 04. GET https://api.anthropic.com/api/claude_cli/bootstrap

**阶段**：启动期 bootstrap **状态码**：200 **请求大小**：0 B **响应大小**：279 B

**用途**：CLI 引导：返回该账户对应的模型映射、计费维度元信息、组织信息。

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
| date | Sun, 03 May 2026 15:27:58 GMT |
| content-type | application/json |
| transfer-encoding | chunked |
| connection | close |
| request-id | req_REDACTED |
| strict-transport-security | max-age=31536000; includeSubDomains; preload |
| anthropic-organization-id | 00000000-0000-0000-0000-000000000002 |
| server | cloudflare |
| x-envoy-upstream-service-time | 63 |
| content-encoding | gzip |
| vary | Accept-Encoding |
| server-timing | x-originResponse;dur=65 |
| cf-cache-status | DYNAMIC |
| set-cookie | _cfuvid=REDACTED; HttpOnly; SameSite=None; Secure; Path=/; Domain=api.anthropic.com |
| content-security-policy | default-src 'none'; frame-ancestors 'none' |
| x-robots-tag | none |
| cf-ray | REDACTED-cf-ray |

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

---
_原始 JSON_：[`rows/04-GET-api.anthropic.com_api_claude_cli_bootstrap.json`](../rows/04-GET-api.anthropic.com_api_claude_cli_bootstrap.json)
