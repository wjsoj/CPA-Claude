#!/usr/bin/env python3
"""
读取 rows/NN-*.json，按统一模板生成 docs/NN-*.md。

每篇 markdown 给出：URL、所有 request header、request body 字段表 +
最小示例、所有 response header、response body 字段表 + 最小示例。

OAuth 等敏感字段不做脱敏 —— 这是用户本机抓自己流量的分析文件。
"""
import json, glob, os, base64, re, sys
from collections import OrderedDict

# 脚本固定锚定到 crack/ 根目录，跟工作目录无关
HERE = os.path.dirname(os.path.abspath(__file__))
CRACK_ROOT = os.path.dirname(HERE)

MODE = sys.argv[1] if len(sys.argv) > 1 else 'kiro'
# 多数 mode 都是 crack/<mode>/{rows,docs}，但 kiro-login 是 kiro 的子流程，落在 crack/kiro/login/{rows,docs}
if MODE == 'kiro-login':
    ROWS_DIR = os.path.join(CRACK_ROOT, 'kiro', 'login', 'rows')
    DOCS_DIR = os.path.join(CRACK_ROOT, 'kiro', 'login', 'docs')
else:
    ROWS_DIR = os.path.join(CRACK_ROOT, MODE, 'rows')
    DOCS_DIR = os.path.join(CRACK_ROOT, MODE, 'docs')
os.makedirs(DOCS_DIR, exist_ok=True)

# ---------- 每条 row 的人写注释（按 mode 切分） ----------
NOTES_BY_MODE = {}
EXTRA_BY_MODE = {}

# ---------- OAuth official mode ----------
NOTES_BY_MODE['oauth'] = {
    1:  ("GrowthBook 风格的 feature flag / A-B 实验拉取。请求体上送设备指纹 + 账号属性，响应体下发整套 `tengu_*` 旗标。注意是用 **bun 内置 fetch** 直接打的（绕过 Stainless SDK）。",
         "启动期 bootstrap"),
    2:  ("拉取 claude.ai 账号偏好（onboarding 状态、横幅 dismiss、特性开关）。", "启动期 bootstrap"),
    3:  ("`grove` 通知/宽限期开关。", "启动期 bootstrap"),
    4:  ("CLI 引导：返回该账户对应的模型映射、计费维度元信息、组织信息。", "启动期 bootstrap"),
    5:  ("'penguin mode'（额度溢出按需付费）开关。", "启动期 bootstrap"),
    6:  ("**额度探测**。`max_tokens=1` + Haiku + 单字 `quota`，目的：① 验证 OAuth 仍有效 ② 拿 5h/7d 速率限制 header。返回非流式 JSON。",
         "启动期"),
    7:  ("用户在 `~/.claude.json` 配的本地 MCP server 初始化。`Authorization` 是用户自定义 token，跟 anthropic 无关。本次连接拒绝（用户没起服务） → 502。",
         "MCP 发现"),
    8:  ("Anthropic 公共 MCP 注册表第 1 页。axios 直连，**不带 OAuth**。", "MCP 发现"),
    9:  ("用户已在云端配置的私有 MCP server 列表（本账户为空）。带 OAuth + `mcp-servers-2025-12-04` beta。", "MCP 发现"),
    10: ("Claude Code 自更新检测。返回纯文本版本号一行。", "MCP 发现"),
    11: ("bun/node 进程的 npm 镜像预连接。Whistle 没装该域 CA → captureError。**不是 Anthropic 流量**。", "杂噪"),
    12: ("Anthropic 公共 MCP 注册表第 2 页（`cursor=com.crypto.mcp/crypto-com:1.0.0`）。", "MCP 发现"),
    13: ("Anthropic 公共 MCP 注册表第 3 页（`cursor=io.customer/mcp:1.0.0`）。", "MCP 发现"),
    14: ("**启动期 telemetry 大批**：99 条 `tengu_*` 事件聚合上报到 Anthropic 自家 event_logging。包含 skill 加载、目录扫描、MCP 连接结果、版本 lock 等。", "Telemetry"),
    15: ("npm 镜像噪声（同 #11）。", "杂噪"),
    16: ("**Datadog 公共 intake** 第一批。带写死的 `dd-api-key: pubea5604404508cdd34afb69e6f42a05bc`（pub key，仅写入）。本批 4 条：`tengu_exit/tengu_started/tengu_timer/tengu_init`。", "Telemetry"),
    17: ("**首条真实业务消息（核心）**。SSE 流式。模型 `claude-opus-4-7`、`max_tokens=64000`、9 条 message、4 块 system、8 个工具。带 11 个 `anthropic-beta`，开启 1M 上下文 / interleaved thinking / context_management / output_config.effort / cache-diagnosis 等高级特性。",
         "业务"),
    18: ("工具回合 2。结构等同 #17，`messages` 增长到 21 条（追加上轮的 tool_use/tool_result）。", "业务"),
    19: ("中段 telemetry。43 条事件，主要是 `tengu_sysprompt_boundary_found` / `tengu_api_cache_breakpoints` / `tengu_attachment_compute_duration`。", "Telemetry"),
    20: ("工具回合 3。结构等同 #17。", "业务"),
    21: ("Datadog 第二批。3 条：`tengu_api_success` x2 + `tengu_tool_use_success`。多了 `pre_normalized_model/cost_u_s_d/ttft_ms/cached_input_tokens/...` 等性能指标字段。",
         "Telemetry"),
    22: ("工具回合 4。结构等同 #17。", "业务"),
    23: ("中段 telemetry。30 条事件。", "Telemetry"),
    24: ("Datadog 第三批。4 条，含 `chrome_bridge_connection_succeeded`。新增 `tool_name/is_mcp/duration_ms/permission_duration_ms` 等字段。",
         "Telemetry"),
    25: ("中段 telemetry。8 条事件，全是工具调用相关：`tengu_tool_use_granted_in_config/tengu_tool_use_can_use_tool_allowed/tengu_tool_use_progress/chrome_bridge_connection_started/tengu_prompt_cache_diagnosis_received/...`",
         "Telemetry"),
    26: ("工具回合 5（本会话最后一轮 /v1/messages）。", "业务"),
    27: ("中段 telemetry。16 条事件。", "Telemetry"),
    28: ("Datadog 第四批。2 条：`tengu_tool_use_success/tengu_api_success`。", "Telemetry"),
    29: ("npm 镜像噪声。", "杂噪"),
    30: ("末段 telemetry。4 条事件：`tengu_keybinding_fired` x3 + `tengu_paste_text`。键盘交互上报。", "Telemetry"),
    31: ("npm 镜像噪声。", "杂噪"),
    32: ("进程退出引发的最后一发 telemetry，**aborted** —— 连接中断，body 没发出。", "Telemetry"),
}

# ---------- 关键请求的字段深挖 (oauth) ----------

EXTRA = {}
EXTRA[1] = """
**`attributes`（GrowthBook 属性 / 设备指纹）**

| Field | Type | 含义 |
|---|---|---|
| id | string | 同 deviceID — machine-id 的 SHA-256，64 hex |
| sessionId | uuid | CLI 进程级，每次启动新生成 |
| deviceID | string | 与 id 同值 |
| platform | string | `linux` / `darwin` / `win32` |
| organizationUUID | uuid | Anthropic 组织 |
| accountUUID | uuid | Anthropic 账号 |
| userType | string | `external` / `internal` |
| subscriptionType | string | `max` / `pro` / `team` … |
| rateLimitTier | string | 如 `default_claude_max_20x` |
| firstTokenTime | epoch ms | 该账号首次成功请求的时间戳 |
| email | string | 账号邮箱 |
| appVersion | string | `2.1.126` |
| entrypoint | string | `cli` / `vscode` 等 |

**`features`（响应）**：每个 key 是 `tengu_*` 旗标，对应一个 GrowthBook feature 结构：
```json
{
  "value":  <旗标值，可为 bool/string/int/object>,
  "on":     true,
  "off":    false,
  "source": "defaultValue" | "force" | "experiment" | "override",
  "experiment":       <可选 experiment 定义>,
  "experimentResult": <可选命中结果，含 variationId、value、hashUsed、hashAttribute、hashValue、featureId、key>,
  "ruleId": <可选规则 id, 形如 "fr_xxx">
}
```
hash 字段表明 GrowthBook 用 `attributes.id`（即 deviceID）做一致性 hash，所以同一台机器的实验分组稳定。
"""

EXTRA[6] = """
**关键差异（vs 业务请求 #17）**

- `anthropic-beta` 只有 5 个：`oauth-2025-04-20`、`interleaved-thinking-2025-05-14`、`redact-thinking-2026-02-12`、`context-management-2025-06-27`、`prompt-caching-scope-2026-01-05`
- 没有 `claude-code-20250219`、`context-1m-2025-08-07`、`advisor-tool-*`、`advanced-tool-use-*`、`effort-*`、`cache-diagnosis-*` —— 因为 quota probe 不带工具/不参与 effort 调度/不需要 1M 上下文
- 请求体只有 4 个顶层字段：`model / max_tokens / messages / metadata`，没有 `system / tools / thinking / context_management / output_config / diagnostics / stream`

**`metadata.user_id`** —— 是个**字符串化的 JSON**：
```json
"{\\"device_id\\":\\"0000000000000000000000000000000000000000000000000000000000000000\\",\\"account_uuid\\":\\"00000000-0000-0000-0000-000000000001\\",\\"session_id\\":\\"d85790bb-6261-43c0-982d-550eb177c8d5\\"}"
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
"""

EXTRA[14] = """
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
"""

EXTRA[16] = """
**Body**：JSON 数组，每项 = 一条 Datadog log entry。本批 4 条，message 分别为 `tengu_exit / tengu_started / tengu_timer / tengu_init`。

`items[i]` 的字段（每条都齐）：

| Field | Type | 含义 |
|---|---|---|
| ddsource | string | 固定 `nodejs` |
| ddtags | string | DD tag 串，逗号分隔，含 `event:、arch:、client_type:、entrypoint:、model:、platform:、subscription_type:、user_bucket:、user_type:、version:、version_base:` |
| message | string | 事件名（同 telemetry 的 `event_name`） |
| service | string | `claude-code` |
| hostname | string | `claude-code` |
| env | string | `external` |
| model | string | `claude-opus-4-7` |
| session_id | uuid |  |
| user_type | string |  |
| betas | string | 同 anthropic-beta |
| entrypoint | string | `cli` |
| is_interactive | string | `"true"`（这里是字符串而不是 bool） |
| client_type | string | `cli` |
| process_metrics | object | `{uptime, rss, heapTotal, heapUsed, external, arrayBuffers, constrainedMemory, cpuUsage:{user,system}}` |
| swe_bench_run_id / swe_bench_instance_id / swe_bench_task_id | string | SWE-bench 评测专用，正常会话为空 |
| subscription_type | string | `max` |
| rh | string | run hash，跟 telemetry `additional_metadata.rh` 同 |
| platform / platform_raw / arch / node_version / terminal / shell / package_managers / runtimes | string |  |
| is_running_with_bun / is_ci / is_claubbit / is_claude_code_remote / is_local_agent_mode / is_conductor / is_github_action / is_claude_code_action / is_claude_ai_auth | bool | 环境探测 |
| version / version_base | string |  |
| build_time | ISO8601 |  |
| deployment_environment | string |  |
| linux_kernel / linux_distro_id | string |  |
| vcs | string | `git` |
| user_bucket | int | 取自 deviceID 的随机分桶 (0..N) |

**特定事件特有字段**（仅当 message 匹配时出现）

`tengu_exit` 多带：`last_session_*`（cost / api_duration / tool_duration / duration / lines_added / lines_removed / total_input_tokens / total_output_tokens / total_cache_creation_input_tokens / total_cache_read_input_tokens / fps_average / fps_low_1_pct / graceful_shutdown / id）+ `frame_duration_ms_*`（count/min/max/avg/p50/p95/p99）+ `pre_tool_hook_duration_ms_*`（同上 7 项）。

`tengu_init` / `tengu_started` / `tengu_timer` 以基础字段为主。

后续批（#21/#24/#28）会带不同附加字段，见各自文档。

**鉴权**：仅靠请求头 `dd-api-key: pubea5604404508cdd34afb69e6f42a05bc`（明文 public intake key，写死在客户端，仅写入权限），不带任何 Anthropic 凭据。

**响应**：HTTP 202，body `{}`。
"""

EXTRA[17] = """
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
"""

EXTRA_BY_MODE['oauth'] = EXTRA

# ============================================================
# ============== ApiKey (third-party) mode ===================
# ============================================================

NOTES_BY_MODE['apikey'] = {
    1:  ("'penguin mode'（额度溢出付费）开关。**仍然带 OAuth Bearer** —— 因为用户本机同时登录了 OAuth，CLI 默认用它打这条 anthropic.com 端点。", "启动期 bootstrap"),
    2:  ("CLI 引导：返回模型映射 + **完整 OAuth 账户信息**（account_uuid/email/organization_uuid/...）。即便客户端配置了三方 API，这条仍然走 OAuth 跑到 anthropic.com 拿账户元信息。",
         "启动期 bootstrap"),
    3:  ("**新端点 `/v1/models?limit=1000`**（去三方 base url）。OpenAI 风格的模型清单。请求带的是三方 API key（不是 OAuth）。响应列出三方支持的模型 ID，每个标注 `owned_by` 与 `supported_endpoint_types`。",
         "启动期 bootstrap"),
    4:  ("用户本地 MCP server 初始化，连接拒绝 → 502。同 OAuth 模式 #07。", "MCP 发现"),
    5:  ("MCP 公共注册表第 1 页。axios 直连，不带任何认证。", "MCP 发现"),
    6:  ("用户在云端配置的私有 MCP server 列表（本账户为空）。**仍走 OAuth bearer 到 anthropic.com**。", "MCP 发现"),
    7:  ("Claude Code 自更新检测，纯文本版本号。同 OAuth #10。", "MCP 发现"),
    8:  ("npm 镜像噪声。", "杂噪"),
    9:  ("MCP 注册表第 2 页。", "MCP 发现"),
    10: ("MCP 注册表第 3 页。", "MCP 发现"),
    11: ("**启动期 telemetry 大批 90 条**。本批 vs OAuth：体积接近（162k vs 196k），但 **Authorization header 没了** —— apikey 模式下 telemetry **匿名上报**到 anthropic.com，不带 OAuth bearer 也不带任何 anthropic-beta。事件中 `auth=null, email=null`。",
         "Telemetry"),
    12: ("npm 噪声。", "杂噪"),
    13: ("Datadog public intake 第一批。dd-api-key 还是同一个公钥。message 包含 `tengu_init/tengu_started/tengu_timer/tengu_exit` 四个老朋友。",
         "Telemetry"),
    14: ("**首条真实业务消息（核心）**。SSE 流式发到 **`www.fucheers.top`**（三方 API 网关，伪装成 Anthropic 接口）。Authorization 用三方 key `sk-REDACTED...`。请求体相比 OAuth 模式有大量裁剪：8 个 anthropic-beta（少 oauth/advanced-tool-use/cache-diagnosis 三个）、3 个 system 块（少了一个、且全部 cache_control 用默认 5min 没 1h 没 global）、**34 个工具直接全展开**（不再走 ToolSearch 延迟加载）。响应 server 是 `openresty`，无 `anthropic-organization-id` / 无 `anthropic-ratelimit-*` / 无 `request-id: req_xxx`。",
         "业务"),
    15: ("工具回合 2。", "业务"),
    16: ("中段 telemetry 41 条。同 #11 匿名上报。", "Telemetry"),
    17: ("自更新版本号又拉一次。", "杂噪"),
    18: ("工具回合 3。", "业务"),
    19: ("Datadog 第二批 6 条（按 message 数量推测，含 api_success / tool_use_success 等）。", "Telemetry"),
    20: ("中段 telemetry 44 条。", "Telemetry"),
    21: ("工具回合 4，**最长一次（16.6 秒，149 KB 请求 + 30 KB 响应）**。", "业务"),
    22: ("中段 telemetry 19 条。", "Telemetry"),
    23: ("npm 噪声。", "杂噪"),
    24: ("Datadog 第三批。", "Telemetry"),
    25: ("末段 telemetry，**aborted**（进程退出）。", "Telemetry"),
    26: ("末段 Datadog，**aborted**。", "Telemetry"),
}

EXTRA = {}

EXTRA[2] = """
**与 OAuth 模式 #04 的区别**：完全相同 —— **同样的 OAuth Bearer + 返回同一份 oauth_account**。说明即便 CLI 配置了三方 API key，本地 OAuth 凭据仍存在，会被 bootstrap 端点直接拿来认证。
"""

EXTRA[3] = """
**新端点解析**：`https://www.fucheers.top/v1/models?limit=1000`

**Request Headers（关键）**
```
Authorization: Bearer sk-REDACTED   ← 三方 API key
User-Agent: claude-code/2.1.126
anthropic-version: 2023-06-01
```
没有 `anthropic-beta`、没有 `x-stainless-*`、没有 `x-claude-code-session-id` —— 这是 axios 直接调的辅助探测请求。

**Response Body（OpenAI list 风格）**
```json
{
  "object": "list",
  "success": true,
  "data": [
    {
      "id": "claude-opus-4-7",
      "object": "model",
      "created": 1626777600,
      "owned_by": "vertex-ai",
      "supported_endpoint_types": ["anthropic", "openai"]
    },
    {"id": "claude-haiku-4-5-20251001", "owned_by": "vertex-ai", ...},
    {"id": "claude-sonnet-4-6", "owned_by": "vertex-ai", ...},
    {"id": "claude-opus-4-6", "owned_by": "vertex-ai", ...},
    {"id": "claude-opus-4-6-thinking", "owned_by": "custom", ...},
    {"id": "claude-opus-4-7-thinking", "owned_by": "custom", ...},
    {"id": "claude-sonnet-4-6-thinking", "owned_by": "custom", ...}
  ]
}
```

`owned_by="vertex-ai"` 表明上游是 Google Vertex AI 的 Anthropic 接口，被这个网关转回 Anthropic 协议格式。`*-thinking` 是网关额外暴露的"强制思考"伪模型。

**OAuth 模式没有这条请求** —— OAuth 模式靠 `/api/claude_cli/bootstrap` 拿模型映射，不需要单独的 `/v1/models`。
"""

EXTRA[11] = """
**对比 OAuth 模式 #14 (event_logging)**

| 字段 | OAuth 模式 | ApiKey 模式 |
|---|---|---|
| `Authorization` 请求头 | `Bearer sk-ant-oat01-...` | **无** |
| `anthropic-beta` 请求头 | `oauth-2025-04-20` | **无** |
| `x-service-name` 请求头 | `claude-code` | `claude-code`（同） |
| event_data.auth | `{organization_uuid, account_uuid}` | **null** |
| event_data.email | `redacted@example.com` | **null** |
| event_data.session_id | OAuth session 的 uuid | apikey session 的 uuid |
| event_data.device_id | sha256(machine-id) | **同一个 sha256（机器没变）** |
| env.is_claude_ai_auth | `true` | `false` |

**结论**：apikey 模式下，遥测**匿名上报**到 Anthropic（无 Bearer，事件本身也不带 email/account/org）；但 device_id (machine-id sha256) 仍然出现，所以从设备维度仍可被关联。
"""

EXTRA[14] = """
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
| `anthropic-organization-id` | `dda51f19-...` | **无** |
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

OAuth：`{"device_id":"...sha256...","account_uuid":"4fe8ffc6-...","session_id":"<uuid>"}`
ApiKey：`{"device_id":"...sha256...","account_uuid":"","session_id":"<uuid>"}` ← `account_uuid` 是空字符串。
"""

EXTRA_BY_MODE['apikey'] = EXTRA

# ============================================================
# ===================== Login (PKCE) mode ====================
# ============================================================

NOTES_BY_MODE['login'] = {
    1:  ("启动期 **空载健康探针**：CLI 进程拉起后第一发请求，仅校验到 Anthropic 的 TLS / HTTP 联通。**不带任何 Authorization** —— 这是登录前唯一一条不带凭据的 anthropic.com 请求。",
         "启动 / 登录前"),
    2:  ("**上一会话的退出 telemetry**（claude-code/2.1.126，`x-service-name: claude-code`）。这是 CLI 重新拉起前残留 ipc buffer 里的事件批次，里面绝大多数是 `tengu_exit/tengu_started/tengu_init/tengu_timer` 等 lifecycle 事件 —— 跟 *本次* 登录流程无关，但跟它紧挨着发出，所以一并捕获。**注意带的是 OAuth Bearer**，说明这是上次登录态下产生的事件。",
         "启动 / Telemetry"),
    3:  ("Datadog 镜像批次，对应 #02 的部分事件（`tengu_exit/tengu_started/tengu_timer/tengu_init`）。同样是上次会话的尾声。",
         "启动 / Telemetry"),
    4:  ("**OAuth Token Exchange（核心）**。CLI 浏览器跳转完成后，把 `code + code_verifier + state` 提交到 `platform.claude.com/v1/oauth/token` 换取 access/refresh token。**这是新版本登录的关键端点 —— 注意 host 是 `platform.claude.com` 而非旧版的 `console.anthropic.com`**。请求由 axios 发出（不是 Bun fetch），不带任何 Bearer。",
         "登录 — Token Exchange"),
    5:  ("拿到新 access_token 后立刻 `GET /api/oauth/profile` 拉取账户元信息（account uuid、display_name、email、has_claude_max、组织 uuid/name/类型/billing_type/rate_limit_tier、绑定的 application = Claude Code）。",
         "登录 — 账户初始化"),
    6:  ("拉取账户在 Claude CLI 维度的角色/工作区映射：`organization_uuid + organization_role + workspace_uuid/role`。本账户为 admin、无 workspace 概念。",
         "登录 — 账户初始化"),
    7:  ("**GrowthBook 拉特性旗标**（与首条业务消息前的 bootstrap 用同一端点 `/api/eval/sdk-zAZezfDKGoZuXXKe`），登录刚完成立刻打一发，把 deviceID + 全部账号属性上送做实验分组。**用 Bun fetch（不是 axios），带 OAuth Bearer + `anthropic-beta: oauth-2025-04-20`**。",
         "登录 — bootstrap"),
    8:  ("登录完成后第一批 telemetry：`tengu_oauth_started / tengu_oauth_success / tengu_init / tengu_started` 等共 9~12 条，标记账户切换的生命周期。同 #02 用 `claude-code/2.1.126` UA + `x-service-name: claude-code`。",
         "登录 — Telemetry"),
    9:  ("拉 claude.ai 账户偏好（onboarding 状态、横幅 dismiss、是否启用 artifacts/citations/latex 等预览特性）。`claude-code/2.1.126` UA + `oauth-2025-04-20`。",
         "登录 — bootstrap"),
    10: ("Grove（速率宽限期/通知频率）开关。`claude-cli/2.1.126 (external, cli)` UA + `oauth-2025-04-20`。",
         "登录 — bootstrap"),
    11: ("CLI 引导：返回该账户支持的模型映射（`kelp_forest_sonnet=1000000` 等）+ 完整 `oauth_account` 信息（账户/组织 uuid + 邮箱 + 名称 + 类型 + rate_limit_tier）。`claude-code/2.1.126` UA。",
         "登录 — bootstrap"),
    12: ("Penguin Mode（额度溢出按需付费）开关。本账户 `enabled=false, disabled_reason=extra_usage_disabled`。axios UA。",
         "登录 — bootstrap"),
}

EXTRA = {}

EXTRA[4] = """
**核心：PKCE Authorization Code 流程**

浏览器侧（不经代理，CLI 内 spawn 系统浏览器）：
```
GET https://claude.com/cai/oauth/authorize
    ?code=true
    &client_id=9d1c250a-e61b-44d9-88ed-5944d1962f5e
    &response_type=code
    &redirect_uri=http%3A%2F%2Flocalhost%3A46473%2Fcallback
    &scope=org%3Acreate_api_key+user%3Aprofile+user%3Ainference+user%3Asessions%3Aclaude_code+user%3Amcp_servers+user%3Afile_upload
    &code_challenge=CODE_CHALLENGE_REDACTED
    &code_challenge_method=S256
    &state=OAUTH_STATE_REDACTED
```

| 参数 | 含义 | 是否随机 |
|---|---|---|
| `code=true` | 使用 Authorization Code 模式（与隐式模式区分） | 固定 |
| `client_id` | Claude Code 的 OAuth 应用 UUID | **固定 `9d1c250a-e61b-44d9-88ed-5944d1962f5e`**（与下文 `application.uuid` 一致） |
| `response_type=code` | 标准 PKCE 模式 | 固定 |
| `redirect_uri` | 本地回调，端口随启动随机选（本次 `46473`） | 端口随机；CLI 监听该端口 |
| `scope` | 6 个 scope（`+` 分隔） | 固定 |
| `code_challenge` | `BASE64URL(SHA256(code_verifier))` | 每次新生成 |
| `code_challenge_method` | `S256`（不接受 `plain`） | 固定 |
| `state` | CSRF 防护 + 关联 challenge↔verifier | 每次新生成 |

授权完成后浏览器被 30x 到 `http://localhost:46473/callback?code=...&state=...`，CLI 内 HTTP 服务器拿到 `code`。

---

**本步抓到的 `POST platform.claude.com/v1/oauth/token` 请求体**（脱敏后）：

```json
{
  "grant_type": "authorization_code",
  "code": "OAUTH_CODE_REDACTED",
  "redirect_uri": "http://localhost:46473/callback",
  "client_id": "9d1c250a-e61b-44d9-88ed-5944d1962f5e",
  "code_verifier": "CODE_VERIFIER_REDACTED",
  "state": "OAUTH_STATE_REDACTED"
}
```

| Field | 必填 | 含义 |
|---|---|---|
| grant_type | ✓ | 固定 `authorization_code` |
| code | ✓ | 浏览器回调里拿到的一次性 code |
| redirect_uri | ✓ | **必须与授权时一致**（含端口）；服务端会校验 |
| client_id | ✓ | 同授权 URL 的 `client_id` |
| code_verifier | ✓ | 客户端原始随机串，服务端用 `SHA256/base64url` 复算与 `code_challenge` 比对 |
| state | _可选_ | 服务端不强制校验，CLI 也回带一次便于自查 |

请求头要点：
- `Content-Type: application/json`
- `User-Agent: axios/1.13.6`（**不是** Bun，也不是 claude-cli/claude-code）
- **没有 Authorization**（这步本来就是换凭据）

---

**响应体字段**（脱敏后）：

```json
{
  "token_type": "Bearer",
  "access_token": "sk-ant-oat01-REDACTED",
  "expires_in": 28800,
  "refresh_token": "sk-ant-ort01-REDACTED",
  "scope": "user:file_upload user:inference user:mcp_servers user:profile user:sessions:claude_code",
  "token_uuid": "00000000-0000-0000-0000-000000000003",
  "organization": {
    "uuid": "00000000-0000-0000-0000-000000000002",
    "name": "redacted@example.com's Organization"
  },
  "account": {
    "uuid": "00000000-0000-0000-0000-000000000001",
    "email_address": "redacted@example.com"
  }
}
```

| Field | 含义 |
|---|---|
| token_type | 固定 `Bearer` |
| access_token | 形如 `sk-ant-oat01-...`，约 130~140 char，base64url alphabet |
| expires_in | **8 小时**（28800 s）—— 比之前版本的 1h/24h 有变化 |
| refresh_token | 形如 `sk-ant-ort01-...`，长度类似 |
| scope | 与授权时申请的 scope 一致（**已剔除 `org:create_api_key`**，服务端只发了 5 个；说明该 scope 需要额外权限） |
| token_uuid | 该 token 的服务端记录 ID |
| organization.uuid / name | 默认组织 |
| account.uuid / email_address | 账户 UUID + 登录邮箱 |

**响应头特征**：
- `set-cookie: __cf_bm=...; Domain=claude.com`（Cloudflare bot management cookie，跨 *.claude.com 共享）
- `cf-cache-status: DYNAMIC`、`server: cloudflare`、`cf-ray: ...-LAX`
- `x-envoy-upstream-service-time: 126`（后端 envoy）
- `via: 1.1 google`（GCP 出口）

---

**与 CPA-Claude 现有实现的对比**

`internal/auth/login.go` 现有的 `finishAnthropicLogin` 流程要点对照本次抓包：

| 项 | 现有实现 | 本次实测 | 是否一致 |
|---|---|---|---|
| 授权 URL host | `claude.com/cai/oauth/authorize`（旧分支可能 `console.anthropic.com`） | `claude.com/cai/oauth/authorize` | ✓ |
| client_id | 同 | `9d1c250a-e61b-44d9-88ed-5944d1962f5e` | ✓ |
| redirect_uri | `http://localhost:<port>/callback` | 同 | ✓ |
| code_challenge_method | `S256` | 同 | ✓ |
| token endpoint | （旧实现）`https://console.anthropic.com/v1/oauth/token` | **`https://platform.claude.com/v1/oauth/token`** | **✗ 需迁移** |
| token endpoint UA | `axios/...` | `axios/1.13.6` | 调整版本号 |
| 响应 expires_in | （未必检查） | `28800` (8h) | ⚠ 注意刷新阈值（CPA 现在是过期前 5min 内刷新，可继续沿用） |
| 返回字段中是否含 `account_uuid/organization_uuid` | 已捕获存盘 | 仍提供 `account.uuid / organization.uuid` | ✓ |
"""

EXTRA[5] = """
**作用**：换 token 后立刻校验账户身份并取回结构化资料。

**鉴权**：`Authorization: Bearer <new access_token>`，`User-Agent: axios/1.13.6`，**不带任何 anthropic-beta**。

**响应体**：
```json
{
  "account": {
    "uuid": "00000000-0000-0000-0000-000000000001",
    "full_name": "REDACTED_USER",
    "display_name": "REDACTED_USER",
    "email": "redacted@example.com",
    "has_claude_max": true,
    "has_claude_pro": false,
    "created_at": "2026-04-20T15:09:41.735788Z"
  },
  "organization": {
    "uuid": "00000000-0000-0000-0000-000000000002",
    "name": "redacted@example.com's Organization",
    "organization_type": "claude_max",
    "billing_type": "google_play_subscription",
    "rate_limit_tier": "default_claude_max_20x",
    "seat_tier": null,
    "has_extra_usage_enabled": false,
    "subscription_status": null,
    "subscription_created_at": "2026-05-03T10:01:19.591854Z",
    "cc_onboarding_flags": {},
    "claude_code_trial_ends_at": null,
    "claude_code_trial_duration_days": null
  },
  "application": {
    "uuid": "9d1c250a-e61b-44d9-88ed-5944d1962f5e",
    "name": "Claude Code",
    "slug": "claude-code"
  }
}
```

**给 CPA-Claude 落库的字段**：`account.uuid`（→ `auth.AccountUUID`）、`organization.uuid`（→ `auth.OrganizationUUID`）、`account.email`（→ `auth.Email`）。`account.display_name` 可以选填到 `auth.Label`。
"""

EXTRA[7] = """
**关键差异 vs `/v1/messages` 前的 GrowthBook 调用**：完全相同的端点和请求结构。意味着登录流程会**额外打一次** `eval/sdk-...`，目的是用刚拿到的 OAuth 凭据 + 完整账号属性（`subscriptionType=max`、`rateLimitTier`、`accountUUID`）重新刷一次 feature flag 分组。

**鉴权**：`Authorization: Bearer <new access_token>`、`anthropic-beta: oauth-2025-04-20`、`User-Agent: Bun/1.3.14`（注意是 Bun fetch，不是 axios）。
"""

EXTRA[11] = """
**响应体**：
```json
{
  "client_data": {
    "kelp_forest_sonnet": "1000000"
  },
  "additional_model_options": null,
  "additional_model_costs": null,
  "oauth_account": {
    "account_uuid": "00000000-0000-0000-0000-000000000001",
    "account_email": "redacted@example.com",
    "organization_uuid": "00000000-0000-0000-0000-000000000002",
    "organization_name": "redacted@example.com's Organization",
    "organization_type": "claude_max",
    "organization_rate_limit_tier": "default_claude_max_20x",
    "user_rate_limit_tier": null,
    "seat_tier": null
  }
}
```

| Field | 含义 |
|---|---|
| client_data | 模型/特性映射，`kelp_forest_sonnet=1000000` 表示 Sonnet 1M 上下文 quota |
| additional_model_options | 该账户额外暴露的模型（本次 null） |
| additional_model_costs | 该账户的特殊定价（本次 null） |
| oauth_account.* | 同 #05 `/api/oauth/profile` 的部分字段，但 key 名加了 `account_/organization_` 前缀 —— 给 CLI bootstrap 用一份扁平化 view |

**注意**：这条与启动期 bootstrap（非登录场景下的同一端点）共用响应 schema —— 也就是说登录后端点会被打两次：登录链路里这一次（#11），以及之后正常会话每次启动都会再打一次。
"""

EXTRA_BY_MODE['login'] = EXTRA

# ============================================================
# ============== Kiro / Amazon-Q CLI mode ====================
# ============================================================
# 抓包对象：Kiro-CLI (Amazon Q for CLI 2.4.1)，从启动 → 同一会话 11 个对话 turn → 退出
# 共 48 条请求，4 个 host：
#   prod.us-east-1.auth.desktop.kiro.dev   ← Kiro 自家身份服务（1 条 refreshToken）
#   cognito-identity.us-east-1.amazonaws.com  ← AWS Cognito（1 条 GetCredentialsForIdentity）
#   q.us-east-1.amazonaws.com              ← CodeWhisperer Runtime + Streaming（Bearer = Kiro accessToken）
#   client-telemetry.us-east-1.amazonaws.com  ← Amazon Toolkit Telemetry（SigV4 with STS creds）

NOTES_BY_MODE['kiro'] = {
    1:  ("**Kiro 身份刷新（核心入口）**。`POST prod.us-east-1.auth.desktop.kiro.dev/refreshToken`，body `{refreshToken}` → 响应 `{accessToken, refreshToken, expiresAt, ...}`。这是 Kiro 自己实现的 token endpoint（不是 AWS Cognito 也不是 OAuth），UA 写死 `Kiro-CLI`。Kiro 的 `accessToken` 之后会作为 **Bearer** 直接打 CodeWhisperer 接口。",
         "启动 — 身份"),
    2:  ("**AWS STS 凭据交换**。`AWSCognitoIdentityService.GetCredentialsForIdentity`（content-type `application/x-amz-json-1.1`）。把已有的 `IdentityId` 换成临时 STS（`AccessKeyId/SecretKey/SessionToken/Expiration`）。这套 STS **只用于打 client-telemetry**（SigV4），不影响 CodeWhisperer 的 Bearer 认证。",
         "启动 — 身份"),
    3:  ("**模型列表探测**。`AmazonCodeWhispererService.ListAvailableModels`（x-amz-json-1.0）。query 上带 `origin=KIRO_CLI&profileArn=arn:aws:codewhisperer:us-east-1:699475941385:profile/EHGA3GRVQMUK`（该 profileArn 是 Amazon Q for Free Tier 的公共 profile，所有 Kiro 用户共享）。响应返回 `{defaultModel, models[]}`，含 `auto / claude-sonnet-4 / claude-3.7 / ...` 及各自的 `promptCaching` 元信息。",
         "启动 — bootstrap"),
    4:  ("**Toolkit Telemetry 第 1 弹**：`amazonqcli_dailyHeartbeat`（每天上报一次的存活心跳）。POST `client-telemetry.us-east-1.amazonaws.com/metrics`，body 是 JSON `{AWSProduct, AWSProductVersion, ClientID, MetricData[], OS, OSArchitecture, OSVersion, ParentProduct, ParentProductVersion}`。**SigV4 签名**用 #02 拿到的 STS 临时凭据（不是 Bearer）。响应 200 + 空 body。",
         "启动 — Telemetry"),
    5:  ("**Toolkit Telemetry 第 2 弹**：`codewhispererterminal_cliSubcommandExecuted`（CLI 子命令执行打点）。MetricData 多带 `credentialStartUrl` / `credentialSourceId` / `result=Succeeded` 等元信息。",
         "启动 — Telemetry"),
    6:  ("**首条业务消息（核心）**。`AmazonCodeWhispererStreamingService.GenerateAssistantResponse`。body 顶层两字段：`conversationState` + `profileArn`。`conversationState` 包含 `{conversationId, history[], currentMessage, chatTriggerType, customizationArn}`。响应是 **AWS eventstream** 二进制帧（`application/vnd.amazon.eventstream`），需要按 12-byte 头 + headers + payload 的标准帧式解码。",
         "业务 — Turn 1"),
    7:  ("**Turn 1 完成后上报**：`AmazonCodeWhispererService.SendTelemetryEvent`，`telemetryEvent.chatAddMessageEvent`。包含 `conversationId / messageId / timeToFirstChunkMilliseconds / timeBetweenChunks[] / chatTriggerInteraction / hasCodeSnippet / customizationArn / activeEditorTotalCharacters / cwsprChatPromptLength / cwsprChatConversationType`。**通过 CodeWhisperer 自家接口上报，不是 client-telemetry** —— 跟 toolkit telemetry 是两套并行通道。",
         "业务 — Turn 1 Telemetry"),
    8:  ("Toolkit telemetry：`codewhispererterminal_addedMessage`（chat 用户消息加入）。", "业务 — Telemetry"),
    9:  ("Toolkit telemetry：`amazonq_promptToCompletion`（提示→完成耗时）。", "业务 — Telemetry"),
    10: ("Turn 2 GenerateAssistantResponse。`conversationState.history` 累加上一轮的 user/assistant 消息。", "业务 — Turn 2"),
    11: ("Turn 2 SendTelemetryEvent。", "业务 — Turn 2 Telemetry"),
    12: ("Toolkit telemetry：`codewhispererterminal_addedMessage`。", "业务 — Telemetry"),
    13: ("Toolkit telemetry：`amazonq_promptToCompletion`。", "业务 — Telemetry"),
    14: ("Turn 3 GenerateAssistantResponse。", "业务 — Turn 3"),
    15: ("Turn 3 SendTelemetryEvent。", "业务 — Turn 3 Telemetry"),
    16: ("Turn 4 GenerateAssistantResponse（请求体涨到 65 KB）。", "业务 — Turn 4"),
    17: ("Toolkit telemetry：`codewhispererterminal_addedMessage`。", "业务 — Telemetry"),
    18: ("Toolkit telemetry：`amazonq_promptToCompletion`。", "业务 — Telemetry"),
    19: ("Turn 4 SendTelemetryEvent（晚到 —— Kiro 的 telemetry 上报并不严格跟随 turn 顺序）。", "业务 — Turn 4 Telemetry"),
    20: ("Toolkit telemetry。", "业务 — Telemetry"),
    21: ("Toolkit telemetry。", "业务 — Telemetry"),
    22: ("Turn 5 GenerateAssistantResponse（请求体仅 1.3 KB —— 可能是新 conversationId 或继续上一轮工具调用而无新 history）。", "业务 — Turn 5"),
    23: ("Turn 6 GenerateAssistantResponse（67 KB —— 累计 history 增长）。", "业务 — Turn 6"),
    24: ("Turn 6 SendTelemetryEvent。", "业务 — Turn 6 Telemetry"),
    25: ("Turn 7 GenerateAssistantResponse（119 KB）。", "业务 — Turn 7"),
    26: ("Toolkit telemetry。", "业务 — Telemetry"),
    27: ("Toolkit telemetry。", "业务 — Telemetry"),
    28: ("Toolkit telemetry。", "业务 — Telemetry"),
    29: ("Turn 8 GenerateAssistantResponse（124 KB）。", "业务 — Turn 8"),
    30: ("Turn 8 SendTelemetryEvent。", "业务 — Turn 8 Telemetry"),
    31: ("Toolkit telemetry。", "业务 — Telemetry"),
    32: ("Toolkit telemetry。", "业务 — Telemetry"),
    33: ("Turn 7 SendTelemetryEvent（更晚到 —— 印证 telemetry 与对话流是异步解耦的）。", "业务 — Turn 7 Telemetry"),
    34: ("Turn 9 GenerateAssistantResponse（130 KB）。", "业务 — Turn 9"),
    35: ("Toolkit telemetry。", "业务 — Telemetry"),
    36: ("Toolkit telemetry。", "业务 — Telemetry"),
    37: ("**Turn 10 GenerateAssistantResponse（最长一次，请求 134 KB / 响应 53 KB）**。", "业务 — Turn 10"),
    38: ("Turn 10 SendTelemetryEvent，**aborted** —— 上一次响应未完整结束/被 Ctrl-C 打断，CLI 直接丢弃这次发包。", "业务 — Turn 10 Telemetry"),
    39: ("Turn 10 SendTelemetryEvent 重试，成功。", "业务 — Turn 10 Telemetry"),
    40: ("Toolkit telemetry。", "业务 — Telemetry"),
    41: ("Toolkit telemetry。", "业务 — Telemetry"),
    42: ("SendTelemetryEvent（推断为补传上一轮的事件）。", "业务 — Telemetry"),
    43: ("Toolkit telemetry（本批 4.4 KB，明显比常规批次大 —— 可能附带了 `chatInteractWithMessageEvent` 类的扩展事件）。", "业务 — Telemetry"),
    44: ("Toolkit telemetry。", "业务 — Telemetry"),
    45: ("**Turn 11（最后一轮）GenerateAssistantResponse**。", "业务 — Turn 11"),
    46: ("Turn 11 SendTelemetryEvent。", "业务 — Turn 11 Telemetry"),
    47: ("Toolkit telemetry。", "业务 — Telemetry"),
    48: ("末尾 Toolkit telemetry，**aborted** —— Kiro CLI 进程退出时还有未发完的批次。", "退出"),
}

EXTRA = {}

EXTRA[1] = """
**Kiro 自有身份服务**：`prod.us-east-1.auth.desktop.kiro.dev` 是 Kiro Desktop 的独立 token endpoint，跟 AWS Cognito 没关系（Cognito 在下一步只用来换 STS）。

**Request**
- `Content-Type: application/json`
- `User-Agent: Kiro-CLI`（**写死无版本号**，跟 AWS SDK UA 完全不同）
- **无 Authorization**（用 body 里的 refreshToken 自证身份）

```json
{ "refreshToken": "aor..." }
```

`refreshToken` 是约 220 char 的 Kiro 私有格式：`{prefix}AAAAA{base64}:{ECDSA-sig}` —— 形如 OAuth2 refresh token 但带签名校验。

**Response**
```json
{
  "accessToken":  "aoa...",       // ~220 char, 同样带签名
  "refreshToken": "aor...",       // 一次性轮换（rolling refresh），下次必须用新的
  "expiresAt":    "ISO8601",      // 通常 1 小时
  "...":          "..."
}
```

**CPA-Claude 对接备注**：如要把 Kiro 凭据接进 pool，要做：
1. 落盘 `refreshToken`（必须用最新的，每次刷新都要持久化）
2. 每次过期前用同一 endpoint refresh，**body 严格 `{refreshToken}`，不带 client_id/grant_type**
3. accessToken 直接当 Bearer 用，目标只有 `q.us-east-1.amazonaws.com`
"""

EXTRA[2] = """
**AWS Cognito Identity Pool 标准调用**：`AWSCognitoIdentityService.GetCredentialsForIdentity`

**Request**
- `Content-Type: application/x-amz-json-1.1`（注意 1.1，不是 1.0）
- `X-Amz-Target: AWSCognitoIdentityService.GetCredentialsForIdentity`
- `User-Agent: aws-sdk-rust/1.3.16 os/linux lang/rust/1.92.0`（rust SDK）
- `X-Amz-User-Agent: aws-sdk-rust/1.3.16 ua/2.1 api/cognitoidentity/1.100.0 ... app/AmazonQ-For-CLI`
- `Amz-Sdk-Invocation-Id: <uuid>`、`Amz-Sdk-Request: attempt=1; max=3`
- **无 Authorization**（Cognito Identity 这一步本身是 "unauthenticated/identity-only" 模式）

```json
{ "IdentityId": "us-east-1:<uuid>" }
```

`IdentityId` 是 Cognito 给本设备分配的稳定 ID（首次启动时通过另一个 `GetId` 调用拿到，本抓包没出现，说明本机已有缓存）。

**Response**：
```json
{
  "IdentityId": "us-east-1:<uuid>",
  "Credentials": {
    "AccessKeyId":  "ASIA...",
    "Expiration":   <epoch_float>,
    "SecretKey":    "<40 char>",
    "SessionToken": "IQoJb3JpZ2luX2VjEIH//..."  // 长达 1500 char 的 STS session token
  }
}
```

**只服务 toolkit telemetry**：这套 STS 临时凭据**只**用于给 `client-telemetry.us-east-1.amazonaws.com/metrics` 做 SigV4（见 #04 起的 telemetry 请求头里的 `x-amz-security-token`）。CodeWhisperer 业务接口（`q.us-east-1.amazonaws.com`）走的是 Kiro accessToken Bearer，不用这套 STS。
"""

EXTRA[3] = """
**AmazonCodeWhispererService.ListAvailableModels**

**Request**
- URL：`POST https://q.us-east-1.amazonaws.com/?origin=KIRO_CLI&profileArn=...`（**query 也带 profileArn，body 内还会再带一次**）
- `Content-Type: application/x-amz-json-1.0`
- `X-Amz-Target: AmazonCodeWhispererService.ListAvailableModels`
- `Authorization: Bearer <Kiro accessToken from #01>`（**Bearer，不是 SigV4**）
- `X-Amz-User-Agent: aws-sdk-rust/1.3.16 ua/2.1 api/codewhispererruntime/0.1.16551 os/linux lang/rust/1.92.0 m/F,C app/AmazonQ-For-CLI`

```json
{
  "origin": "KIRO_CLI",
  "profileArn": "arn:aws:codewhisperer:us-east-1:699475941385:profile/EHGA3GRVQMUK"
}
```

`profileArn` 的 account `699475941385` 是 Amazon 自家的 Q-for-Free-Tier profile（所有 Kiro 用户共享，**不是用户自己的 AWS account**）。CPA-Claude 转发时这个 arn 应当原样透传或直接 hard-code。

**Response** — 模型清单（节选）：
```json
{
  "defaultModel": { "modelId": "auto" },
  "models": [
    { "modelId": "auto",                "modelName": "auto",                "promptCaching": {...} },
    { "modelId": "CLAUDE_SONNET_4_5_V1_0", "modelName": "claude-sonnet-4-5", "promptCaching": {...} },
    { "modelId": "CLAUDE_SONNET_4_V1_0",   "modelName": "claude-sonnet-4",   "promptCaching": {...} },
    { "modelId": "CLAUDE_3_7_SONNET_V1_0", "modelName": "claude-3.7-sonnet", "promptCaching": {...} },
    ...
  ]
}
```

每个模型条目可能带：
- `modelName` —— 展示名
- `description`
- `promptCaching.maximumCacheCheckpointsAllowed` —— prompt cache 上限
- `supportedContentMediaType[]` / `supportedFeatureFlags[]`

**CPA-Claude 设计提示**：客户端是先 `ListAvailableModels` → 才在后续 `GenerateAssistantResponse` 里指定 `modelId`。代理时如要做 model rewrite，要拦住 ListAvailableModels 给出"只允许的"白名单。
"""

EXTRA[4] = """
**Toolkit Telemetry endpoint — SigV4 路径**

**鉴权**：与 CodeWhisperer 接口完全独立的认证机制——用 #02 拿到的 STS 临时凭据做 SigV4 签名。请求头里能看到：
- `X-Amz-Date: 20260524T080424Z`
- `X-Amz-Security-Token: IQoJb3JpZ2luX2Vj...`（同 #02 响应里的 SessionToken）
- `Authorization: AWS4-HMAC-SHA256 Credential=ASIA.../20260524/us-east-1/execute-api/aws4_request, SignedHeaders=content-length;content-type;host;x-amz-date;x-amz-security-token;x-amz-user-agent, Signature=<hex>`

注意 service 是 **`execute-api`**（API Gateway），region `us-east-1`。

**Request body**（JSON，**非 Smithy 协议**）：

```json
{
  "AWSProduct":         "CodeWhisperer for Terminal",
  "AWSProductVersion":  "2.4.1",
  "ClientID":           "<uuid>",          // 设备级稳定 ID（同 Cognito IdentityId 派生）
  "MetricData": [
    {
      "MetricName":     "amazonqcli_dailyHeartbeat",
      "EpochTimestamp": 1779609863355,
      "Unit":           "None",
      "Value":          1.0,
      "Metadata": [
        { "Key": "source", "Value": "" },
        ...
      ]
    }
  ],
  "OS":                 "linux",
  "OSArchitecture":     "x86_64",
  "OSVersion":          "<kernel>",
  "ParentProduct":      "CodeWhisperer for Terminal",
  "ParentProductVersion": "2.4.1"
}
```

**Response**：HTTP 200，**空 body**。

**事件清单（本会话观察到的 MetricName）**
- `amazonqcli_dailyHeartbeat`
- `codewhispererterminal_cliSubcommandExecuted`
- `codewhispererterminal_addedMessage`
- `amazonq_promptToCompletion`
- `codewhispererterminal_userLoggedIn` / `_userLoggedOut`（启动/退出时）
- `codewhispererterminal_dialogDismissed`、`codewhispererterminal_dialogShown`

**`Metadata[]` 常见 Key**：
`credentialStartUrl` / `credentialSourceId` / `result` / `reason` /
`source` / `cwsprChatConversationType` / `cwsprChatHasCodeSnippet` /
`cwsprChatTriggerInteraction` / `cwsprChatProgrammingLanguage` /
`amazonqConversationId` / `amazonqMessageId` / `amazonqRequestId` /
`duration` / `durationMicroseconds` / `inputTokenCount` / `outputTokenCount` /
`cacheReadTokenCount` / `cacheWriteTokenCount`
"""

EXTRA[6] = """
**核心业务：`AmazonCodeWhispererStreamingService.GenerateAssistantResponse`**

**Request**
- `Content-Type: application/x-amz-json-1.0`
- `X-Amz-Target: AmazonCodeWhispererStreamingService.GenerateAssistantResponse`
- `Authorization: Bearer <Kiro accessToken>`
- `X-Amz-User-Agent: aws-sdk-rust/1.3.16 ua/2.1 api/codewhispererstreaming/0.1.16551 os/linux lang/rust/1.92.0 m/F app/AmazonQ-For-CLI`

**Body 顶层**：
```json
{
  "conversationState": {
    "conversationId":    "<uuid>",
    "currentMessage": {
      "userInputMessage": {
        "content":             "<user text + context dump>",
        "userInputMessageContext": {
          "envState":   { "operatingSystem": "linux", "currentWorkingDirectory": "...", "envVariables": [...] },
          "shellState": { "shellName": "zsh", "shellHistory": [...] },
          "gitState":   { ... },
          "toolResults": [ ... ]      // 工具回合时这里塞上一轮的 tool 结果
        },
        "modelId":             "CLAUDE_SONNET_4_5_V1_0" | "auto",
        "origin":              "CLI"
      }
    },
    "history": [
      { "userInputMessage":      { ... } },
      { "assistantResponseMessage": { "content": "...", "toolUses": [...] } },
      ...
    ],
    "chatTriggerType":   "MANUAL",
    "customizationArn":  null
  },
  "profileArn": "arn:aws:codewhisperer:us-east-1:699475941385:profile/EHGA3GRVQMUK"
}
```

注意 Kiro 的 prompt 工程：
- **用户消息里手动塞了"CONTEXT ENTRY"段**：把项目内的 README、关键源码作为 `--- CONTEXT ENTRY BEGIN ---` 块拼在用户输入前。这是 Kiro 的上下文注入方式（vs Claude Code 用 system block）。
- **没有 system prompt 字段** —— Kiro 把所有"系统指令"也内联在 `userInputMessage.content` 里。
- **没有显式的 tools 列表** —— 工具定义在 CodeWhisperer 服务端按 `modelId` 配置，客户端不传 schema，只在 `toolResults` 里回上一轮工具产物。

**Response**：`Content-Type: application/vnd.amazon.eventstream`（AWS event-stream 二进制帧）。

每帧结构（标准 AWS event-stream）：
```
+--------+------------+------------+------+---------+------+
| 4 byte | 4 byte     | 4 byte     | hdrs | payload | 4 byte
| total  | hdrs-len   | prelude-crc|      |         | msg-crc
+--------+------------+------------+------+---------+------+
```

Headers 里有 `:event-type` / `:content-type` / `:message-type` 等字段，常见 event-type：
- `initial-response` —— 首帧，含 `conversationId / messageId / requestId`
- `assistantResponseEvent` —— 文本增量，payload 为 JSON `{ "content": "..." }`
- `toolUseEvent` —— 工具调用，payload 为 JSON `{ "toolUseId", "name", "input": "<JSON-as-string>", "stop": bool }`
- `codeReferenceEvent` —— 代码引用合规元数据
- `messageMetadataEvent` —— 终态，含 usage `{inputTokenCount, outputTokenCount, cacheReadTokenCount, cacheWriteTokenCount}`

**CPA-Claude 转发提示**：
1. 上行只需要透传 `conversationState + profileArn`。
2. 下行要做 event-stream → SSE 的协议翻译（如果想把 Kiro 当 Anthropic 上游用）。
3. 计费 token 在 `messageMetadataEvent` 末帧，要解析二进制帧才能拿到。
"""

EXTRA[7] = """
**AmazonCodeWhispererService.SendTelemetryEvent —— 业务侧 telemetry（不是 toolkit telemetry）**

注意这条**和 #04/#05 的 client-telemetry 是两套独立通道**：
- 本条走 `q.us-east-1.amazonaws.com` + Bearer kiro token + Smithy x-amz-json-1.0
- client-telemetry 走 `client-telemetry.us-east-1.amazonaws.com` + SigV4 STS + 纯 JSON

**Body**：
```json
{
  "clientToken":        "<uuid>",            // 会话 nonce
  "telemetryEvent": {
    "chatAddMessageEvent": {
      "conversationId":              "<uuid>",
      "messageId":                   "<uuid>",
      "timeToFirstChunkMilliseconds": 3246.31,
      "timeBetweenChunks":            [0.02, 0.01, 44.24, 51.73, ...],
      "chatTriggerInteraction":      "manual",
      "hasCodeSnippet":              false,
      "customizationArn":            null,
      "activeEditorTotalCharacters": 0,
      "cwsprChatPromptLength":       <int>,
      "cwsprChatConversationType":   "Chat",
      "result":                      "Succeeded"
    }
  },
  "optOutPreference":  "OPTIN",
  "userContext": {
    "clientId":         "<uuid>",
    "ideCategory":      "CLI",
    "operatingSystem":  "LINUX",
    "product":          "CodeWhisperer for Terminal"
  },
  "profileArn":         "arn:aws:codewhisperer:us-east-1:699475941385:profile/EHGA3GRVQMUK",
  "modelId":            "CLAUDE_SONNET_4_5_V1_0"
}
```

**telemetryEvent 还可以是其它子类型**：`chatInteractWithMessageEvent` / `terminalUserInteractionEvent` / `userTriggerDecisionEvent` / `codeCoverageEvent` / `userModificationEvent` 等。Kiro 当前抓包里只看到 `chatAddMessageEvent`。

**Response**：`{}`（空对象）。

**为什么两套 telemetry**：
- `client-telemetry` 是 Amazon 通用的 Toolkit Telemetry（很多 AWS IDE 插件共用），度量"用了什么子命令"等基础指标。
- `SendTelemetryEvent` 是 CodeWhisperer/Q 业务专用，度量"对话质量" —— 服务端要用它做 RLHF / 模型评估。
"""

EXTRA_BY_MODE['kiro'] = EXTRA

# ============================================================
# ============== Kiro Login/Logout (PKCE) mode ===============
# ============================================================
# 14 条请求，覆盖 一次 `kiro login` → `kiro logout` → 再 `kiro login` 完整循环。
# 浏览器侧入口：https://app.kiro.dev/signin?state=<10char>&code_challenge=<base64url43>&
#               code_challenge_method=S256&redirect_uri=http%3A%2F%2Flocalhost%3A3128&
#               redirect_from=kirocli
# CLI 监听 localhost:3128，回调 path `/oauth/callback?login_option=github`（IdP 选择附加在 query）。
# 完成后 CLI POST `/oauth/token` 换 access/refresh token；logout 时 POST `/logout` 带 refresh token。

NOTES_BY_MODE['kiro-login'] = {
    1:  ("**Cognito `GetId`**：用 IdentityPoolId `us-east-1:820fd6d1-95c0-4ca4-bffb-3f01d32da842` 拿一个匿名 IdentityId。**这是无身份联邦（unauthenticated）模式** —— 每次冷启动 / 切账户都会发起一次，每次返回的 IdentityId 都不同（本会话 3 次 GetId 拿到 3 个 ID），仅用于给 toolkit telemetry 签 SigV4。",
         "登录前 — Cognito 识别"),
    2:  ("**Cognito `GetCredentialsForIdentity`**：用上一步的 IdentityId 换 STS 临时凭据（AccessKeyId / SecretKey / SessionToken），用于后续 `client-telemetry.us-east-1` 的 SigV4 签名。", "登录前 — Cognito 换 STS"),
    3:  ("**Toolkit telemetry — 子命令打点**：`MetricName=codewhispererterminal_cliSubcommandExecuted`，`subcommand=login` —— 标记用户跑了 `kiro login`。在 OAuth 流程**开始之前**就发，体现产品使用。",
         "登录前 — Telemetry"),
    4:  ("**OAuth Token Exchange（核心 #1）**：`POST prod.us-east-1.auth.desktop.kiro.dev/oauth/token`，body `{code, code_verifier, redirect_uri}` —— **没有 client_id、没有 grant_type、没有 state**。响应 `{accessToken, expiresIn:3600, profileArn, refreshToken}` —— **极简，没有 token_type / scope / id_token / email / account 信息**。详见字段深挖。",
         "登录 — Token Exchange"),
    5:  ("**Toolkit telemetry — 登录成功**：`MetricName=codewhispererterminal_userLoggedIn`，标记 OAuth 流程已结束。", "登录 — Telemetry"),
    6:  ("**Logout（核心 #2）**：`POST prod.us-east-1.auth.desktop.kiro.dev/logout`，body 仅 `{refreshToken}`（**带的是 refresh token，不是 access token；服务端会主动让两条都失效**）。响应 200 + 空 body。",
         "登出 — 撤销"),
    7:  ("Cognito GetId（**第二次**） —— `kiro logout` 完成后，Cognito 缓存被一并清空，需要重新拿匿名 identity 继续上报 telemetry。", "登出 — Cognito 识别"),
    8:  ("Cognito GetCredentialsForIdentity（**第二次**），同上。", "登出 — Cognito 换 STS"),
    9:  ("Toolkit telemetry：`subcommand=logout` —— 标记用户跑了 `kiro logout`。注意是在拿到新的 STS 后才发，所以**第 9 条而不是第 7 条**才是登出动作的真正『用户视角时间点』。",
         "登出 — Telemetry"),
    10: ("Cognito GetId（**第三次**）—— 再次执行 `kiro login` 触发的又一轮匿名 identity 申请。", "再登录 — Cognito 识别"),
    11: ("Cognito GetCredentialsForIdentity（**第三次**）。", "再登录 — Cognito 换 STS"),
    12: ("Toolkit telemetry：`subcommand=login`（再一次）。", "再登录 — Telemetry"),
    13: ("**OAuth Token Exchange 第二轮**：跟 #04 同 endpoint 同 body 结构；新 code + 新 code_verifier，签发出新的一对 access/refresh token。", "再登录 — Token Exchange"),
    14: ("Toolkit telemetry：`userLoggedIn` 第二次。", "再登录 — Telemetry"),
}

EXTRA = {}

EXTRA[1] = """
**Cognito Identity Pool 是公共共享的**

`IdentityPoolId = us-east-1:820fd6d1-95c0-4ca4-bffb-3f01d32da842` —— Amazon 自家给 Q for CLI 用户共享的匿名 identity pool（**所有 Kiro 用户都用这一个 pool ID**）。配合 #02 的 `GetCredentialsForIdentity`，等价于：
> Kiro CLI 在 Amazon 控股的 AWS 账号下，以"匿名访客"身份获得了一小段 STS 凭据，唯一权限是签名调用 `client-telemetry.us-east-1.amazonaws.com/metrics`。

**Request**
- `Content-Type: application/x-amz-json-1.1`
- `X-Amz-Target: AWSCognitoIdentityService.GetId`
- `User-Agent: aws-sdk-rust/1.3.10 os/linux lang/rust/1.92.0`（注意是 **1.3.10**，而 chat 路径用的是 1.3.16 —— 不同子模块独立指定 SDK 版本）
- **无 Authorization**（IdentityPool 设为 "unauthenticated"）

```json
{ "IdentityPoolId": "us-east-1:820fd6d1-95c0-4ca4-bffb-3f01d32da842" }
```

**Response**
```json
{ "IdentityId": "us-east-1:<uuid>" }
```

**注意**：本会话 3 次 GetId 返回了 3 个不同的 IdentityId（622b0cc5-14d1-... / 622b0cc5-1493-... / 622b0cc5-1409-...）。前 8 char 前缀重复说明 pool 内部按 prefix 分桶，但每次发出实际是新 ID —— 这是**匿名 pool 默认行为**（无 login 关联，每次都是新会话）。

**和首次 chat 会话的差异**：[`crack/kiro/docs/02`](../../docs/02-POST-cognito-identity.us-east-1.amazonaws.com_.md) 没有 `GetId` 调用，是因为 Kiro 把 IdentityId **持久化缓存到磁盘**（`~/.config/amazon-q/cache/cognito.json` 之类的位置），冷启动有缓存就直接走 `GetCredentialsForIdentity`；本次抓包是登录链路，CLI 主动清缓存（或缓存过期）后才会触发 `GetId`。
"""

EXTRA[4] = """
**核心：Kiro `/oauth/token` 端点**

URL: `POST https://prod.us-east-1.auth.desktop.kiro.dev/oauth/token`

**Request Headers**
| Header | Value |
|---|---|
| `Content-Type` | `application/json` |
| `User-Agent` | `Kiro-CLI`（**写死无版本号**） |
| `Authorization` | _无_ —— OAuth code-for-token 自证 |

**Request Body**（仅 3 字段，示例值已脱敏）
```json
{
  "code":         "KIRO_OAUTH_CODE_X_REDACTED",
  "code_verifier":"KIRO_CODE_VERIFIER_X_REDACTED",
  "redirect_uri": "http://localhost:3128/oauth/callback?login_option=github"
}
```

| Field | 含义 | 是否随机 |
|---|---|---|
| `code` | 浏览器回调里拿到的一次性 code，**标准 UUIDv4 格式** —— Kiro 服务端用 uuid 生成 code，不是 base64 随机串 | 每次新生成 |
| `code_verifier` | 32 字节随机数 base64url 编码（43 char），与浏览器侧 `code_challenge` 应满足 `BASE64URL(SHA256(verifier)) == challenge` | 每次新生成 |
| `redirect_uri` | **必须与授权时一致**（包括 query 里的 `login_option`）；服务端会精确字符串校验 | 固定 host:port + 浮动 login_option |

**没有的字段**（vs 标准 OAuth2 RFC 6749）
- ❌ `grant_type` —— 这个 endpoint 只支持 code exchange，所以省略
- ❌ `client_id` —— 端点本身就绑死给 Kiro CLI 用，不区分客户端
- ❌ `state` —— state 是 CSRF 防护，CLI 在浏览器回调时已经校验过（state 不出现在到 Kiro 服务端的请求里，只在 CLI ↔ 浏览器侧流转）

---

**Response Body**（4 字段，示例值已脱敏）
```json
{
  "accessToken":  "aoaAAAAAREDACTED_KIRO_ACCESS_TOKEN",
  "expiresIn":    3600,
  "profileArn":   "arn:aws:codewhisperer:us-east-1:699475941385:profile/EHGA3GRVQMUK",
  "refreshToken": "aorAAAAAREDACTED_KIRO_REFRESH_TOKEN"
}
```

| Field | 含义 |
|---|---|
| `accessToken` | 形如 `aoaAAAAA{base64}:{ECDSA-sig}`，约 220 char；之后直接当 Bearer 打 `q.us-east-1.amazonaws.com` |
| `expiresIn` | **3600（1 小时）** —— 过期后用 [`/refreshToken`](../../docs/01-POST-prod.us-east-1.auth.desktop.kiro.dev_refreshToken.md) 端点（**注意是另一个 endpoint，不是 `/oauth/token`**）换新 token |
| `profileArn` | 固定 `arn:aws:codewhisperer:us-east-1:699475941385:profile/EHGA3GRVQMUK`（Amazon Q free-tier 公共 profile，**所有用户都是这个串**） |
| `refreshToken` | 形如 `aorAAAAA{base64}:{ECDSA-sig}`，约 220 char；用于 `/refreshToken` 端点；**rolling refresh**（每次刷新都换新的 refresh token，老的立刻失效） |

**没有的字段**（vs 标准 OAuth2）
- ❌ `token_type` —— 默认就是 Bearer
- ❌ `scope` —— Kiro 没有 scope 概念
- ❌ `id_token` —— Kiro 不发 OIDC id_token
- ❌ `email` / `account_uuid` / `organization_uuid` —— 跟 Anthropic OAuth 不同，Kiro 不在 token-exchange 阶段返回任何账户元信息（账户信息只能从 accessToken 自身的内部结构 / 后续 API 调用拿到）

---

**Response Headers**
| Header | Value | 含义 |
|---|---|---|
| `Content-Type` | `application/json` | |
| `Server` | _空_ | Kiro 服务端不暴露具体 server 软件 |
| `Connection` | `keep-alive` | |

注意 **响应没有 set-cookie、没有 Cloudflare 头、没有 `request-id` / `traceresponse`** —— 这是个非常"裸"的 endpoint，不走 CDN（直接 ALB / Lambda 推测）。

---

**浏览器侧授权流程**（CLI 不抓包，仅复述）

CLI `kiro login` → spawn 浏览器打开：
```
https://app.kiro.dev/signin
    ?state=KIRO_OAUTH_STATE
    &code_challenge=KIRO_CODE_CHALLENGE_REDACTED
    &code_challenge_method=S256
    &redirect_uri=http%3A%2F%2Flocalhost%3A3128
    &redirect_from=kirocli
```

| 参数 | 含义 |
|---|---|
| `state` | 10 char 随机字符串（CSRF + 关联 challenge↔verifier） |
| `code_challenge` | `BASE64URL(SHA256(code_verifier))`，43 char |
| `code_challenge_method` | `S256`（不接受 `plain`） |
| `redirect_uri` | **host:port 固定为 `http://localhost:3128`（注意端口 3128 是 hard-coded，不像 Claude Code 那样随机端口）** |
| `redirect_from` | 固定 `kirocli` —— 让 app.kiro.dev 知道是 CLI 触发的，渲染对应的 UI 提示 |

用户在 `app.kiro.dev/signin` 选 IdP（GitHub / Google / Builder ID）→ 完成 IdP 登录 → 浏览器 302 到：
```
http://localhost:3128/oauth/callback?code=<uuid>&state=<echo>&login_option=github
```

CLI 监听端口 3128 拿到 `code`，**把 `?login_option=github` 也拼回到 `redirect_uri` 里**（这就是为什么 #04 的 `redirect_uri` 包含 `?login_option=github`，是浏览器侧加上的），然后 POST `/oauth/token`。

---

**CPA-Claude 对接备注**

| 任务 | 实现方式 |
|---|---|
| 落盘 token 对 | 存 `accessToken / refreshToken / expiresAt = now + 3600s / profileArn`；**accessToken 和 refreshToken 一定要原子写**（rolling refresh 一旦丢失 refreshToken，账户死） |
| 刷新前阈值 | 建议过期前 5min 触发 `/refreshToken`（用另一个 endpoint，body `{refreshToken}`） |
| 多账户 | 一个 OAuth 账户对应一组 `(accessToken, refreshToken)`；profileArn 全部一样 = `arn:aws:codewhisperer:us-east-1:699475941385:profile/EHGA3GRVQMUK`，可全局常量 |
| 代理触发登录 | CPA 后端要承担 CLI 角色：driver 浏览器 → 监听 localhost:3128 → 自动复现 PKCE。⚠️ 注意 localhost:3128 是写死的，如果服务器有 3128 占用（squid 默认端口！）必须先释放或让 CLI 改端口 |
"""

EXTRA[6] = """
**Logout endpoint — Kiro 自家撤销**

URL: `POST https://prod.us-east-1.auth.desktop.kiro.dev/logout`

**Request Headers**：同 [`/refreshToken`](../../docs/01-POST-prod.us-east-1.auth.desktop.kiro.dev_refreshToken.md) —— `Content-Type: application/json`、`User-Agent: Kiro-CLI`、无 Authorization。

**Request Body**
```json
{ "refreshToken": "aorAAAAAREDACTED_KIRO_REFRESH_TOKEN" }
```

注意是**带 refreshToken**，不是 accessToken。原因：refreshToken 在服务端有数据库记录（rolling 链表），可被精确撤销；access token 是签名 token 没有服务端状态，只能等过期。**撤销 refreshToken 后，对应的 accessToken 也立即失效**（服务端会把同一个会话标记为 revoked，accessToken 即便没过期也会被拒）。

**Response**：HTTP 200，**空 body**。

**CPA-Claude 对接备注**：
- 如果 CPA 删除一个 OAuth 凭据，**应主动调用 `/logout`** 把服务端的 refresh chain 也撤销，否则该 token 在服务端继续占用 quota 池。
- `/logout` 不需要 Bearer，所以即便 accessToken 已经过期也能调（只要 refreshToken 还有效）。
- 错误处理：refreshToken 已失效时返回什么？本次没抓到，CPA 实现时应当容忍 4xx 当作"已撤销" treated as success。
"""

EXTRA[7] = """
**为什么 logout 后又两次 GetId / GetCredentialsForIdentity？**

Kiro CLI 把 Cognito 临时凭据**关联到 OAuth 会话**：登出意味着销毁全部凭据状态，包括 Cognito 缓存的 IdentityId。再启动后必须重新走 GetId → GetCredentialsForIdentity 拿一份新的匿名 STS，否则 toolkit telemetry 无 SigV4 可签。

匿名 pool 的 IdentityId **不能复用** —— 它在服务端只是"无关联的临时 handle"，每次 GetId 都是新的（甚至没有去重）。所以 #07/#10 拿到的两个 IdentityId 互不相同。
"""

EXTRA_BY_MODE['kiro-login'] = EXTRA

# ---------- 选择当前 mode 的 NOTES/EXTRA ----------
NOTES = NOTES_BY_MODE.get(MODE, {})
EXTRA = EXTRA_BY_MODE.get(MODE, {})

# ---------- 工具：JSON 顶层 schema ----------
def py_type(v):
    if v is None: return 'null'
    if isinstance(v, bool): return 'bool'
    if isinstance(v, int): return 'int'
    if isinstance(v, float): return 'float'
    if isinstance(v, str): return 'string'
    if isinstance(v, list): return f'array[{len(v)}]'
    if isinstance(v, dict): return f'object{{{len(v)}}}'
    return type(v).__name__

def schema_table(obj, max_depth=2, depth=0, prefix=''):
    rows = []
    if isinstance(obj, dict):
        for k, v in obj.items():
            t = py_type(v)
            sample = ''
            if isinstance(v, (str, int, float, bool)) or v is None:
                s = json.dumps(v, ensure_ascii=False)
                sample = (s[:60] + '...') if len(s) > 60 else s
            elif isinstance(v, list) and v:
                sample = f'[{py_type(v[0])}, ...]'
            elif isinstance(v, dict) and v:
                sample = '{' + ', '.join(list(v.keys())[:3]) + ('...' if len(v) > 3 else '') + '}'
            rows.append((f'{prefix}{k}', t, sample))
            if depth < max_depth - 1 and isinstance(v, dict):
                rows.extend(schema_table(v, max_depth, depth+1, prefix=prefix + k + '.'))
    return rows

def md_table(headers, rows):
    out = ['| ' + ' | '.join(headers) + ' |',
           '|' + '|'.join(['---'] * len(headers)) + '|']
    for r in rows:
        out.append('| ' + ' | '.join(str(c).replace('|','\\|').replace('\n',' ') for c in r) + ' |')
    return '\n'.join(out)

def parse_body_json(s):
    if not s: return None
    s2 = s.lstrip()
    if not (s2.startswith('{') or s2.startswith('[')):
        return None
    try:
        return json.loads(s)
    except: return None

# ---------- 渲染单个 row ----------
def render(idx, path):
    d = json.load(open(path))
    note, phase = NOTES.get(idx, ('', ''))
    method = d.get('method', '?')
    url = d.get('url', '')
    status = d.get('statusCode', '?')
    rh = d.get('reqHeaders') or {}
    sh = d.get('resHeaders') or {}
    rb = d.get('reqBody') or ''
    rs = d.get('resBody') or ''

    title = f'# {idx:02d}. {method} {url}'
    out = [title, '']
    out.append(f'**阶段**：{phase} **状态码**：{status} **请求大小**：{d.get("reqSize")} B **响应大小**：{d.get("resSize")} B')
    out.append('')
    out.append(f'**用途**：{note}')
    out.append('')

    # ---- Request line ----
    out.append('## 请求行')
    out.append('')
    out.append('```')
    out.append(f'{method} {url}')
    out.append('```')
    out.append('')

    # ---- Request headers ----
    out.append(f'## 请求头（共 {len(rh)} 个）')
    out.append('')
    if rh:
        rows = []
        for k, v in rh.items():
            vs = str(v)
            if len(vs) > 200: vs = vs[:200] + '…'
            rows.append((k, vs))
        out.append(md_table(['Header', 'Value'], rows))
    else:
        out.append('_无_')
    out.append('')

    # ---- Request body ----
    out.append('## 请求体')
    out.append('')
    if not rb:
        out.append('_无_')
    else:
        ct = rh.get('content-type', '?')
        out.append(f'- **Content-Type**：`{ct}`')
        out.append(f'- **Content-Length**：`{rh.get("content-length", "?")}` B（解码后实际 `{len(rb)}` B）')
        j = parse_body_json(rb)
        if j is None:
            out.append(f'- **格式**：非 JSON / 文本')
            out.append('')
            out.append('### 内容片段')
            out.append('```')
            out.append(rb[:600] + ('...' if len(rb) > 600 else ''))
            out.append('```')
        else:
            out.append(f'- **格式**：JSON ({"object" if isinstance(j, dict) else f"array[{len(j)}]"})')
            out.append('')
            out.append('### 顶层字段')
            if isinstance(j, dict):
                out.append(md_table(['Field', 'Type', 'Sample'], schema_table(j, 1)))
            elif isinstance(j, list) and j and isinstance(j[0], dict):
                out.append(f'_数组共 {len(j)} 项，每项结构相同。展示第 0 项字段：_')
                out.append('')
                out.append(md_table(['Field', 'Type', 'Sample'], schema_table(j[0], 1)))
    out.append('')

    # ---- Response headers ----
    out.append(f'## 响应头（共 {len(sh)} 个）')
    out.append('')
    if sh:
        rows = []
        for k, v in sh.items():
            if isinstance(v, list):
                vs = '; '.join(str(x) for x in v)
            else:
                vs = str(v)
            if len(vs) > 200: vs = vs[:200] + '…'
            rows.append((k, vs))
        out.append(md_table(['Header', 'Value'], rows))
    else:
        out.append('_无_')
    out.append('')

    # ---- Response body ----
    out.append('## 响应体')
    out.append('')
    if not rs:
        out.append('_无_')
    else:
        ct = sh.get('content-type', '?')
        out.append(f'- **Content-Type**：`{ct}`')
        out.append(f'- **解码后大小**：`{len(rs)}` B')
        if rs.startswith('event:') or '\nevent:' in rs[:500]:
            out.append('- **格式**：SSE (Server-Sent Events)')
            evs = re.findall(r'event:\s*(\S+)', rs)
            from collections import Counter
            cnt = Counter(evs)
            out.append('')
            out.append('### SSE 事件统计')
            out.append(md_table(['event', 'count'], list(cnt.items())))
            # show one example of each event type
            out.append('')
            out.append('### 各事件首条示例')
            seen = set()
            for m in re.finditer(r'event:\s*(\S+)\s*\ndata:\s*(.*)', rs):
                name, data = m.group(1), m.group(2).strip()
                if name in seen: continue
                seen.add(name)
                out.append(f'**`{name}`**')
                out.append('```json')
                out.append(data[:400] + ('...' if len(data) > 400 else ''))
                out.append('```')
            out.append('')
            continue_after_loop = True
        else:
            j = parse_body_json(rs)
            if j is None:
                out.append('- **格式**：非 JSON / 文本')
                out.append('')
                out.append('### 内容')
                out.append('```')
                out.append(rs[:600] + ('...' if len(rs) > 600 else ''))
                out.append('```')
            else:
                out.append(f'- **格式**：JSON ({"object" if isinstance(j, dict) else f"array[{len(j)}]"})')
                out.append('')
                out.append('### 顶层字段')
                if isinstance(j, dict):
                    out.append(md_table(['Field', 'Type', 'Sample'], schema_table(j, 1)))
                elif isinstance(j, list) and j and isinstance(j[0], dict):
                    out.append(f'_数组共 {len(j)} 项。展示第 0 项字段：_')
                    out.append('')
                    out.append(md_table(['Field', 'Type', 'Sample'], schema_table(j[0], 1)))
                else:
                    out.append('```json')
                    out.append(json.dumps(j, ensure_ascii=False, indent=2)[:800])
                    out.append('```')
    out.append('')

    if idx in EXTRA:
        out.append('## 字段深挖')
        out.append('')
        out.append(EXTRA[idx].rstrip())
        out.append('')

    out.append('---')
    out.append(f'_原始 JSON_：[`rows/{os.path.basename(path)}`](../rows/{os.path.basename(path)})')
    out.append('')
    return '\n'.join(out)

# ---------- 主 ----------
files = sorted(glob.glob(f'{ROWS_DIR}/[0-9][0-9]-*.json'))
for f in files:
    base = os.path.basename(f)
    idx = int(base[:2])
    md = render(idx, f)
    out_name = base.replace('.json', '.md')
    with open(f'{DOCS_DIR}/{out_name}', 'w') as fp:
        fp.write(md)
print(f'wrote {len(files)} markdown files to {DOCS_DIR}/')
