# 13. GET https://api.anthropic.com/mcp-registry/v0/servers?version=latest&limit=100&visibility=commercial%2Cgsuite%2Centerprise%2Chealth&cursor=io.customer%2Fmcp%3A1.0.0

**阶段**：MCP 发现 **状态码**：200 **请求大小**：0 B **响应大小**：16325 B

**用途**：Anthropic 公共 MCP 注册表第 3 页（`cursor=io.customer/mcp:1.0.0`）。

## 请求行

```
GET https://api.anthropic.com/mcp-registry/v0/servers?version=latest&limit=100&visibility=commercial%2Cgsuite%2Centerprise%2Chealth&cursor=io.customer%2Fmcp%3A1.0.0
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
| date | Sun, 03 May 2026 15:28:02 GMT |
| content-type | application/json |
| transfer-encoding | chunked |
| connection | close |
| x-request-id | dab17410-80a7-43c9-9321-c1a73e921ce0 |
| access-control-allow-origin | * |
| access-control-allow-methods | GET, OPTIONS |
| access-control-allow-headers | * |
| x-envoy-upstream-service-time | 4 |
| content-encoding | gzip |
| vary | Accept-Encoding |
| server | cloudflare |
| server-timing | x-originResponse;dur=9 |
| cf-cache-status | DYNAMIC |
| set-cookie | _cfuvid=REDACTED; HttpOnly; SameSite=None; Secure; Path=/; Domain=api.anthropic.com |
| content-security-policy | default-src 'none'; frame-ancestors 'none' |
| x-robots-tag | none |
| cf-ray | 9f604b10cabaccff-LAX |

## 响应体

- **Content-Type**：`application/json`
- **解码后大小**：`70545` B
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| servers | array[24] | [object{2}, ...] |
| metadata | object{1} | {count} |

---
_原始 JSON_：[`rows/13-GET-api.anthropic.com_mcp-registry_v0_servers.json`](../rows/13-GET-api.anthropic.com_mcp-registry_v0_servers.json)
