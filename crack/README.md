# Claude Code 抓包档案

`claude-cli/2.1.126` 在 Linux + bun + Node v24.3.0 下，从**进程启动**到**首条用户消息发出**的全部网络流量，对比两种鉴权姿势：

- **OAuth 官方订阅**（`Bearer sk-ant-oat01-...` → `api.anthropic.com`）
- **三方 API Key**（`Bearer sk-REDACTED...` → `www.fucheers.top` 网关）

## 目录

```
crack/
├── README.md         ← 本文件（双模式入口）
├── COMPARE.md        ← OAuth vs ApiKey 全方位对比 ★ 重点
├── scripts/          ← 所有处理脚本（split / gen / sanitize），见 scripts/README.md
├── raw/              ← Whistle 原始 dump（两次会话的全量 + 单次会话带 body）
│   ├── oauth-dump-full.json
│   ├── oauth-session-full.json
│   ├── apikey-dump-full.json
│   └── apikey-session-full.json
├── oauth/            ← OAuth 模式（32 条请求）
│   ├── docs/         ← 32 个独立 markdown
│   └── rows/         ← 32 个 JSON 原文（已 gunzip/brotli 解码）
├── apikey/           ← ApiKey 模式（26 条请求）
│   ├── docs/         ← 26 个独立 markdown
│   └── rows/         ← 26 个 JSON 原文
├── login/            ← OAuth PKCE 登录链路（12 条请求 + 自带 raw/）
│   ├── README.md     ← PKCE 流程总览
│   ├── docs/
│   ├── rows/
│   └── raw/
└── .archive/         ← 旧的单文档汇总（已废弃）
```

## 脱敏说明

发布到公开仓库前，下列字段已在所有 raw / rows / docs / md 文件中**统一替换为占位符**，可放心阅读和重新生成：

| 原始字段 | 占位符 |
|---|---|
| OAuth Bearer (`sk-ant-oat01-...`) | `sk-ant-oat01-REDACTED` |
| 三方 API Key (`sk-...`) | `sk-REDACTED` |
| 邮箱 | `redacted@example.com` |
| account_uuid | `00000000-0000-0000-0000-000000000001` |
| organization_uuid | `00000000-0000-0000-0000-000000000002` |
| device_id (64-hex) | 64 个 0 |
| 主机用户名 / 路径 | `wjs` → `user`, `/home/wjs/` → `/home/user/` |
| LAN IP | `10.3.31.133` / `10.129.81.88` → `10.0.0.10` / `10.0.0.20` |
| Linux 内核 | `7.0.3-arch1-1` → `6.10.0-generic` |
| Linux 发行版 | `arch` → `generic` |
| 终端 | `konsole` → `xterm` |

公开协议字段保留原值，包括：Datadog client token (`pubea5604...` 是全球共享的公开收件密钥，与账号无关，参见 COMPARE.md)、GrowthBook SDK key (`zAZezfDKGoZuXXKe`，同样硬编码在 CC 二进制里)、所有 Anthropic / Datadog endpoint URL。

## 共用上下文（两种模式都成立）

- **同一台机器、同一个 device_id**：`0000000000000000000000000000000000000000000000000000000000000000`（machine-id 的 SHA-256）
- **OAuth Bearer**：`sk-ant-oat01-REDACTED`（仅 OAuth 模式 + apikey 模式打 anthropic.com 时复用）
- **三方 ApiKey**：`sk-REDACTED`（仅 apikey 模式打 `www.fucheers.top`）
- **Datadog Public Intake Key**：`pubea5604404508cdd34afb69e6f42a05bc`（公钥写死客户端，两模式共用）
- **Anthropic 账户**：`redacted@example.com` / account `00000000-0000-0000-0000-000000000001` / org `00000000-0000-0000-0000-000000000002`（subscription `max`，rateLimit `default_claude_max_20x`）

## 推荐阅读顺序

1. **先看 [COMPARE.md](COMPARE.md)** —— 直接对比两种模式各维度差异。
2. **看 OAuth 端最关键的两条**：
   - [oauth/docs/06](oauth/docs/06-POST-api.anthropic.com_v1_messages.md) — 额度探测，理解 `/v1/messages` 最简形态 + ratelimit 响应头
   - [oauth/docs/17](oauth/docs/17-POST-api.anthropic.com_v1_messages.md) — 首条业务消息，理解完整请求体结构
3. **看 ApiKey 端最关键的三条**：
   - [apikey/docs/03](apikey/docs/03-GET-www.fucheers.top_v1_models.md) — 三方独有的 `/v1/models` 模型列表端点
   - [apikey/docs/14](apikey/docs/14-POST-www.fucheers.top_v1_messages.md) — 首条业务消息，对比 OAuth 模式的削减
   - [apikey/docs/11](apikey/docs/11-POST-api.anthropic.com_api_event_logging_v2_batch.md) — 匿名 telemetry 与 OAuth 的差异
4. **看分模式索引**：
   - [oauth/](oauth/) 下的所有 docs（按 idx 顺序）
   - [apikey/](apikey/) 下的所有 docs

## 重新生成

所有处理脚本都在 [`scripts/`](scripts/README.md)。脚本路径自锚定，cwd 无关：

```bash
python3 crack/scripts/split.py oauth      # raw/oauth-session-full.json → oauth/rows/
python3 crack/scripts/split.py apikey     # raw/apikey-session-full.json → apikey/rows/
python3 crack/scripts/split.py login      # login/raw/login-session-full.json → login/rows/
python3 crack/scripts/sanitize.py         # 跨 crack/ 全量脱敏（幂等）
python3 crack/scripts/gen.py oauth        # oauth/rows/ → oauth/docs/
python3 crack/scripts/gen.py apikey       # apikey/rows/ → apikey/docs/
python3 crack/scripts/gen.py login        # login/rows/ → login/docs/
```

修改 `scripts/gen.py` 里的 `NOTES_BY_MODE['<mode>']` / `EXTRA_BY_MODE['<mode>']` 可调整每条 markdown 的注释和"字段深挖"附录。

## 几个关键发现（提炼自 COMPARE.md）

1. **OAuth 凭据残留泄漏**：apikey 模式下，所有走 `api.anthropic.com` 的非 telemetry 端点（bootstrap/penguin/mcp_servers/...）**仍然带本地残留的 OAuth Bearer**，CLI 没做隔离。
2. **device_id 跨模式不变**：machine-id sha256 在两种模式下完全一致 → Anthropic 可以把"匿名 apikey 用户"和已知 OAuth 账户关联到同一设备。
3. **业务上游切换**：OAuth → `api.anthropic.com (cloudflare)`；ApiKey → `www.fucheers.top (openresty + new-api 网关特征)`。
4. **anthropic-beta 三个差集**：apikey 模式少 `oauth-2025-04-20` / `advanced-tool-use-2025-11-20` / `cache-diagnosis-2026-04-07`。
5. **工具列表 8 vs 34**：OAuth 走 `ToolSearch` 延迟加载，apikey 全展开（请求体大 4 倍）。
6. **Cache 配置降级**：OAuth 用 `ephemeral 1h scope=global` 双层缓存（首次命中 45410/47081 token）；apikey 用默认 5min 单层（首次完全 miss，全量 37438 token 计费）+ 三方网关不透传缓存元数据。
7. **额度探测仅 OAuth 有**：apikey 模式没有 `POST /v1/messages` 的 quota probe（Haiku 单字 "quota"）。
8. **GrowthBook + claude.ai 偏好 + grove 仅 OAuth 有**：apikey 模式跳过这 3 条 bootstrap 请求。
9. **新增 `/v1/models?limit=1000`**：apikey 模式独有，三方网关返回 OpenAI 风格的模型清单。
10. **Telemetry 匿名化但仍可识别**：apikey 模式 telemetry 不带 email/account/org/auth，但 device_id 仍在 → 设备级关联仍然成立。
