# 21. POST https://www.fucheers.top/v1/messages?beta=true

**阶段**：业务 **状态码**：200 **请求大小**：150290 B **响应大小**：31534 B

**用途**：工具回合 4，**最长一次（16.6 秒，149 KB 请求 + 30 KB 响应）**。

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
| content-length | 150290 |

## 请求体

- **Content-Type**：`application/json`
- **Content-Length**：`150290` B（解码后实际 `149395` B）
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| model | string | "claude-sonnet-4-6" |
| messages | array[7] | [object{2}, ...] |
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
| date | Sun, 03 May 2026 15:54:34 GMT |
| content-type | text/event-stream |
| transfer-encoding | chunked |
| connection | keep-alive |
| cache-control | no-cache |
| x-new-api-version | v0.1.0 |
| x-oneapi-request-id | 202605031554331333777918268d9d6bYULiGX2 |
| strict-transport-security | max-age=31536000 |

## 响应体

- **Content-Type**：`text/event-stream`
- **解码后大小**：`30849` B
- **格式**：SSE (Server-Sent Events)

### SSE 事件统计
| event | count |
|---|---|
| message_start | 1 |
| ping | 1 |
| content_block_start | 1 |
| content_block_delta | 247 |
| content_block_stop | 1 |
| message_delta | 1 |
| message_stop | 1 |

### 各事件首条示例
**`message_start`**
```json
{"message":{"content":[],"id":"msg_938dbe7fbe204845a3c91a0964731aa5","model":"claude-sonnet-4-6","role":"assistant","stop_reason":null,"stop_sequence":null,"type":"message","usage":{"input_tokens":37457,"output_tokens":1}},"type":"message_start"}
```
**`ping`**
```json
{"type": "ping"}
```
**`content_block_start`**
```json
{"content_block":{"text":"","type":"text"},"index":0,"type":"content_block_start"}
```
**`content_block_delta`**
```json
{"delta":{"text":"项","type":"text_delta"},"index":0,"type":"content_block_delta"}
```
**`content_block_stop`**
```json
{"index":0,"type":"content_block_stop"}
```
**`message_delta`**
```json
{"delta":{"stop_reason":"end_turn","stop_sequence":null},"type":"message_delta","usage":{"input_tokens":45443,"output_tokens":746}}
```
**`message_stop`**
```json
{"type":"message_stop"}
```


---
_原始 JSON_：[`rows/21-POST-www.fucheers.top_v1_messages.json`](../rows/21-POST-www.fucheers.top_v1_messages.json)
