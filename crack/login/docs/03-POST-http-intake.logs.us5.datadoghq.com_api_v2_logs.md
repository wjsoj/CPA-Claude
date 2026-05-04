# 03. POST https://http-intake.logs.us5.datadoghq.com/api/v2/logs

**阶段**：启动 / Telemetry **状态码**：202 **请求大小**：5973 B **响应大小**：2 B

**用途**：Datadog 镜像批次，对应 #02 的部分事件（`tengu_exit/tengu_started/tengu_timer/tengu_init`）。同样是上次会话的尾声。

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
| content-length | 5973 |
| connection | close |

## 请求体

- **Content-Type**：`application/json`
- **Content-Length**：`5973` B（解码后实际 `5973` B）
- **格式**：JSON (array[3])

### 顶层字段
_数组共 3 项，每项结构相同。展示第 0 项字段：_

| Field | Type | Sample |
|---|---|---|
| ddsource | string | "nodejs" |
| ddtags | string | "event:tengu_exit,arch:x64,client_type:cli,entrypoint:cli,mo... |
| message | string | "tengu_exit" |
| service | string | "claude-code" |
| hostname | string | "claude-code" |
| env | string | "external" |
| model | string | "claude-sonnet-4-6" |
| session_id | string | "00000000-0000-0000-0000-000000000010" |
| user_type | string | "external" |
| betas | string | "claude-code-20250219,interleaved-thinking-2025-05-14,redact... |
| entrypoint | string | "cli" |
| is_interactive | string | "true" |
| client_type | string | "cli" |
| process_metrics | object{8} | {uptime, rss, heapTotal...} |
| swe_bench_run_id | string | "" |
| swe_bench_instance_id | string | "" |
| swe_bench_task_id | string | "" |
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
| is_claude_ai_auth | bool | false |
| version | string | "2.1.126" |
| version_base | string | "2.1.126" |
| build_time | string | "2026-04-30T16:01:00Z" |
| deployment_environment | string | "unknown-linux" |
| linux_kernel | string | "6.10.0-generic" |
| linux_distro_id | string | "arch" |
| vcs | string | "git" |
| last_session_cost | float | 29.451797250000006 |
| last_session_api_duration | int | 3996513 |
| last_session_tool_duration | int | 209024 |
| last_session_duration | int | 38448561 |
| last_session_lines_added | int | 3958 |
| last_session_lines_removed | int | 539 |
| last_session_total_input_tokens | int | 2196 |
| last_session_total_output_tokens | int | 245137 |
| last_session_total_cache_creation_input_tokens | int | 491413 |
| last_session_total_cache_read_input_tokens | int | 40482122 |
| last_session_fps_average | float | 1.95 |
| last_session_fps_low_1_pct | float | 189.05 |
| last_session_graceful_shutdown | bool | true |
| last_session_id | string | "0d7d3701-b10d-49a7-9324-8189c8c54152" |
| frame_duration_ms_count | int | 74696 |
| frame_duration_ms_min | float | 0.1302249999716878 |
| frame_duration_ms_max | float | 206.43788999971002 |
| frame_duration_ms_avg | float | 1.9429131238489357 |
| frame_duration_ms_p50 | float | 1.6990444999100873 |
| frame_duration_ms_p95 | float | 3.499544149683788 |
| frame_duration_ms_p99 | float | 4.202784329969433 |
| pre_tool_hook_duration_ms_count | int | 326 |
| pre_tool_hook_duration_ms_min | int | 0 |
| pre_tool_hook_duration_ms_max | int | 124 |
| pre_tool_hook_duration_ms_avg | float | 10.128834355828221 |
| pre_tool_hook_duration_ms_p50 | int | 0 |
| pre_tool_hook_duration_ms_p95 | int | 72 |
| pre_tool_hook_duration_ms_p99 | float | 98.75 |
| hook_duration_ms_count | int | 121 |
| hook_duration_ms_min | int | 0 |
| hook_duration_ms_max | int | 115 |
| hook_duration_ms_avg | float | 25.487603305785125 |
| hook_duration_ms_p50 | int | 0 |
| hook_duration_ms_p95 | int | 90 |
| hook_duration_ms_p99 | float | 104.99999999999999 |
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
| date | Mon, 04 May 2026 02:08:55 GMT |
| connection | close |

## 响应体

- **Content-Type**：`application/json`
- **解码后大小**：`2` B
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|

---
_原始 JSON_：[`rows/03-POST-http-intake.logs.us5.datadoghq.com_api_v2_logs.json`](../rows/03-POST-http-intake.logs.us5.datadoghq.com_api_v2_logs.json)
