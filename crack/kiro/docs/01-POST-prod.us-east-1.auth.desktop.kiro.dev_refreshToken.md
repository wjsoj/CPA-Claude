# 01. POST https://prod.us-east-1.auth.desktop.kiro.dev/refreshToken

**阶段**：启动 — 身份 **状态码**：200 **请求大小**：249 B **响应大小**：596 B

**用途**：**Kiro 身份刷新（核心入口）**。`POST prod.us-east-1.auth.desktop.kiro.dev/refreshToken`，body `{refreshToken}` → 响应 `{accessToken, refreshToken, expiresAt, ...}`。这是 Kiro 自己实现的 token endpoint（不是 AWS Cognito 也不是 OAuth），UA 写死 `Kiro-CLI`。Kiro 的 `accessToken` 之后会作为 **Bearer** 直接打 CodeWhisperer 接口。

## 请求行

```
POST https://prod.us-east-1.auth.desktop.kiro.dev/refreshToken
```

## 请求头（共 6 个）

| Header | Value |
|---|---|
| host | prod.us-east-1.auth.desktop.kiro.dev |
| content-type | application/json |
| user-agent | Kiro-CLI |
| accept | */* |
| accept-encoding | gzip |
| content-length | 249 |

## 请求体

- **Content-Type**：`application/json`
- **Content-Length**：`249` B（解码后实际 `54` B）
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| refreshToken | string | "aorAAAAAREDACTED_KIRO_REFRESH_TOKEN" |

## 响应头（共 7 个）

| Header | Value |
|---|---|
| content-type | application/json |
| date | Sun, 24 May 2026 08:04:22 GMT |
| x-amzn-requestid | 65821852-92c5-488f-b96a-30676f0d2aa3 |
| x-cache | Miss from cloudfront |
| via | 1.1 9feee68c149ffc812d2a7f5683100dd2.cloudfront.net (CloudFront) |
| x-amz-cf-pop | JFK52-P5 |
| x-amz-cf-id | tNm1ek21FFMJAOjyKjYsTFPz8mQVU5GdK7Y03uYZbMq4c4is_hZOJg== |

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

---
_原始 JSON_：[`rows/01-POST-prod.us-east-1.auth.desktop.kiro.dev_refreshToken.json`](../rows/01-POST-prod.us-east-1.auth.desktop.kiro.dev_refreshToken.json)
