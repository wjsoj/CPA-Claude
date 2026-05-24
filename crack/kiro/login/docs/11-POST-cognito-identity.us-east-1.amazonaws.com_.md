# 11. POST https://cognito-identity.us-east-1.amazonaws.com/

**阶段**：再登录 — Cognito 换 STS **状态码**：200 **请求大小**：63 B **响应大小**：1756 B

**用途**：Cognito GetCredentialsForIdentity（**第三次**）。

## 请求行

```
POST https://cognito-identity.us-east-1.amazonaws.com/
```

## 请求头（共 9 个）

| Header | Value |
|---|---|
| content-type | application/x-amz-json-1.1 |
| x-amz-target | AWSCognitoIdentityService.GetCredentialsForIdentity |
| content-length | 63 |
| user-agent | aws-sdk-rust/1.3.10 os/linux lang/rust/1.92.0 |
| x-amz-user-agent | aws-sdk-rust/1.3.10 ua/2.1 api/cognitoidentity/1.91.0 os/linux lang/rust/1.92.0 m/E md/http#hyper-1.x app/AmazonQ-For-CLI |
| amz-sdk-request | attempt=1; max=1 |
| amz-sdk-invocation-id | 00000000-0000-0000-0000-000000000050 |
| host | cognito-identity.us-east-1.amazonaws.com |
| connection | close |

## 请求体

- **Content-Type**：`application/x-amz-json-1.1`
- **Content-Length**：`63` B（解码后实际 `63` B）
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| IdentityId | string | "us-east-1:00000000-0000-0000-0000-000000000053" |

## 响应头（共 6 个）

| Header | Value |
|---|---|
| date | Sun, 24 May 2026 08:25:14 GMT |
| content-type | application/x-amz-json-1.1 |
| content-length | 1756 |
| connection | close |
| x-amzn-requestid | 4e3eea88-fe3d-492e-a5be-c866a15830d0 |
| strict-transport-security | max-age=31536000; includeSubDomains |

## 响应体

- **Content-Type**：`application/x-amz-json-1.1`
- **解码后大小**：`258` B
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| Credentials | object{4} | {AccessKeyId, Expiration, SecretKey...} |
| IdentityId | string | "us-east-1:00000000-0000-0000-0000-000000000053" |

---
_原始 JSON_：[`rows/11-POST-cognito-identity.us-east-1.amazonaws.com_.json`](../rows/11-POST-cognito-identity.us-east-1.amazonaws.com_.json)
