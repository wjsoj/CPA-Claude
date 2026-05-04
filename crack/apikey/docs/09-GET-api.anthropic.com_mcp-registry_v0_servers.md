# 09. GET https://api.anthropic.com/mcp-registry/v0/servers?version=latest&limit=100&visibility=commercial%2Cgsuite%2Centerprise%2Chealth&cursor=com.crypto.mcp%2Fcrypto-com%3A1.0.0

**阶段**：MCP 发现 **状态码**：200 **请求大小**：0 B **响应大小**：68848 B

**用途**：MCP 注册表第 2 页。

## 请求行

```
GET https://api.anthropic.com/mcp-registry/v0/servers?version=latest&limit=100&visibility=commercial%2Cgsuite%2Centerprise%2Chealth&cursor=com.crypto.mcp%2Fcrypto-com%3A1.0.0
```

## 请求头（共 5 个）

| Header | Value |
|---|---|
| accept | application/json, text/plain, */* |
| accept-encoding | gzip, br |
| user-agent | axios/1.13.6 |
| host | api.anthropic.com |
| connection | close |

## 请求体

_无_

## 响应头（共 18 个）

| Header | Value |
|---|---|
| date | Sun, 03 May 2026 15:53:55 GMT |
| content-type | application/json |
| transfer-encoding | chunked |
| connection | close |
| x-request-id | 2b2462af-2831-467d-a934-93db26691d7f |
| access-control-allow-origin | * |
| access-control-allow-methods | GET, OPTIONS |
| access-control-allow-headers | * |
| x-envoy-upstream-service-time | 7 |
| content-encoding | gzip |
| vary | Accept-Encoding |
| server | cloudflare |
| server-timing | x-originResponse;dur=9 |
| set-cookie | _cfuvid=REDACTED; HttpOnly; SameSite=None; Secure; Path=/; Domain=api.anthropic.com |
| x-robots-tag | none |
| cf-cache-status | DYNAMIC |
| content-security-policy | default-src 'none'; frame-ancestors 'none' |
| cf-ray | REDACTED-cf-ray |

## 响应体

- **Content-Type**：`application/json`
- **解码后大小**：`300166` B
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| servers | array[100] | [object{2}, ...] |
| metadata | object{2} | {count, nextCursor} |

---
_原始 JSON_：[`rows/09-GET-api.anthropic.com_mcp-registry_v0_servers.json`](../rows/09-GET-api.anthropic.com_mcp-registry_v0_servers.json)
