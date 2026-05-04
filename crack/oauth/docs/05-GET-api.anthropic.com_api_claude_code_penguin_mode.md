# 05. GET https://api.anthropic.com/api/claude_code_penguin_mode

**阶段**：启动期 bootstrap **状态码**：200 **请求大小**：0 B **响应大小**：75 B

**用途**：'penguin mode'（额度溢出按需付费）开关。

## 请求行

```
GET https://api.anthropic.com/api/claude_code_penguin_mode
```

## 请求头（共 7 个）

| Header | Value |
|---|---|
| accept | application/json, text/plain, */* |
| accept-encoding | gzip, br |
| authorization | Bearer sk-ant-oat01-REDACTED |
| user-agent | axios/1.13.6 |
| anthropic-beta | oauth-2025-04-20 |
| host | api.anthropic.com |
| connection | close |

## 请求体

_无_

## 响应头（共 17 个）

| Header | Value |
|---|---|
| date | Sun, 03 May 2026 15:27:58 GMT |
| content-type | application/json |
| transfer-encoding | chunked |
| connection | close |
| request-id | req_011CafsW7jMnJ2sj7EFaQnrJ |
| strict-transport-security | max-age=31536000; includeSubDomains; preload |
| anthropic-organization-id | 00000000-0000-0000-0000-000000000002 |
| server | cloudflare |
| x-envoy-upstream-service-time | 68 |
| content-encoding | gzip |
| vary | Accept-Encoding |
| server-timing | x-originResponse;dur=70 |
| set-cookie | _cfuvid=REDACTED; HttpOnly; SameSite=None; Secure; Path=/; Domain=api.anthropic.com |
| x-robots-tag | none |
| cf-cache-status | DYNAMIC |
| content-security-policy | default-src 'none'; frame-ancestors 'none' |
| cf-ray | 9f604af2be2524e5-LAX |

## 响应体

- **Content-Type**：`application/json`
- **解码后大小**：`58` B
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| enabled | bool | false |
| disabled_reason | string | "extra_usage_disabled" |

---
_原始 JSON_：[`rows/05-GET-api.anthropic.com_api_claude_code_penguin_mode.json`](../rows/05-GET-api.anthropic.com_api_claude_code_penguin_mode.json)
