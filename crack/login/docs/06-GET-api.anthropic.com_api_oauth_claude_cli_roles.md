# 06. GET https://api.anthropic.com/api/oauth/claude_cli/roles

**阶段**：登录 — 账户初始化 **状态码**：200 **请求大小**：0 B **响应大小**：159 B

**用途**：拉取账户在 Claude CLI 维度的角色/工作区映射：`organization_uuid + organization_role + workspace_uuid/role`。本账户为 admin、无 workspace 概念。

## 请求行

```
GET https://api.anthropic.com/api/oauth/claude_cli/roles
```

## 请求头（共 6 个）

| Header | Value |
|---|---|
| accept | application/json, text/plain, */* |
| accept-encoding | gzip, br |
| authorization | Bearer sk-ant-oat01-REDACTED |
| user-agent | axios/1.13.6 |
| host | api.anthropic.com |
| connection | close |

## 请求体

_无_

## 响应头（共 16 个）

| Header | Value |
|---|---|
| date | Mon, 04 May 2026 02:09:04 GMT |
| content-type | application/json |
| transfer-encoding | chunked |
| connection | close |
| request-id | req_REDACTED |
| strict-transport-security | max-age=31536000; includeSubDomains; preload |
| server | cloudflare |
| x-envoy-upstream-service-time | 103 |
| content-encoding | gzip |
| vary | Accept-Encoding |
| server-timing | x-originResponse;dur=106 |
| set-cookie | _cfuvid=REDACTED; HttpOnly; SameSite=None; Secure; Path=/; Domain=api.anthropic.com |
| x-robots-tag | none |
| cf-cache-status | DYNAMIC |
| content-security-policy | default-src 'none'; frame-ancestors 'none' |
| cf-ray | REDACTED-cf-ray |

## 响应体

- **Content-Type**：`application/json`
- **解码后大小**：`212` B
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| organization_uuid | string | "00000000-0000-0000-0000-000000000002" |
| organization_name | string | "redacted@example.com's Organization" |
| organization_role | string | "admin" |
| workspace_uuid | null | null |
| workspace_name | null | null |
| workspace_role | null | null |

---
_原始 JSON_：[`rows/06-GET-api.anthropic.com_api_oauth_claude_cli_roles.json`](../rows/06-GET-api.anthropic.com_api_oauth_claude_cli_roles.json)
