# 07. POST https://q.us-east-1.amazonaws.com/

**阶段**：业务 — Turn 1 Telemetry **状态码**：200 **请求大小**：1089 B **响应大小**：2 B

**用途**：**Turn 1 完成后上报**：`AmazonCodeWhispererService.SendTelemetryEvent`，`telemetryEvent.chatAddMessageEvent`。包含 `conversationId / messageId / timeToFirstChunkMilliseconds / timeBetweenChunks[] / chatTriggerInteraction / hasCodeSnippet / customizationArn / activeEditorTotalCharacters / cwsprChatPromptLength / cwsprChatConversationType`。**通过 CodeWhisperer 自家接口上报，不是 client-telemetry** —— 跟 toolkit telemetry 是两套并行通道。

## 请求行

```
POST https://q.us-east-1.amazonaws.com/
```

## 请求头（共 13 个）

| Header | Value |
|---|---|
| content-type | application/x-amz-json-1.0 |
| x-amz-target | AmazonCodeWhispererService.SendTelemetryEvent |
| content-length | 1089 |
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
- **Content-Length**：`1089` B（解码后实际 `1089` B）
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| clientToken | string | "00000000-0000-0000-0000-000000000042" |
| telemetryEvent | object{1} | {chatAddMessageEvent} |
| optOutPreference | string | "OPTIN" |
| userContext | object{5} | {ideCategory, operatingSystem, product...} |
| profileArn | string | "arn:aws:codewhisperer:us-east-1:699475941385:profile/EHGA3G... |
| modelId | string | "auto" |

## 响应头（共 10 个）

| Header | Value |
|---|---|
| date | Sun, 24 May 2026 08:04:43 GMT |
| content-type | application/x-amz-json-1.0 |
| content-length | 2 |
| connection | close |
| x-amzn-requestid | c8795f3f-f572-430b-a15d-77454ca7b24c |
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

## 字段深挖


**AmazonCodeWhispererService.SendTelemetryEvent —— 业务侧 telemetry（不是 toolkit telemetry）**

注意这条**和 #04/#05 的 client-telemetry 是两套独立通道**：
- 本条走 `q.us-east-1.amazonaws.com` + Bearer kiro token + Smithy x-amz-json-1.0
- client-telemetry 走 `client-telemetry.us-east-1.amazonaws.com` + SigV4 STS + 纯 JSON

**Body**：
```json
{
  "clientToken":        "<uuid>",            // 会话 nonce
  "telemetryEvent": {
    "chatAddMessageEvent": {
      "conversationId":              "<uuid>",
      "messageId":                   "<uuid>",
      "timeToFirstChunkMilliseconds": 3246.31,
      "timeBetweenChunks":            [0.02, 0.01, 44.24, 51.73, ...],
      "chatTriggerInteraction":      "manual",
      "hasCodeSnippet":              false,
      "customizationArn":            null,
      "activeEditorTotalCharacters": 0,
      "cwsprChatPromptLength":       <int>,
      "cwsprChatConversationType":   "Chat",
      "result":                      "Succeeded"
    }
  },
  "optOutPreference":  "OPTIN",
  "userContext": {
    "clientId":         "<uuid>",
    "ideCategory":      "CLI",
    "operatingSystem":  "LINUX",
    "product":          "CodeWhisperer for Terminal"
  },
  "profileArn":         "arn:aws:codewhisperer:us-east-1:699475941385:profile/EHGA3GRVQMUK",
  "modelId":            "CLAUDE_SONNET_4_5_V1_0"
}
```

**telemetryEvent 还可以是其它子类型**：`chatInteractWithMessageEvent` / `terminalUserInteractionEvent` / `userTriggerDecisionEvent` / `codeCoverageEvent` / `userModificationEvent` 等。Kiro 当前抓包里只看到 `chatAddMessageEvent`。

**Response**：`{}`（空对象）。

**为什么两套 telemetry**：
- `client-telemetry` 是 Amazon 通用的 Toolkit Telemetry（很多 AWS IDE 插件共用），度量"用了什么子命令"等基础指标。
- `SendTelemetryEvent` 是 CodeWhisperer/Q 业务专用，度量"对话质量" —— 服务端要用它做 RLHF / 模型评估。

---
_原始 JSON_：[`rows/07-POST-q.us-east-1.amazonaws.com_.json`](../rows/07-POST-q.us-east-1.amazonaws.com_.json)
