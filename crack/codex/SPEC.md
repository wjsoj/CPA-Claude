# Codex CLI fingerprint — capture target `codex-tui/0.135.0`

Ground truth captured 2026-05-30 via Whistle from a live `codex` (Rust TUI) session on a
ChatGPT **Pro** subscription. All secrets (Bearer JWT, cookies, account UUID, user id, email,
workspace path, git origin) are redacted in `rows/`. Non-secret fingerprint values are kept verbatim.

## TL;DR — what changed vs the code's current target (`codex_cli_rs/0.125.0`)

| Field | Old (in code) | **New (this capture)** |
|---|---|---|
| client / `originator` | `codex_cli_rs` | **`codex-tui`** |
| `User-Agent` | `codex_cli_rs/0.125.0` | **`codex-tui/0.135.0 (Arch Linux Rolling Release; x86_64) Konsole/260401 (codex-tui; 0.135.0)`** |
| `version` header | `0.125.0` | **`0.135.0`** |
| transport | HTTP POST + SSE | **WebSocket** (`wss://chatgpt.com/backend-api/codex/responses`) |
| `OpenAI-Beta` | `responses=experimental` | **`responses_websockets=2026-02-06`** (WS handshake) |
| default model | — | **`gpt-5.5`**, reasoning_effort `high`, service_tier `default` |
| extra headers | — | `x-codex-beta-features`, `x-codex-turn-metadata`, `x-codex-window-id`, `session-id`, `thread-id`, `x-client-request-id` |

> **Transport note:** 0.135.0 streams the turn over a **WebSocket**, not HTTP POST/SSE. The
> WS frames carrying the Responses payload were **not** captured (Whistle did not record frame
> bodies for this session). The legacy HTTP `POST /backend-api/codex/responses` with
> `OpenAI-Beta: responses=experimental` is what every codex2api proxy (and our existing code)
> uses and is still accepted by the backend; this capture updates the *headers/identity* but the
> request-body shape for HTTP POST is unchanged from the Responses API contract.

## OAuth (unchanged, confirmed)

- token endpoint: `https://auth.openai.com/oauth/token`
- client_id: `app_EMoamEEZ73f0CkXaXp7hrann`  ← confirmed in JWT `client_id`
- id_token claims under `https://api.openai.com/auth`:
  `chatgpt_account_id`, `chatgpt_account_user_id`, `chatgpt_plan_type` (here `pro`),
  `chatgpt_user_id`, `chatgpt_compute_residency`. `aud: ["https://api.openai.com/v1"]`,
  `iss: https://auth.openai.com`, scopes include `offline_access`.
- (The OAuth PKCE login round-trip was outside the capture buffer; not re-captured here.)

## `/backend-api/codex/responses` — WS handshake headers (`rows/01`)

Request (header casing/order preserved from capture):
```
Host: chatgpt.com
Connection: Upgrade
Upgrade: websocket
Sec-WebSocket-Version: 13
chatgpt-account-id: <account_uuid>
authorization: Bearer <id-or-access token>
user-agent: codex-tui/0.135.0 (Arch Linux Rolling Release; x86_64) Konsole/260401 (codex-tui; 0.135.0)
originator: codex-tui
openai-beta: responses_websockets=2026-02-06
version: 0.135.0
x-codex-beta-features: terminal_resize_reflow
x-codex-turn-metadata: {"session_id","thread_id","thread_source":"user","turn_id":"","workspaces":{<cwd>:{associated_remote_urls,latest_git_commit_hash,has_changes}},"sandbox":"seccomp","request_kind":"prewarm","window_id":"<session>:0"}
x-client-request-id: <session uuidv7>
session-id: <session uuidv7>
thread-id: <session uuidv7>   (== session-id for a new thread)
x-codex-window-id: <session uuidv7>:0
```
- `session-id`/`thread-id`/`x-client-request-id`/`x-codex-window-id` are all derived from one
  **UUIDv7** generated per CLI session (`019e7740-…` form). For a brand-new thread, thread-id == session-id.
- response: `101 Switching Protocols`, `sec-websocket-extensions: permessage-deflate`,
  plus `x-models-etag`, `x-openai-proxy-wasm: v0.1` (OpenAI's edge marker).

## `GET /backend-api/wham/usage` — usage/rate-limit (`rows/02`)

The CLI calls this with **only** `authorization: Bearer`, `chatgpt-account-id`, `cookie`, the
codex-tui `user-agent`, `accept: */*`. **No** `oai-client-version` / `oai-client-build-number` /
`x-openai-target-*` headers (those belong to the *web portal* variant, which is what the
user-supplied browser curl shows — do not mimic the web headers from the CLI path).

Response 200 JSON shape:
```jsonc
{
  "user_id","account_id","email","plan_type":"pro",
  "rate_limit": {
    "allowed":true,"limit_reached":false,
    "primary_window":  {"used_percent",  "limit_window_seconds":18000, "reset_after_seconds", "reset_at"},
    "secondary_window":{"used_percent",  "limit_window_seconds":604800,"reset_after_seconds", "reset_at"}
  },
  "code_review_rate_limit": null,
  "additional_rate_limits": [ {"limit_name":"GPT-5.3-Codex-Spark","metered_feature":"codex_bengalfox","rate_limit":{…same window shape…}} ],
  "credits": {"has_credits","unlimited","overage_limit_reached","balance","approx_local_messages":[n,n],"approx_cloud_messages":[n,n]},
  "spend_control": {"reached","individual_limit"},
  "rate_limit_reached_type": null,
  "promo": null, "referral_beacon": null,
  "rate_limit_reset_credits": {"available_count":0}
}
```
New vs cc-core's current `CodexUsageInfo`: `additional_rate_limits[]`, `code_review_rate_limit`,
`rate_limit_reset_credits`, `promo`, `referral_beacon`, `spend_control.individual_limit`.

## `POST /backend-api/codex/analytics-events/events` — telemetry (`rows/03`)

`{"events":[{"event_type":"codex_turn_event","event_params":{…}}]}`. Notable params:
`app_server_client:{product_client_id:"codex-tui",client_name:"codex-tui",client_version:"0.135.0",rpc_transport:"in_process",experimental_api_enabled:true}`,
`runtime:{codex_rs_version:"0.135.0",runtime_os:"linux",runtime_os_version:"Rolling Release",runtime_arch:"x86_64"}`,
`model:"gpt-5.5"`, `model_provider:"openai"`, `reasoning_effort:"high"`, `service_tier:"default"`,
`approval_policy:"on-request"`, `sandbox_policy:"workspace_write"`, `personality:"pragmatic"`,
plus token counters (`input_tokens`,`cached_input_tokens`,`output_tokens`,`reasoning_output_tokens`,`total_tokens`).

## Other CLI background traffic (not needed for proxy parity)

- `POST /backend-api/wham/apps` — MCP-style `{"jsonrpc":"2.0","method":"notifications/initialized"}` (`rows/04`).
- `GET /backend-api/ps/plugins/installed?scope=GLOBAL|WORKSPACE[&includeDownloadUrls=true]` (`rows/05`).
- `POST https://ab.chatgpt.com/otlp/v1/metrics` — OTLP metrics.

## Models seen

`gpt-5.5` (default this session), `gpt-5.3-codex-spark` (metered feature `codex_bengalfox`).
Matches cc-core `CodexModelCatalog` Pro tier. No new model names beyond the catalog.
