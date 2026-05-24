# 21. POST https://client-telemetry.us-east-1.amazonaws.com/metrics

**阶段**：业务 — Telemetry **状态码**：200 **请求大小**：1714 B **响应大小**：0 B

**用途**：Toolkit telemetry。

## 请求行

```
POST https://client-telemetry.us-east-1.amazonaws.com/metrics
```

## 请求头（共 13 个）

| Header | Value |
|---|---|
| content-type | application/json |
| content-length | 1714 |
| user-agent | aws-sdk-rust/1.3.16 os/linux lang/rust/1.92.0 |
| x-amz-user-agent | aws-sdk-rust/1.3.16 ua/2.1 api/toolkittelemetry/1.0.0 os/linux lang/rust/1.92.0 app/AmazonQ-For-CLI |
| x-amz-date | 20260524T080535Z |
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
- **Content-Length**：`1714` B（解码后实际 `1714` B）
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
| date | Sun, 24 May 2026 08:05:36 GMT |
| x-amzn-trace-id | Root=1-6a12b150-131bcec90124c61f297d4080 |
| x-amzn-requestid | cb7260ae-8bdb-4381-a59e-b6f2eca4bbea |
| access-control-allow-origin | * |
| x-amz-apigw-id | d3CkrHSjoAMEgDg= |
| x-cache | Miss from cloudfront |
| via | 1.1 d8ee07f5c6ac1b8b69325e218d2fdcda.cloudfront.net (CloudFront) |
| x-amz-cf-pop | JFK50-P14 |
| x-amz-cf-id | cgRIULV8dPvTrnFdCJyb9M-QMibfKegnGSVcd0SrY6X5LDs2cn9Jew== |

## 响应体

_无_

---
_原始 JSON_：[`rows/21-POST-client-telemetry.us-east-1.amazonaws.com_metrics.json`](../rows/21-POST-client-telemetry.us-east-1.amazonaws.com_metrics.json)
