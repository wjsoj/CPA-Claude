# 09. GET https://api.anthropic.com/v1/mcp_servers?limit=1000

**阶段**：MCP 发现 **状态码**：200 **请求大小**：0 B **响应大小**：28 B

**用途**：用户已在云端配置的私有 MCP server 列表（本账户为空）。带 OAuth + `mcp-servers-2025-12-04` beta。

## 请求行

```
GET https://api.anthropic.com/v1/mcp_servers?limit=1000
```

## 请求头（共 9 个）

| Header | Value |
|---|---|
| accept | application/json, text/plain, */* |
| accept-encoding | gzip, br |
| authorization | Bearer sk-ant-oat01-REDACTED |
| content-type | application/json |
| user-agent | axios/1.13.6 |
| anthropic-beta | mcp-servers-2025-12-04 |
| anthropic-version | 2023-06-01 |
| host | api.anthropic.com |
| connection | close |

## 请求体

_无_

## 响应头（共 14 个）

| Header | Value |
|---|---|
| date | Sun, 03 May 2026 15:27:58 GMT |
| content-type | application/json |
| content-length | 28 |
| connection | close |
| request-id | req_011CafsWAVoH7UbBB1asRDcv |
| strict-transport-security | max-age=31536000; includeSubDomains; preload |
| server | cloudflare |
| x-envoy-upstream-service-time | 77 |
| server-timing | x-originResponse;dur=80 |
| set-cookie | _cfuvid=REDACTED; HttpOnly; SameSite=None; Secure; Path=/; Domain=api.anthropic.com |
| x-robots-tag | none |
| cf-cache-status | DYNAMIC |
| content-security-policy | default-src 'none'; frame-ancestors 'none' |
| cf-ray | 9f604af6ba442ac3-LAX |

## 响应体

- **Content-Type**：`application/json`
- **解码后大小**：`28` B
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| data | array[0] |  |
| next_page | null | null |

---
_原始 JSON_：[`rows/09-GET-api.anthropic.com_v1_mcp_servers.json`](../rows/09-GET-api.anthropic.com_v1_mcp_servers.json)
