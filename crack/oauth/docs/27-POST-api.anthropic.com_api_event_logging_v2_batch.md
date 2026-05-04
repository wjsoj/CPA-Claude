# 27. POST https://api.anthropic.com/api/event_logging/v2/batch

**阶段**：Telemetry **状态码**：200 **请求大小**：32529 B **响应大小**：57 B

**用途**：中段 telemetry。16 条事件。

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
| content-length | 32529 |
| connection | close |

## 请求体

- **Content-Type**：`application/json`
- **Content-Length**：`32529` B（解码后实际 `32529` B）
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| events | array[16] | [object{2}, ...] |

## 响应头（共 16 个）

| Header | Value |
|---|---|
| date | Sun, 03 May 2026 15:29:11 GMT |
| content-type | application/json |
| transfer-encoding | chunked |
| connection | close |
| request-id | req_REDACTED |
| strict-transport-security | max-age=31536000; includeSubDomains; preload |
| server | cloudflare |
| x-envoy-upstream-service-time | 14 |
| content-encoding | gzip |
| vary | Accept-Encoding |
| server-timing | x-originResponse;dur=17 |
| set-cookie | _cfuvid=REDACTED; HttpOnly; SameSite=None; Secure; Path=/; Domain=api.anthropic.com |
| x-robots-tag | none |
| cf-cache-status | DYNAMIC |
| content-security-policy | default-src 'none'; frame-ancestors 'none' |
| cf-ray | REDACTED-cf-ray |

## 响应体

- **Content-Type**：`application/json`
- **解码后大小**：`40` B
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| accepted_count | int | 16 |
| rejected_count | int | 0 |

---
_原始 JSON_：[`rows/27-POST-api.anthropic.com_api_event_logging_v2_batch.json`](../rows/27-POST-api.anthropic.com_api_event_logging_v2_batch.json)
