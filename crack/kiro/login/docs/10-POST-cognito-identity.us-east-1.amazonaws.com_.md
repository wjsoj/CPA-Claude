# 10. POST https://cognito-identity.us-east-1.amazonaws.com/

**阶段**：再登录 — Cognito 识别 **状态码**：200 **请求大小**：67 B **响应大小**：63 B

**用途**：Cognito GetId（**第三次**）—— 再次执行 `kiro login` 触发的又一轮匿名 identity 申请。

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
| date | Sun, 24 May 2026 08:25:12 GMT |
| content-type | application/x-amz-json-1.1 |
| content-length | 63 |
| connection | close |
| x-amzn-requestid | 8d793b39-8d80-40e3-81e1-2a45303d6278 |
| strict-transport-security | max-age=31536000; includeSubDomains |

## 响应体

- **Content-Type**：`application/x-amz-json-1.1`
- **解码后大小**：`63` B
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| IdentityId | string | "us-east-1:00000000-0000-0000-0000-000000000053" |

---
_原始 JSON_：[`rows/10-POST-cognito-identity.us-east-1.amazonaws.com_.json`](../rows/10-POST-cognito-identity.us-east-1.amazonaws.com_.json)
