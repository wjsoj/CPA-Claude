# 19. POST https://http-intake.logs.us5.datadoghq.com/api/v2/logs

**阶段**：Telemetry **状态码**：202 **请求大小**：16056 B **响应大小**：2 B

**用途**：Datadog 第二批 6 条（按 message 数量推测，含 api_success / tool_use_success 等）。

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
| content-length | 16056 |
| connection | close |

## 请求体

- **Content-Type**：`application/json`
- **Content-Length**：`16056` B（解码后实际 `16056` B）
- **格式**：JSON (array[8])

### 顶层字段
_数组共 8 项，每项结构相同。展示第 0 项字段：_

| Field | Type | Sample |
|---|---|---|
| ddsource | string | "nodejs" |
| ddtags | string | "event:tengu_tool_use_success,arch:x64,client_type:cli,entry... |
| message | string | "tengu_tool_use_success" |
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
| process_metrics | object{9} | {uptime, rss, heapTotal...} |
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
| message_i_d | string | "msg_517512916e51483d8815b94b0bcfcdc5" |
| tool_name | string | "Bash" |
| is_mcp | bool | false |
| duration_ms | int | 152 |
| pre_tool_hook_duration_ms | int | 0 |
| permission_duration_ms | int | 3 |
| tool_result_size_bytes | int | 1415 |
| tool_input_size_bytes | int | 108 |
| file_extension | string | "toml" |
| bash_command_len | int | 52 |
| query_chain_id | string | "8497416a-6819-40ff-87ef-8e5f2a8bb8b1" |
| query_depth | int | 0 |
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
| date | Sun, 03 May 2026 15:54:29 GMT |
| connection | close |

## 响应体

- **Content-Type**：`application/json`
- **解码后大小**：`2` B
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|

---
_原始 JSON_：[`rows/19-POST-http-intake.logs.us5.datadoghq.com_api_v2_logs.json`](../rows/19-POST-http-intake.logs.us5.datadoghq.com_api_v2_logs.json)
