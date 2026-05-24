# 38. POST https://q.us-east-1.amazonaws.com/

**阶段**：业务 — Turn 10 Telemetry **状态码**：aborted **请求大小**：1719 B **响应大小**：None B

**用途**：Turn 10 SendTelemetryEvent，**aborted** —— 上一次响应未完整结束/被 Ctrl-C 打断，CLI 直接丢弃这次发包。

## 请求行

```
POST https://q.us-east-1.amazonaws.com/
```

## 请求头（共 13 个）

| Header | Value |
|---|---|
| content-type | application/x-amz-json-1.0 |
| x-amz-target | AmazonCodeWhispererService.SendTelemetryEvent |
| content-length | 1719 |
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

_无_

## 响应头（共 0 个）

_无_

## 响应体

_无_

---
_原始 JSON_：[`rows/38-POST-q.us-east-1.amazonaws.com_.json`](../rows/38-POST-q.us-east-1.amazonaws.com_.json)
