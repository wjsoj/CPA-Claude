# 07. POST https://cognito-identity.us-east-1.amazonaws.com/

**阶段**：登出 — Cognito 识别 **状态码**：200 **请求大小**：67 B **响应大小**：63 B

**用途**：Cognito GetId（**第二次**） —— `kiro logout` 完成后，Cognito 缓存被一并清空，需要重新拿匿名 identity 继续上报 telemetry。

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
| date | Sun, 24 May 2026 08:25:06 GMT |
| content-type | application/x-amz-json-1.1 |
| content-length | 63 |
| connection | close |
| x-amzn-requestid | 90dd9902-7355-4f8b-831f-69905320a41b |
| strict-transport-security | max-age=31536000; includeSubDomains |

## 响应体

- **Content-Type**：`application/x-amz-json-1.1`
- **解码后大小**：`63` B
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| IdentityId | string | "us-east-1:00000000-0000-0000-0000-000000000052" |

## 字段深挖


**为什么 logout 后又两次 GetId / GetCredentialsForIdentity？**

Kiro CLI 把 Cognito 临时凭据**关联到 OAuth 会话**：登出意味着销毁全部凭据状态，包括 Cognito 缓存的 IdentityId。再启动后必须重新走 GetId → GetCredentialsForIdentity 拿一份新的匿名 STS，否则 toolkit telemetry 无 SigV4 可签。

匿名 pool 的 IdentityId **不能复用** —— 它在服务端只是"无关联的临时 handle"，每次 GetId 都是新的（甚至没有去重）。所以 #07/#10 拿到的两个 IdentityId 互不相同。

---
_原始 JSON_：[`rows/07-POST-cognito-identity.us-east-1.amazonaws.com_.json`](../rows/07-POST-cognito-identity.us-east-1.amazonaws.com_.json)
