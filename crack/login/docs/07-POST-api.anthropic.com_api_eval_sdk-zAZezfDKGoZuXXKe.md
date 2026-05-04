# 07. POST https://api.anthropic.com/api/eval/sdk-zAZezfDKGoZuXXKe

**阶段**：登录 — bootstrap **状态码**：200 **请求大小**：593 B **响应大小**：9744 B

**用途**：**GrowthBook 拉特性旗标**（与首条业务消息前的 bootstrap 用同一端点 `/api/eval/sdk-zAZezfDKGoZuXXKe`），登录刚完成立刻打一发，把 deviceID + 全部账号属性上送做实验分组。**用 Bun fetch（不是 axios），带 OAuth Bearer + `anthropic-beta: oauth-2025-04-20`**。

## 请求行

```
POST https://api.anthropic.com/api/eval/sdk-zAZezfDKGoZuXXKe
```

## 请求头（共 9 个）

| Header | Value |
|---|---|
| authorization | Bearer sk-ant-oat01-REDACTED |
| content-type | application/json |
| anthropic-beta | oauth-2025-04-20 |
| connection | keep-alive |
| user-agent | Bun/1.3.14 |
| accept | */* |
| host | api.anthropic.com |
| accept-encoding | gzip, br |
| content-length | 593 |

## 请求体

- **Content-Type**：`application/json`
- **Content-Length**：`593` B（解码后实际 `593` B）
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| attributes | object{13} | {id, sessionId, deviceID...} |
| forcedVariations | object{0} |  |
| forcedFeatures | array[0] |  |
| url | string | "" |

## 响应头（共 16 个）

| Header | Value |
|---|---|
| date | Mon, 04 May 2026 02:09:05 GMT |
| content-type | application/json |
| transfer-encoding | chunked |
| connection | keep-alive |
| request-id | req_REDACTED |
| strict-transport-security | max-age=31536000; includeSubDomains; preload |
| server | cloudflare |
| x-envoy-upstream-service-time | 25 |
| content-encoding | gzip |
| vary | Accept-Encoding |
| server-timing | x-originResponse;dur=28 |
| set-cookie | _cfuvid=REDACTED; HttpOnly; SameSite=None; Secure; Path=/; Domain=api.anthropic.com |
| x-robots-tag | none |
| cf-cache-status | DYNAMIC |
| content-security-policy | default-src 'none'; frame-ancestors 'none' |
| cf-ray | REDACTED-cf-ray |

## 响应体

- **Content-Type**：`application/json`
- **解码后大小**：`46897` B
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| features | object{225} | {tengu_marble_whisper, tengu_surreal_dali, tengu_attribution_header...} |

## 字段深挖


**关键差异 vs `/v1/messages` 前的 GrowthBook 调用**：完全相同的端点和请求结构。意味着登录流程会**额外打一次** `eval/sdk-...`，目的是用刚拿到的 OAuth 凭据 + 完整账号属性（`subscriptionType=max`、`rateLimitTier`、`accountUUID`）重新刷一次 feature flag 分组。

**鉴权**：`Authorization: Bearer <new access_token>`、`anthropic-beta: oauth-2025-04-20`、`User-Agent: Bun/1.3.14`（注意是 Bun fetch，不是 axios）。

---
_原始 JSON_：[`rows/07-POST-api.anthropic.com_api_eval_sdk-zAZezfDKGoZuXXKe.json`](../rows/07-POST-api.anthropic.com_api_eval_sdk-zAZezfDKGoZuXXKe.json)
