# 22. POST https://api.anthropic.com/v1/messages?beta=true

**阶段**：业务 **状态码**：200 **请求大小**：132079 B **响应大小**：1317 B

**用途**：工具回合 4。结构等同 #17。

## 请求行

```
POST https://api.anthropic.com/v1/messages?beta=true
```

## 请求头（共 22 个）

| Header | Value |
|---|---|
| accept | application/json |
| authorization | Bearer sk-ant-oat01-REDACTED |
| content-type | application/json |
| user-agent | claude-cli/2.1.126 (external, cli) |
| x-claude-code-session-id | 0d7d3701-b10d-49a7-9324-8189c8c54152 |
| x-stainless-arch | x64 |
| x-stainless-lang | js |
| x-stainless-os | Linux |
| x-stainless-package-version | 0.81.0 |
| x-stainless-retry-count | 0 |
| x-stainless-runtime | node |
| x-stainless-runtime-version | v24.3.0 |
| x-stainless-timeout | 600 |
| anthropic-beta | claude-code-20250219,oauth-2025-04-20,context-1m-2025-08-07,interleaved-thinking-2025-05-14,redact-thinking-2026-02-12,context-management-2025-06-27,prompt-caching-scope-2026-01-05,advisor-tool-2026-0… |
| anthropic-dangerous-direct-browser-access | true |
| anthropic-version | 2023-06-01 |
| x-app | cli |
| x-client-request-id | 8ee010e9-56df-4320-a95f-78d9d08cf76a |
| connection | keep-alive |
| host | api.anthropic.com |
| accept-encoding | gzip, br |
| content-length | 132079 |

## 请求体

- **Content-Type**：`application/json`
- **Content-Length**：`132079` B（解码后实际 `129991` B）
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| model | string | "claude-opus-4-7" |
| messages | array[25] | [object{2}, ...] |
| system | array[4] | [object{2}, ...] |
| tools | array[9] | [object{3}, ...] |
| metadata | object{1} | {user_id} |
| max_tokens | int | 64000 |
| thinking | object{1} | {type} |
| context_management | object{1} | {edits} |
| output_config | object{1} | {effort} |
| diagnostics | object{1} | {previous_message_id} |
| stream | bool | true |

## 响应头（共 30 个）

| Header | Value |
|---|---|
| date | Sun, 03 May 2026 15:28:46 GMT |
| content-type | text/event-stream; charset=utf-8 |
| transfer-encoding | chunked |
| connection | keep-alive |
| cache-control | no-cache |
| anthropic-ratelimit-unified-status | allowed |
| anthropic-ratelimit-unified-5h-status | allowed |
| anthropic-ratelimit-unified-5h-reset | 1777824000 |
| anthropic-ratelimit-unified-5h-utilization | 0.05 |
| anthropic-ratelimit-unified-7d-status | allowed |
| anthropic-ratelimit-unified-7d-reset | 1778018400 |
| anthropic-ratelimit-unified-7d-utilization | 0.01 |
| anthropic-ratelimit-unified-representative-claim | five_hour |
| anthropic-ratelimit-unified-fallback-percentage | 0.5 |
| anthropic-ratelimit-unified-reset | 1777824000 |
| anthropic-ratelimit-unified-overage-disabled-reason | org_level_disabled |
| anthropic-ratelimit-unified-overage-status | rejected |
| request-id | req_REDACTED |
| strict-transport-security | max-age=31536000; includeSubDomains; preload |
| anthropic-organization-id | 00000000-0000-0000-0000-000000000002 |
| traceresponse | 00-10e1b5581e4d966304f584e30652f936-f54ba5e6ee4a6c70-01 |
| server | cloudflare |
| x-envoy-upstream-service-time | 2268 |
| content-encoding | gzip |
| vary | Accept-Encoding |
| cf-cache-status | DYNAMIC |
| set-cookie | _cfuvid=REDACTED; HttpOnly; SameSite=None; Secure; Path=/; Domain=api.anthropic.com |
| content-security-policy | default-src 'none'; frame-ancestors 'none' |
| x-robots-tag | none |
| cf-ray | REDACTED-cf-ray |

## 响应体

- **Content-Type**：`text/event-stream; charset=utf-8`
- **解码后大小**：`3180` B
- **格式**：SSE (Server-Sent Events)

### SSE 事件统计
| event | count |
|---|---|
| message_start | 1 |
| content_block_start | 3 |
| ping | 1 |
| content_block_delta | 6 |
| content_block_stop | 3 |
| message_delta | 1 |
| message_stop | 1 |

### 各事件首条示例
**`message_start`**
```json
{"type":"message_start","message":{"model":"claude-opus-4-7","id":"msg_014bzSzwTndgamn8UdjKxWMm","type":"message","role":"assistant","content":[],"stop_reason":null,"stop_sequence":null,"stop_details":null,"usage":{"input_tokens":6,"cache_creation_input_tokens":34210,"cache_read_input_tokens":14871,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":34210},"output_tokens":8...
```
**`content_block_start`**
```json
{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}            }
```
**`ping`**
```json
{"type": "ping"}
```
**`content_block_delta`**
```json
{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"EpkCClkIDRgCKkD2MSzlffPHofe+B0Phc78DCfFTxYVMz/4S/84ucTLqHQ8tkz/5hRLBPDjT6+TwySfyrCCTemrdIgqRBOLglJtkMg9jbGF1ZGUtb3B1cy00LTc4ABIMAKrq4Hropq8UxvB3GgwnsXboqgHKxlReHUgiMBHqBZvwX3snu8W9qkmdiHqrF8J6FkratWPbZVtAalFIZ1P1Rvpg31AjaOrKnsiZqipuiWFFYy0nw+YslOvr+aIxLmNYIFkBlMN6igIbgir55HFhcHufxnnRgt3XjY/caYmGcpTDXb+nJaB+lLytT...
```
**`content_block_stop`**
```json
{"type":"content_block_stop","index":0}
```
**`message_delta`**
```json
{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null,"stop_details":null},"usage":{"input_tokens":6,"cache_creation_input_tokens":34210,"cache_read_input_tokens":14871,"output_tokens":180,"iterations":[{"input_tokens":6,"output_tokens":180,"cache_read_input_tokens":14871,"cache_creation_input_tokens":34210,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_inpu...
```
**`message_stop`**
```json
{"type":"message_stop"         }
```


---
_原始 JSON_：[`rows/22-POST-api.anthropic.com_v1_messages.json`](../rows/22-POST-api.anthropic.com_v1_messages.json)
