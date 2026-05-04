# OAuth 官方订阅 vs 三方 API Key —— Claude Code 全量请求对比

同一台机器、同一个 `claude-cli/2.1.126`、同一个 `device_id`，对比两种鉴权姿势从启动到首条消息的所有出口流量。

- OAuth 模式（[oauth/](oauth/)）：32 条请求
- ApiKey 模式（[apikey/](apikey/)）：26 条请求

> 注：OAuth 模式中 #11/#15/#29/#31 是 npm 镜像噪声、#32 是退出 aborted；apikey 模式中 #8/#12/#23 是 npm 噪声、#25/#26 是退出 aborted。下面对比已剔除噪声。

---

## 1. 请求列表对比（去噪声后）

| 阶段 | OAuth 请求 | ApiKey 请求 | 差异 |
|---|---|---|---|
| 启动 bootstrap | `POST /api/eval/sdk-zAZezfDKGoZuXXKe` | — | **OAuth 独有**：GrowthBook feature flag 拉取（带账号属性） |
| 启动 bootstrap | `GET /api/oauth/account/settings` | — | **OAuth 独有**：claude.ai 个人偏好 |
| 启动 bootstrap | `GET /api/claude_code_grove` | — | **OAuth 独有**：grove 通知开关 |
| 启动 bootstrap | `GET /api/claude_cli/bootstrap` | `GET /api/claude_cli/bootstrap` | **同**（apikey 仍带 OAuth bearer） |
| 启动 bootstrap | `GET /api/claude_code_penguin_mode` | `GET /api/claude_code_penguin_mode` | **同**（apikey 仍带 OAuth bearer） |
| 启动 bootstrap | — | `GET https://www.fucheers.top/v1/models?limit=1000` | **ApiKey 独有**：列三方支持的模型 |
| 启动 quota | `POST /v1/messages` (Haiku, max_tokens=1, "quota") | — | **OAuth 独有**：额度探测，拿 ratelimit header |
| MCP 发现 | `POST localhost:8080/answer/api/v1/mcp` | `POST localhost:8080/answer/api/v1/mcp` | 同 |
| MCP 发现 | `GET /mcp-registry/v0/servers` x3 (cursor 翻页) | `GET /mcp-registry/v0/servers` x3 | 同 |
| MCP 发现 | `GET /v1/mcp_servers?limit=1000` | `GET /v1/mcp_servers?limit=1000` | 同（apikey 仍带 OAuth bearer） |
| 自更新 | `GET downloads.claude.ai/claude-code-releases/latest` | 同（出现两次） | 同 |
| Telemetry | `POST /api/event_logging/v2/batch` x6 | x4 | apikey 批数少，且 **无 Authorization header** |
| Telemetry (DD) | `POST datadoghq.com/api/v2/logs` x4 | x3 | 同（dd-api-key 共用） |
| 业务 | `POST /v1/messages?beta=true` x5 → `api.anthropic.com` | `POST /v1/messages?beta=true` x4 → `www.fucheers.top` | **关键差异**（详见 §3） |

---

## 2. 鉴权姿势

| 端点类别 | OAuth 模式 | ApiKey 模式 |
|---|---|---|
| `/v1/messages` 业务 | `Authorization: Bearer sk-ant-oat01-...`（Anthropic OAuth） | `Authorization: Bearer sk-REDACTED...`（三方 key） |
| `/v1/models` 模型清单 | 不存在 | `Authorization: Bearer sk-REDACTED...`（三方 key） |
| `/api/claude_cli/bootstrap` 等账号端点 | OAuth bearer | **OAuth bearer**（仍走 anthropic.com） |
| `/v1/mcp_servers` | OAuth bearer + `anthropic-beta: mcp-servers-2025-12-04` | **同** |
| `/api/event_logging/v2/batch` | OAuth bearer + `anthropic-beta: oauth-2025-04-20` + `x-service-name: claude-code` | **无 Authorization** + 无 `anthropic-beta` + 仅 `x-service-name` |
| `/mcp-registry/v0/servers` | 无认证（公共） | 无认证（公共） |
| `downloads.claude.ai` | 无认证 | 无认证 |
| `localhost:8080` MCP | 用户自定义 token | 用户自定义 token |
| `datadoghq.com /api/v2/logs` | `dd-api-key: pubea5...`（公钥写死） | **同** |

> **关键观察**：apikey 模式下，**所有走 `api.anthropic.com` 的非 telemetry 端点仍然带 OAuth bearer** —— 因为本机仍存有 OAuth 凭据。CLI 没把它清掉，自动复用。

---

## 3. 业务请求 `/v1/messages` 全方位对比

### 3.1 URL & 上游

| | OAuth | ApiKey |
|---|---|---|
| Host | `api.anthropic.com` | `www.fucheers.top` |
| Path | `/v1/messages?beta=true` | `/v1/messages?beta=true` |
| Server response | `cloudflare` | `openresty`（**新-API/one-api 项目**特征） |

### 3.2 请求头

| Header | OAuth | ApiKey |
|---|---|---|
| `Authorization` | `Bearer sk-ant-oat01-...` | `Bearer sk-REDACTED...` |
| `x-stainless-timeout` | `600` | `3000`（更长） |
| `x-client-request-id` | 每次新 UUID（如 `162f9d16-...`） | **不发** |
| `anthropic-version` | `2023-06-01` | `2023-06-01`（同） |
| `anthropic-dangerous-direct-browser-access` | `true` | `true` |
| `anthropic-beta` | **11 个** | **8 个** |
| `x-app: cli` | ✓ | ✓ |
| `user-agent` | `claude-cli/2.1.126 (external, cli)` | 同 |
| `x-claude-code-session-id` | UUID | UUID |
| `x-stainless-{arch,lang,os,pkg-version,retry-count,runtime,runtime-version}` | 同 | 同 |

**`anthropic-beta` 三个差集**：
- `oauth-2025-04-20`：OAuth 模式独有 —— 标记走 OAuth 鉴权路径，三方网关不需要
- `advanced-tool-use-2025-11-20`：OAuth 模式独有 —— 启用 `ToolSearch` 等高级工具，三方不支持
- `cache-diagnosis-2026-04-07`：OAuth 模式独有 —— 启用 `diagnostics.previous_message_id` 字段

### 3.3 请求体顶层字段

| Field | OAuth | ApiKey | 说明 |
|---|---|---|---|
| `model` | `claude-opus-4-7` | `claude-sonnet-4-6` | 用户在两种模式下选了不同默认模型 |
| `max_tokens` | 64000 | 32000 | apikey 上限更小 |
| `stream` | true | true | 同 |
| `thinking` | `{type:adaptive}` | `{type:adaptive}` | 同 |
| `context_management` | `{edits:[{type:clear_thinking_20251015,keep:all}]}` | 同 | 同（features beta 都启用了） |
| `output_config` | `{effort:medium}` | `{effort:medium}` | 同 |
| **`diagnostics`** | `{previous_message_id:msg_xxx}` | **缺失** | apikey 没启 `cache-diagnosis` beta |
| `metadata.user_id` | `{device_id, account_uuid:"00000000-...", session_id}` | `{device_id, account_uuid:"", session_id}` | account_uuid **空字符串** |
| `system` | `array[4]` | `array[3]` | 见 §3.4 |
| `tools` | `array[8]` | **`array[34]`** | 见 §3.5 |
| `messages` | `array[19]`（深对话） | `array[1]`（首条） | 时序差异 |

### 3.4 system 数组对比

| idx | OAuth (4 块) | ApiKey (3 块) |
|---|---|---|
| 0 | `cch=251fe;`（计费 header）| `cch=3282d;`（cch hash 不同 = build hash 不同）|
| 1 | `You are Claude Code...` 57B（无 cache_control） | 同上 + `cache_control:{type:ephemeral}` |
| 2 | 主 system 9925B + `{ephemeral, ttl:1h, scope:global}` | 主 system **26994B** + `{ephemeral}`（默认 5min, 无 ttl 无 scope） |
| 3 | per-session 20660B + `{ephemeral, ttl:1h}` | **不存在**（与 system[2] 合并） |

> **缓存效果差异巨大**：OAuth 拆成 global+session 1h 双层 ephemeral，命中率极高（首次 `cache_read=45410, cache_creation=2670`）；ApiKey 全用默认 5min 单层，**第一次完全不命中**（`input_tokens=37438` 全量计费），且 `cache_creation_*` 字段三方根本没透传。

### 3.5 tools 数组对比

| | OAuth | ApiKey |
|---|---|---|
| 数量 | **8** | **34** |
| 工具名 | Agent / Bash / Edit / Read / ScheduleWakeup / Skill / **ToolSearch** / Write | 全部展开（详见 [apikey docs/14](apikey/docs/14-POST-www.fucheers.top_v1_messages.md)） |
| 包含的 MCP 工具 | **0**（全部走 ToolSearch 延迟加载） | **5 个**（`mcp__context7__*`, `mcp__figma__*`, `mcp__plugin_context7_context7__*`） |
| 包含的内置 deferred 工具 | **0**（全部走 ToolSearch） | **20+**（CronCreate/CronDelete/CronList/EnterPlanMode/ExitPlanMode/EnterWorktree/ExitWorktree/LSP/Monitor/NotebookEdit/PushNotification/RemoteTrigger/TaskCreate/TaskGet/TaskList/TaskOutput/TaskStop/TaskUpdate/WebFetch/WebSearch/AskUserQuestion …） |

> **关键机制差异**：OAuth 模式靠 `advanced-tool-use-2025-11-20` beta，把 27 个工具全部"延迟加载" —— 客户端只发 8 个核心，模型用 `ToolSearch` 按需要拉 schema；ApiKey 模式因为三方网关不支持该 beta，**降级为经典模式：所有工具的完整 schema 一次性塞进 tools 数组**（这就是请求体大很多但缓存又没用上的原因）。

### 3.6 响应头对比

| Header | OAuth | ApiKey |
|---|---|---|
| `server` | `cloudflare` | `openresty` |
| `request-id` | `req_011CafsXw8vsa4sbNtMkxRzm` | **无** |
| `x-oneapi-request-id` | **无** | `202605031554087193120128268d9d6dq1LkluG` |
| `x-new-api-version` | **无** | `v0.1.0` |
| `anthropic-organization-id` | `00000000-...` | **无** |
| `anthropic-ratelimit-unified-status` | `allowed` | **无** |
| `anthropic-ratelimit-unified-5h-utilization` | `0.05` | **无** |
| `anthropic-ratelimit-unified-7d-utilization` | `0.01` | **无** |
| `anthropic-ratelimit-unified-overage-disabled-reason` | `org_level_disabled` | **无** |
| `traceresponse` | W3C trace | **无** |
| `cf-ray` | `9f604b...` | **无** |
| `set-cookie _cfuvid` | 有 | **无** |
| `content-security-policy` | `default-src 'none'; ...` | **无** |
| `strict-transport-security` | `max-age=31536000; includeSubDomains; preload` | `max-age=31536000`（弱） |
| `content-encoding` | `gzip` | **无**（明文 SSE） |

### 3.7 SSE 响应内容差异

- **JSON 字段顺序**：Anthropic 原生 `{"type":"...","message":{...}}`；三方网关按字母序输出 `{"message":{...},"type":"..."}` —— 反序列化看不出，但拼包/正则提取要小心。
- **`message_start.message.usage` 字段**：

| 字段 | OAuth | ApiKey |
|---|---|---|
| `input_tokens` | `6`（cache 命中后剩余） | `37438`（全量发送） |
| `cache_creation_input_tokens` | `2670` | **缺失** |
| `cache_read_input_tokens` | `45410` | **缺失** |
| `cache_creation.ephemeral_5m_input_tokens` | `0` | **缺失** |
| `cache_creation.ephemeral_1h_input_tokens` | `2670` | **缺失** |
| `service_tier` | `standard` | **缺失** |
| `inference_geo` | `not_available` | **缺失** |
| `output_tokens` | `5` | `1` |

> 三方网关 **不透传缓存元数据** —— 即使后端 Anthropic 命中了 cache，客户端也无从查证。

---

## 4. Telemetry 对比 (`/api/event_logging/v2/batch`)

| | OAuth | ApiKey |
|---|---|---|
| 总批次 | 6 + 1 aborted | 4 + 1 aborted |
| 总事件数 | 200（35 unique 事件名） | 194（左右） |
| `Authorization` 请求头 | `Bearer sk-ant-oat01-...` | **无**（匿名） |
| `anthropic-beta` 请求头 | `oauth-2025-04-20` | **无** |
| `x-service-name` 请求头 | `claude-code` | `claude-code`（同） |
| event_data.email | `redacted@example.com` | **null** |
| event_data.auth.account_uuid | `00000000-...` | **null** |
| event_data.auth.organization_uuid | `00000000-...` | **null** |
| event_data.session_id | OAuth session 的 uuid | apikey session 的 uuid |
| event_data.device_id | `1225ef802...` (sha256) | **同一个 sha256（机器没变）** |
| env.is_claude_ai_auth | `true` | **`false`** |

> **隐私结论**：apikey 模式下遥测**不带 email、不带 account/org UUID、不带 OAuth Bearer**，但**仍带 device_id (machine-id sha256)** —— 设备级仍可追踪（即便用户切到三方 API，仍然能跟之前的 OAuth 行为关联到同一台机器）。

---

## 5. 一句话总结

| 维度 | OAuth 模式 | ApiKey 模式 |
|---|---|---|
| 总请求数（含噪声） | 32 | 26 |
| 业务上游 | `api.anthropic.com` | `www.fucheers.top`（OneAPI 风格网关） |
| 业务鉴权 | OAuth Bearer | 三方 API Key |
| 账号/MCP/penguin/bootstrap 端点 | OAuth Bearer | **同样的 OAuth Bearer**（CLI 用本机残留凭据） |
| Telemetry 鉴权 | OAuth Bearer + email + account_uuid | **匿名**（仅 device_id） |
| `anthropic-beta` 数量 | 11 | 8（少 oauth/advanced-tool-use/cache-diagnosis） |
| 工具列表 | 8（核心） + ToolSearch 按需 | **34**（全展开，无 ToolSearch） |
| Prompt cache | 1h ephemeral + global scope，命中率高 | 默认 5min，**首次完全不命中** |
| 模型 | `claude-opus-4-7` | `claude-sonnet-4-6` |
| 响应头 ratelimit/org/request-id | 全套 | **几乎全无** |
| GrowthBook / claude.ai 个人偏好 / grove 拉取 | **有** | **没有** |
| 额度探测 (Haiku quota probe) | **有** | **没有** |
| 自更新版本检查 | 有 | 有 |
| Datadog 公共 intake 上报 | 有（OAuth 信息） | 有（脱敏字段） |

**风险/观察**

1. 即使配置三方 API key，`api.anthropic.com` 上的 6 个端点（bootstrap/penguin/mcp_servers + telemetry）**还会按 OAuth 凭据继续访问**，CLI 没做隔离 —— OAuth 账号信息持续向 Anthropic 上报。
2. **`device_id` 在两种模式下完全一致** —— Anthropic 可以把"匿名 apikey 用户"和已知 OAuth 账户关联到同一台设备。
3. 三方网关不支持 `prompt-caching-scope-2026-01-05` global scope，导致**首次请求就要把 30000+ token 全量计费**，远比 OAuth 模式贵。
4. 三方模式工具列表 4 倍膨胀（8 → 34），加上没缓存命中，**单次请求 token 开销显著更大**。
5. Datadog `dd-api-key` 在两种模式下相同（公钥），**与鉴权姿势无关**。
6. 三方网关 `www.fucheers.top` 暴露 `x-oneapi-request-id` 和 `x-new-api-version: v0.1.0` —— 是 [新-API / one-api](https://github.com/songquanpeng/one-api) 项目的特征 header。
