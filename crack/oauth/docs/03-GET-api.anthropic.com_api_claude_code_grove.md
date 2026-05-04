# 03. GET https://api.anthropic.com/api/claude_code_grove

**阶段**：启动期 bootstrap **状态码**：200 **请求大小**：0 B **响应大小**：107 B

**用途**：`grove` 通知/宽限期开关。

## 请求行

```
GET https://api.anthropic.com/api/claude_code_grove
```

## 请求头（共 7 个）

| Header | Value |
|---|---|
| accept | application/json, text/plain, */* |
| accept-encoding | gzip, br |
| authorization | Bearer sk-ant-oat01-REDACTED |
| user-agent | claude-cli/2.1.126 (external, cli) |
| anthropic-beta | oauth-2025-04-20 |
| host | api.anthropic.com |
| connection | close |

## 请求体

_无_

## 响应头（共 16 个）

| Header | Value |
|---|---|
| date | Sun, 03 May 2026 15:27:56 GMT |
| content-type | application/json |
| transfer-encoding | chunked |
| connection | close |
| request-id | req_011CafsW2fG144tmQpKBBYxJ |
| strict-transport-security | max-age=31536000; includeSubDomains; preload |
| server | cloudflare |
| x-envoy-upstream-service-time | 113 |
| content-encoding | gzip |
| vary | Accept-Encoding |
| server-timing | x-originResponse;dur=116 |
| set-cookie | _cfuvid=REDACTED; HttpOnly; SameSite=None; Secure; Path=/; Domain=api.anthropic.com |
| x-robots-tag | none |
| cf-cache-status | DYNAMIC |
| content-security-policy | default-src 'none'; frame-ancestors 'none' |
| cf-ray | 9f604aeb4c1f13aa-LAX |

## 响应体

- **Content-Type**：`application/json`
- **解码后大小**：`107` B
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| grove_enabled | bool | true |
| domain_excluded | bool | false |
| notice_is_grace_period | bool | false |
| notice_reminder_frequency | int | 0 |

---
_原始 JSON_：[`rows/03-GET-api.anthropic.com_api_claude_code_grove.json`](../rows/03-GET-api.anthropic.com_api_claude_code_grove.json)
