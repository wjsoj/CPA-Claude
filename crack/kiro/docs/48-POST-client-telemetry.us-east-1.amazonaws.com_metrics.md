# 48. POST https://client-telemetry.us-east-1.amazonaws.com/metrics

**阶段**：退出 **状态码**：aborted **请求大小**：1632 B **响应大小**：None B

**用途**：末尾 Toolkit telemetry，**aborted** —— Kiro CLI 进程退出时还有未发完的批次。

## 请求行

```
POST https://client-telemetry.us-east-1.amazonaws.com/metrics
```

## 请求头（共 13 个）

| Header | Value |
|---|---|
| content-type | application/json |
| content-length | 1632 |
| user-agent | aws-sdk-rust/1.3.16 os/linux lang/rust/1.92.0 |
| x-amz-user-agent | aws-sdk-rust/1.3.16 ua/2.1 api/toolkittelemetry/1.0.0 os/linux lang/rust/1.92.0 app/AmazonQ-For-CLI |
| x-amz-date | 20260524T080703Z |
| authorization | AWS4-HMAC-SHA256 Credential=ASIAREDACTEDAWSAKID0/20260524/us-east-1/execute-api/aws4_request, SignedHeaders=content-length;content-type;host;x-amz-date;x-amz-security-token;x-amz-user-agent, Signature… |
| x-amz-security-token | IQoJb3JpZ2luX2Vj_STS_SESSION_TOKEN_REDACTED |
| amz-sdk-request | attempt=1; max=1 |
| amz-sdk-invocation-id | 00000000-0000-0000-0000-000000000050 |
| accept | */* |
| accept-encoding | gzip |
| host | client-telemetry.us-east-1.amazonaws.com |
| connection | close |

## 请求体

_无_

## 响应头（共 0 个）

_无_

## 响应体

_无_

---
_原始 JSON_：[`rows/48-POST-client-telemetry.us-east-1.amazonaws.com_metrics.json`](../rows/48-POST-client-telemetry.us-east-1.amazonaws.com_metrics.json)
