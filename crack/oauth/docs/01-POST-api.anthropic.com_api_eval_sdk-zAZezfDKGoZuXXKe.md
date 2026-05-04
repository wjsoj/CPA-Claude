# 01. POST https://api.anthropic.com/api/eval/sdk-zAZezfDKGoZuXXKe

**阶段**：启动期 bootstrap **状态码**：200 **请求大小**：593 B **响应大小**：9649 B

**用途**：GrowthBook 风格的 feature flag / A-B 实验拉取。请求体上送设备指纹 + 账号属性，响应体下发整套 `tengu_*` 旗标。注意是用 **bun 内置 fetch** 直接打的（绕过 Stainless SDK）。

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
| date | Sun, 03 May 2026 15:27:56 GMT |
| content-type | application/json |
| transfer-encoding | chunked |
| connection | keep-alive |
| request-id | req_011CafsW1puRYHVuW6U3HotE |
| strict-transport-security | max-age=31536000; includeSubDomains; preload |
| server | cloudflare |
| x-envoy-upstream-service-time | 61 |
| content-encoding | gzip |
| vary | Accept-Encoding |
| server-timing | x-originResponse;dur=63 |
| set-cookie | _cfuvid=REDACTED; HttpOnly; SameSite=None; Secure; Path=/; Domain=api.anthropic.com |
| x-robots-tag | none |
| cf-cache-status | DYNAMIC |
| content-security-policy | default-src 'none'; frame-ancestors 'none' |
| cf-ray | 9f604aea0beca0c6-LAX |

## 响应体

- **Content-Type**：`application/json`
- **解码后大小**：`46947` B
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| features | object{227} | {tengu_collage_kaleidoscope, tengu_gypsum_kite, tengu_cedar_inlet...} |

## 字段深挖


**`attributes`（GrowthBook 属性 / 设备指纹）**

| Field | Type | 含义 |
|---|---|---|
| id | string | 同 deviceID — machine-id 的 SHA-256，64 hex |
| sessionId | uuid | CLI 进程级，每次启动新生成 |
| deviceID | string | 与 id 同值 |
| platform | string | `linux` / `darwin` / `win32` |
| organizationUUID | uuid | Anthropic 组织 |
| accountUUID | uuid | Anthropic 账号 |
| userType | string | `external` / `internal` |
| subscriptionType | string | `max` / `pro` / `team` … |
| rateLimitTier | string | 如 `default_claude_max_20x` |
| firstTokenTime | epoch ms | 该账号首次成功请求的时间戳 |
| email | string | 账号邮箱 |
| appVersion | string | `2.1.126` |
| entrypoint | string | `cli` / `vscode` 等 |

**`features`（响应）**：每个 key 是 `tengu_*` 旗标，对应一个 GrowthBook feature 结构：
```json
{
  "value":  <旗标值，可为 bool/string/int/object>,
  "on":     true,
  "off":    false,
  "source": "defaultValue" | "force" | "experiment" | "override",
  "experiment":       <可选 experiment 定义>,
  "experimentResult": <可选命中结果，含 variationId、value、hashUsed、hashAttribute、hashValue、featureId、key>,
  "ruleId": <可选规则 id, 形如 "fr_xxx">
}
```
hash 字段表明 GrowthBook 用 `attributes.id`（即 deviceID）做一致性 hash，所以同一台机器的实验分组稳定。

---
_原始 JSON_：[`rows/01-POST-api.anthropic.com_api_eval_sdk-zAZezfDKGoZuXXKe.json`](../rows/01-POST-api.anthropic.com_api_eval_sdk-zAZezfDKGoZuXXKe.json)
