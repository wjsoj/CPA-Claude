# 03. GET https://www.fucheers.top/v1/models?limit=1000

**阶段**：启动期 bootstrap **状态码**：200 **请求大小**：0 B **响应大小**：1028 B

**用途**：**新端点 `/v1/models?limit=1000`**（去三方 base url）。OpenAI 风格的模型清单。请求带的是三方 API key（不是 OAuth）。响应列出三方支持的模型 ID，每个标注 `owned_by` 与 `supported_endpoint_types`。

## 请求行

```
GET https://www.fucheers.top/v1/models?limit=1000
```

## 请求头（共 7 个）

| Header | Value |
|---|---|
| authorization | Bearer sk-REDACTED |
| user-agent | claude-code/2.1.126 |
| anthropic-version | 2023-06-01 |
| connection | keep-alive |
| accept | */* |
| host | www.fucheers.top |
| accept-encoding | gzip, br |

## 请求体

_无_

## 响应头（共 8 个）

| Header | Value |
|---|---|
| server | openresty |
| date | Sun, 03 May 2026 15:53:52 GMT |
| content-type | application/json; charset=utf-8 |
| content-length | 1028 |
| connection | keep-alive |
| x-new-api-version | v0.1.0 |
| x-oneapi-request-id | 202605031553524596323188268d9d6lqzWuWqa |
| strict-transport-security | max-age=31536000 |

## 响应体

- **Content-Type**：`application/json; charset=utf-8`
- **解码后大小**：`1028` B
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| data | array[7] | [object{5}, ...] |
| object | string | "list" |
| success | bool | true |

## 字段深挖


**新端点解析**：`https://www.fucheers.top/v1/models?limit=1000`

**Request Headers（关键）**
```
Authorization: Bearer sk-REDACTED   ← 三方 API key
User-Agent: claude-code/2.1.126
anthropic-version: 2023-06-01
```
没有 `anthropic-beta`、没有 `x-stainless-*`、没有 `x-claude-code-session-id` —— 这是 axios 直接调的辅助探测请求。

**Response Body（OpenAI list 风格）**
```json
{
  "object": "list",
  "success": true,
  "data": [
    {
      "id": "claude-opus-4-7",
      "object": "model",
      "created": 1626777600,
      "owned_by": "vertex-ai",
      "supported_endpoint_types": ["anthropic", "openai"]
    },
    {"id": "claude-haiku-4-5-20251001", "owned_by": "vertex-ai", ...},
    {"id": "claude-sonnet-4-6", "owned_by": "vertex-ai", ...},
    {"id": "claude-opus-4-6", "owned_by": "vertex-ai", ...},
    {"id": "claude-opus-4-6-thinking", "owned_by": "custom", ...},
    {"id": "claude-opus-4-7-thinking", "owned_by": "custom", ...},
    {"id": "claude-sonnet-4-6-thinking", "owned_by": "custom", ...}
  ]
}
```

`owned_by="vertex-ai"` 表明上游是 Google Vertex AI 的 Anthropic 接口，被这个网关转回 Anthropic 协议格式。`*-thinking` 是网关额外暴露的"强制思考"伪模型。

**OAuth 模式没有这条请求** —— OAuth 模式靠 `/api/claude_cli/bootstrap` 拿模型映射，不需要单独的 `/v1/models`。

---
_原始 JSON_：[`rows/03-GET-www.fucheers.top_v1_models.json`](../rows/03-GET-www.fucheers.top_v1_models.json)
