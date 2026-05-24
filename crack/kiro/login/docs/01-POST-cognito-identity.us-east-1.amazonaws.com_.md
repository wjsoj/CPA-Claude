# 01. POST https://cognito-identity.us-east-1.amazonaws.com/

**阶段**：登录前 — Cognito 识别 **状态码**：200 **请求大小**：67 B **响应大小**：63 B

**用途**：**Cognito `GetId`**：用 IdentityPoolId `us-east-1:820fd6d1-95c0-4ca4-bffb-3f01d32da842` 拿一个匿名 IdentityId。**这是无身份联邦（unauthenticated）模式** —— 每次冷启动 / 切账户都会发起一次，每次返回的 IdentityId 都不同（本会话 3 次 GetId 拿到 3 个 ID），仅用于给 toolkit telemetry 签 SigV4。

## 请求行

```
POST https://cognito-identity.us-east-1.amazonaws.com/
```

## 请求头（共 9 个）

| Header | Value |
|---|---|
| content-type | application/x-amz-json-1.1 |
| x-amz-target | AWSCognitoIdentityService.GetId |
| content-length | 67 |
| user-agent | aws-sdk-rust/1.3.10 os/linux lang/rust/1.92.0 |
| x-amz-user-agent | aws-sdk-rust/1.3.10 ua/2.1 api/cognitoidentity/1.91.0 os/linux lang/rust/1.92.0 m/E md/http#hyper-1.x app/AmazonQ-For-CLI |
| amz-sdk-request | attempt=1; max=1 |
| amz-sdk-invocation-id | 00000000-0000-0000-0000-000000000050 |
| host | cognito-identity.us-east-1.amazonaws.com |
| connection | close |

## 请求体

- **Content-Type**：`application/x-amz-json-1.1`
- **Content-Length**：`67` B（解码后实际 `67` B）
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| IdentityPoolId | string | "us-east-1:820fd6d1-95c0-4ca4-bffb-3f01d32da842" |

## 响应头（共 6 个）

| Header | Value |
|---|---|
| date | Sun, 24 May 2026 08:24:35 GMT |
| content-type | application/x-amz-json-1.1 |
| content-length | 63 |
| connection | close |
| x-amzn-requestid | 3f49dbd8-67fa-4a60-b141-f40d071af69b |
| strict-transport-security | max-age=31536000; includeSubDomains |

## 响应体

- **Content-Type**：`application/x-amz-json-1.1`
- **解码后大小**：`63` B
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| IdentityId | string | "us-east-1:00000000-0000-0000-0000-000000000051" |

## 字段深挖


**Cognito Identity Pool 是公共共享的**

`IdentityPoolId = us-east-1:820fd6d1-95c0-4ca4-bffb-3f01d32da842` —— Amazon 自家给 Q for CLI 用户共享的匿名 identity pool（**所有 Kiro 用户都用这一个 pool ID**）。配合 #02 的 `GetCredentialsForIdentity`，等价于：
> Kiro CLI 在 Amazon 控股的 AWS 账号下，以"匿名访客"身份获得了一小段 STS 凭据，唯一权限是签名调用 `client-telemetry.us-east-1.amazonaws.com/metrics`。

**Request**
- `Content-Type: application/x-amz-json-1.1`
- `X-Amz-Target: AWSCognitoIdentityService.GetId`
- `User-Agent: aws-sdk-rust/1.3.10 os/linux lang/rust/1.92.0`（注意是 **1.3.10**，而 chat 路径用的是 1.3.16 —— 不同子模块独立指定 SDK 版本）
- **无 Authorization**（IdentityPool 设为 "unauthenticated"）

```json
{ "IdentityPoolId": "us-east-1:820fd6d1-95c0-4ca4-bffb-3f01d32da842" }
```

**Response**
```json
{ "IdentityId": "us-east-1:<uuid>" }
```

**注意**：本会话 3 次 GetId 返回了 3 个不同的 IdentityId（622b0cc5-14d1-... / 622b0cc5-1493-... / 622b0cc5-1409-...）。前 8 char 前缀重复说明 pool 内部按 prefix 分桶，但每次发出实际是新 ID —— 这是**匿名 pool 默认行为**（无 login 关联，每次都是新会话）。

**和首次 chat 会话的差异**：[`crack/kiro/docs/02`](../../docs/02-POST-cognito-identity.us-east-1.amazonaws.com_.md) 没有 `GetId` 调用，是因为 Kiro 把 IdentityId **持久化缓存到磁盘**（`~/.config/amazon-q/cache/cognito.json` 之类的位置），冷启动有缓存就直接走 `GetCredentialsForIdentity`；本次抓包是登录链路，CLI 主动清缓存（或缓存过期）后才会触发 `GetId`。

---
_原始 JSON_：[`rows/01-POST-cognito-identity.us-east-1.amazonaws.com_.json`](../rows/01-POST-cognito-identity.us-east-1.amazonaws.com_.json)
