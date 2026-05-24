# Kiro CLI — Login / Logout 链路抓包

`kiro login` → `kiro logout` → `kiro login` 一次完整循环的全部网络流量，**14 条请求**。

补充 [`crack/kiro/`](../README.md) 主档案 —— 主档案是"已登录会话的业务流"，本目录是"凭据生命周期"。

## 浏览器侧入口（不抓包，仅复述）

`kiro login` 在 CLI 内 spawn 系统浏览器，打开：

```
https://app.kiro.dev/signin
    ?state=KIRO_OAUTH_STATE
    &code_challenge=KIRO_CODE_CHALLENGE_REDACTED
    &code_challenge_method=S256
    &redirect_uri=http%3A%2F%2Flocalhost%3A3128
    &redirect_from=kirocli
```

| 参数 | 含义 | 是否随机 |
|---|---|---|
| `state` | 10 char 随机串，CSRF + 关联 challenge↔verifier | 每次新生成 |
| `code_challenge` | `BASE64URL(SHA256(code_verifier))`，43 char | 每次新生成 |
| `code_challenge_method` | `S256`（不接受 `plain`） | 固定 |
| `redirect_uri` | **`http://localhost:3128`（端口 3128 是 hard-code，不像 Claude Code 随端口）** | 固定 host:port |
| `redirect_from` | 固定 `kirocli` —— 让 app.kiro.dev 渲染 CLI 专用提示 | 固定 |

用户在 app.kiro.dev/signin 选 IdP（GitHub / Google / Builder ID）→ 完成 IdP 登录 → 浏览器被 302 到：

```
http://localhost:3128/oauth/callback?code=<uuidv4>&state=<echo>&login_option=github
```

CLI 监听端口 3128，从回调拿到 `code` 与 `login_option`，把 **`?login_option=github` 拼回到** `redirect_uri` 里，然后开始本目录抓到的网络请求。

## 14 条请求时序

| # | 阶段 | host | 端点 | 备注 |
|---|---|---|---|---|
| 01 | 登录前 | cognito-identity | `GetId` | 申请匿名 IdentityId（pool 全局共享） |
| 02 | 登录前 | cognito-identity | `GetCredentialsForIdentity` | 换 STS 临时凭据（用于 telemetry SigV4） |
| 03 | 登录前 | client-telemetry | `/metrics` | `cliSubcommandExecuted subcommand=login` |
| **04** | **登录** | **prod.us-east-1.auth.desktop.kiro.dev** | **`/oauth/token`** | **PKCE code-for-token 兑换 ★** |
| 05 | 登录 | client-telemetry | `/metrics` | `userLoggedIn` |
| **06** | **登出** | **prod.us-east-1.auth.desktop.kiro.dev** | **`/logout`** | **body 仅 `{refreshToken}` ★** |
| 07 | 登出后 | cognito-identity | `GetId`（**第 2 次**） | 登出销毁了 Cognito 缓存，重新拿匿名 ID |
| 08 | 登出后 | cognito-identity | `GetCredentialsForIdentity`（第 2 次） | |
| 09 | 登出后 | client-telemetry | `/metrics` | `subcommand=logout` |
| 10 | 再登录 | cognito-identity | `GetId`（**第 3 次**） | |
| 11 | 再登录 | cognito-identity | `GetCredentialsForIdentity`（第 3 次） | |
| 12 | 再登录 | client-telemetry | `/metrics` | `subcommand=login` |
| **13** | **再登录** | **prod.us-east-1.auth.desktop.kiro.dev** | **`/oauth/token`** | **新 code + 新 verifier，新一对 token ★** |
| 14 | 再登录 | client-telemetry | `/metrics` | `userLoggedIn` |

## 三个核心端点速查

### 1. `/oauth/token` — 登录换 token

| | 值 |
|---|---|
| URL | `POST prod.us-east-1.auth.desktop.kiro.dev/oauth/token` |
| Auth | 无（code 自证） |
| UA | `Kiro-CLI` |
| Req body | `{code, code_verifier, redirect_uri}` — **三字段** |
| Req 例 | `{"code":"e80a8da3-...","code_verifier":"qKNjgq_V...","redirect_uri":"http://localhost:3128/oauth/callback?login_option=github"}` |
| Res body | `{accessToken, expiresIn, profileArn, refreshToken}` — **四字段** |
| Res 例 | `{"accessToken":"aoa...:sig","expiresIn":3600,"profileArn":"arn:aws:codewhisperer:us-east-1:699475941385:profile/EHGA3GRVQMUK","refreshToken":"aor...:sig"}` |
| `expiresIn` | **3600 秒（1 小时）** |
| 后续刷新 | 用 [**另一个 endpoint `/refreshToken`**](../docs/01-POST-prod.us-east-1.auth.desktop.kiro.dev_refreshToken.md)，**不是同一个 `/oauth/token`** |

**vs 标准 OAuth2 (RFC 6749) 的削减**
- ❌ `grant_type` — endpoint 只支持 code exchange，所以省了
- ❌ `client_id` — endpoint 本身就绑死给 Kiro CLI 用
- ❌ `state` — 浏览器侧已校验，CLI 不向 Kiro 服务端发
- ❌ `token_type`（响应） — 默认就是 Bearer
- ❌ `scope`（响应） — Kiro 没 scope 概念
- ❌ `id_token` — Kiro 不发 OIDC id token
- ❌ 任何账户元信息（email / org / uuid） — 必须靠后续 API 调用拿

### 2. `/logout` — 撤销 refresh chain

| | 值 |
|---|---|
| URL | `POST prod.us-east-1.auth.desktop.kiro.dev/logout` |
| Auth | 无 |
| UA | `Kiro-CLI` |
| Req body | `{refreshToken}` — **仅一个字段，注意是 refresh 不是 access** |
| Res body | 空 |

`/logout` 让服务端把整条 refresh 链路标记为 revoked，对应的 accessToken **即便未过期也会立即被拒**。

### 3. `/refreshToken` — 续 access token（**注意是别的 endpoint**）

详见主档案 [`crack/kiro/docs/01`](../docs/01-POST-prod.us-east-1.auth.desktop.kiro.dev_refreshToken.md)。

| | 值 |
|---|---|
| URL | `POST prod.us-east-1.auth.desktop.kiro.dev/refreshToken` ← **末尾无 `/oauth`** |
| Auth | 无 |
| UA | `Kiro-CLI` |
| Req body | `{refreshToken}` |
| Res body | `{accessToken, refreshToken, expiresAt, ...}` — **rolling refresh，每次都换新 refresh token** |

**三个 endpoint 速记**：
- `/oauth/token` —— 一次性 code 换 token
- `/refreshToken` —— 已有 refresh token 续期
- `/logout` —— 撤销 refresh chain

## Cognito Identity Pool 的角色

| | 值 |
|---|---|
| Pool ID | `us-east-1:820fd6d1-95c0-4ca4-bffb-3f01d32da842` |
| 性质 | **公共匿名 pool** —— 所有 Kiro 用户共享 |
| 用途 | **仅**为 `client-telemetry.us-east-1.amazonaws.com/metrics` 提供 SigV4 签名所需的 STS 临时凭据 |
| 与 Kiro 账户的关系 | **完全独立** —— Cognito IdentityId 不和 Kiro OAuth 账户绑定，logout/login 切账户后 IdentityId 也会刷一个新的 |

**每次冷启动 / 登出后**都会调用一次 `GetId + GetCredentialsForIdentity` 拿新 STS（缓存被一并清空）。本会话因为登录→登出→再登录，所以 Cognito 调用出现了 3 轮（每轮 2 条），共 6 条。

## 与 Anthropic Claude Code PKCE 登录的对比

| 维度 | Anthropic Claude Code（[`crack/login/`](../../login/README.md)） | Kiro CLI（本目录） |
|---|---|---|
| 浏览器侧 host | `claude.com/cai/oauth/authorize` | `app.kiro.dev/signin` |
| client_id | `9d1c250a-e61b-44d9-88ed-5944d1962f5e`（CLI 应用 UUID） | 无（端点绑死给 CLI） |
| redirect_uri 端口 | **随机**（CLI 启动时分配空闲端口，如 `46473`） | **写死 `3128`** |
| redirect_uri path | `/callback` | `/oauth/callback?login_option=<idp>` |
| code 格式 | base64url 随机串 | **UUIDv4** |
| code_verifier | base64url 32 字节 | base64url 32 字节 |
| code_challenge_method | `S256` | `S256` |
| 6 个 scope | `org:create_api_key user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload` | **无 scope 概念** |
| token endpoint | `platform.claude.com/v1/oauth/token` | `prod.us-east-1.auth.desktop.kiro.dev/oauth/token` |
| token endpoint UA | `axios/1.13.6` | `Kiro-CLI` |
| 请求 body 字段 | 6 个（`grant_type, code, redirect_uri, client_id, code_verifier, state`） | 3 个（`code, code_verifier, redirect_uri`） |
| 响应 expires_in | **28800（8h）** | **3600（1h）** |
| 响应是否含账户信息 | ✓（`organization.uuid, account.uuid, account.email_address`） | ✗（需后续 API 拉取） |
| Logout endpoint | 无独立端点（Anthropic 没暴露 CLI 端 logout） | `prod.us-east-1.auth.desktop.kiro.dev/logout`（body `{refreshToken}`） |
| Refresh 流程 | 同一个 `/v1/oauth/token`，body `grant_type=refresh_token + refresh_token=...` | **独立 endpoint `/refreshToken`**，body `{refreshToken}` |

## CPA-Claude 对接备注

| 任务 | 实现 |
|---|---|
| 加 Kiro OAuth 凭据 | 用户在浏览器完成 `app.kiro.dev/signin` 后，CPA 后端必须能监听 `localhost:3128`（**注意 squid 默认占该端口**，需先释放）。拿到 code 后 POST `/oauth/token`，存 `{accessToken, refreshToken, expiresAt = now + 3600s, profileArn}`。**rolling refresh 一旦丢 refreshToken 账户死，必须原子写盘**。 |
| 续期 | 过期前 5 min 调 `/refreshToken`（**别用 `/oauth/token`**），body `{refreshToken}`。 |
| 删除凭据 | 主动调 `/logout` 销毁服务端 refresh chain（不撤销会一直占用 pool 配额）。 |
| profileArn | 全局常量 `arn:aws:codewhisperer:us-east-1:699475941385:profile/EHGA3GRVQMUK`，所有用户都是这个串。 |

## 重新生成

```bash
# 1. 把新 dump 放到 crack/kiro/login/raw/kiro-login-session-full.json
# 2. 若起始 rowId 变化，更新 split.py 里的 KIRO_LOGIN_START_ROWID 常量
python3 crack/scripts/split.py kiro-login    # raw → rows
python3 crack/scripts/sanitize.py            # 全量脱敏（幂等）
python3 crack/scripts/gen.py kiro-login      # rows → docs
python3 crack/scripts/sanitize.py            # 再跑一次（gen 可能把 rows 里的明文搬到 docs）
```

## 目录

```
crack/kiro/login/
├── README.md           ← 本文件
├── raw/
│   └── kiro-login-session-full.json   ← Whistle 原始 dump（含其它噪声请求）
├── rows/               ← 14 个 JSON（已 base64 解码 + gunzip）
│   ├── 01..14.json
│   └── _manifest.json
└── docs/               ← 14 个 markdown
    └── 01..14.md
```
