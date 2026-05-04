# 01. GET https://api.anthropic.com/api/claude_code_penguin_mode

**阶段**：启动期 bootstrap **状态码**：200 **请求大小**：0 B **响应大小**：75 B

**用途**：'penguin mode'（额度溢出付费）开关。**仍然带 OAuth Bearer** —— 因为用户本机同时登录了 OAuth，CLI 默认用它打这条 anthropic.com 端点。

## 请求行

```
GET https://api.anthropic.com/api/claude_code_penguin_mode
```

## 请求头（共 7 个）

| Header | Value |
|---|---|
| accept | application/json, text/plain, */* |
| accept-encoding | gzip, br |
| authorization | Bearer sk-ant-oat01-REDACTED |
| user-agent | axios/1.13.6 |
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
| request-id | req_REDACTED |
| strict-transport-security | max-age=31536000; includeSubDomains; preload |
| anthropic-organization-id | 00000000-0000-0000-0000-000000000002 |
| server | cloudflare |
| x-envoy-upstream-service-time | 90 |
| content-encoding | gzip |
| vary | Accept-Encoding |
| server-timing | x-originResponse;dur=92 |
| set-cookie | _cfuvid=REDACTED; HttpOnly; SameSite=None; Secure; Path=/; Domain=api.anthropic.com |
| x-robots-tag | none |
| cf-cache-status | DYNAMIC |
| content-security-policy | default-src 'none'; frame-ancestors 'none' |
| cf-ray | REDACTED-cf-ray |

## 响应体

- **Content-Type**：`application/json`
- **解码后大小**：`58` B
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| enabled | bool | false |
| disabled_reason | string | "extra_usage_disabled" |

---
_原始 JSON_：[`rows/01-GET-api.anthropic.com_api_claude_code_penguin_mode.json`](../rows/01-GET-api.anthropic.com_api_claude_code_penguin_mode.json)
