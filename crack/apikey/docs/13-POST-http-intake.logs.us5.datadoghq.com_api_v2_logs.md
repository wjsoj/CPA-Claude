# 13. POST https://http-intake.logs.us5.datadoghq.com/api/v2/logs

**阶段**：Telemetry **状态码**：202 **请求大小**：7540 B **响应大小**：2 B

**用途**：Datadog public intake 第一批。dd-api-key 还是同一个公钥。message 包含 `tengu_init/tengu_started/tengu_timer/tengu_exit` 四个老朋友。

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
| content-length | 7540 |
| connection | close |

## 请求体

- **Content-Type**：`application/json`
- **Content-Length**：`7540` B（解码后实际 `7540` B）
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
| model | string | "claude-sonnet-4-6" |
| session_id | string | "ec194dda-5172-4c55-a4d5-87e5904750cc" |
| user_type | string | "external" |
| betas | string | "claude-code-20250219,interleaved-thinking-2025-05-14,redact... |
| entrypoint | string | "cli" |
| is_interactive | string | "true" |
| client_type | string | "cli" |
| process_metrics | object{8} | {uptime, rss, heapTotal...} |
| swe_bench_run_id | string | "" |
| swe_bench_instance_id | string | "" |
| swe_bench_task_id | string | "" |
| rh | string | "d214c24d8a148177" |
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
| last_session_cost | int | 0 |
| last_session_api_duration | int | 0 |
| last_session_tool_duration | int | 0 |
| last_session_duration | int | 10858 |
| last_session_lines_added | int | 0 |
| last_session_lines_removed | int | 0 |
| last_session_total_input_tokens | int | 0 |
| last_session_total_output_tokens | int | 0 |
| last_session_total_cache_creation_input_tokens | int | 0 |
| last_session_total_cache_read_input_tokens | int | 0 |
| last_session_fps_average | float | 3.97 |
| last_session_fps_low_1_pct | float | 128.08 |
| last_session_graceful_shutdown | bool | true |
| last_session_id | string | "f9b38aa4-cde9-427c-8717-81952022d03c" |
| frame_duration_ms_count | int | 38 |
| frame_duration_ms_min | float | 0.11421399999994719 |
| frame_duration_ms_max | float | 7.8076750000000175 |
| frame_duration_ms_avg | float | 0.9400303157894876 |
| frame_duration_ms_p50 | float | 0.818694499999765 |
| frame_duration_ms_p95 | float | 1.9196836500000116 |
| frame_duration_ms_p99 | float | 5.938168230000024 |
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
| date | Sun, 03 May 2026 15:54:07 GMT |
| connection | close |

## 响应体

- **Content-Type**：`application/json`
- **解码后大小**：`2` B
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|

---
_原始 JSON_：[`rows/13-POST-http-intake.logs.us5.datadoghq.com_api_v2_logs.json`](../rows/13-POST-http-intake.logs.us5.datadoghq.com_api_v2_logs.json)
