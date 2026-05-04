# 25. POST https://api.anthropic.com/api/event_logging/v2/batch

**阶段**：Telemetry **状态码**：200 **请求大小**：16999 B **响应大小**：56 B

**用途**：中段 telemetry。8 条事件，全是工具调用相关：`tengu_tool_use_granted_in_config/tengu_tool_use_can_use_tool_allowed/tengu_tool_use_progress/chrome_bridge_connection_started/tengu_prompt_cache_diagnosis_received/...`

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
| content-length | 16999 |
| connection | close |

## 请求体

- **Content-Type**：`application/json`
- **Content-Length**：`16999` B（解码后实际 `16999` B）
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| events | array[8] | [object{2}, ...] |

## 响应头（共 16 个）

| Header | Value |
|---|---|
| date | Sun, 03 May 2026 15:29:00 GMT |
| content-type | application/json |
| transfer-encoding | chunked |
| connection | close |
| request-id | req_011CafsahR1bUtkvUMRNoHRL |
| strict-transport-security | max-age=31536000; includeSubDomains; preload |
| server | cloudflare |
| x-envoy-upstream-service-time | 47 |
| content-encoding | gzip |
| vary | Accept-Encoding |
| server-timing | x-originResponse;dur=51 |
| set-cookie | _cfuvid=REDACTED; HttpOnly; SameSite=None; Secure; Path=/; Domain=api.anthropic.com |
| x-robots-tag | none |
| cf-cache-status | DYNAMIC |
| content-security-policy | default-src 'none'; frame-ancestors 'none' |
| cf-ray | 9f604c77083dde8b-LAX |

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
_原始 JSON_：[`rows/25-POST-api.anthropic.com_api_event_logging_v2_batch.json`](../rows/25-POST-api.anthropic.com_api_event_logging_v2_batch.json)
