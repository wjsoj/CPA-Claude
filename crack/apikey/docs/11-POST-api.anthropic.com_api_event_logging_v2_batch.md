# 11. POST https://api.anthropic.com/api/event_logging/v2/batch

**阶段**：Telemetry **状态码**：200 **请求大小**：162143 B **响应大小**：57 B

**用途**：**启动期 telemetry 大批 90 条**。本批 vs OAuth：体积接近（162k vs 196k），但 **Authorization header 没了** —— apikey 模式下 telemetry **匿名上报**到 anthropic.com，不带 OAuth bearer 也不带任何 anthropic-beta。事件中 `auth=null, email=null`。

## 请求行

```
POST https://api.anthropic.com/api/event_logging/v2/batch
```

## 请求头（共 8 个）

| Header | Value |
|---|---|
| accept | application/json, text/plain, */* |
| accept-encoding | gzip, br |
| content-type | application/json |
| user-agent | claude-code/2.1.126 |
| x-service-name | claude-code |
| host | api.anthropic.com |
| content-length | 162143 |
| connection | close |

## 请求体

- **Content-Type**：`application/json`
- **Content-Length**：`162143` B（解码后实际 `162143` B）
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| events | array[90] | [object{2}, ...] |

## 响应头（共 16 个）

| Header | Value |
|---|---|
| date | Sun, 03 May 2026 15:54:03 GMT |
| content-type | application/json |
| transfer-encoding | chunked |
| connection | close |
| request-id | req_REDACTED |
| strict-transport-security | max-age=31536000; includeSubDomains; preload |
| server | cloudflare |
| x-envoy-upstream-service-time | 19 |
| content-encoding | gzip |
| vary | Accept-Encoding |
| server-timing | x-originResponse;dur=21 |
| set-cookie | _cfuvid=REDACTED; HttpOnly; SameSite=None; Secure; Path=/; Domain=api.anthropic.com |
| x-robots-tag | none |
| cf-cache-status | DYNAMIC |
| content-security-policy | default-src 'none'; frame-ancestors 'none' |
| cf-ray | REDACTED-cf-ray |

## 响应体

- **Content-Type**：`application/json`
- **解码后大小**：`40` B
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| accepted_count | int | 90 |
| rejected_count | int | 0 |

## 字段深挖


**对比 OAuth 模式 #14 (event_logging)**

| 字段 | OAuth 模式 | ApiKey 模式 |
|---|---|---|
| `Authorization` 请求头 | `Bearer sk-ant-oat01-...` | **无** |
| `anthropic-beta` 请求头 | `oauth-2025-04-20` | **无** |
| `x-service-name` 请求头 | `claude-code` | `claude-code`（同） |
| event_data.auth | `{organization_uuid, account_uuid}` | **null** |
| event_data.email | `redacted@example.com` | **null** |
| event_data.session_id | OAuth session 的 uuid | apikey session 的 uuid |
| event_data.device_id | sha256(machine-id) | **同一个 sha256（机器没变）** |
| env.is_claude_ai_auth | `true` | `false` |

**结论**：apikey 模式下，遥测**匿名上报**到 Anthropic（无 Bearer，事件本身也不带 email/account/org）；但 device_id (machine-id sha256) 仍然出现，所以从设备维度仍可被关联。

---
_原始 JSON_：[`rows/11-POST-api.anthropic.com_api_event_logging_v2_batch.json`](../rows/11-POST-api.anthropic.com_api_event_logging_v2_batch.json)
