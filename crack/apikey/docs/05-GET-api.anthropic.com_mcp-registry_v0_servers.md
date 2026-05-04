# 05. GET https://api.anthropic.com/mcp-registry/v0/servers?version=latest&limit=100&visibility=commercial%2Cgsuite%2Centerprise%2Chealth

**阶段**：MCP 发现 **状态码**：200 **请求大小**：0 B **响应大小**：82743 B

**用途**：MCP 公共注册表第 1 页。axios 直连，不带任何认证。

## 请求行

```
GET https://api.anthropic.com/mcp-registry/v0/servers?version=latest&limit=100&visibility=commercial%2Cgsuite%2Centerprise%2Chealth
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
| date | Sun, 03 May 2026 15:53:53 GMT |
| content-type | application/json |
| transfer-encoding | chunked |
| connection | close |
| x-request-id | 79a905d3-fadf-4d4c-a32b-539a45125536 |
| access-control-allow-origin | * |
| access-control-allow-methods | GET, OPTIONS |
| access-control-allow-headers | * |
| x-envoy-upstream-service-time | 15 |
| content-encoding | gzip |
| vary | Accept-Encoding |
| server | cloudflare |
| server-timing | x-originResponse;dur=17 |
| cf-cache-status | DYNAMIC |
| set-cookie | _cfuvid=REDACTED; HttpOnly; SameSite=None; Secure; Path=/; Domain=api.anthropic.com |
| content-security-policy | default-src 'none'; frame-ancestors 'none' |
| x-robots-tag | none |
| cf-ray | REDACTED-cf-ray |

## 响应体

- **Content-Type**：`application/json`
- **解码后大小**：`320601` B
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| servers | array[100] | [object{2}, ...] |
| metadata | object{2} | {count, nextCursor} |

---
_原始 JSON_：[`rows/05-GET-api.anthropic.com_mcp-registry_v0_servers.json`](../rows/05-GET-api.anthropic.com_mcp-registry_v0_servers.json)
