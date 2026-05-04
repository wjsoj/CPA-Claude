# 11. GET https://api.anthropic.com/api/claude_cli/bootstrap

**阶段**：登录 — bootstrap **状态码**：200 **请求大小**：0 B **响应大小**：279 B

**用途**：CLI 引导：返回该账户支持的模型映射（`kelp_forest_sonnet=1000000` 等）+ 完整 `oauth_account` 信息（账户/组织 uuid + 邮箱 + 名称 + 类型 + rate_limit_tier）。`claude-code/2.1.126` UA。

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
| date | Mon, 04 May 2026 02:09:33 GMT |
| content-type | application/json |
| transfer-encoding | chunked |
| connection | close |
| request-id | req_REDACTED |
| strict-transport-security | max-age=31536000; includeSubDomains; preload |
| anthropic-organization-id | 00000000-0000-0000-0000-000000000002 |
| server | cloudflare |
| x-envoy-upstream-service-time | 67 |
| content-encoding | gzip |
| vary | Accept-Encoding |
| server-timing | x-originResponse;dur=69 |
| set-cookie | _cfuvid=REDACTED; HttpOnly; SameSite=None; Secure; Path=/; Domain=api.anthropic.com |
| x-robots-tag | none |
| cf-cache-status | DYNAMIC |
| content-security-policy | default-src 'none'; frame-ancestors 'none' |
| cf-ray | REDACTED-cf-ray |

## 响应体

- **Content-Type**：`application/json`
- **解码后大小**：`472` B
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| client_data | object{1} | {kelp_forest_sonnet} |
| additional_model_options | null | null |
| additional_model_costs | null | null |
| oauth_account | object{8} | {account_uuid, account_email, organization_uuid...} |

## 字段深挖


**响应体**：
```json
{
  "client_data": {
    "kelp_forest_sonnet": "1000000"
  },
  "additional_model_options": null,
  "additional_model_costs": null,
  "oauth_account": {
    "account_uuid": "00000000-0000-0000-0000-000000000001",
    "account_email": "redacted@example.com",
    "organization_uuid": "00000000-0000-0000-0000-000000000002",
    "organization_name": "redacted@example.com's Organization",
    "organization_type": "claude_max",
    "organization_rate_limit_tier": "default_claude_max_20x",
    "user_rate_limit_tier": null,
    "seat_tier": null
  }
}
```

| Field | 含义 |
|---|---|
| client_data | 模型/特性映射，`kelp_forest_sonnet=1000000` 表示 Sonnet 1M 上下文 quota |
| additional_model_options | 该账户额外暴露的模型（本次 null） |
| additional_model_costs | 该账户的特殊定价（本次 null） |
| oauth_account.* | 同 #05 `/api/oauth/profile` 的部分字段，但 key 名加了 `account_/organization_` 前缀 —— 给 CLI bootstrap 用一份扁平化 view |

**注意**：这条与启动期 bootstrap（非登录场景下的同一端点）共用响应 schema —— 也就是说登录后端点会被打两次：登录链路里这一次（#11），以及之后正常会话每次启动都会再打一次。

---
_原始 JSON_：[`rows/11-GET-api.anthropic.com_api_claude_cli_bootstrap.json`](../rows/11-GET-api.anthropic.com_api_claude_cli_bootstrap.json)
