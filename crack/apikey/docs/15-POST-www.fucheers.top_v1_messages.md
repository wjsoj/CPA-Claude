# 15. POST https://www.fucheers.top/v1/messages?beta=true

**阶段**：业务 **状态码**：200 **请求大小**：142986 B **响应大小**：13166 B

**用途**：工具回合 2。

## 请求行

```
POST https://www.fucheers.top/v1/messages?beta=true
```

## 请求头（共 21 个）

| Header | Value |
|---|---|
| accept | application/json |
| authorization | Bearer sk-REDACTED |
| content-type | application/json |
| user-agent | claude-cli/2.1.126 (external, cli) |
| x-claude-code-session-id | ec194dda-5172-4c55-a4d5-87e5904750cc |
| x-stainless-arch | x64 |
| x-stainless-lang | js |
| x-stainless-os | Linux |
| x-stainless-package-version | 0.81.0 |
| x-stainless-retry-count | 0 |
| x-stainless-runtime | node |
| x-stainless-runtime-version | v24.3.0 |
| x-stainless-timeout | 3000 |
| anthropic-beta | claude-code-20250219,interleaved-thinking-2025-05-14,redact-thinking-2026-02-12,context-management-2025-06-27,prompt-caching-scope-2026-01-05,advisor-tool-2026-03-01,context-1m-2025-08-07,effort-2025-… |
| anthropic-dangerous-direct-browser-access | true |
| anthropic-version | 2023-06-01 |
| x-app | cli |
| connection | keep-alive |
| host | www.fucheers.top |
| accept-encoding | gzip, br |
| content-length | 142986 |

## 请求体

- **Content-Type**：`application/json`
- **Content-Length**：`142986` B（解码后实际 `142133` B）
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| model | string | "claude-sonnet-4-6" |
| messages | array[3] | [object{2}, ...] |
| system | array[3] | [object{2}, ...] |
| tools | array[34] | [object{3}, ...] |
| metadata | object{1} | {user_id} |
| max_tokens | int | 32000 |
| thinking | object{1} | {type} |
| context_management | object{1} | {edits} |
| output_config | object{1} | {effort} |
| stream | bool | true |

## 响应头（共 9 个）

| Header | Value |
|---|---|
| server | openresty |
| date | Sun, 03 May 2026 15:54:15 GMT |
| content-type | text/event-stream |
| transfer-encoding | chunked |
| connection | keep-alive |
| cache-control | no-cache |
| x-new-api-version | v0.1.0 |
| x-oneapi-request-id | 202605031554141479420238268d9d6fFwz7PGL |
| strict-transport-security | max-age=31536000 |

## 响应体

- **Content-Type**：`text/event-stream`
- **解码后大小**：`13166` B
- **格式**：SSE (Server-Sent Events)

### SSE 事件统计
| event | count |
|---|---|
| message_start | 1 |
| content_block_start | 2 |
| ping | 1 |
| content_block_delta | 88 |
| content_block_stop | 2 |
| message_delta | 1 |
| message_stop | 1 |

### 各事件首条示例
**`message_start`**
```json
{"message":{"content":[],"id":"msg_41dd8b85a3b2430181f6c756a7cfd5b3","model":"claude-sonnet-4-6","role":"assistant","stop_reason":null,"stop_sequence":null,"type":"message","usage":{"input_tokens":37457,"output_tokens":1}},"type":"message_start"}
```
**`content_block_start`**
```json
{"content_block":{"id":"tooluse_bq7esSTmXyG6grCIQrNlXx","input":{},"name":"Bash","type":"tool_use"},"index":0,"type":"content_block_start"}
```
**`ping`**
```json
{"type": "ping"}
```
**`content_block_delta`**
```json
{"delta":{"partial_json":"{\"com","type":"input_json_delta"},"index":0,"type":"content_block_delta"}
```
**`content_block_stop`**
```json
{"index":0,"type":"content_block_stop"}
```
**`message_delta`**
```json
{"delta":{"stop_reason":"tool_use","stop_sequence":null},"type":"message_delta","usage":{"input_tokens":42108,"output_tokens":232}}
```
**`message_stop`**
```json
{"type":"message_stop"}
```


---
_原始 JSON_：[`rows/15-POST-www.fucheers.top_v1_messages.json`](../rows/15-POST-www.fucheers.top_v1_messages.json)
