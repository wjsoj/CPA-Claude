# 42. POST https://q.us-east-1.amazonaws.com/

**阶段**：业务 — Telemetry **状态码**：200 **请求大小**：1808 B **响应大小**：2 B

**用途**：SendTelemetryEvent（推断为补传上一轮的事件）。

## 请求行

```
POST https://q.us-east-1.amazonaws.com/
```

## 请求头（共 13 个）

| Header | Value |
|---|---|
| content-type | application/x-amz-json-1.0 |
| x-amz-target | AmazonCodeWhispererService.SendTelemetryEvent |
| content-length | 1808 |
| user-agent | aws-sdk-rust/1.3.16 ua/2.1 api/codewhispererruntime/0.1.16551 os/linux lang/rust/1.92.0 md/appVersion-2.4.1 app/AmazonQ-For-CLI |
| x-amz-user-agent | aws-sdk-rust/1.3.16 ua/2.1 api/codewhispererruntime/0.1.16551 os/linux lang/rust/1.92.0 m/F app/AmazonQ-For-CLI |
| x-amzn-codewhisperer-optout | false |
| authorization | Bearer aoaAAAAAREDACTED_KIRO_ACCESS_TOKEN |
| amz-sdk-request | attempt=1; max=3 |
| amz-sdk-invocation-id | 00000000-0000-0000-0000-000000000050 |
| accept | */* |
| accept-encoding | gzip |
| host | q.us-east-1.amazonaws.com |
| connection | close |

## 请求体

- **Content-Type**：`application/x-amz-json-1.0`
- **Content-Length**：`1808` B（解码后实际 `1808` B）
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| clientToken | string | "458d05ba-afce-44aa-f59c-8bed3cba7865" |
| telemetryEvent | object{1} | {chatAddMessageEvent} |
| optOutPreference | string | "OPTIN" |
| userContext | object{5} | {ideCategory, operatingSystem, product...} |
| profileArn | string | "arn:aws:codewhisperer:us-east-1:699475941385:profile/EHGA3G... |
| modelId | string | "auto" |

## 响应头（共 10 个）

| Header | Value |
|---|---|
| date | Sun, 24 May 2026 08:06:46 GMT |
| content-type | application/x-amz-json-1.0 |
| content-length | 2 |
| connection | close |
| x-amzn-requestid | b4708a2f-89df-4eb3-8933-bc8753397057 |
| x-xss-protection | 1; mode=block |
| strict-transport-security | max-age=47304000; includeSubDomains |
| x-frame-options | DENY |
| cache-control | no-cache |
| x-content-type-options | nosniff |

## 响应体

- **Content-Type**：`application/x-amz-json-1.0`
- **解码后大小**：`2` B
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|

---
_原始 JSON_：[`rows/42-POST-q.us-east-1.amazonaws.com_.json`](../rows/42-POST-q.us-east-1.amazonaws.com_.json)
