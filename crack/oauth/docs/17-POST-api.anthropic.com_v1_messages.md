# 17. POST https://api.anthropic.com/v1/messages?beta=true

**阶段**：业务 **状态码**：200 **请求大小**：129059 B **响应大小**：791 B

**用途**：**首条真实业务消息（核心）**。SSE 流式。模型 `claude-opus-4-7`、`max_tokens=64000`、9 条 message、4 块 system、8 个工具。带 11 个 `anthropic-beta`，开启 1M 上下文 / interleaved thinking / context_management / output_config.effort / cache-diagnosis 等高级特性。

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
| x-client-request-id | 162f9d16-8f25-4e2c-81f8-1ad89452fa1c |
| connection | keep-alive |
| host | api.anthropic.com |
| accept-encoding | gzip, br |
| content-length | 129059 |

## 请求体

- **Content-Type**：`application/json`
- **Content-Length**：`129059` B（解码后实际 `127143` B）
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| model | string | "claude-opus-4-7" |
| messages | array[19] | [object{2}, ...] |
| system | array[4] | [object{2}, ...] |
| tools | array[8] | [object{3}, ...] |
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
| date | Sun, 03 May 2026 15:28:23 GMT |
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
| request-id | req_011CafsXw8vsa4sbNtMkxRzm |
| strict-transport-security | max-age=31536000; includeSubDomains; preload |
| anthropic-organization-id | 00000000-0000-0000-0000-000000000002 |
| traceresponse | 00-83ebcfb9575376d991968f84ed740848-dc53d43a4ca2330e-01 |
| server | cloudflare |
| x-envoy-upstream-service-time | 985 |
| content-encoding | gzip |
| vary | Accept-Encoding |
| set-cookie | _cfuvid=REDACTED; HttpOnly; SameSite=None; Secure; Path=/; Domain=api.anthropic.com |
| x-robots-tag | none |
| cf-cache-status | DYNAMIC |
| content-security-policy | default-src 'none'; frame-ancestors 'none' |
| cf-ray | 9f604b895a2ea2b3-LAX |

## 响应体

- **Content-Type**：`text/event-stream; charset=utf-8`
- **解码后大小**：`2718` B
- **格式**：SSE (Server-Sent Events)

### SSE 事件统计
| event | count |
|---|---|
| message_start | 1 |
| content_block_start | 1 |
| ping | 1 |
| content_block_delta | 9 |
| content_block_stop | 1 |
| message_delta | 1 |
| message_stop | 1 |

### 各事件首条示例
**`message_start`**
```json
{"type":"message_start","message":{"model":"claude-opus-4-7","id":"msg_01CfDzdTf5qimjFKcWf1sycf","type":"message","role":"assistant","content":[],"stop_reason":null,"stop_sequence":null,"stop_details":null,"usage":{"input_tokens":6,"cache_creation_input_tokens":2670,"cache_read_input_tokens":45410,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":2670},"output_tokens":5,"...
```
**`content_block_start`**
```json
{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_0171iMmLa4gZGS1gaE2BH41t","name":"Bash","input":{},"caller":{"type":"direct"}}               }
```
**`ping`**
```json
{"type": "ping"}
```
**`content_block_delta`**
```json
{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":""}       }
```
**`content_block_stop`**
```json
{"type":"content_block_stop","index":0}
```
**`message_delta`**
```json
{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null,"stop_details":null},"usage":{"input_tokens":6,"cache_creation_input_tokens":2670,"cache_read_input_tokens":45410,"output_tokens":92,"iterations":[{"input_tokens":6,"output_tokens":92,"cache_read_input_tokens":45410,"cache_creation_input_tokens":2670,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_to...
```
**`message_stop`**
```json
{"type":"message_stop"    }
```


## 字段深挖


**`anthropic-beta` 完整列表（11 个）**

| Beta | 启用作用 |
|---|---|
| `claude-code-20250219` | Claude Code 私有协议层 |
| `oauth-2025-04-20` | 走 OAuth Bearer 而非 API key 时必带 |
| `context-1m-2025-08-07` | Sonnet/Opus 1M 上下文 |
| `interleaved-thinking-2025-05-14` | thinking 块与 content 交替 |
| `redact-thinking-2026-02-12` | 服务端可对 thinking 做 redaction |
| `context-management-2025-06-27` | 启用 `context_management` 字段（thinking 修剪等） |
| `prompt-caching-scope-2026-01-05` | `cache_control.scope=global` 跨 session 缓存 |
| `advisor-tool-2026-03-01` | Advisor 工具 |
| `advanced-tool-use-2025-11-20` | 高级工具调用语义 |
| `effort-2025-11-24` | 启用 `output_config.effort` 字段 |
| `cache-diagnosis-2026-04-07` | 启用 `diagnostics.previous_message_id` 等缓存诊断 |

**请求体顶层字段全表**

| Field | Type | 说明 |
|---|---|---|
| model | string | `claude-opus-4-7`（无 `[1m]` 后缀） |
| max_tokens | int | `64000` |
| stream | bool | `true` → SSE |
| thinking | object | `{ "type": "adaptive" }` 自适应思考长度 |
| context_management | object | `{ "edits": [{"type":"clear_thinking_20251015","keep":"all"}] }` 服务端修剪 thinking |
| output_config | object | `{ "effort": "medium" }`（low/medium/high） |
| diagnostics | object | `{ "previous_message_id": "msg_xxx" }` 上轮 assistant 消息 ID，用于缓存命中诊断 |
| metadata | object | `{ "user_id": "<JSON-string device_id/account_uuid/session_id>" }` |
| system | array[4] | 见下 |
| tools | array[8] | 见下 |
| messages | array[19] | user/assistant 交替的对话历史 |

**`system[*]` 详解**

| idx | type | cache_control | text 长度 | 内容性质 |
|-----|------|---------------|-----------|----------|
| 0 | text | _无_ | 81 | 伪 system 块，实际是计费 header：`x-anthropic-billing-header: cc_version=2.1.126.c5f; cc_entrypoint=cli; cch=251fe;` |
| 1 | text | _无_ | 57 | `You are Claude Code, Anthropic's official CLI for Claude.` |
| 2 | text | `{"type":"ephemeral","ttl":"1h","scope":"global"}` | 9925 | Claude Code 主 system prompt（角色/安全/工作流/任务执行/copyright …）；**scope=global** 跨 session 缓存 |
| 3 | text | `{"type":"ephemeral","ttl":"1h"}` | 20660 | per-session 附加：text-output 风格、agent 列表、工具 manifest、claudeMd、currentDate、userEmail 等 session 级 context |

**`tools[*]` 内置 8 个**：`Agent` / `Bash` / `Edit` / `Read` / `ScheduleWakeup` / `Skill` / `ToolSearch` / `Write`。

每个 tool 元素结构：`{ "name": str, "description": str, "input_schema": JSONSchema }`。

> 注：所有 MCP 工具、技能、插件**不在 tools 列表**里 —— 它们的名字进了 system 块的 deferred-tools manifest，要靠 `ToolSearch` 按需注入 schema。

**`messages[*]`** 共 19 条，user/assistant 严格交替；`content` 是结构化数组：

| 元素 type | 出现位置 | 字段 |
|---|---|---|
| `text` | user / assistant | `{ type, text }` |
| `tool_use` | assistant | `{ type, id: "toolu_xxx", name, input, caller? }` |
| `tool_result` | user | `{ type, tool_use_id: "toolu_xxx", content }` |
| `thinking` | assistant | `{ type, thinking, signature? }` （受 redact-thinking beta 影响） |

**`metadata.user_id`** —— 字符串化 JSON，反序列化字段：`device_id / account_uuid / session_id`。

**响应 SSE 流（共 15 条事件）**

| 事件 | 出现次数 | 含义 |
|---|---|---|
| `message_start` | 1 | 第一帧，含模型 / msg_id / 初始 usage（cache_read=45410, cache_creation=2670, ephemeral_1h=2670） |
| `content_block_start` | 1 | 开始一个 content block；本次是 `tool_use`（Bash 工具调用） |
| `ping` | 1 | 保活 |
| `content_block_delta` | 9 | 流式增量 — 这次全是 `input_json_delta`（拼工具入参 JSON） |
| `content_block_stop` | 1 | block 结束 |
| `message_delta` | 1 | 终态：`stop_reason="tool_use"`，最终 usage（含 `iterations[]` 多轮统计） |
| `message_stop` | 1 | 流结束 |

**`message_start.message.usage`（首帧）** 与 **`message_delta.usage`（终态）** 字段：

| Field | 含义 |
|---|---|
| input_tokens | 实质增量输入 token（不含缓存） |
| cache_creation_input_tokens | 写入 ephemeral cache 的 token 数 |
| cache_read_input_tokens | 复用 cache 的 token 数 |
| cache_creation.ephemeral_5m_input_tokens | 写到 5min 桶的 token |
| cache_creation.ephemeral_1h_input_tokens | 写到 1h 桶的 token |
| output_tokens | 输出 token |
| iterations[] | 仅终态：每个工具回合的细分 usage |
| service_tier | `standard` / `priority` |
| inference_geo | 推理地理区域 |

> **关键观察**：本次首字节 985ms，`cache_read_input_tokens=45410, cache_creation_input_tokens=2670` —— 大头 prompt 命中缓存（system[2] global + system[3] per-session 1h），新写入仅 2670 token（多半是新增的对话历史末尾）。

---
_原始 JSON_：[`rows/17-POST-api.anthropic.com_v1_messages.json`](../rows/17-POST-api.anthropic.com_v1_messages.json)
