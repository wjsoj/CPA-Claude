# 04. POST https://platform.claude.com/v1/oauth/token

**阶段**：登录 — Token Exchange **状态码**：200 **请求大小**：309 B **响应大小**：488 B

**用途**：**OAuth Token Exchange（核心）**。CLI 浏览器跳转完成后，把 `code + code_verifier + state` 提交到 `platform.claude.com/v1/oauth/token` 换取 access/refresh token。**这是新版本登录的关键端点 —— 注意 host 是 `platform.claude.com` 而非旧版的 `console.anthropic.com`**。请求由 axios 发出（不是 Bun fetch），不带任何 Bearer。

## 请求行

```
POST https://platform.claude.com/v1/oauth/token
```

## 请求头（共 7 个）

| Header | Value |
|---|---|
| accept | application/json, text/plain, */* |
| accept-encoding | gzip, br |
| content-type | application/json |
| user-agent | axios/1.13.6 |
| host | platform.claude.com |
| content-length | 309 |
| connection | close |

## 请求体

- **Content-Type**：`application/json`
- **Content-Length**：`309` B（解码后实际 `236` B）
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| grant_type | string | "authorization_code" |
| code | string | "OAUTH_CODE_REDACTED" |
| redirect_uri | string | "http://localhost:46473/callback" |
| client_id | string | "9d1c250a-e61b-44d9-88ed-5944d1962f5e" |
| code_verifier | string | "CODE_VERIFIER_REDACTED" |
| state | string | "OAUTH_STATE_REDACTED" |

## 响应头（共 19 个）

| Header | Value |
|---|---|
| date | Mon, 04 May 2026 02:09:02 GMT |
| content-type | application/json |
| transfer-encoding | chunked |
| connection | close |
| cache-control | no-store |
| pragma | no-cache |
| request-id | req_REDACTED |
| strict-transport-security | max-age=31536000; includeSubDomains; preload |
| server | cloudflare |
| x-envoy-upstream-service-time | 126 |
| content-encoding | gzip |
| vary | Accept-Encoding |
| server-timing | x-originResponse;dur=126 |
| via | 1.1 google |
| alt-svc | h3=":443"; ma=86400 |
| cf-cache-status | DYNAMIC |
| set-cookie | __cf_bm=REDACTED; HttpOnly; Secure; Path=/; Domain=claude.com; Expires=Mon, 04 May 2026 02:39:02 GMT |
| x-content-type-options | nosniff |
| cf-ray | REDACTED-cf-ray |

## 响应体

- **Content-Type**：`application/json`
- **解码后大小**：`497` B
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| token_type | string | "Bearer" |
| access_token | string | "sk-ant-oat01-REDACTED" |
| expires_in | int | 28800 |
| refresh_token | string | "sk-ant-ort01-REDACTED" |
| scope | string | "user:file_upload user:inference user:mcp_servers user:profi... |
| token_uuid | string | "00000000-0000-0000-0000-000000000003" |
| organization | object{2} | {uuid, name} |
| account | object{2} | {uuid, email_address} |

## 字段深挖


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
- `set-cookie: __cf_bm=REDACTED; Domain=claude.com`（Cloudflare bot management cookie，跨 *.claude.com 共享）
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

---
_原始 JSON_：[`rows/04-POST-platform.claude.com_v1_oauth_token.json`](../rows/04-POST-platform.claude.com_v1_oauth_token.json)
