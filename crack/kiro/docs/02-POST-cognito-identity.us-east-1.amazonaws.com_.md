# 02. POST https://cognito-identity.us-east-1.amazonaws.com/

**阶段**：启动 — 身份 **状态码**：200 **请求大小**：63 B **响应大小**：1756 B

**用途**：**AWS STS 凭据交换**。`AWSCognitoIdentityService.GetCredentialsForIdentity`（content-type `application/x-amz-json-1.1`）。把已有的 `IdentityId` 换成临时 STS（`AccessKeyId/SecretKey/SessionToken/Expiration`）。这套 STS **只用于打 client-telemetry**（SigV4），不影响 CodeWhisperer 的 Bearer 认证。

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
| user-agent | aws-sdk-rust/1.3.16 os/linux lang/rust/1.92.0 |
| x-amz-user-agent | aws-sdk-rust/1.3.16 ua/2.1 api/cognitoidentity/1.100.0 os/linux lang/rust/1.92.0 m/E md/http#hyper-1.x app/AmazonQ-For-CLI |
| amz-sdk-request | attempt=1; max=3 |
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
| IdentityId | string | "us-east-1:00000000-0000-0000-0000-000000000040" |

## 响应头（共 6 个）

| Header | Value |
|---|---|
| date | Sun, 24 May 2026 08:04:24 GMT |
| content-type | application/x-amz-json-1.1 |
| content-length | 1756 |
| connection | close |
| x-amzn-requestid | edf8854f-6e16-438e-b2cb-2e418096a3ff |
| strict-transport-security | max-age=31536000; includeSubDomains |

## 响应体

- **Content-Type**：`application/x-amz-json-1.1`
- **解码后大小**：`258` B
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| Credentials | object{4} | {AccessKeyId, Expiration, SecretKey...} |
| IdentityId | string | "us-east-1:00000000-0000-0000-0000-000000000040" |

## 字段深挖


**AWS Cognito Identity Pool 标准调用**：`AWSCognitoIdentityService.GetCredentialsForIdentity`

**Request**
- `Content-Type: application/x-amz-json-1.1`（注意 1.1，不是 1.0）
- `X-Amz-Target: AWSCognitoIdentityService.GetCredentialsForIdentity`
- `User-Agent: aws-sdk-rust/1.3.16 os/linux lang/rust/1.92.0`（rust SDK）
- `X-Amz-User-Agent: aws-sdk-rust/1.3.16 ua/2.1 api/cognitoidentity/1.100.0 ... app/AmazonQ-For-CLI`
- `Amz-Sdk-Invocation-Id: <uuid>`、`Amz-Sdk-Request: attempt=1; max=3`
- **无 Authorization**（Cognito Identity 这一步本身是 "unauthenticated/identity-only" 模式）

```json
{ "IdentityId": "us-east-1:<uuid>" }
```

`IdentityId` 是 Cognito 给本设备分配的稳定 ID（首次启动时通过另一个 `GetId` 调用拿到，本抓包没出现，说明本机已有缓存）。

**Response**：
```json
{
  "IdentityId": "us-east-1:<uuid>",
  "Credentials": {
    "AccessKeyId":  "ASIA...",
    "Expiration":   <epoch_float>,
    "SecretKey":    "<40 char>",
    "SessionToken": "IQoJb3JpZ2luX2VjEIH//..."  // 长达 1500 char 的 STS session token
  }
}
```

**只服务 toolkit telemetry**：这套 STS 临时凭据**只**用于给 `client-telemetry.us-east-1.amazonaws.com/metrics` 做 SigV4（见 #04 起的 telemetry 请求头里的 `x-amz-security-token`）。CodeWhisperer 业务接口（`q.us-east-1.amazonaws.com`）走的是 Kiro accessToken Bearer，不用这套 STS。

---
_原始 JSON_：[`rows/02-POST-cognito-identity.us-east-1.amazonaws.com_.json`](../rows/02-POST-cognito-identity.us-east-1.amazonaws.com_.json)
