# 16. POST https://http-intake.logs.us5.datadoghq.com/api/v2/logs

**阶段**：Telemetry **状态码**：202 **请求大小**：8192 B **响应大小**：2 B

**用途**：**Datadog 公共 intake** 第一批。带写死的 `dd-api-key: pubea5604404508cdd34afb69e6f42a05bc`（pub key，仅写入）。本批 4 条：`tengu_exit/tengu_started/tengu_timer/tengu_init`。

## 请求行

```
POST https://http-intake.logs.us5.datadoghq.com/api/v2/logs
```

## 请求头（共 8 个）

| Header | Value |
|---|---|
| accept | application/json, text/plain, */* |
| accept-encoding | gzip, br |
| content-type | application/json |
| user-agent | axios/1.13.6 |
| dd-api-key | pubea5604404508cdd34afb69e6f42a05bc |
| host | http-intake.logs.us5.datadoghq.com |
| content-length | 8192 |
| connection | close |

## 请求体

- **Content-Type**：`application/json`
- **Content-Length**：`8192` B（解码后实际 `8192` B）
- **格式**：JSON (array[4])

### 顶层字段
_数组共 4 项，每项结构相同。展示第 0 项字段：_

| Field | Type | Sample |
|---|---|---|
| ddsource | string | "nodejs" |
| ddtags | string | "event:tengu_exit,arch:x64,client_type:cli,entrypoint:cli,mo... |
| message | string | "tengu_exit" |
| service | string | "claude-code" |
| hostname | string | "claude-code" |
| env | string | "external" |
| model | string | "claude-opus-4-7" |
| session_id | string | "d85790bb-6261-43c0-982d-550eb177c8d5" |
| user_type | string | "external" |
| betas | string | "claude-code-20250219,oauth-2025-04-20,context-1m-2025-08-07... |
| entrypoint | string | "cli" |
| is_interactive | string | "true" |
| client_type | string | "cli" |
| process_metrics | object{8} | {uptime, rss, heapTotal...} |
| swe_bench_run_id | string | "" |
| swe_bench_instance_id | string | "" |
| swe_bench_task_id | string | "" |
| subscription_type | string | "max" |
| rh | string | "3ee214335af448e2" |
| platform | string | "linux" |
| platform_raw | string | "linux" |
| arch | string | "x64" |
| node_version | string | "v24.3.0" |
| terminal | string | "xterm" |
| shell | string | "zsh" |
| package_managers | string | "npm,yarn,pnpm" |
| runtimes | string | "bun,deno,node" |
| is_running_with_bun | bool | true |
| is_ci | bool | false |
| is_claubbit | bool | false |
| is_claude_code_remote | bool | false |
| is_local_agent_mode | bool | false |
| is_conductor | bool | false |
| is_github_action | bool | false |
| is_claude_code_action | bool | false |
| is_claude_ai_auth | bool | true |
| version | string | "2.1.126" |
| version_base | string | "2.1.126" |
| build_time | string | "2026-04-30T16:01:00Z" |
| deployment_environment | string | "unknown-linux" |
| linux_kernel | string | "6.10.0-generic" |
| linux_distro_id | string | "arch" |
| vcs | string | "git" |
| last_session_cost | float | 0.40112649999999994 |
| last_session_api_duration | int | 74526 |
| last_session_tool_duration | int | 1602 |
| last_session_duration | int | 173615 |
| last_session_lines_added | int | 0 |
| last_session_lines_removed | int | 0 |
| last_session_total_input_tokens | int | 13 |
| last_session_total_output_tokens | int | 3340 |
| last_session_total_cache_creation_input_tokens | int | 30632 |
| last_session_total_cache_read_input_tokens | int | 252223 |
| last_session_fps_average | float | 4.13 |
| last_session_fps_low_1_pct | float | 253.78 |
| last_session_graceful_shutdown | bool | true |
| last_session_id | string | "0d7d3701-b10d-49a7-9324-8189c8c54152" |
| frame_duration_ms_count | int | 711 |
| frame_duration_ms_min | float | 0.13154600000416394 |
| frame_duration_ms_max | float | 11.022872000000234 |
| frame_duration_ms_avg | float | 0.9368810421940419 |
| frame_duration_ms_p50 | float | 0.8362070000002859 |
| frame_duration_ms_p95 | float | 1.7207904999959283 |
| frame_duration_ms_p99 | float | 3.9143601999996402 |
| pre_tool_hook_duration_ms_count | int | 7 |
| pre_tool_hook_duration_ms_min | int | 0 |
| pre_tool_hook_duration_ms_max | int | 1 |
| pre_tool_hook_duration_ms_avg | float | 0.5714285714285714 |
| pre_tool_hook_duration_ms_p50 | int | 1 |
| pre_tool_hook_duration_ms_p95 | int | 1 |
| pre_tool_hook_duration_ms_p99 | int | 1 |
| user_bucket | int | 21 |

## 响应头（共 8 个）

| Header | Value |
|---|---|
| content-type | application/json |
| content-length | 2 |
| cross-origin-resource-policy | cross-origin |
| accept-encoding | identity,gzip,x-gzip,deflate,x-deflate,zstd |
| x-content-type-options | nosniff |
| strict-transport-security | max-age=31536000; includeSubDomains; preload |
| date | Sun, 03 May 2026 15:28:11 GMT |
| connection | close |

## 响应体

- **Content-Type**：`application/json`
- **解码后大小**：`2` B
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|

## 字段深挖


**Body**：JSON 数组，每项 = 一条 Datadog log entry。本批 4 条，message 分别为 `tengu_exit / tengu_started / tengu_timer / tengu_init`。

`items[i]` 的字段（每条都齐）：

| Field | Type | 含义 |
|---|---|---|
| ddsource | string | 固定 `nodejs` |
| ddtags | string | DD tag 串，逗号分隔，含 `event:、arch:、client_type:、entrypoint:、model:、platform:、subscription_type:、user_bucket:、user_type:、version:、version_base:` |
| message | string | 事件名（同 telemetry 的 `event_name`） |
| service | string | `claude-code` |
| hostname | string | `claude-code` |
| env | string | `external` |
| model | string | `claude-opus-4-7` |
| session_id | uuid |  |
| user_type | string |  |
| betas | string | 同 anthropic-beta |
| entrypoint | string | `cli` |
| is_interactive | string | `"true"`（这里是字符串而不是 bool） |
| client_type | string | `cli` |
| process_metrics | object | `{uptime, rss, heapTotal, heapUsed, external, arrayBuffers, constrainedMemory, cpuUsage:{user,system}}` |
| swe_bench_run_id / swe_bench_instance_id / swe_bench_task_id | string | SWE-bench 评测专用，正常会话为空 |
| subscription_type | string | `max` |
| rh | string | run hash，跟 telemetry `additional_metadata.rh` 同 |
| platform / platform_raw / arch / node_version / terminal / shell / package_managers / runtimes | string |  |
| is_running_with_bun / is_ci / is_claubbit / is_claude_code_remote / is_local_agent_mode / is_conductor / is_github_action / is_claude_code_action / is_claude_ai_auth | bool | 环境探测 |
| version / version_base | string |  |
| build_time | ISO8601 |  |
| deployment_environment | string |  |
| linux_kernel / linux_distro_id | string |  |
| vcs | string | `git` |
| user_bucket | int | 取自 deviceID 的随机分桶 (0..N) |

**特定事件特有字段**（仅当 message 匹配时出现）

`tengu_exit` 多带：`last_session_*`（cost / api_duration / tool_duration / duration / lines_added / lines_removed / total_input_tokens / total_output_tokens / total_cache_creation_input_tokens / total_cache_read_input_tokens / fps_average / fps_low_1_pct / graceful_shutdown / id）+ `frame_duration_ms_*`（count/min/max/avg/p50/p95/p99）+ `pre_tool_hook_duration_ms_*`（同上 7 项）。

`tengu_init` / `tengu_started` / `tengu_timer` 以基础字段为主。

后续批（#21/#24/#28）会带不同附加字段，见各自文档。

**鉴权**：仅靠请求头 `dd-api-key: pubea5604404508cdd34afb69e6f42a05bc`（明文 public intake key，写死在客户端，仅写入权限），不带任何 Anthropic 凭据。

**响应**：HTTP 202，body `{}`。

---
_原始 JSON_：[`rows/16-POST-http-intake.logs.us5.datadoghq.com_api_v2_logs.json`](../rows/16-POST-http-intake.logs.us5.datadoghq.com_api_v2_logs.json)
