# 04. POST https://prod.us-east-1.auth.desktop.kiro.dev/oauth/token

**阶段**：登录 — Token Exchange **状态码**：200 **请求大小**：183 B **响应大小**：596 B

**用途**：**OAuth Token Exchange（核心 #1）**：`POST prod.us-east-1.auth.desktop.kiro.dev/oauth/token`，body `{code, code_verifier, redirect_uri}` —— **没有 client_id、没有 grant_type、没有 state**。响应 `{accessToken, expiresIn:3600, profileArn, refreshToken}` —— **极简，没有 token_type / scope / id_token / email / account 信息**。详见字段深挖。

## 请求行

```
POST https://prod.us-east-1.auth.desktop.kiro.dev/oauth/token
```

## 请求头（共 6 个）

| Header | Value |
|---|---|
| host | prod.us-east-1.auth.desktop.kiro.dev |
| content-type | application/json |
| user-agent | Kiro-CLI |
| accept | */* |
| accept-encoding | gzip |
| content-length | 183 |

## 请求体

- **Content-Type**：`application/json`
- **Content-Length**：`183` B（解码后实际 `159` B）
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| code | string | "KIRO_OAUTH_CODE_1_REDACTED" |
| code_verifier | string | "KIRO_CODE_VERIFIER_1_REDACTED" |
| redirect_uri | string | "http://localhost:3128/oauth/callback?login_option=github" |

## 响应头（共 7 个）

| Header | Value |
|---|---|
| content-type | application/json |
| date | Sun, 24 May 2026 08:24:50 GMT |
| x-amzn-requestid | 9c7acac7-e50f-4914-911c-7847c41765e1 |
| x-cache | Miss from cloudfront |
| via | 1.1 b4aed0fc17149bbf4e91539a66d546a0.cloudfront.net (CloudFront) |
| x-amz-cf-pop | JFK52-P5 |
| x-amz-cf-id | M3o63Og5LEHpl7E1o_Rwfvl6lUX1iRmziIV06eVNcHj7vjEnykRUVw== |

## 响应体

- **Content-Type**：`application/json`
- **解码后大小**：`203` B
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| accessToken | string | "aoaAAAAAREDACTED_KIRO_ACCESS_TOKEN" |
| expiresIn | int | 3600 |
| profileArn | string | "arn:aws:codewhisperer:us-east-1:699475941385:profile/EHGA3G... |
| refreshToken | string | "aorAAAAAREDACTED_KIRO_REFRESH_TOKEN" |

## 字段深挖


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

---
_原始 JSON_：[`rows/04-POST-prod.us-east-1.auth.desktop.kiro.dev_oauth_token.json`](../rows/04-POST-prod.us-east-1.auth.desktop.kiro.dev_oauth_token.json)
