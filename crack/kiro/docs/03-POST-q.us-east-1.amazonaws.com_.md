# 03. POST https://q.us-east-1.amazonaws.com/?origin=KIRO_CLI&profileArn=arn%3Aaws%3Acodewhisperer%3Aus-east-1%3A699475941385%3Aprofile%2FEHGA3GRVQMUK

**阶段**：启动 — bootstrap **状态码**：200 **请求大小**：102 B **响应大小**：5952 B

**用途**：**模型列表探测**。`AmazonCodeWhispererService.ListAvailableModels`（x-amz-json-1.0）。query 上带 `origin=KIRO_CLI&profileArn=arn:aws:codewhisperer:us-east-1:699475941385:profile/EHGA3GRVQMUK`（该 profileArn 是 Amazon Q for Free Tier 的公共 profile，所有 Kiro 用户共享）。响应返回 `{defaultModel, models[]}`，含 `auto / claude-sonnet-4 / claude-3.7 / ...` 及各自的 `promptCaching` 元信息。

## 请求行

```
POST https://q.us-east-1.amazonaws.com/?origin=KIRO_CLI&profileArn=arn%3Aaws%3Acodewhisperer%3Aus-east-1%3A699475941385%3Aprofile%2FEHGA3GRVQMUK
```

## 请求头（共 13 个）

| Header | Value |
|---|---|
| content-type | application/x-amz-json-1.0 |
| x-amz-target | AmazonCodeWhispererService.ListAvailableModels |
| content-length | 102 |
| user-agent | aws-sdk-rust/1.3.16 ua/2.1 api/codewhispererruntime/0.1.16551 os/linux lang/rust/1.92.0 md/appVersion-2.4.1 app/AmazonQ-For-CLI |
| x-amz-user-agent | aws-sdk-rust/1.3.16 ua/2.1 api/codewhispererruntime/0.1.16551 os/linux lang/rust/1.92.0 m/F,C app/AmazonQ-For-CLI |
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
- **Content-Length**：`102` B（解码后实际 `102` B）
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| origin | string | "KIRO_CLI" |
| profileArn | string | "arn:aws:codewhisperer:us-east-1:699475941385:profile/EHGA3G... |

## 响应头（共 5 个）

| Header | Value |
|---|---|
| date | Sun, 24 May 2026 08:04:25 GMT |
| content-type | application/x-amz-json-1.0 |
| content-length | 5952 |
| connection | close |
| x-amzn-requestid | 3a712954-9929-43ff-be1c-d5bcb373c842 |

## 响应体

- **Content-Type**：`application/x-amz-json-1.0`
- **解码后大小**：`5952` B
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| defaultModel | object{1} | {modelId} |
| models | array[13] | [object{8}, ...] |

## 字段深挖


**AmazonCodeWhispererService.ListAvailableModels**

**Request**
- URL：`POST https://q.us-east-1.amazonaws.com/?origin=KIRO_CLI&profileArn=...`（**query 也带 profileArn，body 内还会再带一次**）
- `Content-Type: application/x-amz-json-1.0`
- `X-Amz-Target: AmazonCodeWhispererService.ListAvailableModels`
- `Authorization: Bearer <Kiro accessToken from #01>`（**Bearer，不是 SigV4**）
- `X-Amz-User-Agent: aws-sdk-rust/1.3.16 ua/2.1 api/codewhispererruntime/0.1.16551 os/linux lang/rust/1.92.0 m/F,C app/AmazonQ-For-CLI`

```json
{
  "origin": "KIRO_CLI",
  "profileArn": "arn:aws:codewhisperer:us-east-1:699475941385:profile/EHGA3GRVQMUK"
}
```

`profileArn` 的 account `699475941385` 是 Amazon 自家的 Q-for-Free-Tier profile（所有 Kiro 用户共享，**不是用户自己的 AWS account**）。CPA-Claude 转发时这个 arn 应当原样透传或直接 hard-code。

**Response** — 模型清单（节选）：
```json
{
  "defaultModel": { "modelId": "auto" },
  "models": [
    { "modelId": "auto",                "modelName": "auto",                "promptCaching": {...} },
    { "modelId": "CLAUDE_SONNET_4_5_V1_0", "modelName": "claude-sonnet-4-5", "promptCaching": {...} },
    { "modelId": "CLAUDE_SONNET_4_V1_0",   "modelName": "claude-sonnet-4",   "promptCaching": {...} },
    { "modelId": "CLAUDE_3_7_SONNET_V1_0", "modelName": "claude-3.7-sonnet", "promptCaching": {...} },
    ...
  ]
}
```

每个模型条目可能带：
- `modelName` —— 展示名
- `description`
- `promptCaching.maximumCacheCheckpointsAllowed` —— prompt cache 上限
- `supportedContentMediaType[]` / `supportedFeatureFlags[]`

**CPA-Claude 设计提示**：客户端是先 `ListAvailableModels` → 才在后续 `GenerateAssistantResponse` 里指定 `modelId`。代理时如要做 model rewrite，要拦住 ListAvailableModels 给出"只允许的"白名单。

---
_原始 JSON_：[`rows/03-POST-q.us-east-1.amazonaws.com_.json`](../rows/03-POST-q.us-east-1.amazonaws.com_.json)
