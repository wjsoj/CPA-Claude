# 02. POST https://api.anthropic.com/api/event_logging/v2/batch

**阶段**：启动 / Telemetry **状态码**：200 **请求大小**：30315 B **响应大小**：57 B

**用途**：**上一会话的退出 telemetry**（claude-code/2.1.126，`x-service-name: claude-code`）。这是 CLI 重新拉起前残留 ipc buffer 里的事件批次，里面绝大多数是 `tengu_exit/tengu_started/tengu_init/tengu_timer` 等 lifecycle 事件 —— 跟 *本次* 登录流程无关，但跟它紧挨着发出，所以一并捕获。**注意带的是 OAuth Bearer**，说明这是上次登录态下产生的事件。

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
| content-length | 30315 |
| connection | close |

## 请求体

- **Content-Type**：`application/json`
- **Content-Length**：`30315` B（解码后实际 `30351` B）
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| events | array[18] | [object{2}, ...] |

## 响应头（共 16 个）

| Header | Value |
|---|---|
| date | Mon, 04 May 2026 02:08:51 GMT |
| content-type | application/json |
| transfer-encoding | chunked |
| connection | close |
| request-id | req_REDACTED |
| strict-transport-security | max-age=31536000; includeSubDomains; preload |
| server | cloudflare |
| x-envoy-upstream-service-time | 10 |
| content-encoding | gzip |
| vary | Accept-Encoding |
| server-timing | x-originResponse;dur=12 |
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
| accepted_count | int | 18 |
| rejected_count | int | 0 |

---
_原始 JSON_：[`rows/02-POST-api.anthropic.com_api_event_logging_v2_batch.json`](../rows/02-POST-api.anthropic.com_api_event_logging_v2_batch.json)
