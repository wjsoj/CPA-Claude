# 04. POST https://client-telemetry.us-east-1.amazonaws.com/metrics

**阶段**：启动 — Telemetry **状态码**：200 **请求大小**：363 B **响应大小**：0 B

**用途**：**Toolkit Telemetry 第 1 弹**：`amazonqcli_dailyHeartbeat`（每天上报一次的存活心跳）。POST `client-telemetry.us-east-1.amazonaws.com/metrics`，body 是 JSON `{AWSProduct, AWSProductVersion, ClientID, MetricData[], OS, OSArchitecture, OSVersion, ParentProduct, ParentProductVersion}`。**SigV4 签名**用 #02 拿到的 STS 临时凭据（不是 Bearer）。响应 200 + 空 body。

## 请求行

```
POST https://client-telemetry.us-east-1.amazonaws.com/metrics
```

## 请求头（共 13 个）

| Header | Value |
|---|---|
| content-type | application/json |
| content-length | 363 |
| user-agent | aws-sdk-rust/1.3.16 os/linux lang/rust/1.92.0 |
| x-amz-user-agent | aws-sdk-rust/1.3.16 ua/2.1 api/toolkittelemetry/1.0.0 os/linux lang/rust/1.92.0 app/AmazonQ-For-CLI |
| x-amz-date | 20260524T080424Z |
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
- **Content-Length**：`363` B（解码后实际 `363` B）
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
| date | Sun, 24 May 2026 08:04:25 GMT |
| x-amzn-trace-id | Root=1-6a12b109-17faf9a45563afca4764ad67 |
| x-amzn-requestid | 3eef2cbb-b451-4936-8597-cda5d4d13dd5 |
| access-control-allow-origin | * |
| x-amz-apigw-id | d3CZlG9YIAMEZQw= |
| x-cache | Miss from cloudfront |
| via | 1.1 544ef62f4f978150b1046f6301e68852.cloudfront.net (CloudFront) |
| x-amz-cf-pop | JFK50-P14 |
| x-amz-cf-id | OaH9-eLJulJFJK_HbUbqpvApVVdd9diiN5vBBhNv1y0o_eRmzHqJYA== |

## 响应体

_无_

## 字段深挖


**Toolkit Telemetry endpoint — SigV4 路径**

**鉴权**：与 CodeWhisperer 接口完全独立的认证机制——用 #02 拿到的 STS 临时凭据做 SigV4 签名。请求头里能看到：
- `X-Amz-Date: 20260524T080424Z`
- `X-Amz-Security-Token: IQoJb3JpZ2luX2Vj...`（同 #02 响应里的 SessionToken）
- `Authorization: AWS4-HMAC-SHA256 Credential=ASIA.../20260524/us-east-1/execute-api/aws4_request, SignedHeaders=content-length;content-type;host;x-amz-date;x-amz-security-token;x-amz-user-agent, Signature=<hex>`

注意 service 是 **`execute-api`**（API Gateway），region `us-east-1`。

**Request body**（JSON，**非 Smithy 协议**）：

```json
{
  "AWSProduct":         "CodeWhisperer for Terminal",
  "AWSProductVersion":  "2.4.1",
  "ClientID":           "<uuid>",          // 设备级稳定 ID（同 Cognito IdentityId 派生）
  "MetricData": [
    {
      "MetricName":     "amazonqcli_dailyHeartbeat",
      "EpochTimestamp": 1779609863355,
      "Unit":           "None",
      "Value":          1.0,
      "Metadata": [
        { "Key": "source", "Value": "" },
        ...
      ]
    }
  ],
  "OS":                 "linux",
  "OSArchitecture":     "x86_64",
  "OSVersion":          "<kernel>",
  "ParentProduct":      "CodeWhisperer for Terminal",
  "ParentProductVersion": "2.4.1"
}
```

**Response**：HTTP 200，**空 body**。

**事件清单（本会话观察到的 MetricName）**
- `amazonqcli_dailyHeartbeat`
- `codewhispererterminal_cliSubcommandExecuted`
- `codewhispererterminal_addedMessage`
- `amazonq_promptToCompletion`
- `codewhispererterminal_userLoggedIn` / `_userLoggedOut`（启动/退出时）
- `codewhispererterminal_dialogDismissed`、`codewhispererterminal_dialogShown`

**`Metadata[]` 常见 Key**：
`credentialStartUrl` / `credentialSourceId` / `result` / `reason` /
`source` / `cwsprChatConversationType` / `cwsprChatHasCodeSnippet` /
`cwsprChatTriggerInteraction` / `cwsprChatProgrammingLanguage` /
`amazonqConversationId` / `amazonqMessageId` / `amazonqRequestId` /
`duration` / `durationMicroseconds` / `inputTokenCount` / `outputTokenCount` /
`cacheReadTokenCount` / `cacheWriteTokenCount`

---
_原始 JSON_：[`rows/04-POST-client-telemetry.us-east-1.amazonaws.com_metrics.json`](../rows/04-POST-client-telemetry.us-east-1.amazonaws.com_metrics.json)
