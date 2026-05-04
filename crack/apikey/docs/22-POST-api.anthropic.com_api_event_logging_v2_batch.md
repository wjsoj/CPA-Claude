# 22. POST https://api.anthropic.com/api/event_logging/v2/batch

**阶段**：Telemetry **状态码**：200 **请求大小**：34640 B **响应大小**：57 B

**用途**：中段 telemetry 19 条。

## 请求行

```
POST https://api.anthropic.com/api/event_logging/v2/batch
```

## 请求头（共 8 个）

| Header | Value |
|---|---|
| accept | application/json, text/plain, */* |
| accept-encoding | gzip, br |
| content-type | application/json |
| user-agent | claude-code/2.1.126 |
| x-service-name | claude-code |
| host | api.anthropic.com |
| content-length | 34640 |
| connection | close |

## 请求体

- **Content-Type**：`application/json`
- **Content-Length**：`34640` B（解码后实际 `34640` B）
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| events | array[19] | [object{2}, ...] |

## 响应头（共 16 个）

| Header | Value |
|---|---|
| date | Sun, 03 May 2026 15:54:42 GMT |
| content-type | application/json |
| transfer-encoding | chunked |
| connection | close |
| request-id | req_011CafuYPm3zpZrkwmTTfBxm |
| strict-transport-security | max-age=31536000; includeSubDomains; preload |
| server | cloudflare |
| x-envoy-upstream-service-time | 13 |
| content-encoding | gzip |
| vary | Accept-Encoding |
| server-timing | x-originResponse;dur=26 |
| cf-cache-status | DYNAMIC |
| set-cookie | _cfuvid=REDACTED; HttpOnly; SameSite=None; Secure; Path=/; Domain=api.anthropic.com |
| content-security-policy | default-src 'none'; frame-ancestors 'none' |
| x-robots-tag | none |
| cf-ray | 9f60721dfe0cd4d9-LAX |

## 响应体

- **Content-Type**：`application/json`
- **解码后大小**：`40` B
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| accepted_count | int | 19 |
| rejected_count | int | 0 |

---
_原始 JSON_：[`rows/22-POST-api.anthropic.com_api_event_logging_v2_batch.json`](../rows/22-POST-api.anthropic.com_api_event_logging_v2_batch.json)
