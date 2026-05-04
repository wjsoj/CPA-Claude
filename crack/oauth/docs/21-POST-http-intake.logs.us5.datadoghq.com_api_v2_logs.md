# 21. POST https://http-intake.logs.us5.datadoghq.com/api/v2/logs

**阶段**：Telemetry **状态码**：202 **请求大小**：7435 B **响应大小**：2 B

**用途**：Datadog 第二批。3 条：`tengu_api_success` x2 + `tengu_tool_use_success`。多了 `pre_normalized_model/cost_u_s_d/ttft_ms/cached_input_tokens/...` 等性能指标字段。

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
| content-length | 7435 |
| connection | close |

## 请求体

- **Content-Type**：`application/json`
- **Content-Length**：`7435` B（解码后实际 `7435` B）
- **格式**：JSON (array[3])

### 顶层字段
_数组共 3 项，每项结构相同。展示第 0 项字段：_

| Field | Type | Sample |
|---|---|---|
| ddsource | string | "nodejs" |
| ddtags | string | "event:tengu_api_success,arch:x64,client_type:cli,entrypoint... |
| message | string | "tengu_api_success" |
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
| pre_normalized_model | string | "claude-opus-4-7[1m]" |
| message_count | int | 19 |
| message_tokens | int | 47883 |
| input_tokens | int | 6 |
| output_tokens | int | 92 |
| cached_input_tokens | int | 45410 |
| uncached_input_tokens | int | 2670 |
| duration_ms | int | 3416 |
| duration_ms_including_retries | int | 3420 |
| attempt | int | 1 |
| ttft_ms | int | 3094 |
| build_age_mins | int | 4287 |
| provider | string | "firstParty" |
| request_id | string | "req_011CafsXw8vsa4sbNtMkxRzm" |
| stop_reason | string | "tool_use" |
| cost_u_s_d | float | 0.041722499999999996 |
| did_fall_back_to_non_streaming | bool | false |
| is_non_interactive_session | bool | false |
| print | bool | false |
| is_t_t_y | bool | true |
| query_source | string | "repl_main_thread" |
| query_chain_id | string | "3a543269-d439-4178-befe-54428f5673aa" |
| query_depth | int | 0 |
| permission_mode | string | "bypassPermissions" |
| global_cache_strategy | string | "system_prompt" |
| text_content_length | int | 0 |
| tool_use_content_lengths | string | "{\"Bash\":57}" |
| fast_mode | bool | false |
| previous_request_id | string | "req_011CafsNWWqvfF2AKhu2B2SR" |
| ms_in_connecting | int | 3094 |
| ms_in_streaming | int | 322 |
| ms_in_degraded | int | 0 |
| ms_in_offline | int | 0 |
| offline_entries | int | 0 |
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
| date | Sun, 03 May 2026 15:28:39 GMT |
| connection | close |

## 响应体

- **Content-Type**：`application/json`
- **解码后大小**：`2` B
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|

---
_原始 JSON_：[`rows/21-POST-http-intake.logs.us5.datadoghq.com_api_v2_logs.json`](../rows/21-POST-http-intake.logs.us5.datadoghq.com_api_v2_logs.json)
