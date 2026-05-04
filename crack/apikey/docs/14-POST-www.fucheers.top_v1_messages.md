# 14. POST https://www.fucheers.top/v1/messages?beta=true

**阶段**：业务 **状态码**：200 **请求大小**：139096 B **响应大小**：8040 B

**用途**：**首条真实业务消息（核心）**。SSE 流式发到 **`www.fucheers.top`**（三方 API 网关，伪装成 Anthropic 接口）。Authorization 用三方 key `sk-REDACTED...`。请求体相比 OAuth 模式有大量裁剪：8 个 anthropic-beta（少 oauth/advanced-tool-use/cache-diagnosis 三个）、3 个 system 块（少了一个、且全部 cache_control 用默认 5min 没 1h 没 global）、**34 个工具直接全展开**（不再走 ToolSearch 延迟加载）。响应 server 是 `openresty`，无 `anthropic-organization-id` / 无 `anthropic-ratelimit-*` / 无 `request-id: req_xxx`。

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
| content-length | 139096 |

## 请求体

- **Content-Type**：`application/json`
- **Content-Length**：`139096` B（解码后实际 `138269` B）
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| model | string | "claude-sonnet-4-6" |
| messages | array[1] | [object{2}, ...] |
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
| date | Sun, 03 May 2026 15:54:10 GMT |
| content-type | text/event-stream |
| transfer-encoding | chunked |
| connection | keep-alive |
| cache-control | no-cache |
| x-new-api-version | v0.1.0 |
| x-oneapi-request-id | 202605031554087193120128268d9d6dq1LkluG |
| strict-transport-security | max-age=31536000 |

## 响应体

- **Content-Type**：`text/event-stream`
- **解码后大小**：`8014` B
- **格式**：SSE (Server-Sent Events)

### SSE 事件统计
| event | count |
|---|---|
| message_start | 1 |
| ping | 1 |
| content_block_start | 3 |
| content_block_delta | 50 |
| content_block_stop | 3 |
| message_delta | 1 |
| message_stop | 1 |

### 各事件首条示例
**`message_start`**
```json
{"message":{"content":[],"id":"msg_517512916e51483d8815b94b0bcfcdc5","model":"claude-sonnet-4-6","role":"assistant","stop_reason":null,"stop_sequence":null,"type":"message","usage":{"input_tokens":37438,"output_tokens":1}},"type":"message_start"}
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
{"delta":{"text":"让我快速浏览一下","type":"text_delta"},"index":0,"type":"content_block_delta"}
```
**`content_block_stop`**
```json
{"index":0,"type":"content_block_stop"}
```
**`message_delta`**
```json
{"delta":{"stop_reason":"tool_use","stop_sequence":null},"type":"message_delta","usage":{"input_tokens":40521,"output_tokens":124}}
```
**`message_stop`**
```json
{"type":"message_stop"}
```


## 字段深挖


**与 OAuth 模式 #17 的全方位对比**

### 1. URL & Auth
| | OAuth | ApiKey |
|---|---|---|
| Host | `api.anthropic.com` | `www.fucheers.top` |
| Path | `/v1/messages?beta=true` | `/v1/messages?beta=true` |
| Authorization | `Bearer sk-ant-oat01-...` (Anthropic OAuth) | `Bearer sk-REDACTED` (三方 key) |

### 2. `anthropic-beta` 请求头（OAuth 11 个 vs ApiKey 8 个）

| Beta | OAuth | ApiKey |
|---|---|---|
| `claude-code-20250219` | ✓ | ✓ |
| `oauth-2025-04-20` | ✓ | **✗** |
| `context-1m-2025-08-07` | ✓ | ✓ |
| `interleaved-thinking-2025-05-14` | ✓ | ✓ |
| `redact-thinking-2026-02-12` | ✓ | ✓ |
| `context-management-2025-06-27` | ✓ | ✓ |
| `prompt-caching-scope-2026-01-05` | ✓ | ✓ |
| `advisor-tool-2026-03-01` | ✓ | ✓ |
| `advanced-tool-use-2025-11-20` | ✓ | **✗** |
| `effort-2025-11-24` | ✓ | ✓ |
| `cache-diagnosis-2026-04-07` | ✓ | **✗** |

### 3. 其他请求头差异

| Header | OAuth | ApiKey |
|---|---|---|
| `x-stainless-timeout` | `600` | `3000`（更长） |
| `x-client-request-id` | 每次新 UUID | **无** |
| `anthropic-dangerous-direct-browser-access` | `true` | `true`（同） |
| `x-app` | `cli` | `cli`（同） |

### 4. 请求体顶层字段（OAuth 11 个 vs ApiKey 10 个）

| Field | OAuth | ApiKey |
|---|---|---|
| model | `claude-opus-4-7` | `claude-sonnet-4-6` |
| max_tokens | 64000 | 32000 |
| stream | true | true |
| thinking | `{type:adaptive}` | `{type:adaptive}` |
| context_management | `{edits:[...]}` | `{edits:[...]}` |
| output_config | `{effort:medium}` | `{effort:medium}` |
| **diagnostics** | `{previous_message_id:...}` | **缺失** |
| metadata.user_id | 含 account_uuid | account_uuid 为**空字符串** |
| system | array[4] | array[3] |
| tools | array[8]（仅核心） | **array[34]（全展开）** |
| messages | array[19]（深对话） | array[1]（首条） |

### 5. system 数组对比

| idx | OAuth | ApiKey |
|---|---|---|
| 0 | 计费 header `cch=251fe;` | 计费 header `cch=3282d;`（cch 不同 = build hash 不同） |
| 1 | `You are Claude Code...`（无 cache_control） | `You are Claude Code...`（**带 `{type:ephemeral}`，无 ttl 无 scope**） |
| 2 | 主 system 9925 字 + `{ephemeral, ttl:1h, scope:global}` | 主 system 26994 字 + `{ephemeral}`（**默认 5min, 无 global**） |
| 3 | per-session 20660 字 + `{ephemeral, ttl:1h}` | **不存在** |

> ApiKey 把 system[2] 和 system[3] 合并为更长的一块，且**全用默认 5min cache 不用 global scope** —— 三方网关可能不支持 1h/global 缓存。

### 6. tools 数组对比

| | OAuth | ApiKey |
|---|---|---|
| 数量 | 8 | 34 |
| 列表 | Agent / Bash / Edit / Read / ScheduleWakeup / Skill / ToolSearch / Write | 全部展开：Agent / AskUserQuestion / Bash / Cron[Create/Delete/List] / Edit / Enter[PlanMode/Worktree] / Exit[PlanMode/Worktree] / LSP / Monitor / NotebookEdit / PushNotification / Read / RemoteTrigger / ScheduleWakeup / Skill / Task[Create/Get/List/Output/Stop/Update] / WebFetch / WebSearch / Write **+** mcp__context7__* / mcp__figma__* / mcp__plugin_context7_context7__* |

> **关键差异**：OAuth 模式有 `ToolSearch`，所有 deferred tools / MCP 工具走 ToolSearch 延迟加载；ApiKey 模式**直接把全部工具的 schema 塞进 tools 列表**。
> 推测原因：ToolSearch 依赖 anthropic-beta `advanced-tool-use-2025-11-20`，三方网关不支持，所以退化到经典模式。

### 7. 响应头对比

| Header | OAuth (`api.anthropic.com`) | ApiKey (`www.fucheers.top`) |
|---|---|---|
| `server` | `cloudflare` | `openresty` |
| `request-id` | `req_011CafsXw8vsa4sbNtMkxRzm` | **无** |
| `x-oneapi-request-id` | **无** | `202605031554087193120128268d9d6dq1LkluG`（新-API/one-api 项目特征） |
| `x-new-api-version` | **无** | `v0.1.0` |
| `anthropic-organization-id` | `00000000-...` | **无** |
| `anthropic-ratelimit-unified-*` | 全套 14 个 | **全无** |
| `traceresponse` | W3C trace | **无** |
| `cf-ray` | 有 | **无** |
| `set-cookie _cfuvid` | 有 | **无** |
| `content-security-policy` | 有 | **无** |
| `strict-transport-security` | `max-age=31536000; includeSubDomains; preload` | `max-age=31536000`（弱化） |

### 8. SSE 响应内容差异

- **JSON key 顺序不同**：Anthropic 原生输出 `{type, message}`，三方网关输出 `{message, type}`（按字母序排）。
- **`message_start.message.usage` 字段大幅缩水**：

| | OAuth | ApiKey |
|---|---|---|
| input_tokens | 6（基本全 cache 命中） | **37438**（全量发送） |
| cache_creation_input_tokens | 2670 | **缺失** |
| cache_read_input_tokens | 45410 | **缺失** |
| cache_creation.{ephemeral_5m,1h}_input_tokens | 有 | **缺失** |
| service_tier | `standard` | **缺失** |
| inference_geo | `not_available` | **缺失** |

> 三方网关 **没有透传缓存元数据** —— 即便底层支持，客户端也无从得知是否命中。

### 9. metadata.user_id 对比

OAuth：`{"device_id":"...sha256...","account_uuid":"00000000-...","session_id":"<uuid>"}`
ApiKey：`{"device_id":"...sha256...","account_uuid":"","session_id":"<uuid>"}` ← `account_uuid` 是空字符串。

---
_原始 JSON_：[`rows/14-POST-www.fucheers.top_v1_messages.json`](../rows/14-POST-www.fucheers.top_v1_messages.json)
