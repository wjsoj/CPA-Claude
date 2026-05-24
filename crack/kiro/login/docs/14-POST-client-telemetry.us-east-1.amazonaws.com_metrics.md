# 14. POST https://client-telemetry.us-east-1.amazonaws.com/metrics

**阶段**：再登录 — Telemetry **状态码**：200 **请求大小**：445 B **响应大小**：0 B

**用途**：Toolkit telemetry：`userLoggedIn` 第二次。

## 请求行

```
POST https://client-telemetry.us-east-1.amazonaws.com/metrics
```

## 请求头（共 13 个）

| Header | Value |
|---|---|
| content-type | application/json |
| content-length | 445 |
| user-agent | aws-sdk-rust/1.3.10 os/linux lang/rust/1.92.0 |
| x-amz-user-agent | aws-sdk-rust/1.3.10 ua/2.1 api/toolkittelemetry/1.0.0 os/linux lang/rust/1.92.0 app/AmazonQ-For-CLI |
| x-amz-date | 20260524T082519Z |
| authorization | AWS4-HMAC-SHA256 Credential=ASIAREDACTEDAWSAKID3/20260524/us-east-1/execute-api/aws4_request, SignedHeaders=content-length;content-type;host;x-amz-date;x-amz-security-token;x-amz-user-agent, Signature… |
| x-amz-security-token | IQoJb3JpZ2luX2Vj_STS_SESSION_TOKEN_REDACTED |
| amz-sdk-request | attempt=1; max=1 |
| amz-sdk-invocation-id | 00000000-0000-0000-0000-000000000050 |
| accept | */* |
| accept-encoding | gzip |
| host | client-telemetry.us-east-1.amazonaws.com |
| connection | close |

## 请求体

- **Content-Type**：`application/json`
- **Content-Length**：`445` B（解码后实际 `445` B）
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
| date | Sun, 24 May 2026 08:25:19 GMT |
| x-amzn-trace-id | Root=1-6a12b5ef-4e0ed4a82ff4a84103a3eef0 |
| x-amzn-requestid | a4c3ae54-6e8b-4df5-a767-d33a6699559f |
| access-control-allow-origin | * |
| x-amz-apigw-id | d3FdgE2RoAMELZw= |
| x-cache | Miss from cloudfront |
| via | 1.1 8516d06bcbccdb0dfdec60c326106f14.cloudfront.net (CloudFront) |
| x-amz-cf-pop | JFK50-P14 |
| x-amz-cf-id | evKvy3kBfUmx6YkQEAwSxFr_9MQ86WYw3H5b_Uzef-jHZhoBCegb8g== |

## 响应体

_无_

---
_原始 JSON_：[`rows/14-POST-client-telemetry.us-east-1.amazonaws.com_metrics.json`](../rows/14-POST-client-telemetry.us-east-1.amazonaws.com_metrics.json)
