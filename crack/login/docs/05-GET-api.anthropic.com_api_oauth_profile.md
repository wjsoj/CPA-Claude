# 05. GET https://api.anthropic.com/api/oauth/profile

**阶段**：登录 — 账户初始化 **状态码**：200 **请求大小**：0 B **响应大小**：449 B

**用途**：拿到新 access_token 后立刻 `GET /api/oauth/profile` 拉取账户元信息（account uuid、display_name、email、has_claude_max、组织 uuid/name/类型/billing_type/rate_limit_tier、绑定的 application = Claude Code）。

## 请求行

```
GET https://api.anthropic.com/api/oauth/profile
```

## 请求头（共 7 个）

| Header | Value |
|---|---|
| accept | application/json, text/plain, */* |
| accept-encoding | gzip, br |
| authorization | Bearer sk-ant-oat01-REDACTED |
| content-type | application/json |
| user-agent | axios/1.13.6 |
| host | api.anthropic.com |
| connection | close |

## 请求体

_无_

## 响应头（共 16 个）

| Header | Value |
|---|---|
| date | Mon, 04 May 2026 02:09:03 GMT |
| content-type | application/json |
| transfer-encoding | chunked |
| connection | close |
| request-id | req_REDACTED |
| strict-transport-security | max-age=31536000; includeSubDomains; preload |
| server | cloudflare |
| x-envoy-upstream-service-time | 74 |
| content-encoding | gzip |
| vary | Accept-Encoding |
| server-timing | x-originResponse;dur=75 |
| set-cookie | _cfuvid=REDACTED; HttpOnly; SameSite=None; Secure; Path=/; Domain=api.anthropic.com |
| x-robots-tag | none |
| cf-cache-status | DYNAMIC |
| content-security-policy | default-src 'none'; frame-ancestors 'none' |
| cf-ray | REDACTED-cf-ray |

## 响应体

- **Content-Type**：`application/json`
- **解码后大小**：`784` B
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| account | object{7} | {uuid, full_name, display_name...} |
| organization | object{12} | {uuid, name, organization_type...} |
| application | object{3} | {uuid, name, slug} |

## 字段深挖


**作用**：换 token 后立刻校验账户身份并取回结构化资料。

**鉴权**：`Authorization: Bearer <new access_token>`，`User-Agent: axios/1.13.6`，**不带任何 anthropic-beta**。

**响应体**：
```json
{
  "account": {
    "uuid": "00000000-0000-0000-0000-000000000001",
    "full_name": "REDACTED_USER",
    "display_name": "REDACTED_USER",
    "email": "redacted@example.com",
    "has_claude_max": true,
    "has_claude_pro": false,
    "created_at": "2026-04-20T15:09:41.735788Z"
  },
  "organization": {
    "uuid": "00000000-0000-0000-0000-000000000002",
    "name": "redacted@example.com's Organization",
    "organization_type": "claude_max",
    "billing_type": "google_play_subscription",
    "rate_limit_tier": "default_claude_max_20x",
    "seat_tier": null,
    "has_extra_usage_enabled": false,
    "subscription_status": null,
    "subscription_created_at": "2026-05-03T10:01:19.591854Z",
    "cc_onboarding_flags": {},
    "claude_code_trial_ends_at": null,
    "claude_code_trial_duration_days": null
  },
  "application": {
    "uuid": "9d1c250a-e61b-44d9-88ed-5944d1962f5e",
    "name": "Claude Code",
    "slug": "claude-code"
  }
}
```

**给 CPA-Claude 落库的字段**：`account.uuid`（→ `auth.AccountUUID`）、`organization.uuid`（→ `auth.OrganizationUUID`）、`account.email`（→ `auth.Email`）。`account.display_name` 可以选填到 `auth.Label`。

---
_原始 JSON_：[`rows/05-GET-api.anthropic.com_api_oauth_profile.json`](../rows/05-GET-api.anthropic.com_api_oauth_profile.json)
