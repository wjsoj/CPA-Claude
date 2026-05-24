# 06. POST https://prod.us-east-1.auth.desktop.kiro.dev/logout

**阶段**：登出 — 撤销 **状态码**：200 **请求大小**：251 B **响应大小**：0 B

**用途**：**Logout（核心 #2）**：`POST prod.us-east-1.auth.desktop.kiro.dev/logout`，body 仅 `{refreshToken}`（**带的是 refresh token，不是 access token；服务端会主动让两条都失效**）。响应 200 + 空 body。

## 请求行

```
POST https://prod.us-east-1.auth.desktop.kiro.dev/logout
```

## 请求头（共 6 个）

| Header | Value |
|---|---|
| host | prod.us-east-1.auth.desktop.kiro.dev |
| content-type | application/json |
| user-agent | Kiro-CLI |
| accept | */* |
| accept-encoding | gzip |
| content-length | 251 |

## 请求体

- **Content-Type**：`application/json`
- **Content-Length**：`251` B（解码后实际 `54` B）
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| refreshToken | string | "aorAAAAAREDACTED_KIRO_REFRESH_TOKEN" |

## 响应头（共 7 个）

| Header | Value |
|---|---|
| content-type | application/json |
| date | Sun, 24 May 2026 08:25:06 GMT |
| x-amzn-requestid | 1df5e051-9796-416b-92fa-2dae4311fb57 |
| x-cache | Miss from cloudfront |
| via | 1.1 a35a15e72ad59a60ddc8752bdb709706.cloudfront.net (CloudFront) |
| x-amz-cf-pop | JFK52-P5 |
| x-amz-cf-id | WsgR9H5-2kjZND-2dD6jD3PMJDfcY3MrC9Fp8h5NYaNdHi-D82Q71Q== |

## 响应体

_无_

## 字段深挖


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

---
_原始 JSON_：[`rows/06-POST-prod.us-east-1.auth.desktop.kiro.dev_logout.json`](../rows/06-POST-prod.us-east-1.auth.desktop.kiro.dev_logout.json)
