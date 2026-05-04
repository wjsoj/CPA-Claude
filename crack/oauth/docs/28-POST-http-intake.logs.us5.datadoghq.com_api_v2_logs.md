# 28. POST https://http-intake.logs.us5.datadoghq.com/api/v2/logs

**阶段**：Telemetry **状态码**：202 **请求大小**：4733 B **响应大小**：2 B

**用途**：Datadog 第四批。2 条：`tengu_tool_use_success/tengu_api_success`。

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
| content-length | 4733 |
| connection | close |

## 请求体

- **Content-Type**：`application/json`
- **Content-Length**：`4733` B（解码后实际 `4733` B）
- **格式**：JSON (array[2])

### 顶层字段
_数组共 2 项，每项结构相同。展示第 0 项字段：_

| Field | Type | Sample |
|---|---|---|
| ddsource | string | "nodejs" |
| ddtags | string | "event:tengu_tool_use_success,arch:x64,client_type:cli,entry... |
| message | string | "tengu_tool_use_success" |
| service | string | "claude-code" |
| hostname | string | "claude-code" |
| env | string | "external" |
| model | string | "claude-opus-4-7" |
| session_id | string | "0d7d3701-b10d-49a7-9324-8189c8c54152" |
| user_type | string | "external" |
| betas | string | "claude-code-20250219,oauth-2025-04-20,context-1m-2025-08-07... |
| entrypoint | string | "cli" |
| is_interactive | string | "true" |
| client_type | string | "cli" |
| process_metrics | object{9} | {uptime, rss, heapTotal...} |
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
| message_i_d | string | "msg_014bzSzwTndgamn8UdjKxWMm" |
| tool_name | string | "mcp_tool" |
| is_mcp | bool | true |
| duration_ms | int | 11848 |
| pre_tool_hook_duration_ms | int | 1 |
| permission_duration_ms | int | 1 |
| tool_result_size_bytes | int | 492 |
| tool_input_size_bytes | int | 2 |
| query_chain_id | string | "7e688a90-53e7-42ae-9e49-b40c9ab0cb60" |
| query_depth | int | 1 |
| mcp_server_type | string | "stdio" |
| request_id | string | "req_011CafsZXvUkvjhPHMCkAGgM" |
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
| date | Sun, 03 May 2026 15:29:17 GMT |
| connection | close |

## 响应体

- **Content-Type**：`application/json`
- **解码后大小**：`2` B
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|

---
_原始 JSON_：[`rows/28-POST-http-intake.logs.us5.datadoghq.com_api_v2_logs.json`](../rows/28-POST-http-intake.logs.us5.datadoghq.com_api_v2_logs.json)
