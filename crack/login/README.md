# Claude Code 登录抓包档案（PKCE OAuth 2.0）

`claude-cli/2.1.126` 在 Linux + bun + Node v24.3.0 下，从用户在浏览器完成授权回到 CLI、再到拿到 access_token 并初始化账户上下文的**完整网络流量**。共 12 条登录链路相关请求，按时间顺序排列。

## 目录

```
crack/login/
├── README.md                ← 本文件（PKCE 流程总览 + 与 CPA-Claude 现状对比）
├── _split.py                ← raw → rows 解码脚本
├── _sanitize.py             ← 脱敏脚本（与 crack/README.md 占位符表一致）
├── raw/
│   ├── login-dump-full.json     ← whistle 后端最新 100 行 dump（包含历史噪声）
│   └── login-session-full.json  ← 显式 ids= 拉到的 55 行（含完整登录链路）
├── rows/                    ← 12 条登录请求的 JSON 原文（已 gunzip/brotli 解码）
│   ├── 01-GET-…api_hello.json
│   ├── 02-POST-…api_event_logging_v2_batch.json
│   ├── 03-POST-…datadoghq.com_api_v2_logs.json
│   ├── 04-POST-platform.claude.com_v1_oauth_token.json   ★ Token Exchange 核心
│   ├── 05-GET-…api_oauth_profile.json
│   ├── 06-GET-…api_oauth_claude_cli_roles.json
│   ├── 07-POST-…api_eval_sdk-zAZezfDKGoZuXXKe.json
│   ├── 08-POST-…api_event_logging_v2_batch.json
│   ├── 09-GET-…api_oauth_account_settings.json
│   ├── 10-GET-…api_claude_code_grove.json
│   ├── 11-GET-…api_claude_cli_bootstrap.json
│   └── 12-GET-…api_claude_code_penguin_mode.json
└── docs/                    ← 12 个独立 markdown
```

## 浏览器侧（不经代理）

CLI 启动 OAuth 时 spawn 系统浏览器跳到：

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

| 参数 | 是否随机 | 备注 |
|---|---|---|
| `code=true` | 固定 | 区分隐式模式 |
| `client_id=9d1c250a-e61b-44d9-88ed-5944d1962f5e` | **固定** | Claude Code 的 OAuth 应用 UUID |
| `response_type=code` | 固定 | PKCE 标准 |
| `redirect_uri=http://localhost:<port>/callback` | **端口随启动随机** | 本次 `46473`；CLI 内 HTTP 服务器监听 |
| `scope` | 固定 6 个 | 见下表 |
| `code_challenge` | **每次新生成** | `BASE64URL(SHA256(code_verifier))`，43 字符 |
| `code_challenge_method=S256` | 固定 | 不接受 `plain` |
| `state` | **每次新生成** | CSRF 防护，回调时原样回传 |

scope 列表：`org:create_api_key`、`user:profile`、`user:inference`、`user:sessions:claude_code`、`user:mcp_servers`、`user:file_upload`。

授权完成后浏览器被 30x 到 `http://localhost:46473/callback?code=...&state=...`，CLI 拿到一次性 `code`。

## 服务器侧（这 12 条都被 whistle 拦下）

| # | Phase | Method | URL | UA | Auth | Beta |
|---|---|---|---|---|---|---|
| 01 | 启动前 | GET | `api.anthropic.com/api/hello` | `claude-cli/2.1.126 (external, cli)` | _无_ | _无_ |
| 02 | 上次会话 telemetry | POST | `api.anthropic.com/api/event_logging/v2/batch` | `claude-code/2.1.126` | _OAuth (旧 token)_ | _无_ |
| 03 | 上次会话 telemetry | POST | `http-intake.logs.us5.datadoghq.com/api/v2/logs` | `axios/1.13.6` | `dd-api-key: pubea5604…` | _无_ |
| 04 | **Token Exchange** | POST | `platform.claude.com/v1/oauth/token` | `axios/1.13.6` | _无_ | _无_ |
| 05 | 账户初始化 | GET | `api.anthropic.com/api/oauth/profile` | `axios/1.13.6` | OAuth (new) | _无_ |
| 06 | 账户初始化 | GET | `api.anthropic.com/api/oauth/claude_cli/roles` | `axios/1.13.6` | OAuth (new) | _无_ |
| 07 | bootstrap | POST | `api.anthropic.com/api/eval/sdk-zAZezfDKGoZuXXKe` | `Bun/1.3.14` | OAuth (new) | `oauth-2025-04-20` |
| 08 | 登录 telemetry | POST | `api.anthropic.com/api/event_logging/v2/batch` | `claude-code/2.1.126` | OAuth (new) | `oauth-2025-04-20` |
| 09 | bootstrap | GET | `api.anthropic.com/api/oauth/account/settings` | `claude-code/2.1.126` | OAuth (new) | `oauth-2025-04-20` |
| 10 | bootstrap | GET | `api.anthropic.com/api/claude_code_grove` | `claude-cli/2.1.126 (external, cli)` | OAuth (new) | `oauth-2025-04-20` |
| 11 | bootstrap | GET | `api.anthropic.com/api/claude_cli/bootstrap` | `claude-code/2.1.126` | OAuth (new) | `oauth-2025-04-20` |
| 12 | bootstrap | GET | `api.anthropic.com/api/claude_code_penguin_mode` | `axios/1.13.6` | OAuth (new) | `oauth-2025-04-20` |

注：#02/#03 是 CLI 重新拉起前 ipc buffer 残留的上一会话事件，**不是登录流程**本身的一部分，只因紧挨着发出而一并捕获。新登录流程的核心是 #04（token exchange），完成后 #05~#12 并发拉账户初始化数据。

## ★ Token Exchange 核心（请求 #04）

**请求**：
```
POST https://platform.claude.com/v1/oauth/token
Content-Type: application/json
User-Agent: axios/1.13.6
Accept: application/json, text/plain, */*
Accept-Encoding: gzip, br

{
  "grant_type":   "authorization_code",
  "code":         "<浏览器回调拿到的一次性 code>",
  "redirect_uri": "http://localhost:46473/callback",
  "client_id":    "9d1c250a-e61b-44d9-88ed-5944d1962f5e",
  "code_verifier":"<原始 random verifier，CLI 本地保存>",
  "state":        "<授权时生成的 state>"
}
```

**响应**：HTTP 200 + JSON
```json
{
  "token_type":    "Bearer",
  "access_token":  "sk-ant-oat01-……",
  "expires_in":    28800,
  "refresh_token": "sk-ant-ort01-……",
  "scope":         "user:file_upload user:inference user:mcp_servers user:profile user:sessions:claude_code",
  "token_uuid":    "<服务端 token 记录 ID>",
  "organization":  { "uuid": "...", "name": "<email>'s Organization" },
  "account":       { "uuid": "...", "email_address": "<email>" }
}
```

**关键事实**：
- **新 host：`platform.claude.com`**（不是 `console.anthropic.com`，也不是 `api.anthropic.com`）
- `expires_in=28800`（**8 小时**）
- **scope 比申请时少 1 个**：`org:create_api_key` 被服务端剔除（需要额外授权才能获得）
- 响应里直接给出 `account.uuid + organization.uuid + email`，无需后续再调 profile 也能拿到

## 与 CPA-Claude 实现的对齐情况（v0.2.0 已完成）

下表标注的"现状"是 **v0.2.0 之后的实际行为**。`internal/auth/login.go` + `internal/auth/oauth.go` + `internal/auth/oauth_axios.go` 三处文件按本表完成迁移。

| 项 | CPA-Claude 现状 (v0.2.0+) | 实测真实 CC | 是否对齐 |
|---|---|---|---|
| 授权 URL | `https://claude.com/cai/oauth/authorize` | 同 | ✓ |
| Token endpoint | `https://platform.claude.com/v1/oauth/token` | 同 | ✓ |
| `client_id` | `9d1c250a-e61b-44d9-88ed-5944d1962f5e` | 同 | ✓ |
| `redirect_uri` | `http://localhost:54545/callback`（固定端口） | `http://localhost:<random>/callback` | 端口不同（CPA 配合 admin UI 固定）；服务端只校验"两次提交一致"，OK |
| scope（6 项） | `org:create_api_key user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload` | 同 | ✓ |
| 授权 URL 参数顺序 | 手工拼接：`code, client_id, response_type, redirect_uri, scope, code_challenge, code_challenge_method, state` | 同 | ✓ |
| PKCE verifier 长度 | 32 B → 43 char base64url no padding | 同 | ✓ |
| state 长度 | 32 B → 43 char base64url no padding | 同 | ✓ |
| Token-exchange body 字段顺序 | 用 ordered struct 强制：`grant_type, code, redirect_uri, client_id, code_verifier, state` | 同 | ✓ |
| Token-exchange / refresh UA | `axios/1.13.6` | 同 | ✓ |
| Token-exchange / refresh headers | `Content-Type / Accept: …,*/* / Accept-Encoding: gzip, br / Connection: close` | 同 | ✓ |
| 响应 gzip / brotli 解码 | `oauth_axios.go:readAxiosOAuthBody` | （CC 也手工解码） | ✓ |
| 解析响应 `account.uuid / organization.uuid / email_address` | 存盘到 `auth.AccountUUID` / `OrganizationUUID` / `Email` | 同 | ✓ |
| `expires_in` 处理 | 信任服务端值算 `expired` | `28800` | ✓ |

**未模拟（也无需模拟）的部分**：

登录后 #05~#12 的 8 条 bootstrap 请求（profile/roles/eval/event_logging/account_settings/grove/bootstrap/penguin_mode）。这些是真 CC 客户端在登录"内部"做的，CPA 作为代理本身并不发起登录后的客户端 bootstrap —— sidecar 已经覆盖 bootstrap/penguin/grove 等等价端点用于业务请求时的拟态。如果真要做"登录完成后立即把这台账号的 sidecar 启起来"，可在 `finishAnthropicLogin` 返回前显式 `s.sidecar.Notify(a, "")`，但这在常规场景下没有必要。

## 脱敏说明

与 `crack/README.md` 一致的占位符表，外加本批新增字段：

| 原始字段 | 占位符 |
|---|---|
| OAuth Bearer (`sk-ant-oat01-…`) | `sk-ant-oat01-REDACTED` |
| OAuth Refresh Token (`sk-ant-ort01-…`) | `sk-ant-ort01-REDACTED` |
| 邮箱 | `redacted@example.com` |
| 显示名 / full_name | `REDACTED_USER` |
| account_uuid | `00000000-0000-0000-0000-000000000001` |
| organization_uuid | `00000000-0000-0000-0000-000000000002` |
| token_uuid | `00000000-0000-0000-0000-000000000003` |
| session_id | `00000000-0000-0000-0000-000000000010` |
| device_id (64-hex) | 64 个 0 |
| OAuth `code` | `OAUTH_CODE_REDACTED` |
| OAuth `code_verifier` | `CODE_VERIFIER_REDACTED` |
| OAuth `state` | `OAUTH_STATE_REDACTED` |
| 主机用户名 / 路径 | `wjs` → `user`，`/home/wjs/` → `/home/user/` |
| Linux 内核 | `7.0.3-arch1-1` → `6.10.0-generic` |
| Linux 发行版 | `arch` → `generic`（只在 `linux_distro_id` 字段） |
| 终端 | `konsole` → `xterm` |
| 主机名 | `host` → `host` |
| Cloudflare cookie | `__cf_bm=…` / `_cfuvid=…` → `…REDACTED` |
| `cf-ray` / `request-id` | `REDACTED-cf-ray` / `req_REDACTED` |

公开字段保留原值：Datadog public intake key (`pubea5604…`，全球共享)、GrowthBook SDK key (`zAZezfDKGoZuXXKe`)、Anthropic OAuth client_id (`9d1c250a-e61b-44d9-88ed-5944d1962f5e`，OAuth 应用公开标识)。

## 重新生成

```bash
cd crack
python3 login/_split.py            # raw → rows
python3 login/_sanitize.py         # 在原地脱敏 raw + rows
python3 _gen.py login              # rows → docs
```
