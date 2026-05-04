# 14. POST https://api.anthropic.com/api/event_logging/v2/batch

**阶段**：Telemetry **状态码**：200 **请求大小**：196697 B **响应大小**：57 B

**用途**：**启动期 telemetry 大批**：99 条 `tengu_*` 事件聚合上报到 Anthropic 自家 event_logging。包含 skill 加载、目录扫描、MCP 连接结果、版本 lock 等。

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
| content-length | 196697 |
| connection | close |

## 请求体

- **Content-Type**：`application/json`
- **Content-Length**：`196697` B（解码后实际 `196697` B）
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| events | array[99] | [object{2}, ...] |

## 响应头（共 16 个）

| Header | Value |
|---|---|
| date | Sun, 03 May 2026 15:28:07 GMT |
| content-type | application/json |
| transfer-encoding | chunked |
| connection | close |
| request-id | req_011CafsWoYkWxeoTm8mK61d3 |
| strict-transport-security | max-age=31536000; includeSubDomains; preload |
| server | cloudflare |
| x-envoy-upstream-service-time | 62 |
| content-encoding | gzip |
| vary | Accept-Encoding |
| server-timing | x-originResponse;dur=66 |
| cf-cache-status | DYNAMIC |
| set-cookie | _cfuvid=REDACTED; HttpOnly; SameSite=None; Secure; Path=/; Domain=api.anthropic.com |
| content-security-policy | default-src 'none'; frame-ancestors 'none' |
| x-robots-tag | none |
| cf-ray | 9f604b29b8abbd64-LAX |

## 响应体

- **Content-Type**：`application/json`
- **解码后大小**：`40` B
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| accepted_count | int | 99 |
| rejected_count | int | 0 |

## 字段深挖


**Body schema**

```json
{
  "events": [ <event_data wrapper> ... ]
}
```

每条 `events[i]`：
| Field | Type | 含义 |
|---|---|---|
| event_type | string | 固定 `ClaudeCodeInternalEvent` |
| event_data | object | 事件负载（见下） |

`event_data` 的固定字段：
| Field | Type | 含义 |
|---|---|---|
| event_name | string | `tengu_*` 名称（详细列表见下方） |
| client_timestamp | ISO8601 | 客户端打点时间 |
| model | string | 当前会话主模型，例 `claude-opus-4-7[1m]` |
| session_id | uuid | 同 `x-claude-code-session-id` |
| user_type | string | `external/internal` |
| betas | string | 当前会话启用的 beta 列表 |
| env | object | 见 `env` 子表 |
| entrypoint | string | `cli` |
| is_interactive | bool |  |
| client_type | string | `cli` |
| process | base64(JSON) | Node `process` 资源快照 |
| additional_metadata | base64(JSON) | 该事件特有的字段 |
| auth | object | `{organization_uuid, account_uuid}` |
| event_id | string |  |
| device_id | string | machine-id sha256 |
| email | string | **明文** |

`env` 子结构：
| Field | Type | Sample |
|---|---|---|
| platform | string | `linux` |
| node_version | string | `v24.3.0` |
| terminal | string | `xterm` |
| package_managers | string | `npm,yarn,pnpm` |
| runtimes | string | `bun,deno,node` |
| is_running_with_bun | bool | true |
| is_ci | bool | false |
| is_claubbit | bool | false（Claubbit = Anthropic 内测环境） |
| is_github_action | bool | false |
| is_claude_code_action | bool | false |
| is_claude_ai_auth | bool | true（用 OAuth 而不是 API key） |
| version / version_base | string | `2.1.126` |
| arch | string | `x64` |
| is_claude_code_remote | bool | false（cloud 远程模式） |
| deployment_environment | string | `unknown-linux` |
| is_conductor | bool | false（Anthropic 编排模式） |
| build_time | ISO8601 |  |
| is_local_agent_mode | bool |  |
| linux_distro_id | string | `arch` |
| linux_kernel | string | `6.10.0-generic` |
| vcs | string | `git` |
| platform_raw | string | `linux` |
| shell | string | `zsh` |

`process` 解码后：
```json
{ "uptime": 0.5, "rss": 318939136, "heapTotal": 39016448, "heapUsed": 34119517,
  "external": 12833992, "arrayBuffers": 521, "constrainedMemory": 15901335552,
  "cpuUsage": { "user": 496864, "system": 157197 } }
```

`additional_metadata` 解码后随事件不同，例 `tengu_dir_search`：
```json
{ "rh": "3ee214335af448e2", "durationMs": 9, "managedFilesFound": 0,
  "userFilesFound": 0, "projectFilesFound": 0, "projectDirsSearched": 0,
  "subdir": "commands" }
```

**本批 99 条事件去重后的 35 个 event_name**（按出现频次降序）：

`tengu_skill_loaded`(42) ・ `tengu_plugin_enabled_for_session`(11) ・ `tengu_dir_search`(8) ・
`tengu_mcp_server_connection_succeeded`(3) ・ `tengu_mcp_tools_listed`(3) ・
`tengu_frontmatter_shadow_unknown_key`(2) ・ `tengu_prompt_suggestion_init`(2) ・
`tengu_version_lock_acquired`(1) ・ `tengu_exit`(1) ・ `tengu_claude_in_chrome_setup`(1) …

(含 `tengu_started/tengu_init/tengu_timer/tengu_mcp_server_connection_failed/...`)

**Response**：`{ "accepted_count": 99, "rejected_count": 0 }`

---
_原始 JSON_：[`rows/14-POST-api.anthropic.com_api_event_logging_v2_batch.json`](../rows/14-POST-api.anthropic.com_api_event_logging_v2_batch.json)
