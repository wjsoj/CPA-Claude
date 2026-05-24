# 06. POST https://q.us-east-1.amazonaws.com/

**阶段**：业务 — Turn 1 **状态码**：200 **请求大小**：49374 B **响应大小**：6441 B

**用途**：**首条业务消息（核心）**。`AmazonCodeWhispererStreamingService.GenerateAssistantResponse`。body 顶层两字段：`conversationState` + `profileArn`。`conversationState` 包含 `{conversationId, history[], currentMessage, chatTriggerType, customizationArn}`。响应是 **AWS eventstream** 二进制帧（`application/vnd.amazon.eventstream`），需要按 12-byte 头 + headers + payload 的标准帧式解码。

## 请求行

```
POST https://q.us-east-1.amazonaws.com/
```

## 请求头（共 13 个）

| Header | Value |
|---|---|
| content-type | application/x-amz-json-1.0 |
| x-amz-target | AmazonCodeWhispererStreamingService.GenerateAssistantResponse |
| content-length | 49374 |
| user-agent | aws-sdk-rust/1.3.16 ua/2.1 api/codewhispererstreaming/0.1.16551 os/linux lang/rust/1.92.0 md/appVersion-2.4.1 app/AmazonQ-For-CLI |
| x-amz-user-agent | aws-sdk-rust/1.3.16 ua/2.1 api/codewhispererstreaming/0.1.16551 os/linux lang/rust/1.92.0 m/F app/AmazonQ-For-CLI |
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
- **Content-Length**：`49374` B（解码后实际 `43292` B）
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| conversationState | object{5} | {conversationId, history, currentMessage...} |
| profileArn | string | "arn:aws:codewhisperer:us-east-1:699475941385:profile/EHGA3G... |

## 响应头（共 10 个）

| Header | Value |
|---|---|
| date | Sun, 24 May 2026 08:04:39 GMT |
| content-type | application/vnd.amazon.eventstream |
| transfer-encoding | chunked |
| connection | close |
| x-amzn-requestid | 3c8087df-1a3f-4a54-9725-f78743eab5bc |
| x-xss-protection | 1; mode=block |
| strict-transport-security | max-age=47304000; includeSubDomains |
| x-frame-options | DENY |
| cache-control | no-cache |
| x-content-type-options | nosniff |

## 响应体

- **Content-Type**：`application/vnd.amazon.eventstream`
- **解码后大小**：`8605` B
- **格式**：非 JSON / 文本

### 内容
```
[binary base64]: AAAAhQAAAGBRloHTCzpldmVudC10eXBlBwAQaW5pdGlhbC1yZXNwb25zZQ06Y29udGVudC10eXBlBwAaYXBwbGljYXRpb24veC1hbXotanNvbi0xLjANOm1lc3NhZ2UtdHlwZQcABWV2ZW50eyJjb252ZXJzYXRpb25JZCI6IiJ9d61O6AAAAI4AAABcCSnMRQs6ZXZlbnQtdHlwZQcAFmFzc2lzdGFudFJlc3BvbnNlRXZlbnQNOmNvbnRlbnQtdHlwZQcAEGFwcGxpY2F0aW9uL2pzb24NOm1lc3NhZ2UtdHlwZQcABWV2ZW50eyJjb250ZW50Ijoi5L2gIiwibW9kZWxJZCI6ImF1dG8iff/h7Z8AAACYAAAAXOaJrmcLOmV2ZW50LXR5cGUHABZhc3Npc3RhbnRSZXNwb25zZUV2ZW50DTpjb250ZW50LXR5cGUHABBhcHBsaWNhdGlvbi9qc29uDTptZXNzYWdlLXR5cGUHAAVldmVudHsiY29udGVudCI6IuWlve+8geaIkeaYryAiLCJtb2RlbElkIjoiYXV0byJ9cE7R3wAAAJIAAABcrDm...
```

## 字段深挖


**核心业务：`AmazonCodeWhispererStreamingService.GenerateAssistantResponse`**

**Request**
- `Content-Type: application/x-amz-json-1.0`
- `X-Amz-Target: AmazonCodeWhispererStreamingService.GenerateAssistantResponse`
- `Authorization: Bearer <Kiro accessToken>`
- `X-Amz-User-Agent: aws-sdk-rust/1.3.16 ua/2.1 api/codewhispererstreaming/0.1.16551 os/linux lang/rust/1.92.0 m/F app/AmazonQ-For-CLI`

**Body 顶层**：
```json
{
  "conversationState": {
    "conversationId":    "<uuid>",
    "currentMessage": {
      "userInputMessage": {
        "content":             "<user text + context dump>",
        "userInputMessageContext": {
          "envState":   { "operatingSystem": "linux", "currentWorkingDirectory": "...", "envVariables": [...] },
          "shellState": { "shellName": "zsh", "shellHistory": [...] },
          "gitState":   { ... },
          "toolResults": [ ... ]      // 工具回合时这里塞上一轮的 tool 结果
        },
        "modelId":             "CLAUDE_SONNET_4_5_V1_0" | "auto",
        "origin":              "CLI"
      }
    },
    "history": [
      { "userInputMessage":      { ... } },
      { "assistantResponseMessage": { "content": "...", "toolUses": [...] } },
      ...
    ],
    "chatTriggerType":   "MANUAL",
    "customizationArn":  null
  },
  "profileArn": "arn:aws:codewhisperer:us-east-1:699475941385:profile/EHGA3GRVQMUK"
}
```

注意 Kiro 的 prompt 工程：
- **用户消息里手动塞了"CONTEXT ENTRY"段**：把项目内的 README、关键源码作为 `--- CONTEXT ENTRY BEGIN ---` 块拼在用户输入前。这是 Kiro 的上下文注入方式（vs Claude Code 用 system block）。
- **没有 system prompt 字段** —— Kiro 把所有"系统指令"也内联在 `userInputMessage.content` 里。
- **没有显式的 tools 列表** —— 工具定义在 CodeWhisperer 服务端按 `modelId` 配置，客户端不传 schema，只在 `toolResults` 里回上一轮工具产物。

**Response**：`Content-Type: application/vnd.amazon.eventstream`（AWS event-stream 二进制帧）。

每帧结构（标准 AWS event-stream）：
```
+--------+------------+------------+------+---------+------+
| 4 byte | 4 byte     | 4 byte     | hdrs | payload | 4 byte
| total  | hdrs-len   | prelude-crc|      |         | msg-crc
+--------+------------+------------+------+---------+------+
```

Headers 里有 `:event-type` / `:content-type` / `:message-type` 等字段，常见 event-type：
- `initial-response` —— 首帧，含 `conversationId / messageId / requestId`
- `assistantResponseEvent` —— 文本增量，payload 为 JSON `{ "content": "..." }`
- `toolUseEvent` —— 工具调用，payload 为 JSON `{ "toolUseId", "name", "input": "<JSON-as-string>", "stop": bool }`
- `codeReferenceEvent` —— 代码引用合规元数据
- `messageMetadataEvent` —— 终态，含 usage `{inputTokenCount, outputTokenCount, cacheReadTokenCount, cacheWriteTokenCount}`

**CPA-Claude 转发提示**：
1. 上行只需要透传 `conversationState + profileArn`。
2. 下行要做 event-stream → SSE 的协议翻译（如果想把 Kiro 当 Anthropic 上游用）。
3. 计费 token 在 `messageMetadataEvent` 末帧，要解析二进制帧才能拿到。

---
_原始 JSON_：[`rows/06-POST-q.us-east-1.amazonaws.com_.json`](../rows/06-POST-q.us-east-1.amazonaws.com_.json)
