# 43. POST https://client-telemetry.us-east-1.amazonaws.com/metrics

**阶段**：业务 — Telemetry **状态码**：200 **请求大小**：4432 B **响应大小**：0 B

**用途**：Toolkit telemetry（本批 4.4 KB，明显比常规批次大 —— 可能附带了 `chatInteractWithMessageEvent` 类的扩展事件）。

## 请求行

```
POST https://client-telemetry.us-east-1.amazonaws.com/metrics
```

## 请求头（共 13 个）

| Header | Value |
|---|---|
| content-type | application/json |
| content-length | 4432 |
| user-agent | aws-sdk-rust/1.3.16 os/linux lang/rust/1.92.0 |
| x-amz-user-agent | aws-sdk-rust/1.3.16 ua/2.1 api/toolkittelemetry/1.0.0 os/linux lang/rust/1.92.0 app/AmazonQ-For-CLI |
| x-amz-date | 20260524T080646Z |
| authorization | AWS4-HMAC-SHA256 Credential=ASIAREDACTEDAWSAKID0/20260524/us-east-1/execute-api/aws4_request, SignedHeaders=content-length;content-type;host;x-amz-date;x-amz-security-token;x-amz-user-agent, Signature… |
| x-amz-security-token | IQoJb3JpZ2luX2Vj_STS_SESSION_TOKEN_REDACTED |
| amz-sdk-request | attempt=1; max=1 |
| amz-sdk-invocation-id | 00000000-0000-0000-0000-000000000050 |
| accept | */* |
| accept-encoding | gzip |
| host | client-telemetry.us-east-1.amazonaws.com |
| connection | close |

## 请求体

- **Content-Type**：`application/json`
- **Content-Length**：`4432` B（解码后实际 `4432` B）
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| AWSProduct | string | "CodeWhisperer for Terminal" |
| AWSProductVersion | string | "2.4.1" |
| ClientID | string | "00000000-0000-0000-0000-000000000041" |
| MetricData | array[1] | [object{5}, ...] |
| OS | string | "linux" |
| OSArchitecture | string | "x86_64" |
| OSVersion | string | "Linux 7.0.9-arch1-1 - Arch Linux" |

## 响应头（共 12 个）

| Header | Value |
|---|---|
| content-type | application/json |
| content-length | 0 |
| connection | close |
| date | Sun, 24 May 2026 08:06:47 GMT |
| x-amzn-trace-id | Root=1-6a12b197-6a28ba6a15f89e093ba592b2 |
| x-amzn-requestid | f3823706-6543-4277-873e-bd116b8cbbd8 |
| access-control-allow-origin | * |
| x-amz-apigw-id | d3CvvHiAIAMEARQ= |
| x-cache | Miss from cloudfront |
| via | 1.1 1a3ec957d73b46987242b5ccbf625dce.cloudfront.net (CloudFront) |
| x-amz-cf-pop | JFK50-P14 |
| x-amz-cf-id | -BpuU2eYfzlVQUaJPo2vdEFDvwTt_kBOYDoIQI5Mk8IK-UoF5FXoPQ== |

## 响应体

_无_

---
_原始 JSON_：[`rows/43-POST-client-telemetry.us-east-1.amazonaws.com_metrics.json`](../rows/43-POST-client-telemetry.us-east-1.amazonaws.com_metrics.json)
