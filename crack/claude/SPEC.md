# Claude Code 2.1.158 — fingerprint ground truth

Captured 2026-05-31 from a live `claude-cli/2.1.158` OAuth session (whistle dump,
58 requests) that **includes a full fresh OAuth login** (PKCE token-exchange +
post-login startup chain). This is the authoritative reference for the fingerprint
constants in `cc-core/{mimicry,sidecar,auth}` and the vendored copies in
`hypitoken/internal/server/{fingerprint,mimicry,sidecar}.go`.

Client env (from the `event_logging` / datadog telemetry bodies):

```
version / version_base = 2.1.158
build_time             = 2026-05-29T23:26:17Z   <- changed vs 2.1.156
node_version           = v24.3.0      arch = x64        platform = linux
sdk (@anthropic-ai)    = 0.94.0
```

The previous target was **2.1.156**. The chat/telemetry fingerprint is byte-for-byte
unchanged across the bump (14-item beta list, system 4-block layout, metadata shape,
datadog key/UA, env distro/kernel all identical). The only deltas are the version
string, `build_time`, and a previously-stale OAuth User-Agent — see §0.

---

## 0. 2.1.156 → 2.1.158 diff (the entire change set)

| # | where | change |
|---|---|---|
| 1 | `CLICurrentVersion` / UA | `2.1.156` → **`2.1.158`** (UA `claude-cli/2.1.158 (external, cli)`) |
| 2 | sidecar env `build_time` | `2026-05-28T18:30:33Z` → **`2026-05-29T23:26:17Z`** |
| 3 | **OAuth token-exchange + refresh UA** | code was **`axios/1.13.6`**, real CC sends **`axios/1.15.2`** — see §7 |

Item 3 is not strictly a 2.1.158 change — it was already wrong at 2.1.156 (the
sidecar was bumped to `axios/1.15.2` but the OAuth path in `cc-core/auth/oauth.go`
was missed). The 2.1.158 capture confirms `axios/1.15.2` on every axios call, so it
is folded into this update. **Everything below §0 is unchanged from 2.1.156 unless a
line is flagged.**

---

## 1. `/v1/messages?beta=true` — request headers (OAuth chat path)

| header | value |
|---|---|
| `user-agent` | `claude-cli/2.1.158 (external, cli)` |
| `x-stainless-arch` | `x64` |
| `x-stainless-lang` | `js` |
| `x-stainless-os` | `Linux` |
| `x-stainless-package-version` | `0.94.0` |
| `x-stainless-retry-count` | `0` |
| `x-stainless-runtime` | `node` |
| `x-stainless-runtime-version` | `v24.3.0` |
| `x-stainless-timeout` | `600` |
| `anthropic-version` | `2023-06-01` |
| `anthropic-dangerous-direct-browser-access` | `true` |
| `x-app` | `cli` |
| `anthropic-beta` | 14-item list (below) |

### `anthropic-beta` request header (FULL — 14 items, exact order) — UNCHANGED

```
claude-code-20250219,oauth-2025-04-20,context-1m-2025-08-07,interleaved-thinking-2025-05-14,redact-thinking-2026-02-12,thinking-token-count-2026-05-13,context-management-2025-06-27,prompt-caching-scope-2026-01-05,mid-conversation-system-2026-04-07,advisor-tool-2026-03-01,advanced-tool-use-2025-11-20,effort-2025-11-24,extended-cache-ttl-2025-04-11,cache-diagnosis-2026-04-07
```

→ `mimicry.ClaudeAnthropicBetaFull` (cc-core) / `claudeAnthropicBetaFull` (hypitoken).

---

## 2. `/v1/messages` — request **body** shape — UNCHANGED

Top-level keys real CC 2.1.158 sends (verified against the full 289 KB capture):

```
model, messages[], system[4], tools[], metadata{user_id}, max_tokens,
thinking{type:"adaptive"}, context_management{edits:[…]},
output_config{effort:"high"}, stream
```

We deliberately **do not** inject `thinking` / `output_config` /
`context_management` — beta-gated, alters response semantics for non-CC clients.

### system block layout (4 blocks) — UNCHANGED

```
[0] text  cc=none            "x-anthropic-billing-header: cc_version=2.1.158.<3hex>; cc_entrypoint=cli; cch=<5hex>;"
[1] text  cc=none            "You are Claude Code, Anthropic's official CLI for Claude."
[2] text  cc=ephemeral 1h scope:global    <- second-to-last
[3] text  cc=ephemeral 1h    (no scope)    <- last
```

Verified: `system_cache_pattern = [null, null, true, false]` (scope:global on the
second-to-last block, plain ephemeral 1h on the last). Last content block of the
last message also carries `cache_control: ephemeral 1h` (no scope).

### `metadata.user_id` (JSON string) — UNCHANGED

```json
{"device_id":"<sha256 hex>","account_uuid":"<uuid>","session_id":"<uuid>"}
```

---

## 3. `/v1/messages/count_tokens?beta=true`

Not present in this (mid-login) capture; the path is unchanged from 2.1.156. Forwarded
by the proxy, not synthesized.
- `user-agent`: `claude-cli/2.1.158 (external, cli)`
- `anthropic-beta`: `claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14,context-management-2025-06-27,token-counting-2024-11-01`
- body keys: `{model, messages, tools}` — no system, no metadata.

---

## 4. Telemetry: `POST /api/event_logging/v2/batch` — UNCHANGED (except version/build_time)

Headers: `user-agent: claude-code/2.1.158`, `anthropic-beta: oauth-2025-04-20`,
`x-service-name: claude-code`, `connection: close`.

Per-event `event_data`: `model: claude-opus-4-8[1m]`, `env` (§6), `auth`,
`process`/`additional_metadata` base64 blobs.

### `betas` field reported in telemetry — VARIABLE, not a single constant

The telemetry `betas` field is **contextual** — it reflects the betas active for the
event being logged, so the capture contains several variants (8-, 9-, and 14-item).
The 9-item variant matches `mimicry.ClaudeReportedBetas`:

```
claude-code-20250219,oauth-2025-04-20,context-1m-2025-08-07,interleaved-thinking-2025-05-14,redact-thinking-2026-02-12,thinking-token-count-2026-05-13,context-management-2025-06-27,prompt-caching-scope-2026-01-05,mid-conversation-system-2026-04-07
```

Our heartbeat emits one fixed list per tick — a documented, low-risk simplification
(the field is not a billing signal). **No change.**

---

## 5. Telemetry: `POST https://http-intake.logs.us5.datadoghq.com/api/v2/logs` — UNCHANGED

Headers: `user-agent: axios/1.15.2`, `dd-api-key: pubea5604404508cdd34afb69e6f42a05bc`
(re-confirmed global), `connection: close`. Body is a JSON array of flat events with
`renderer_mode`, `feature_name`, `model: claude-opus-4-8`, `build_time` (§6),
`linux_distro_id` / `linux_kernel`. (Datadog heartbeat stays disabled in our sidecar;
constants aligned for correctness.)

---

## 6. `env` block (event_logging) — 2.1.158 contents

```
platform=linux  node_version=v24.3.0  terminal=konsole  shell=zsh
package_managers=npm,yarn,pnpm  runtimes=bun,deno,node  is_running_with_bun=true
is_ci=false  is_claubbit=false  is_github_action=false  is_claude_code_action=false
is_claude_ai_auth=true  is_claude_code_remote=false  is_conductor=false
is_local_agent_mode=false  arch=x64  platform_raw=linux  vcs=git
deployment_environment=unknown-linux
version=2.1.158  version_base=2.1.158  build_time=2026-05-29T23:26:17Z   <- build_time changed
linux_distro_id=arch
linux_kernel=7.0.10-arch1-1
```

Only `build_time` changed vs 2.1.156. distro/kernel pinned profile unchanged.

---

## 7. OAuth login flow — NEW (full PKCE capture, the focus of this round)

This capture includes a fresh `claude login`. Rows `01-oauth_hello` … `09-startup_penguin`
plus `14-releases` document the complete login + startup chain. All secret values
(code, code_verifier, state, access/refresh tokens, account/org UUID, email, device
hash) are masked in `rows/`; the public Claude Code client_id and every fingerprint
value are kept verbatim.

### 7.1 Per-call User-Agent matrix (the key fingerprint)

Real CC mixes **four** client identities across the login + startup chain:

| call | endpoint | User-Agent | notes |
|---|---|---|---|
| `oauth/hello` | `platform.claude.com/v1/oauth/hello` | `claude-cli/2.1.158 (external, cli)` | unauth probe |
| `api/hello` | `api.anthropic.com/api/hello` | `claude-cli/2.1.158 (external, cli)` | unauth probe |
| **token exchange** | `platform.claude.com/v1/oauth/token` | **`axios/1.15.2`** | POST, JSON body |
| `oauth/profile` | `api.anthropic.com/api/oauth/profile` | `axios/1.15.2` | Bearer |
| `claude_cli/roles` | `api.anthropic.com/api/oauth/claude_cli/roles` | `axios/1.15.2` | Bearer |
| `penguin_mode` | `api.anthropic.com/api/claude_code_penguin_mode` | `axios/1.15.2` | Bearer + `anthropic-beta: oauth-2025-04-20` |
| `eval/sdk-…` | `api.anthropic.com/api/eval/sdk-<id>` | `Bun/1.3.14` | POST, `anthropic-beta: oauth-2025-04-20` |
| `claude_code_grove` | `api.anthropic.com/api/claude_code_grove` | `claude-cli/2.1.158 (external, cli)` | Bearer + `anthropic-beta: oauth-2025-04-20` |
| `claude_cli/bootstrap` | `api.anthropic.com/api/claude_cli/bootstrap` | `claude-code/2.1.158` | Bearer |
| `referral/eligibility` | `api.anthropic.com/api/oauth/organizations/<org>/referral/eligibility` | `claude-cli/2.1.158 (external, cli)` | + `anthropic-client-platform: claude_code_cli`, `anthropic-version: 2023-06-01`, `x-organization-uuid: <org>` |

All authenticated calls send `Accept: application/json, text/plain, */*` and
`Accept-Encoding: gzip, br`, `Connection: close`.

### 7.2 Token-exchange request (`rows/02-oauth_token`)

`POST https://platform.claude.com/v1/oauth/token`, `Content-Type: application/json`,
`User-Agent: axios/1.15.2`. JSON body, **insertion order fixed**:

```
grant_type=authorization_code, code, redirect_uri, client_id, code_verifier, state
```

- `client_id = 9d1c250a-e61b-44d9-88ed-5944d1962f5e` (public Claude Code app UUID;
  matches `application.uuid` from `/api/oauth/profile`).
- `redirect_uri` is a random loopback the CLI picks at authorize time
  (`http://localhost:36771/callback` here) — our server pins its own admin port.

### 7.3 Token-exchange response

```jsonc
{
  "token_type": "Bearer",
  "access_token": "sk-ant-oat01-…",
  "expires_in": 28800,
  "refresh_token": "sk-ant-ort01-…",
  "scope": "user:file_upload user:inference user:mcp_servers user:profile user:sessions:claude_code",
  "token_uuid": "<uuid>",
  "organization": {"uuid": "<uuid>", "name": "<…>'s Organization"},
  "account":      {"uuid": "<uuid>", "email_address": "<…>"}
}
```

The `account.uuid` + `organization.uuid` come back **directly from the exchange** —
no separate profile call is needed to anchor identity (our `finishAnthropicLogin`
already reads them from here). A Max account's granted `scope` drops
`org:create_api_key` from the requested set.

### 7.4 `/api/oauth/profile` response shape (`rows/03`)

```
account:      {uuid, full_name, display_name, email, has_claude_max, has_claude_pro, created_at}
organization: {uuid, name, organization_type, billing_type, rate_limit_tier, seat_tier,
               has_extra_usage_enabled, subscription_status, subscription_created_at,
               cc_onboarding_flags, claude_code_trial_ends_at, claude_code_trial_duration_days,
               payment_auth_hosted_invoice_url}
application:  {uuid: 9d1c250a-…, name: "Claude Code", slug: "claude-code"}
```

(`has_claude_max:true`, `organization_type:claude_max`, `rate_limit_tier:default_claude_max_20x`,
`billing_type:google_play_subscription` for the captured Max account.)

---

## 8. Sidecar bootstrap — UNCHANGED (except version)

bootstrap/grove/penguin/eval all fire on first touch with the §7.1 UA matrix.
`downloads.claude.ai/claude-code-releases/latest` returns `2.1.158` (plain text).

---

## Edit checklist (apply to cc-core AND hypitoken's vendored copies)

| # | where | change |
|---|---|---|
| 1 | `mimicry/fingerprint.go` (cc-core) / `internal/server/fingerprint.go` (hypitoken): `CLICurrentVersion` + UA | `2.1.156` → `2.1.158` |
| 2 | `sidecar/sidecar.go` (cc-core) / `internal/server/sidecar.go` (hypitoken): env `build_time` | `2026-05-28T18:30:33Z` → `2026-05-29T23:26:17Z` |
| 3 | **`cc-core/auth/oauth.go`: `anthropicOAuthUA`** | `axios/1.13.6` → **`axios/1.15.2`** (shared by both downstreams via `cc-core/auth`) |
| 4 | comments naming the target | `2.1.146` / `2.1.156` → `2.1.158` |

Nothing else changed: beta lists, system layout, metadata, datadog key/UA, env
distro/kernel, token endpoint/params all verified identical.
