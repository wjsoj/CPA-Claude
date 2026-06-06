# Claude Code 2.1.167 — fingerprint ground truth

Captured 2026-06-06 from a live `claude-cli/2.1.167` OAuth session (whistle dump,
steady-state working session — bootstrap burst + chat + count_tokens + telemetry).
This is the authoritative reference for the fingerprint constants in
`cc-core/{mimicry,sidecar,auth}` and the vendored copies in
`hypitoken/internal/server/{fingerprint,mimicry,sidecar}.go`.

Client env (from the `event_logging` / datadog telemetry bodies):

```
version / version_base = 2.1.167
build_time             = 2026-06-05T23:07:45Z   <- changed vs 2.1.158
node_version           = v24.3.0      arch = x64        platform = linux
sdk (@anthropic-ai)    = 0.94.0
```

The previous target was **2.1.158**. The chat fingerprint is almost entirely
unchanged across the bump (14-item beta list, system 4-block layout, metadata shape,
billing-header format, datadog key/UA, count_tokens beta all identical). The deltas
are the version string, `build_time`, the **quota-probe beta list** (gained one item),
and two new **passthrough** body fields real CC now sends that we deliberately do not
inject — see §0.

> **This capture is steady-state — it does NOT include a fresh OAuth login.** The
> login flow (`oauth/hello`, token exchange, profile, roles, referral) was last
> captured at 2.1.158 and is **unchanged** (axios UA already verified `axios/1.15.2`
> on every axios call in this capture's sidecar burst). The 2.1.158 login rows +
> §7-style login documentation live in git history; reproduce with a fresh
> `claude login` capture when the login path is the focus.

---

## 0. 2.1.158 → 2.1.167 diff (the entire change set)

| # | where | change |
|---|---|---|
| 1 | `CLICurrentVersion` / UA | `2.1.158` → **`2.1.167`** (UA `claude-cli/2.1.167 (external, cli)`) |
| 2 | sidecar env `build_time` | `2026-05-29T23:26:17Z` → **`2026-06-05T23:07:45Z`** |
| 3 | **quota-probe `anthropic-beta`** | gained `thinking-token-count-2026-05-13` after `redact-thinking-2026-02-12` (5 → 6 items) — see §8 |
| 4 | sidecar `linux_kernel` (host profile) | `7.0.10-arch1-1` → `7.0.11-arch1-1` (capture-host kernel; cosmetic host-profile sync, not a CC signal) |
| 5 | **new passthrough body field** `diagnostics` | real CC sends `{"previous_message_id": <id|null>}` top-level on non-Haiku `/v1/messages` — observed-only, NOT injected (§2) |
| 6 | **new passthrough on Haiku title-gen** | the Haiku auto-title request now sends `anthropic-beta: …,structured-outputs-2025-12-15,…` + `output_config.format = json_schema` — observed-only, Haiku path is not mimicked (§2.1) |

`axios/1.15.2` (OAuth token-exchange/refresh + penguin/mcp/downloads UA) is
**unchanged** — re-confirmed on every axios call in this capture. **Everything below
§0 is unchanged from 2.1.158 unless a line is flagged.**

---

## 1. `/v1/messages?beta=true` — request headers (OAuth chat path)

| header | value |
|---|---|
| `user-agent` | `claude-cli/2.1.167 (external, cli)` |
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

## 2. `/v1/messages` — request **body** shape

Top-level keys real CC 2.1.167 sends on a non-Haiku request (verified against the
154 KB chat capture, `rows/05-v1_messages.json`):

```
model, messages[], system[4], tools[], metadata{user_id}, max_tokens,
thinking{type:"adaptive"}, context_management{edits:[…]},
output_config{effort:…}, diagnostics{previous_message_id}, stream
```

`diagnostics` is **NEW vs 2.1.158** (`{"previous_message_id": null}` on the first turn,
the prior message id on follow-ups). We deliberately **do not** inject `thinking` /
`output_config` / `context_management` / `diagnostics`. The first three are beta-gated
and alter response semantics for non-CC clients; `diagnostics` is semantically neutral
but we still omit it — there is nothing meaningful to set `previous_message_id` to, and
omission keeps the injection surface minimal. (See the long NOTE in `mimicry/body.go`.)

### system block layout (4 blocks) — UNCHANGED

```
[0] text  cc=none            "x-anthropic-billing-header: cc_version=2.1.167.<3hex>; cc_entrypoint=cli; cch=<5hex>;"
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

### 2.1 Haiku auto-title request — observed, NOT mimicked

The internal "generate a concise title" call (`model: claude-haiku-4-5-…`,
`max_tokens: 32000`) now carries:

- `anthropic-beta`: `…,advisor-tool-2026-03-01,structured-outputs-2025-12-15,cache-diagnosis-2026-04-07`
  (`structured-outputs-2025-12-15` is the new item)
- `thinking: {"type":"disabled"}`
- `output_config.format`: a `json_schema` object (`{title:string}`)
- system: 3 blocks (billing, CC intro, the title-gen instruction), no `cache_control`

Anthropic does NOT third-party-check Haiku, so the body layer is **skipped entirely
for Haiku models** — none of this affects our constants. Recorded for completeness.

---

## 3. `/v1/messages/count_tokens?beta=true` — UNCHANGED

- `user-agent`: `claude-cli/2.1.167 (external, cli)`
- `anthropic-beta`: `claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14,context-management-2025-06-27,token-counting-2024-11-01`
- body keys: `{model, messages, tools}` — no system, no metadata.

Forwarded by the proxy, not synthesized.

---

## 4. Telemetry: `POST /api/event_logging/v2/batch` — UNCHANGED (except version/build_time)

Headers: `user-agent: claude-code/2.1.167`, `anthropic-beta: oauth-2025-04-20`,
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
(**re-confirmed unchanged in the 2.1.167 capture**), `connection: close`. Body is a
JSON array of flat events with `renderer_mode`, `feature_name`, `model: claude-opus-4-8`,
`build_time` (§6), `linux_distro_id` / `linux_kernel`. (Datadog heartbeat stays disabled
in our sidecar; constants aligned for correctness.)

---

## 6. `env` block (event_logging) — 2.1.167 contents

```
platform=linux  node_version=v24.3.0  terminal=konsole  shell=zsh
package_managers=npm,yarn,pnpm  runtimes=bun,deno,node  is_running_with_bun=true
is_ci=false  is_claubbit=false  is_github_action=false  is_claude_code_action=false
is_claude_ai_auth=true  is_claude_code_remote=false  is_conductor=false
is_local_agent_mode=false  arch=x64  platform_raw=linux  vcs=git
deployment_environment=unknown-linux
version=2.1.167  version_base=2.1.167  build_time=2026-06-05T23:07:45Z   <- build_time changed
linux_distro_id=arch
linux_kernel=7.0.11-arch1-1   <- host kernel (cosmetic profile value)
```

Only `build_time` (and the capture-host `linux_kernel`) changed vs 2.1.158.

---

## 7. OAuth login flow — NOT re-captured this round

This capture is a steady-state working session with no `claude login`. The complete
PKCE login + startup chain (UA matrix, token-exchange request/response shapes,
`/api/oauth/profile` shape) was documented at 2.1.158 and is **unchanged**:

- Every axios call in this capture's sidecar burst (penguin, mcp_servers, downloads)
  sends `axios/1.15.2` — the same UA the token-exchange/refresh path uses, confirming
  `cc-core/auth` (`anthropicOAuthUA = "axios/1.15.2"`) is still correct.
- `client_id = 9d1c250a-e61b-44d9-88ed-5944d1962f5e` (public Claude Code app UUID) and
  the token-exchange param order (`grant_type, code, redirect_uri, client_id,
  code_verifier, state`) are unchanged in `cc-core/auth`.

For the full login documentation see the 2.1.158 SPEC in git history (rows
`01-oauth_hello` … `05-oauth_referral`). Re-capture with a fresh login when the
login path is the focus.

---

## 8. Sidecar bootstrap — UNCHANGED structure, quota-probe beta gained one item

The bootstrap burst fires on first touch with the captured UA matrix
(`rows/01-04`, plus count_tokens / event_logging / datadog / releases):

| endpoint | UA | anthropic-beta |
|---|---|---|
| `api/eval/sdk-…` (GrowthBook) | `Bun/1.3.14` | `oauth-2025-04-20` |
| `oauth/account/settings` | `claude-cli/2.1.167 (external, cli)` | `oauth-2025-04-20` |
| `claude_code_grove` | `claude-cli/2.1.167 (external, cli)` | `oauth-2025-04-20` |
| `claude_cli/bootstrap` | `claude-code/2.1.167` | `oauth-2025-04-20` |
| `claude_code_penguin_mode` | `axios/1.15.2` | `oauth-2025-04-20` |
| quota probe (`/v1/messages`, Haiku "quota") | `claude-cli/2.1.167 (external, cli)` | **6-item list (below)** |
| `mcp-registry/v0/servers` | `claude-cli/2.1.167 (external, cli)` | (none) |
| `v1/mcp_servers` | `axios/1.15.2` | `mcp-servers-2025-12-04` |
| `v1/code/triggers` | `claude-cli/2.1.167 (external, cli)` | `ccr-triggers-2026-01-30` |
| `downloads.claude.ai/.../latest` | `axios/1.15.2` | (none) — returns `2.1.167` plain text |

### quota-probe `anthropic-beta` — CHANGED (5 → 6 items)

```
oauth-2025-04-20,interleaved-thinking-2025-05-14,redact-thinking-2026-02-12,thinking-token-count-2026-05-13,context-management-2025-06-27,prompt-caching-scope-2026-01-05
```

`thinking-token-count-2026-05-13` was inserted after `redact-thinking-2026-02-12`.
→ `sidecar.quotaProbeBeta` (cc-core) / `quotaProbeBeta` (hypitoken). Quota probe body
unchanged: `{model: claude-haiku-4-5-20251001, max_tokens: 1, messages:[{role:user,
content:"quota"}]}`.

(`v1/code/triggers` was already added at CC 2.1.141 and is present in our sidecar.)

---

## Edit checklist (apply to cc-core AND hypitoken's vendored copies)

| # | where | change |
|---|---|---|
| 1 | `mimicry/fingerprint.go` (cc-core) / `internal/server/fingerprint.go` (hypitoken): `CLICurrentVersion` + UA | `2.1.158` → `2.1.167` |
| 2 | `sidecar/sidecar.go` (cc-core) / `internal/server/sidecar.go` (hypitoken): env `build_time` | `2026-05-29T23:26:17Z` → `2026-06-05T23:07:45Z` |
| 3 | `sidecar/sidecar.go` (cc-core) / `internal/server/sidecar.go` (hypitoken): `quotaProbeBeta` | insert `thinking-token-count-2026-05-13` after `redact-thinking-2026-02-12` (5 → 6 items) |
| 4 | `sidecar/sidecar.go` (both): `linux_kernel` host profile | `7.0.10-arch1-1` → `7.0.11-arch1-1` (cosmetic) |
| 5 | comments naming the target | `2.1.156` / `2.1.158` → `2.1.167` |

`cc-core/auth` (axios UA, token endpoint/params) is **unchanged** — hypitoken, which
imports `cc-core/auth` but vendors its own `mimicry`/`sidecar`, needs **no cc-core
version bump for this round**, only the vendored-copy edits above. CPA-Claude consumes
`cc-core/mimicry` + `cc-core/sidecar` and so does need the cc-core version bump in its
`go.mod`.

Nothing else changed: full beta list, system layout, metadata, billing-header format,
datadog key/UA, count_tokens beta, env distro all verified identical.
