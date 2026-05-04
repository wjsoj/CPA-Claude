# 06. POST https://api.anthropic.com/v1/messages?beta=true

**阶段**：启动期 **状态码**：200 **请求大小**：323 B **响应大小**：310 B

**用途**：**额度探测**。`max_tokens=1` + Haiku + 单字 `quota`，目的：① 验证 OAuth 仍有效 ② 拿 5h/7d 速率限制 header。返回非流式 JSON。

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
| x-claude-code-session-id | d85790bb-6261-43c0-982d-550eb177c8d5 |
| x-stainless-arch | x64 |
| x-stainless-lang | js |
| x-stainless-os | Linux |
| x-stainless-package-version | 0.81.0 |
| x-stainless-retry-count | 0 |
| x-stainless-runtime | node |
| x-stainless-runtime-version | v24.3.0 |
| x-stainless-timeout | 600 |
| anthropic-beta | oauth-2025-04-20,interleaved-thinking-2025-05-14,redact-thinking-2026-02-12,context-management-2025-06-27,prompt-caching-scope-2026-01-05 |
| anthropic-dangerous-direct-browser-access | true |
| anthropic-version | 2023-06-01 |
| x-app | cli |
| x-client-request-id | e7aa2abd-83bd-46a1-86b2-dbcb23169e3b |
| connection | keep-alive |
| host | api.anthropic.com |
| accept-encoding | gzip, br |
| content-length | 323 |

## 请求体

- **Content-Type**：`application/json`
- **Content-Length**：`323` B（解码后实际 `323` B）
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| model | string | "claude-haiku-4-5-20251001" |
| max_tokens | int | 1 |
| messages | array[1] | [object{2}, ...] |
| metadata | object{1} | {user_id} |

## 响应头（共 29 个）

| Header | Value |
|---|---|
| date | Sun, 03 May 2026 15:28:00 GMT |
| content-type | application/json |
| transfer-encoding | chunked |
| connection | keep-alive |
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
| request-id | req_011CafsW7t2qroLB3RypPNbT |
| strict-transport-security | max-age=31536000; includeSubDomains; preload |
| anthropic-organization-id | 00000000-0000-0000-0000-000000000002 |
| traceresponse | 00-138cd8cde2497b05e4a4ee179084b24b-f9da893f2b102c20-01 |
| server | cloudflare |
| x-envoy-upstream-service-time | 2889 |
| content-encoding | gzip |
| vary | Accept-Encoding |
| cf-cache-status | DYNAMIC |
| set-cookie | _cfuvid=REDACTED; HttpOnly; SameSite=None; Secure; Path=/; Domain=api.anthropic.com |
| content-security-policy | default-src 'none'; frame-ancestors 'none' |
| x-robots-tag | none |
| cf-ray | 9f604af2bc0b1ddd-LAX |

## 响应体

- **Content-Type**：`application/json`
- **解码后大小**：`500` B
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| model | string | "claude-haiku-4-5-20251001" |
| id | string | "msg_01Vhxi3FyK7TfRipWfWPd2pQ" |
| type | string | "message" |
| role | string | "assistant" |
| content | array[1] | [object{2}, ...] |
| stop_reason | string | "max_tokens" |
| stop_sequence | null | null |
| stop_details | null | null |
| usage | object{7} | {input_tokens, cache_creation_input_tokens, cache_read_input_tokens...} |
| context_management | object{1} | {applied_edits} |

## 字段深挖


**关键差异（vs 业务请求 #17）**

- `anthropic-beta` 只有 5 个：`oauth-2025-04-20`、`interleaved-thinking-2025-05-14`、`redact-thinking-2026-02-12`、`context-management-2025-06-27`、`prompt-caching-scope-2026-01-05`
- 没有 `claude-code-20250219`、`context-1m-2025-08-07`、`advisor-tool-*`、`advanced-tool-use-*`、`effort-*`、`cache-diagnosis-*` —— 因为 quota probe 不带工具/不参与 effort 调度/不需要 1M 上下文
- 请求体只有 4 个顶层字段：`model / max_tokens / messages / metadata`，没有 `system / tools / thinking / context_management / output_config / diagnostics / stream`

**`metadata.user_id`** —— 是个**字符串化的 JSON**：
```json
"{\"device_id\":\"0000000000000000000000000000000000000000000000000000000000000000\",\"account_uuid\":\"00000000-0000-0000-0000-000000000001\",\"session_id\":\"d85790bb-6261-43c0-982d-550eb177c8d5\"}"
```
反序列化后字段：`device_id` / `account_uuid` / `session_id`。所有 `/v1/messages` 都用同样的封装。

**响应 `usage`（非流式）字段表**

| Field | Type | 含义 |
|---|---|---|
| input_tokens | int | 不带缓存的输入 token |
| cache_creation_input_tokens | int | 写入到 ephemeral cache 的输入 token |
| cache_read_input_tokens | int | 命中 cache 复用的输入 token |
| cache_creation.ephemeral_5m_input_tokens | int | 写到 5min TTL 桶 |
| cache_creation.ephemeral_1h_input_tokens | int | 写到 1h TTL 桶 |
| output_tokens | int | 输出 token |
| service_tier | string | `standard` / `priority` |
| inference_geo | string | 推理区域，本次 `not_available` |

**响应中关键 ratelimit header**

| Header | 含义 |
|---|---|
| `anthropic-ratelimit-unified-status` | 总状态 `allowed/throttled/rejected` |
| `anthropic-ratelimit-unified-5h-status` | 5 小时桶状态 |
| `anthropic-ratelimit-unified-5h-reset` | 5h 桶重置 epoch（秒） |
| `anthropic-ratelimit-unified-5h-utilization` | 5h 桶已用比例 (0..1) |
| `anthropic-ratelimit-unified-7d-*` | 同上，7 天周桶 |
| `anthropic-ratelimit-unified-representative-claim` | 当前最受限的桶 (`five_hour`/`seven_day`) |
| `anthropic-ratelimit-unified-fallback-percentage` | 降级阈值 |
| `anthropic-ratelimit-unified-overage-status` | 是否允许溢额 |
| `anthropic-ratelimit-unified-overage-disabled-reason` | 不允许时的原因，本次 `org_level_disabled` |
| `anthropic-organization-id` | 组织 UUID |
| `request-id` | `req_xxx`，反查请求 |
| `traceresponse` | W3C trace context |

---
_原始 JSON_：[`rows/06-POST-api.anthropic.com_v1_messages.json`](../rows/06-POST-api.anthropic.com_v1_messages.json)
