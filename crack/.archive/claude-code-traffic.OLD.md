# Claude Code 启动 → 首条消息 全量请求记录

> 抓包来源：本地 Whistle (127.0.0.1:8899) 反向 MITM `api.anthropic.com`、`downloads.claude.ai`、`http-intake.logs.us5.datadoghq.com` 等。
> 目标软件：`claude-cli/2.1.126` (external, cli)，运行环境 Linux + bun + Node v24.3.0。
> 会话 ID：`d85790bb-6261-43c0-982d-550eb177c8d5`（启动阶段）→ `0d7d3701-b10d-49a7-9324-8189c8c54152`（首条用户消息所在的 CLI 实例）。
> 原始数据：`crack/raw/whistle-dump.json`（全量 100 条）+ `crack/raw/session-c-full.json`（本次会话 32 条带 body）+ `crack/rows/NN-METHOD-host_path.json`（按时间分单文件，已 gunzip/brotli 解码）。

---

## 共用参数（理解后续 header 的速查）

- **OAuth 访问令牌**：`Authorization: Bearer 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`
  Anthropic Claude Max 订阅的 OAuth access token，前缀 `sk-ant-oat01-`。所有打到 `api.anthropic.com` 的业务请求都带它（少数 axios 调 `mcp-registry`/`releases` 的不带）。
- **Datadog API Key**：`dd-api-key: pubea5604404508cdd34afb69e6f42a05bc`（明文写死在 CLI 里，公开发往 Datadog Public Intake）。
- **设备 / 账号 / 组织标识**（在 telemetry & metadata.user_id 里反复出现）：
  - `device_id` / `id` = `1225ef802a7a88454489035a63d1966e11f2ba2065128262b7ff8ca3cd9afe0b`（machine-id 的 SHA-256）
  - `account_uuid` = `4fe8ffc6-4b58-4454-859d-1a6aa823154b`
  - `organization_uuid` = `dda51f19-a74e-4372-bc86-218118aff6e2`
  - `email` = `miara_bernu867@mail.com`
  - `subscriptionType=max`、`rateLimitTier=default_claude_max_20x`
- **常见请求头组合**
  | Header | 出现位置 | 含义 |
  |---|---|---|
  | `anthropic-version: 2023-06-01` | 所有 SDK / claude-cli 走的 anthropic API | API 主版本 |
  | `anthropic-beta` | 凡是真业务 (`/v1/messages`、`/api/eval/*`、`/api/event_logging/*`、`/v1/mcp_servers`) | 启用 beta 能力，逗号分隔多值 |
  | `anthropic-dangerous-direct-browser-access: true` | `/v1/messages` | OAuth 直连模式必须带 |
  | `x-app: cli` | `/v1/messages` | 区分入口 |
  | `x-claude-code-session-id: <uuid>` | `/v1/messages` | 一次 CLI 进程一个 |
  | `x-client-request-id: <uuid>` | 每次 `/v1/messages` 都不同 | 客户端去重/追踪 |
  | `x-stainless-*` | `/v1/messages`（来自 Stainless 生成的 SDK） | SDK arch/lang/runtime/版本，用于上报 |
  | `x-service-name: claude-code` | `/api/event_logging/v2/batch` | 标识服务 |
  | `user-agent: claude-cli/2.1.126 (external, cli)` | `/v1/messages` | 主入口 |
  | `user-agent: claude-code/2.1.126` | `/api/eval/*`、`/api/event_logging/*` | 内部 fetch |
  | `user-agent: Bun/1.3.14` | `/api/eval/sdk-zAZezfDKGoZuXXKe` | 直接走 bun 内置 fetch（绕过了 SDK） |
  | `user-agent: axios/1.13.6` | mcp-registry、`/v1/mcp_servers`、`downloads.claude.ai` | 一组用 axios 的辅助 fetcher |
  | `dd-api-key`、`ddsource: nodejs` | Datadog | 单独走 `http-intake.logs.us5.datadoghq.com` |

---

## 时序总览（32 条，#1–#32 = 截图里 1–27 + 5 条尾部 npm/connect 杂噪）

| # | T+ms | 方法 | 主机 / 路径 | 状态 | 用途 |
|---|---|---|---|---|---|
| 1 | 0 | POST | `api.anthropic.com /api/eval/sdk-zAZezfDKGoZuXXKe` | 200 | GrowthBook A/B/feature flags 拉取 |
| 2 | +162 | GET  | `api.anthropic.com /api/oauth/account/settings` | 200 | claude.ai 个人偏好（onboarding 状态等） |
| 3 | +165 | GET  | `api.anthropic.com /api/claude_code_grove` | 200 | `grove` 通知/宽限期开关 |
| 4 | +1.2s | GET | `api.anthropic.com /api/claude_cli/bootstrap` | 200 | 客户端引导：模型映射 + 账户额度元信息 |
| 5 | +1.2s | GET | `api.anthropic.com /api/claude_code_penguin_mode` | 200 | "penguin mode"（额度溢付）开关 |
| 6 | +1.27s | POST | `api.anthropic.com /v1/messages?beta=true` | 200 | **额度探测**：`max_tokens=1`、Haiku 一字回（"#"），用来预热/校验 OAuth |
| 7 | +1.27s | POST | `localhost:8080/answer/api/v1/mcp` | 502 | 本地配置的 MCP server，连接拒绝（用户没起服务） |
| 8 | +1.94s | GET | `api.anthropic.com /mcp-registry/v0/servers?...` | 200 | MCP 公共注册表 第 1 页 |
| 9 | +1.94s | GET | `api.anthropic.com /v1/mcp_servers?limit=1000` | 200 | 用户已配置的 MCP 服务器列表（空） |
| 10 | +2.4s | GET | `downloads.claude.ai /claude-code-releases/latest` | 200 | 最新版本号 `2.1.126` |
| 11 | +2.6s | CONNECT | `registry.npmmirror.com:443` | captureError | bun/node 进程额外触发 npm registry 连接，被 Whistle 拒证书 |
| 12 | +3.5s | GET | `api.anthropic.com /mcp-registry/v0/servers?...&cursor=com.crypto.mcp/crypto-com:1.0.0` | 200 | MCP registry 第 2 页 |
| 13 | +5.9s | GET | `api.anthropic.com /mcp-registry/v0/servers?...&cursor=io.customer/mcp:1.0.0` | 200 | MCP registry 第 3 页 |
| 14 | +10s  | POST | `api.anthropic.com /api/event_logging/v2/batch` | 200 | 启动期 telemetry 99 条事件批 |
| 15 | +12.6s | CONNECT | `registry.npmmirror.com:443` | captureError | npm registry 再连 |
| 16 | +15s | POST | `http-intake.logs.us5.datadoghq.com /api/v2/logs` | 202 | 同时往 Datadog 公共 intake 发结构化日志 |
| 17 | +25.2s | POST | `api.anthropic.com /v1/messages?beta=true` | 200 (SSE) | **首条真实用户消息**：Opus 4-7 + 8 个工具，流式 |
| 18 | +29.6s | POST | `api.anthropic.com /v1/messages?beta=true` | 200 (SSE) | 模型工具调用回合 2 |
| 19 | +35.2s | POST | `api.anthropic.com /api/event_logging/v2/batch` | 200 | 中段 telemetry |
| 20 | +41.9s | POST | `api.anthropic.com /v1/messages?beta=true` | 200 (SSE) | 工具回合 3 |
| 21 | +43.6s | POST | datadog `/api/v2/logs` | 202 | DD 日志 |
| 22 | +46.8s | POST | `api.anthropic.com /v1/messages?beta=true` | 200 (SSE) | 工具回合 4 |
| 23 | +51.9s | POST | event_logging | 200 | telemetry |
| 24 | +61.8s | POST | datadog | 202 | DD |
| 25 | +63.5s | POST | event_logging | 200 | telemetry |
| 26 | +64.7s | POST | `api.anthropic.com /v1/messages?beta=true` | 200 (SSE) | 工具回合 5 |
| 27 | +74.7s | POST | event_logging | 200 | telemetry |
| 28 | +79.7s | POST | datadog | 202 | DD |
| 29 | +112s | CONNECT | npmmirror | captureError | 噪声 |
| 30 | +119s | POST | event_logging | 200 | telemetry 收尾 |
| 31 | +122s | CONNECT | npmmirror | captureError | 噪声 |
| 32 | +123s | POST | event_logging | aborted | 进程退出，连接中断 |

> 真正"启动→发首条消息"的关键线 = **#1–#17**。#18 之后是模型工具循环里反复回合，结构等同 #17。

---

## 阶段 1：feature flag / 账号配置预热（#1–#5）

### #1 POST `https://api.anthropic.com/api/eval/sdk-zAZezfDKGoZuXXKe`

GrowthBook 风格的实验/旗标拉取。URL 末段是该实验池的 SDK key（公开）。

**Headers (req)**
```
Authorization: Bearer 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
Content-Type: application/json
anthropic-beta: oauth-2025-04-20
User-Agent: Bun/1.3.14
Accept: */*
Accept-Encoding: gzip, br
Content-Length: 593
```

**Body（GrowthBook attributes 上行）**
```json
{
  "attributes": {
    "id": "1225ef802a7a88454489035a63d1966e11f2ba2065128262b7ff8ca3cd9afe0b",
    "sessionId": "d85790bb-6261-43c0-982d-550eb177c8d5",
    "deviceID": "1225ef802a7a88454489035a63d1966e11f2ba2065128262b7ff8ca3cd9afe0b",
    "platform": "linux",
    "organizationUUID": "dda51f19-a74e-4372-bc86-218118aff6e2",
    "accountUUID": "4fe8ffc6-4b58-4454-859d-1a6aa823154b",
    "userType": "external",
    "subscriptionType": "max",
    "rateLimitTier": "default_claude_max_20x",
    "firstTokenTime": 1773926847998,
    "email": "miara_bernu867@mail.com",
    "appVersion": "2.1.126",
    "entrypoint": "cli"
  },
  "forcedVariations": {},
  "forcedFeatures": [],
  "url": ""
}
```

**Response（46.9 KB JSON, gzip）**——含上百个 `tengu_*` feature flag，每条形如：
```json
"tengu_cedar_inlet": {
  "value": "step",
  "on": true,
  "off": false,
  "source": "experiment",
  "experiment": { "key": "tengu_cedar_inlet", "variations": ["off","banner","step"], "hashAttribute": "id" },
  "experimentResult": { "inExperiment": true, "variationId": 2, "value": "step", ... },
  "ruleId": "fr_12sltqnmngchuzk"
}
```

> 关键观察：CLI 用 **bun 自带 fetch**（`User-Agent: Bun/1.3.14`）单独打这个端点，绕过 Stainless SDK；说明 GrowthBook 调用属于"bootstrap-only"基础设施。

---

### #2 GET `https://api.anthropic.com/api/oauth/account/settings`

claude.ai 个人偏好读取。

**Headers**
```
Accept: application/json, text/plain, */*
Authorization: Bearer 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
User-Agent: claude-code/2.1.126
anthropic-beta: oauth-2025-04-20
Connection: close
```

**Response (示例片段)**
```json
{
  "input_menu_pinned_items": null,
  "has_started_claudeai_onboarding": true,
  "has_finished_claudeai_onboarding": true,
  "dismissed_claudeai_banners": [{"banner_id":"install-hub-nux","dismissed_at":"2026-05-03T11:01:59.230000Z"}],
  "enabled_web_search": true,
  "tool_search_mode": "auto",
  "paprika_mode": "off",
  "grove_enabled": true,
  "grove_updated_at": "2026-04-20T15:11:13.706857Z",
  ...约 60 个偏好字段
}
```

---

### #3 GET `https://api.anthropic.com/api/claude_code_grove`

"Grove" 是 Anthropic 用于公告/宽限期的功能开关。

**Response**
```json
{"grove_enabled": true, "domain_excluded": false, "notice_is_grace_period": false, "notice_reminder_frequency": 0}
```

---

### #4 GET `https://api.anthropic.com/api/claude_cli/bootstrap`

CLI 引导信息——告诉客户端可用模型、模型成本、账户绑定信息。

**Response**
```json
{
  "client_data": {"kelp_forest_sonnet": "1000000"},
  "additional_model_options": null,
  "additional_model_costs": null,
  "oauth_account": {
    "account_uuid": "4fe8ffc6-4b58-4454-859d-1a6aa823154b",
    "account_email": "miara_bernu867@mail.com",
    "organization_uuid": "dda51f19-a74e-4372-bc86-218118aff6e2",
    "organization_name": "miara_bernu867@mail.com's Organization",
    "organization_type": "claude_max",
    "organization_rate_limit_tier": "default_claude_max_20x",
    "user_rate_limit_tier": null,
    "seat_tier": null
  }
}
```

> `kelp_forest_sonnet: 1000000` —— Sonnet 1M 上下文 beta 的内部代号。

---

### #5 GET `https://api.anthropic.com/api/claude_code_penguin_mode`

Penguin mode = 额度溢出按需付费开关。

**Response**
```json
{"enabled": false, "disabled_reason": "extra_usage_disabled"}
```

---

## 阶段 2：额度探测（#6）

### #6 POST `https://api.anthropic.com/v1/messages?beta=true`  —— **quota probe**

启动期跑一发最便宜 (`max_tokens:1`, Haiku) 的请求，确认 OAuth 仍有效、能拿 5h/7d ratelimit header。

**Request Headers（关键）**
```
Authorization: Bearer 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
Content-Type: application/json
User-Agent: claude-cli/2.1.126 (external, cli)
x-claude-code-session-id: d85790bb-6261-43c0-982d-550eb177c8d5
x-stainless-arch: x64
x-stainless-lang: js
x-stainless-os: Linux
x-stainless-package-version: 0.81.0
x-stainless-retry-count: 0
x-stainless-runtime: node
x-stainless-runtime-version: v24.3.0
x-stainless-timeout: 600
anthropic-beta: oauth-2025-04-20,interleaved-thinking-2025-05-14,redact-thinking-2026-02-12,context-management-2025-06-27,prompt-caching-scope-2026-01-05
anthropic-dangerous-direct-browser-access: true
anthropic-version: 2023-06-01
x-app: cli
x-client-request-id: e7aa2abd-83bd-46a1-86b2-dbcb23169e3b
```

**Body**
```json
{
  "model": "claude-haiku-4-5-20251001",
  "max_tokens": 1,
  "messages": [{"role": "user", "content": "quota"}],
  "metadata": {
    "user_id": "{\"device_id\":\"1225ef802a7a88454489035a63d1966e11f2ba2065128262b7ff8ca3cd9afe0b\",\"account_uuid\":\"4fe8ffc6-4b58-4454-859d-1a6aa823154b\",\"session_id\":\"d85790bb-6261-43c0-982d-550eb177c8d5\"}"
  }
}
```

**Response Headers（关键）**
```
anthropic-ratelimit-unified-status: allowed
anthropic-ratelimit-unified-5h-status: allowed
anthropic-ratelimit-unified-5h-reset: 1777824000
anthropic-ratelimit-unified-5h-utilization: 0.05
anthropic-ratelimit-unified-7d-status: allowed
anthropic-ratelimit-unified-7d-reset: 1778018400
anthropic-ratelimit-unified-7d-utilization: 0.01
anthropic-ratelimit-unified-representative-claim: five_hour
anthropic-ratelimit-unified-fallback-percentage: 0.5
anthropic-ratelimit-unified-overage-disabled-reason: org_level_disabled
anthropic-ratelimit-unified-overage-status: rejected
request-id: req_011CafsW7t2qroLB3RypPNbT
anthropic-organization-id: dda51f19-a74e-4372-bc86-218118aff6e2
```

**Response Body**
```json
{
  "model": "claude-haiku-4-5-20251001",
  "id": "msg_01Vhxi3FyK7TfRipWfWPd2pQ",
  "type": "message",
  "role": "assistant",
  "content": [{"type": "text", "text": "#"}],
  "stop_reason": "max_tokens",
  "usage": {
    "input_tokens": 8,
    "cache_creation_input_tokens": 0,
    "cache_read_input_tokens": 0,
    "cache_creation": {"ephemeral_5m_input_tokens": 0, "ephemeral_1h_input_tokens": 0},
    "output_tokens": 1,
    "service_tier": "standard",
    "inference_geo": "not_available"
  },
  "context_management": {"applied_edits": []}
}
```

> 注意 `anthropic-beta` 在这条比业务 `/v1/messages`（#17 起）少了 `claude-code-20250219, context-1m-2025-08-07, advisor-tool-2026-03-01, advanced-tool-use-2025-11-20, effort-2025-11-24, cache-diagnosis-2026-04-07` —— 因为这是个无工具/无 1M 上下文/不参与 effort 调度的 quota ping。

---

## 阶段 3：MCP 配置发现（#7–#13）

### #7 POST `http://localhost:8080/answer/api/v1/mcp`  —— 用户配置的 MCP server

**Request Headers**
```
Accept: application/json, text/event-stream
Authorization: Bearer sk_019cdd2958c37d14938fb5e07d8a82b1   ← 这是用户在 ~/.claude.json 里配的 MCP token，跟 anthropic OAuth 无关
Content-Type: application/json
User-Agent: claude-code/2.1.126 (cli)
```
**Body**
```json
{"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{"roots":{},"elicitation":{}},"clientInfo":{"name":"claude-code","title":"Claude Code","version":"2.1.126","description":"Anthropic's agentic coding tool","websiteUrl":"https://claude.com/claude-code"}},"jsonrpc":"2.0","id":0}
```
**Response**：`502` —— 本地 8080 拒绝连接（用户配了但没起这个 MCP server）。

---

### #8/#12/#13 GET `https://api.anthropic.com/mcp-registry/v0/servers?...`

**Headers（无 OAuth！）**
```
Accept: application/json, text/plain, */*
User-Agent: axios/1.13.6
Accept-Encoding: gzip, br
Connection: close
```
> 公共注册表，匿名访问。CLI 用 axios 翻 3 页拉全部 MCP server 元信息（每页 100，约 320 KB）。
> 翻页通过 `cursor=` query 参数推进。

### #9 GET `https://api.anthropic.com/v1/mcp_servers?limit=1000`

**Headers**
```
Authorization: Bearer sk-ant-oat01-...                  ← 这条要带 OAuth（用户私有 MCP）
anthropic-beta: mcp-servers-2025-12-04
anthropic-version: 2023-06-01
User-Agent: axios/1.13.6
Connection: close
```
**Response**：`{"data":[],"next_page":null}`（用户没在云端配 MCP）

### #10 GET `https://downloads.claude.ai/claude-code-releases/latest`

**Headers**：纯 axios，无认证。
**Response**：`2.1.126`（纯文本一行）。CLI 拿这个跟自身版本对比来提示更新。

### #11/#15/#29/#31 CONNECT `registry.npmmirror.com:443`

bun/node 内部对 npm registry 的预连接（可能是 `npm/yarn/pnpm` package-manager 探测引发）。Whistle 没装那边证书，全部 captureError。**不是 Anthropic 流量**。

---

## 阶段 4：启动期 telemetry（#14、#16）

### #14 POST `https://api.anthropic.com/api/event_logging/v2/batch`  —— 196 KB

**Headers**
```
Authorization: Bearer sk-ant-oat01-...
Content-Type: application/json
User-Agent: claude-code/2.1.126
anthropic-beta: oauth-2025-04-20
x-service-name: claude-code
Content-Length: 196697
```

**Body 结构**
```json
{
  "events": [
    {
      "event_type": "ClaudeCodeInternalEvent",
      "event_data": {
        "event_name": "tengu_dir_search",
        "client_timestamp": "2026-05-03T15:27:55.821Z",
        "model": "claude-opus-4-7[1m]",
        "session_id": "d85790bb-...",
        "user_type": "external",
        "betas": "claude-code-20250219,oauth-2025-04-20,context-1m-2025-08-07,...",
        "env": {
          "platform": "linux", "node_version": "v24.3.0", "terminal": "konsole",
          "package_managers": "npm,yarn,pnpm", "runtimes": "bun,deno,node",
          "is_running_with_bun": true, "is_ci": false,
          "version": "2.1.126", "arch": "x64",
          "linux_distro_id": "arch", "linux_kernel": "7.0.3-arch1-1",
          "vcs": "git", "shell": "zsh", ...
        },
        "entrypoint": "cli",
        "is_interactive": true,
        "client_type": "cli",
        "process": "<base64 of {uptime,rss,heapTotal,heapUsed,external,arrayBuffers,constrainedMemory,cpuUsage}>",
        "additional_metadata": "<base64 JSON of event-specific fields>",
        "auth": "...",
        "event_id": "...", "device_id": "...", "email": "..."
      }
    }
    /* … 99 events total in this batch … */
  ]
}
```

**Response**：`{"accepted_count":99,"rejected_count":0}`

观察到的 99 条事件主要 `event_name`（去重后高频）：
`tengu_sysprompt_boundary_found`, `tengu_api_cache_breakpoints`, `tengu_attachment_compute_duration`, `tengu_tool_search_mode_decision`, `tengu_api_before_normalize`, `tengu_api_after_normalize`, `tengu_sysprompt_block`, `tengu_api_query`, `tengu_api_success`, `tengu_mcp_tools_commands_loaded`, `tengu_paste_text`, `tengu_input_prompt`, `tengu_file_history_snapshot_success`, `tengu_memdir_loaded`, `tengu_tool_use_granted_in_config`, `tengu_streaming_tool_execution_used`, `tengu_bash_tool_command_executed`, `tengu_tool_use_success`, `tengu_tool_empty_result`, `tengu_query_before_attachments`, `tengu_attachments`, `tengu_query_after_attachments`, `tengu_mcp_server_connection_failed`, `tengu_context_size`, `tengu_file_suggestions_git_ls_files`, `tengu_prompt_suggestion`, `tengu_tip_shown`, `tengu_dir_search`。

> `process` 与 `additional_metadata` 字段是 **base64 over JSON**（不是加密，仅做体积/编码统一）。

---

### #16 POST `https://http-intake.logs.us5.datadoghq.com/api/v2/logs`

**Headers**
```
Content-Type: application/json
User-Agent: axios/1.13.6
dd-api-key: pubea5604404508cdd34afb69e6f42a05bc          ← Datadog Public Intake Key（明文写死在客户端）
Content-Length: 8192
```

**Body**：JSON 数组，每条形如
```json
{
  "ddsource": "nodejs",
  "ddtags": "event:tengu_exit,arch:x64,client_type:cli,entrypoint:cli,model:claude-opus-4-7,platform:linux,subscription_type:max,user_bucket:21,user_type:external,version:2.1.126,version_base:2.1.126",
  "message": "tengu_exit",
  "service": "claude-code",
  "hostname": "claude-code",
  "env": "external",
  "model": "claude-opus-4-7",
  "session_id": "d85790bb-...",
  "user_type": "external",
  "betas": "...",
  "entrypoint": "cli",
  "is_interactive": "true",
  "client_type": "cli",
  "process_metrics": {"uptime":..., "rss":..., "heapTotal":..., "cpuUsage":{"user":..., "system":...}},
  "subscription_type": "max",
  ...
}
```

> 与 `event_logging` 重复了一份，发往独立的 Datadog 公共日志通道。两条管道并行（用 7d ddtags 做切片）。

---

## 阶段 5：首条业务消息（#17）—— **重头戏**

### #17 POST `https://api.anthropic.com/v1/messages?beta=true`  —— Stream

**Request Headers（完整）**
```
Authorization: Bearer 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
Accept: application/json
Content-Type: application/json
User-Agent: claude-cli/2.1.126 (external, cli)
x-claude-code-session-id: 0d7d3701-b10d-49a7-9324-8189c8c54152
x-stainless-arch: x64
x-stainless-lang: js
x-stainless-os: Linux
x-stainless-package-version: 0.81.0
x-stainless-retry-count: 0
x-stainless-runtime: node
x-stainless-runtime-version: v24.3.0
x-stainless-timeout: 600
anthropic-beta: claude-code-20250219,oauth-2025-04-20,context-1m-2025-08-07,interleaved-thinking-2025-05-14,redact-thinking-2026-02-12,context-management-2025-06-27,prompt-caching-scope-2026-01-05,advisor-tool-2026-03-01,advanced-tool-use-2025-11-20,effort-2025-11-24,cache-diagnosis-2026-04-07
anthropic-dangerous-direct-browser-access: true
anthropic-version: 2023-06-01
x-app: cli
x-client-request-id: 162f9d16-8f25-4e2c-81f8-1ad89452fa1c
Connection: keep-alive
Host: api.anthropic.com
Accept-Encoding: gzip, br
Content-Length: 129059
```

注意比 #6 多了 6 个 beta：
- `claude-code-20250219` —— Claude Code 私有协议层
- `context-1m-2025-08-07` —— Sonnet/Opus 1M 上下文
- `advisor-tool-2026-03-01` —— Advisor 工具
- `advanced-tool-use-2025-11-20` —— Advanced tool use
- `effort-2025-11-24` —— `output_config.effort` 字段
- `cache-diagnosis-2026-04-07` —— `diagnostics` 字段

**Request Body（顶层结构）**
```jsonc
{
  "model": "claude-opus-4-7",
  "max_tokens": 64000,
  "stream": true,
  "thinking": { "type": "adaptive" },
  "context_management": { "edits": [{ "type": "clear_thinking_20251015", "keep": "all" }] },
  "output_config": { "effort": "medium" },
  "diagnostics": { "previous_message_id": "msg_01YZ3FUAJF24e3RA4AsmP7Y8" },
  "metadata": {
    "user_id": "{\"device_id\":\"1225ef80...0b\",\"account_uuid\":\"4fe8ffc6-...\",\"session_id\":\"0d7d3701-...\"}"
  },
  "system": [
    /* 0 */ { "type": "text", "text": "x-anthropic-billing-header: cc_version=2.1.126.c5f; cc_entrypoint=cli; cch=251fe;" /* len 81 */ },
    /* 1 */ { "type": "text", "text": "You are Claude Code, Anthropic's official CLI for Claude." /* len 57 */ },
    /* 2 */ { "type": "text",
              "cache_control": { "type": "ephemeral", "ttl": "1h", "scope": "global" },
              "text": "<约 9925 字的 Claude Code 主 system prompt，含 'IMPORTANT: Assist with authorized security testing ...' 等> " },
    /* 3 */ { "type": "text",
              "cache_control": { "type": "ephemeral", "ttl": "1h" },
              "text": "<约 20660 字的 user-instructions / 工具用法 / 输出约束 / session-specific guidance / claudeMd 等附加 system> " }
  ],
  "tools": [
    { "name": "Agent",   "description": "Launch a new agent ..." , "input_schema": { ... } },
    { "name": "Bash",    "description": "..." , "input_schema": { ... } },
    { "name": "Edit",    "description": "..." , "input_schema": { ... } },
    { "name": "Read",    "description": "..." , "input_schema": { ... } },
    { "name": "ScheduleWakeup", ... },
    { "name": "Skill",   ... },
    { "name": "ToolSearch", ... },
    { "name": "Write",   ... }
  ],
  "messages": [ /* 19 条，user/assistant 交替 */ ]
}
```

> **重要发现 1**：`system` 数组的第 0 块是一个伪 system 块，内容其实是要塞给计费侧的 header：`x-anthropic-billing-header: cc_version=2.1.126.c5f; cc_entrypoint=cli; cch=251fe;`。这是 Anthropic 把"客户端身份/版本/缓存提示"通过 system 块走带的方式（走 system 比走 header 更难被透明代理改）。
>
> **重要发现 2**：缓存策略——只有 system[2] 与 system[3] 两块带 `cache_control` 标记 ephemeral 1h。`system[2]` 多了 `"scope":"global"`（跨 session 全局缓存）。
>
> **重要发现 3**：`context_management.edits[0] = { type:"clear_thinking_20251015", keep:"all" }` 告诉服务端在新一轮 turn 时清掉历史 thinking 块（仅保留对话） —— 这是 Anthropic 的服务端 thinking 修剪 API。
>
> **重要发现 4**：`diagnostics.previous_message_id` —— 把上一条 assistant 回复的 `msg_xxx` ID 传回，用于服务端缓存命中诊断（配合 `cache-diagnosis-2026-04-07` beta）。

仅启用了 8 个 "原生" 工具，所有 MCP 工具走 `ToolSearch` 按需注入（这就是 system 块里那个 deferred-tools 名单的来由）。

**Response Headers（关键）**
```
content-type: text/event-stream; charset=utf-8
cache-control: no-cache
anthropic-ratelimit-unified-status: allowed
anthropic-ratelimit-unified-5h-utilization: 0.05
anthropic-ratelimit-unified-7d-utilization: 0.01
request-id: req_011CafsXw8vsa4sbNtMkxRzm
anthropic-organization-id: dda51f19-a74e-4372-bc86-218118aff6e2
```

**Response Body（SSE 事件流，节选 message_start）**
```
event: message_start
data: {"type":"message_start","message":{"model":"claude-opus-4-7","id":"msg_01CfDzdTf5qimjFKcWf1sycf","type":"message","role":"assistant","content":[],"stop_reason":null,"usage":{"input_tokens":6,"cache_creation_input_tokens":2670,"cache_read_input_tokens":45410,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":2670},"output_tokens":5,"service_tier":"standard","inference_geo":"not_available"},"diagnostics":null}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_0171iMmLa4gZGS1gaE2BH41t","name":"Bash","input":{},"caller":{"type":"direct"}}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"comm"}}
... (流式 partial_json 拼出工具参数)
```

> `usage.cache_read_input_tokens=45410, cache_creation_input_tokens=2670` —— prompt cache 大头命中（4.5w token 复用，2.6k 写入到 1h 桶），首字节延迟 985ms。

---

## 阶段 6：模型工具循环（#18, #20, #22, #26）

每次 = 上一次 SSE 流结束后，CLI 把 `tool_result` 拼回 `messages` 数组，再发一发新的 `/v1/messages?beta=true`。**结构与 #17 完全相同**，只是：
- `messages` 越来越长（21、23、25、… 条）
- `metadata.user_id.session_id` 与 #17 一致 `0d7d3701-...`
- `x-client-request-id` 每次新生成
- 工具列表、system blocks、context_management、diagnostics 不变

举例 #18：`messages` 已扩到 21 条，前缀完全相同（命中 1h ephemeral cache），只在末尾追加新的 `tool_result` + 后续 user 消息。

---

## 阶段 7：增量 telemetry（#19, #23, #25, #27, #30, #32 + datadog #21, #24, #28）

跟 #14、#16 同结构，体量更小：每次 batch 几十条 `tengu_*` 事件，按"工具调用 / 流式回合 / 缓存命中"等里程碑成批上报。每次 datadog batch ≈ 4–9 KB。

---

## 关键安全/隐私观察清单

1. **OAuth Bearer 全裸**：每条业务请求 header 直接挂 `sk-ant-oat01-...`，可被任何 MITM（包括本地 Whistle）截获。CPA-Claude 这类代理就是利用这一点做 fan-out。
2. **Datadog API key 写死在客户端**：`pubea5604404508cdd34afb69e6f42a05bc`（pub 前缀 = public intake key）。任何拦截客户端流量的人都拿得到，但只能 *写* 不能读。
3. **设备指纹强绑定**：`device_id` 是稳定的 SHA-256（推断来自 machine-id），跟 `account_uuid` 一起发送，足以做端绑定、跨会话追踪。
4. **每条 `/v1/messages` 都带 `email`**：通过 metadata.user_id JSON 内嵌。
5. **system 块第一条不是 prompt 而是计费/客户端 header**：`x-anthropic-billing-header: cc_version=...; cc_entrypoint=...; cch=...;`。说明服务端把 system[0] 当成带内 metadata 通道来识别客户端版本。
6. **完整环境上报**：Linux distro、kernel、shell、terminal、所有装的 package manager、runtime（bun/deno/node）、是否 CI/GitHub Action，全部明文进 telemetry。
7. **prompt cache 标记两层 TTL**：`ephemeral 5m` 与 `ephemeral 1h`；`system[2]` 多了 `scope:"global"` 表示跨 session 全局复用。
8. **tools = 9 个内置 + ToolSearch 按需**：MCP/技能/插件全部走 ToolSearch 延迟加载，这是 #17 system 块里那一长串 deferred-tools 列表的成因。

---

## 文件索引

```
crack/
├── claude-code-traffic.md          ← 本文件
├── raw/
│   ├── whistle-dump.json           ← /cgi-bin/get-data 全量（100 条，无 body）
│   └── session-c-full.json         ← /cgi-bin/get-data?ids=...&dumpCount=1（32 条带 body）
└── rows/
    ├── _manifest.json              ← idx → file 映射
    ├── 01-POST-api.anthropic.com_api_eval_sdk-zAZezfDKGoZuXXKe.json
    ├── 02-GET-api.anthropic.com_api_oauth_account_settings.json
    ├── 03-GET-api.anthropic.com_api_claude_code_grove.json
    ├── 04-GET-api.anthropic.com_api_claude_cli_bootstrap.json
    ├── 05-GET-api.anthropic.com_api_claude_code_penguin_mode.json
    ├── 06-POST-api.anthropic.com_v1_messages_beta_true.json     ← quota probe
    ├── 07-POST-localhost8080_answer_api_v1_mcp.json             ← MCP init 502
    ├── 08-GET-api.anthropic.com_mcp-registry_v0_servers...json
    ├── 09-GET-api.anthropic.com_v1_mcp_servers_limit_1000.json
    ├── 10-GET-downloads.claude.ai_claude-code-releases_latest.json
    ├── 11-CONNECT-registry.npmmirror.com443.json
    ├── 12,13-GET-...mcp-registry... cursor pages
    ├── 14-POST-...event_logging_v2_batch.json                   ← 196 KB telemetry
    ├── 16-POST-http-intake.logs.us5.datadoghq.com_api_v2_logs.json
    ├── 17-POST-api.anthropic.com_v1_messages_beta_true.json     ← 首条用户消息（重头）
    ├── 18,20,22,26-POST-...v1/messages...                       ← 工具循环回合
    └── 19,23,25,27,30,32-POST-...event_logging...
        21,24,28-POST-...datadog...
        29,31-CONNECT-npmmirror
```
