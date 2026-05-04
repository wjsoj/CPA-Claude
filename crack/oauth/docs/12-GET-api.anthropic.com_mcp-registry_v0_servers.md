# 12. GET https://api.anthropic.com/mcp-registry/v0/servers?version=latest&limit=100&visibility=commercial%2Cgsuite%2Centerprise%2Chealth&cursor=com.crypto.mcp%2Fcrypto-com%3A1.0.0

**阶段**：MCP 发现 **状态码**：200 **请求大小**：0 B **响应大小**：68848 B

**用途**：Anthropic 公共 MCP 注册表第 2 页（`cursor=com.crypto.mcp/crypto-com:1.0.0`）。

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
| date | Sun, 03 May 2026 15:28:00 GMT |
| content-type | application/json |
| transfer-encoding | chunked |
| connection | close |
| x-request-id | 56782747-8ea8-4ad0-94f6-d9bd11ee4766 |
| access-control-allow-origin | * |
| access-control-allow-methods | GET, OPTIONS |
| access-control-allow-headers | * |
| x-envoy-upstream-service-time | 12 |
| content-encoding | gzip |
| vary | Accept-Encoding |
| server | cloudflare |
| server-timing | x-originResponse;dur=23 |
| set-cookie | _cfuvid=REDACTED; HttpOnly; SameSite=None; Secure; Path=/; Domain=api.anthropic.com |
| x-robots-tag | none |
| cf-cache-status | DYNAMIC |
| content-security-policy | default-src 'none'; frame-ancestors 'none' |
| cf-ray | 9f604b004e1c1e0f-LAX |

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
_原始 JSON_：[`rows/12-GET-api.anthropic.com_mcp-registry_v0_servers.json`](../rows/12-GET-api.anthropic.com_mcp-registry_v0_servers.json)
