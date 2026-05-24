# 09. POST https://client-telemetry.us-east-1.amazonaws.com/metrics

**阶段**：登出 — Telemetry **状态码**：200 **请求大小**：797 B **响应大小**：0 B

**用途**：Toolkit telemetry：`subcommand=logout` —— 标记用户跑了 `kiro logout`。注意是在拿到新的 STS 后才发，所以**第 9 条而不是第 7 条**才是登出动作的真正『用户视角时间点』。

## 请求行

```
POST https://client-telemetry.us-east-1.amazonaws.com/metrics
```

## 请求头（共 13 个）

| Header | Value |
|---|---|
| content-type | application/json |
| content-length | 797 |
| user-agent | aws-sdk-rust/1.3.10 os/linux lang/rust/1.92.0 |
| x-amz-user-agent | aws-sdk-rust/1.3.10 ua/2.1 api/toolkittelemetry/1.0.0 os/linux lang/rust/1.92.0 app/AmazonQ-For-CLI |
| x-amz-date | 20260524T082508Z |
| authorization | AWS4-HMAC-SHA256 Credential=ASIAREDACTEDAWSAKID2/20260524/us-east-1/execute-api/aws4_request, SignedHeaders=content-length;content-type;host;x-amz-date;x-amz-security-token;x-amz-user-agent, Signature… |
| x-amz-security-token | IQoJb3JpZ2luX2Vj_STS_SESSION_TOKEN_REDACTED |
| amz-sdk-request | attempt=1; max=1 |
| amz-sdk-invocation-id | 00000000-0000-0000-0000-000000000050 |
| accept | */* |
| accept-encoding | gzip |
| host | client-telemetry.us-east-1.amazonaws.com |
| connection | close |

## 请求体

- **Content-Type**：`application/json`
- **Content-Length**：`797` B（解码后实际 `795` B）
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
| date | Sun, 24 May 2026 08:25:09 GMT |
| x-amzn-trace-id | Root=1-6a12b5e4-6e6b5a40303e3f40616bfea3 |
| x-amzn-requestid | c9b7e42a-8ead-417e-b2d5-5c846c6e2cf1 |
| access-control-allow-origin | * |
| x-amz-apigw-id | d3Fb1GTJIAMEepQ= |
| x-cache | Miss from cloudfront |
| via | 1.1 05b25779abbb50bacd7e013809191bca.cloudfront.net (CloudFront) |
| x-amz-cf-pop | JFK50-P14 |
| x-amz-cf-id | jlAlIA0CMJ3rF5VWUMFDiRgqigcQVKmFr_jFohzNrHTLMplW3ZtA_A== |

## 响应体

_无_

---
_原始 JSON_：[`rows/09-POST-client-telemetry.us-east-1.amazonaws.com_metrics.json`](../rows/09-POST-client-telemetry.us-east-1.amazonaws.com_metrics.json)
