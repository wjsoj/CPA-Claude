# 04. POST http://localhost:8080/answer/api/v1/mcp

**阶段**：MCP 发现 **状态码**：502 **请求大小**：305 B **响应大小**：270 B

**用途**：用户本地 MCP server 初始化，连接拒绝 → 502。同 OAuth 模式 #07。

## 请求行

```
POST http://localhost:8080/answer/api/v1/mcp
```

## 请求头（共 8 个）

| Header | Value |
|---|---|
| accept | application/json, text/event-stream |
| accept-encoding | identity |
| authorization | Bearer sk_019cdd2958c37d14938fb5e07d8a82b1 |
| content-type | application/json |
| user-agent | claude-code/2.1.126 (cli) |
| connection | Keep-Alive |
| host | localhost:8080 |
| content-length | 305 |

## 请求体

- **Content-Type**：`application/json`
- **Content-Length**：`305` B（解码后实际 `305` B）
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| method | string | "initialize" |
| params | object{3} | {protocolVersion, capabilities, clientInfo} |
| jsonrpc | string | "2.0" |
| id | int | 0 |

## 响应头（共 2 个）

| Header | Value |
|---|---|
| content-type | text/html; charset=utf8 |
| x-server | whistle |

## 响应体

- **Content-Type**：`text/html; charset=utf8`
- **解码后大小**：`270` B
- **格式**：非 JSON / 文本

### 内容
```
<pre>
From: whistle@2.9.101
Node: v24.6.0
Host: host
Date: 2026/5/3 23:53:52
Error: connect ECONNREFUSED ::1:8080
    at TCPConnectWrap.afterConnect [as oncomplete] (node:net:1637:16)


<a href="javascript:;" onclick="location.reload()">Reload this page</a>
</pre>
```

---
_原始 JSON_：[`rows/04-POST-localhost8080_answer_api_v1_mcp.json`](../rows/04-POST-localhost8080_answer_api_v1_mcp.json)
