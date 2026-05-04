# 08. POST https://api.anthropic.com/api/event_logging/v2/batch

**阶段**：登录 — Telemetry **状态码**：200 **请求大小**：13215 B **响应大小**：56 B

**用途**：登录完成后第一批 telemetry：`tengu_oauth_started / tengu_oauth_success / tengu_init / tengu_started` 等共 9~12 条，标记账户切换的生命周期。同 #02 用 `claude-code/2.1.126` UA + `x-service-name: claude-code`。

## 请求行

```
POST https://api.anthropic.com/api/event_logging/v2/batch
```

## 请求头（共 10 个）

| Header | Value |
|---|---|
| accept | application/json, text/plain, */* |
| accept-encoding | gzip, br |
| authorization | Bearer sk-ant-oat01-REDACTED |
| content-type | application/json |
| user-agent | claude-code/2.1.126 |
| anthropic-beta | oauth-2025-04-20 |
| x-service-name | claude-code |
| host | api.anthropic.com |
| content-length | 13215 |
| connection | close |

## 请求体

- **Content-Type**：`application/json`
- **Content-Length**：`13215` B（解码后实际 `13225` B）
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| events | array[8] | [object{2}, ...] |

## 响应头（共 16 个）

| Header | Value |
|---|---|
| date | Mon, 04 May 2026 02:09:12 GMT |
| content-type | application/json |
| transfer-encoding | chunked |
| connection | close |
| request-id | req_REDACTED |
| strict-transport-security | max-age=31536000; includeSubDomains; preload |
| server | cloudflare |
| x-envoy-upstream-service-time | 95 |
| content-encoding | gzip |
| vary | Accept-Encoding |
| server-timing | x-originResponse;dur=97 |
| set-cookie | _cfuvid=REDACTED; HttpOnly; SameSite=None; Secure; Path=/; Domain=api.anthropic.com |
| x-robots-tag | none |
| cf-cache-status | DYNAMIC |
| content-security-policy | default-src 'none'; frame-ancestors 'none' |
| cf-ray | REDACTED-cf-ray |

## 响应体

- **Content-Type**：`application/json`
- **解码后大小**：`39` B
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| accepted_count | int | 8 |
| rejected_count | int | 0 |

---
_原始 JSON_：[`rows/08-POST-api.anthropic.com_api_event_logging_v2_batch.json`](../rows/08-POST-api.anthropic.com_api_event_logging_v2_batch.json)
