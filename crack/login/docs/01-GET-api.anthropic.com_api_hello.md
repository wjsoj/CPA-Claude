# 01. GET https://api.anthropic.com/api/hello

**阶段**：启动 / 登录前 **状态码**：200 **请求大小**：0 B **响应大小**：20 B

**用途**：启动期 **空载健康探针**：CLI 进程拉起后第一发请求，仅校验到 Anthropic 的 TLS / HTTP 联通。**不带任何 Authorization** —— 这是登录前唯一一条不带凭据的 anthropic.com 请求。

## 请求行

```
GET https://api.anthropic.com/api/hello
```

## 请求头（共 5 个）

| Header | Value |
|---|---|
| accept | application/json, text/plain, */* |
| accept-encoding | gzip, br |
| user-agent | claude-cli/2.1.126 (external, cli) |
| host | api.anthropic.com |
| connection | close |

## 请求体

_无_

## 响应头（共 11 个）

| Header | Value |
|---|---|
| date | Mon, 04 May 2026 02:08:40 GMT |
| content-type | application/json |
| content-length | 20 |
| connection | close |
| server-timing | x-originResponse;dur= |
| server | cloudflare |
| set-cookie | _cfuvid=REDACTED; HttpOnly; SameSite=None; Secure; Path=/; Domain=api.anthropic.com |
| x-robots-tag | none |
| cf-cache-status | DYNAMIC |
| content-security-policy | default-src 'none'; frame-ancestors 'none' |
| cf-ray | REDACTED-cf-ray |

## 响应体

- **Content-Type**：`application/json`
- **解码后大小**：`20` B
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| message | string | "hello" |

---
_原始 JSON_：[`rows/01-GET-api.anthropic.com_api_hello.json`](../rows/01-GET-api.anthropic.com_api_hello.json)
