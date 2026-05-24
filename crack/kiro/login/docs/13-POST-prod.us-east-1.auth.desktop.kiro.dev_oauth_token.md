# 13. POST https://prod.us-east-1.auth.desktop.kiro.dev/oauth/token

**阶段**：再登录 — Token Exchange **状态码**：200 **请求大小**：183 B **响应大小**：598 B

**用途**：**OAuth Token Exchange 第二轮**：跟 #04 同 endpoint 同 body 结构；新 code + 新 code_verifier，签发出新的一对 access/refresh token。

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
| code | string | "KIRO_OAUTH_CODE_2_REDACTED" |
| code_verifier | string | "KIRO_CODE_VERIFIER_2_REDACTED" |
| redirect_uri | string | "http://localhost:3128/oauth/callback?login_option=github" |

## 响应头（共 7 个）

| Header | Value |
|---|---|
| content-type | application/json |
| date | Sun, 24 May 2026 08:25:18 GMT |
| x-amzn-requestid | 1a26d263-5d1a-4a6b-ab23-e0db242e0bdb |
| x-cache | Miss from cloudfront |
| via | 1.1 b4aed0fc17149bbf4e91539a66d546a0.cloudfront.net (CloudFront) |
| x-amz-cf-pop | JFK52-P5 |
| x-amz-cf-id | A8RcR5apKsCBFPDbwxyN0MuQ3CMdsXBG6MGERyiN2dBXaMYjhfpklw== |

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

---
_原始 JSON_：[`rows/13-POST-prod.us-east-1.auth.desktop.kiro.dev_oauth_token.json`](../rows/13-POST-prod.us-east-1.auth.desktop.kiro.dev_oauth_token.json)
