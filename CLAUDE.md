# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this repo is

CPA-Claude is a Go reverse-proxy that fans client requests across multiple upstream Anthropic / OpenAI credentials (OAuth + API keys) on **two independent HTTP endpoints** — Claude on `:8317` (`/v1/messages`), Codex on `:8318` (`/v1/chat/completions`, `/v1/responses`). Pool, client tokens, usage ledger, pricing, request log, and admin panel are shared; the credential subset and the body shaping differ per provider.

The reusable proxy core (credential pool + health, usage ledger, pricing, client tokens, request log, rate limiting, the CC mimicry/sidecar fingerprint, advisor sub-usage, stream decompression/relay, thinking-signature handling, Anthropic OAuth login/refresh, Codex JWT) lives in the external module **`github.com/wjsoj/cc-core`**. This repo is the leaner of two sibling forks that consume it — **hypitoken (`/home/wjs/Documents/project/Go/hypitoken`, Go module also named `CPA-Claude`) is the other**. Fingerprint/mimicry/sidecar/auth changes land in cc-core only; then both forks bump the dependency.

**This fork does have the SaaS/billing layer** (`internal/saas/` — wallet, orders, invoices, workspaces, Z-Pay, Resend inbound), gated behind `saas.enabled` (default `false`). hypitoken's actual delta is `shop/` + `webasset/` (+ Kiro), not billing. Do not assume a wallet/quota feature is missing here.

There is a public GitHub Wiki with the long-form version of everything below: <https://github.com/wjsoj/CPA-Claude/wiki>. When you change an architectural invariant, the wiki page for that subsystem is the other place it is written down.

Derivative of [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) (MIT). The Anthropic OAuth refresh, Codex JWT parsing, and uTLS Chrome transport were lifted from upstream and now live in cc-core.

## The cc-core boundary (read this first)

`internal/` contains almost no credential/usage/pricing/fingerprint logic — those are cc-core packages imported and wired here. There is **no `internal/auth/` directory**: when a path below says `auth.Pool` / `auth.Auth` / `usage.Store` / `pricing.Catalog` / `clienttoken.Store` / `requestlog.Writer` / `mimicry.*` / `sidecar.Manager` / `advisor.SubUsage` / `ccstream.*`, the **type lives in cc-core**, not this repo. The Anthropic login flows (`finishAnthropicLogin`, `buildAnthropicAuthURL`, session-cookie login) are `cc-core/auth/{login,login_session}.go`; `proxy.go` only adapts this repo's request context to cc-core's API.

cc-core packages (as of v0.8.66): `advisor auth backup clientguard clienttoken codexws crack docs mimicry pricing ratelimit requestlog sidecar stream thinkingsig usage`.

**Files that used to be here and no longer are** — don't go looking for them: `internal/server/sidecar.go`, `internal/server/sidecar_test.go`, `internal/server/mimicry.go`, the local `maybeDecompressResponse`, and the local `subUsage` type. They are `cc-core/sidecar`, `cc-core/mimicry/identity.go`, `ccstream.Decompress`, and `advisor.SubUsage` respectively.

## Build & run

```bash
make build              # build admin SPA (bun) + Go binary into bin/cpa-claude
make web-dev            # Vite dev server with API proxy to :8317 (frontend hot reload)
make tidy               # go mod tidy
go build ./...          # Go-only build (skips SPA; admin panel falls back to embedded /dist)
go test ./...           # all tests (21 test files, all under internal/)
go test ./internal/server/... -timeout 60s -v -run TestCodexWS   # single test/group
```

**CI does not run `go test`.** `.github/workflows/ci.yml` runs `make web` → `go build ./...` → `go vet ./...` only, so a broken test lands green. Run the suite locally before tagging. CI also pins Go 1.24 while `go.mod` declares 1.26.2 — it works via toolchain auto-download, but the pin is worth bumping.

The admin SPA at `internal/admin/web/` (React 18 + Vite + Tailwind, managed with **bun, not npm**) is built into `internal/admin/web/dist/` and embedded via `//go:embed` in `internal/admin/admin.go`. The `//go:generate` directive there runs `bun install --frozen-lockfile && bun run build`. CI (`.github/workflows/ci.yml`) calls `make web` before `go build`, so SPA is mandatory in releases.

`make build` requires `bun` on PATH. Plain `go build ./...` works without bun if `internal/admin/web/dist/` already contains a build (or the embedded asset can be empty for backend-only iteration).

## Architecture (the parts that span files)

### Endpoint × provider matrix

`internal/server/server.go` constructs **N gin engines, one per enabled endpoint**. Each engine is bound to one provider (`auth.ProviderAnthropic` or `auth.ProviderOpenAI`) and serves only the routes that make sense for it. The "primary" endpoint (Claude if enabled, else Codex) additionally hosts the admin panel + public `/status`.

The shared pieces (`auth.Pool`, `usage.Store`, `clienttoken.Store`, `pricing.Catalog`, `requestlog.Writer`) live on `Server` and are injected into both engines. The split-by-engine matters because: per-provider stickiness, per-provider concurrency budgets, and per-provider RPM limits all key on `(provider | clientToken)` — Claude saturation must NOT block a client's Codex traffic.

### Credential pool & sticky sessions

`cc-core/auth/pool.go` is the credential scheduler. `Pool.Acquire(ctx, provider, clientToken, group, model, exclude...)` picks an OAuth credential by:

1. Sticky reuse — if `clientToken` already has a healthy assignment for this provider, return it (plus a sticky-to-own-group upgrade check, so a client stuck on the shared tier migrates back to its own group when one frees up).
2. `pickOAuthLocked` (`auth/pool.go:550`) over the healthy OAuth in an allowed group. **Spare `max_concurrent` is only a filter, not the ranking.** Candidates are ranked by **lowest cost-weighted rolling usage** (`usageLoad` → `usage.Counts.WeightedTotal`, ~5h window), tie-breaking on credential ID. This deliberately spreads load toward credentials doing less *real* work — a cache-heavy client racking up near-free `cache_read` volume must not starve a credential out.
3. API-key fallback when every OAuth is saturated/quota-exceeded/dead.

**Group matching is tiered, not an exact match.** Tier 1 is the client's own named group (unless that group is `new`); tier 2 is the shared tier `{"new", ""}`. The `new` group additionally carries a 10-idle-hours-per-day schedule (`auth/schedule.go`).

A "session" in pool semantics is one `(provider, client_token, sessionID)` slot observed within `ActiveWindow` (default 5 min, `active_window_minutes`), where `sessionID` is the client's per-window identifier — `clientSlotID` in `proxy.go` reads it from `X-Claude-Code-Session-Id` (Claude Code) or `Session_id` (Codex CLI), falling back to `""` (one slot per token) for raw API callers. This means one user opening N CLI windows presents N independent sessions and can be load-balanced across N different credentials. Per-token RPM and concurrency caps deliberately stay keyed on the token alone, so opening more windows doesn't multiply a client's rate budget. `Pool.Acquire`/`Release`/`Unstick` all take the `sessionID` and must be passed the same value across the request. `Pool.Release` is called once per request to keep the active counter accurate. `Pool.Unstick` breaks the assignment when an upstream error suggests the credential is bad. `Pool.ReportUpstreamError` translates 401/403/429 into the right combination of cooldown / hard-failure / stealth-ban detection — this logic is shared by both Anthropic and Codex paths and changes here ripple everywhere.

Health states are kept on `auth.Auth` itself: `MarkSuccess` / `MarkFailure` / `MarkHardFailure` / `MarkRateLimited` / `MarkUsageLimitReached` / `MarkClientCancel`. `MarkFailure` bumps `ConsecutiveFailures`; **5 consecutive** (`hardFailureThreshold`) auto-hard-fail an OAuth credential. Other thresholds in the same family: `rateLimit429HardFailureThreshold = 15`, `auth401HardFailureThreshold = 8`, `healthGrace = 2m`. Hard failures are sticky (manual clear from admin) except for one daily reset job (`Pool.RunDailyAnthropicAPIKeyReset`) that wipes API-key hard-failures so a transient overnight outage doesn't pin them dead forever.

**API keys are not simply exempt from retirement.** They never auto-*hard*-fail — that part is still true — but they do have a **quarantine circuit breaker** (`auth/pool.go:338`, `auth/quarantine_test.go`): 3 consecutive failures, or **1** definitive `MarkHardFailure`, pauses the channel on a 10s/30s/2m/5m/15m ±20% jitter ladder. Quarantine must never set the sticky hard-failure flag on an API key — that invariant is asserted by test.

**Transient-vs-credential classification is load-bearing** (`cc-core/auth/retry.go` `IsTransientNetErr` + `transientErrFragments`). Wire-level flaps — `connection reset`, `PROTOCOL_ERROR`/`REFUSED_STREAM`, `http2: client conn not usable`/`no cached connection`/**`client connection lost`** — are retried on the *same* credential and must NOT call `MarkFailure`. A 2026-07 outage came from `http2: client connection lost` (a CF-edge-killed pooled h2 conn failing every in-flight stream at once) not being classified transient: bursts of `MarkFailure` crossed the hard-fail threshold and took the whole Codex pool dark. `IsHealthy` also has a **degraded self-recovery** rule (`degradedProbeAfter`, 5 min): a credential with `ConsecutiveFailures ≥ 2` is quarantined but re-probed after the interval, so a transient flap that degrades every credential can't be terminal (without it, unhealthy → never Acquired → never a success to reset the counter → permanently dark until manual clear).

### Anthropic forward path (`internal/server/proxy.go`)

`forward()` is the per-request loop: budget pre-check → RPM gate → concurrency gate → `forwardWithFailover` (up to `maxAttempts = 12` rounds on *different* credentials — a backstop, not a target; it normally stops the moment a healthy credential succeeds or all are excluded). Per attempt, `doForward` (OAuth) or `doForwardAnthropicAPIKey` (API key) actually talks to upstream. When every credential is exhausted, `poolUnavailable` composes the 503 body from the real reason (degraded / rate-limited / hard-failed / saturated) and sets `Retry-After` only when waiting will actually help.

The OAuth path applies **two layers of mimicry** to look like a real Claude Code **2.1.220** client (`mimicry.CLICurrentVersion`). Both layers now live in the **cc-core** module (`github.com/wjsoj/cc-core/mimicry`); `proxy.go` only adapts CPA-Claude's `*auth.Auth` to cc-core's API:

- **Header layer** — `applyAnthropicHeaders` in `proxy.go` is a thin adapter to `mimicry.ApplyClaudeCodeHeaders`, which sets pinned `User-Agent`, `X-Stainless-*`, `Anthropic-Beta`, `X-App`, `X-Claude-Code-Session-Id`, `X-Client-Request-Id`. Constants live in `cc-core/mimicry/fingerprint.go` (`CLICurrentVersion`, `ClaudeCLIUserAgent`, `ClaudeAnthropicBetaFull`, and the telemetry-only `ClaudeReportedBetas`). Whenever you bump the CC version target, **all of these need to move together** or the version in the User-Agent will disagree with the `cc_version=` baked into the body's billing block, which is itself a fingerprint signal.

- **Body layer** — `mimicry.ApplyClaudeCodeBodyMimicry` rewrites system into the canonical 4-block CC layout `[billing, "You are Claude Code...", ...originalSystem-with-cache_control]` (real CC puts `scope:global` on the second-to-last system block and a plain ephemeral 1h breakpoint on the last — some in-file comments still cite the older 2.1.201 capture for this layout), sets `metadata.user_id` to the JSON `{device_id, account_uuid, session_id}` shape, signs `cch=<xxhash5>` of the final body. The client's original prompt is preserved verbatim — only the surrounding wrapper is normalized. **Skipped entirely for Haiku models** (Anthropic doesn't third-party-check Haiku) and for requests whose system already starts with the CC prompt prefix (real CLI passing through).

`ccstream.Decompress` (cc-core `stream/decompress.go`) transparently un-gzips/un-brs upstream responses because we advertise `Accept-Encoding: gzip, br` to match real CC, but every internal path (usage parsing, SSE streamer, model rewrite) wants plain bytes. The old local `maybeDecompressResponse` is gone.

### SimIdentity — the per-account fingerprint anchor

`SimIdentity{ AccountKey, AccountUUID, ClientToken }` (cc-core `mimicry/identity.go`) is the central handle that ties together every identity-bearing field:

- `DeviceIDFor(AccountKey)` — sha256-anchored, **identical for all requests routed through the same OAuth account** (across credential file rotations, across multiple client tokens). Mimics the real CC `machine-id sha256` value.
- `SessionIDFor(id, body)` — derived from `(account, clientToken, sha256(first user message))` so multi-turn conversations keep one session_id but a new conversation rotates. Same function powers both the body's `metadata.user_id.session_id` and the `X-Claude-Code-Session-Id` header so they stay consistent.
- `AccountKey()` on `auth.Auth` falls back through `AccountUUID > Email > ID`. Existing credentials from before the `account_uuid` field was added still work via the email fallback; new logins capture the real UUID from the OAuth token-exchange response.

**Invariant:** for one OAuth account routed by N downstream client tokens, upstream sees one device with N concurrent CC sessions — exactly what one user opening multiple `claude` windows would look like. Don't change this without re-checking every place that derives identity.

### Advisor sub-call billing — `advisor.SubUsage` (cc-core `advisor/subusage.go`)

The `advisor-tool-2026-03-01` beta lets Anthropic run a stronger model (typically `claude-opus-4-7`) as a server-side sub-call inside one `/v1/messages` response. Wire shape: `message_delta.usage.iterations[]` with entries of `type:"message"` (outer model, already in top-level totals) or `type:"advisor_message"` (sub-call, NOT in top-level — has its own `model` field).

`advisor.SubUsage.ReplaceFrom` overwrites (not appends) on every observation because SSE emits cumulative iterations. `recordSubUsage` in `proxy.go` charges the sub-model tokens to the same auth that handled the parent, looks up pricing under the sub-model's own name, and emits a separate requestlog row per sub-model so the admin panel breaks orchestrator vs advisor cost apart. Parent request count (`Requests +1`) is unchanged — one `/v1/messages` is still one request regardless of how many sub-models ran.

In real captures advisor sub-calls always run cache-cold (cache_read/cache_create = 0); the four-counter parsing is kept in case Anthropic enables advisor caching later. (The original `advisor-tool-2026-03-01` round-trip capture lives in git history under the old `crack/advisor/`.)

### Admin panel reads go through the request-log index, not the JSONL

`cmd/server/main.go` calls `requestlog.OpenStore(cfg.LogDir)` (skip with `log_index_disabled: true`), which builds `requests.db` beside the rotated JSONL and makes every admin aggregate a SQL query. It must be opened **after** `requestlog.SetBucketLocation` — the index materializes day labels in the display zone (default `Asia/Shanghai`), and a zone change forces a rebuild — and **before** `requestlog.OpenWithOptions`, because the writer may depend on it (see below). The before-writer constraint is only *hard* when `log_jsonl_disabled` is set, where `OpenWithOptions` errors outright; with the archive on, an out-of-order writer merely drops its early batches to the scanner. Note also that `reconcileBucketLocation` **relabels `bday` in place** on a timezone change (cc-core ≥ v0.8.74) and rebuilds the cube from the relabelled rows. It used to hard-delete `req`/`agg_cube`/`ingest` and let the scanner rebuild, which was destructive under `log_jsonl_disabled` — if you are pinned to an older cc-core, do not change the display zone with the archive off.

The panel's cost is entirely in these aggregates, so the failure mode to watch for is a query that stops using the index and silently falls back to a full scan. Measured on production (980k records / 434MB): `/api/summary` was 15–18s and the pricing panel's unfiltered `/api/requests?limit=1` was 30s against a 15s cache TTL — a cache that could never be hit because the scan outlived it. Both are now tens of milliseconds.

**Where an aggregate comes from** — `agg_cube` (cc-core `requestlog/store.go`, migration 3) pre-sums every low-cardinality dimension at once: `(day, bday, model, client, client_token, provider, auth_id, status, user_id)`. On the production archive that is 10,382 rows standing in for 984k records, so *any* filter built from those columns is answered by grouping the cube. What still reads `req` row by row is what the cube can't express: sub-day `From`/`To` bounds (`cubeEligible` rejects any time bound rather than guessing which ones land on a day boundary), and the entry page itself. This replaced the earlier `agg_day`, which pre-summed one grouping at a time and therefore only helped *unfiltered* queries — `?model=claude-opus-4-7` still cost ~2.9s.

**Dual write** — the writer inserts each batch into `req` as it appends it (`store_write.go` `appendRows`), rather than waiting for the scanner to re-read the file. The two producers are made idempotent by `(src_file, src_off)`: the byte offset the line was written at, unique-indexed, so whichever of the two offers a line second is a no-op. That is what lets the archive be turned off — and what makes a crash safe while it is on, since anything the writer failed to insert is still re-read from the file. `Writer.curOff` resumes from the file's existing length on open; an offset restarting at zero would collide with rows a previous run already indexed.

**`log_jsonl_disabled: true`** stops writing `requests-*.jsonl` entirely and makes the index the only copy. It is off by default and mutually exclusive with `log_index_disabled` (config `Load` refuses the pair). `requests.db` **is** in the backup manifest (`buildManifest` snapshots it with `VACUUM INTO`, and `assertManifestComplete` refuses to ship an archive without it while this flag is on), so the exposure is bounded by the daily backup interval rather than open-ended; `cpa-claude export-requests --from --to` is the way back to a `.jsonl` file. Retention with the archive off is enforced by `pruneBefore` (`DELETE` + `incremental_vacuum`), not by deleting files, and `RewriteClientMask` becomes a single `UPDATE`.

Two admin-layer traps that were part of the same problem:

- `cachedByAuth` in `admin.go` memoizes the summary's per-auth windows (`lifetime` + `24h`) behind singleflight and **must not hold `lifetimeMu` across the aggregate call**. It used to, so two concurrent panel loads served 17s + 18s instead of deduplicating. The 24h window had no cache at all.
- The SPA must issue **one** `/api/summary` per load. `App.tsx` previously fired a second one purely as a 401 probe; `Dashboard` already handles 401 → logout, and `login.tsx` verifies the token against a cheap endpoint instead — specifically `/api/workspaces`, which returns 503 when SaaS is disabled. That still proves the token because auth runs first, but be aware the probe target is a SaaS route.

### Sidecar (auxiliary traffic emulation) — cc-core `sidecar/sidecar.go`

There is no local `internal/server/sidecar.go` any more; the only local surface is the `Server.sidecar` field. `Notify` is called from `doForward` after credential acquisition.

**Sessions are keyed by the account anchor alone**, not `(account, clientToken)` — `m.anchors` is keyed on `accountKey` (`sidecar/sidecar.go:340`), so N downstream client tokens funnelling through one OAuth account share one anchor and fire one bootstrap between them. That is the point: one account should look like one machine.

First touch starts **two** goroutines:

1. **`runBootstrap`** — **10** requests in real CC's captured timing, jittered ±15% (`bootstrapJitterFrac`) because replaying the ladder bit-exact is itself a fingerprint. In order: `growthbook_eval`, `oauth_account_settings`, `claude_code_grove`, `claude_cli_bootstrap`, `claude_code_penguin_mode`, `quota_probe`, `mcp_registry`, `v1_mcp_servers`, `code_triggers`, `claude_code_releases`. Each step has its own `User-Agent` (Bun / axios / claude-code / claude-cli — real CC mixes 4 client identities), its own `Anthropic-Beta`, and `noAuth: true` for the public CDN.
2. **`runHeartbeat`** — POSTs `/api/event_logging/v2/batch` with a `tengu_dir_search` ClaudeCodeInternalEvent, on a **hot/warm/cold cadence ladder** rather than a fixed interval: activity newer than `heartbeatHotWindow` (30s) → every 18s; within `heartbeatWarmWindow` (90s) → every 45s; **past 90s it stops**. This mirrors real CC, where the event fires constantly during typing then goes quiet a minute or two after a pause.

**`runDatadogHeartbeat` exists but is deliberately never started** (`sidecar/sidecar.go:333`). The Datadog logs intake path is fully implemented and body-shape-tested, but the hardcoded public intake key is itself a pinned fingerprint, so the code chooses not to emit it. If you re-enable it, that decision is the thing to re-litigate — not a missing wire-up to fix.

**`bootstrapCooldown = 12h`** — after one account's bootstrap fires, the next `Notify` cannot re-fire it for 12 hours, even across idle eviction. One account therefore bursts at most twice a day no matter how many client tokens funnel through it, matching "user launches CLI in the morning, maybe again in the evening".

A `bootstrapSessionID` is shared by the bootstrap + quota probe + event_logging streams (matches real CC). Distinct from the per-conversation chat session_id from `SessionIDFor`.

**Per-account host differentiation** — the machine-identifying telemetry fields (`linux_distro_id`, `linux_kernel`, `terminal`, `shell` in the event_logging + datadog env blocks, plus the process-memory metrics) are NOT one pinned profile. Each OAuth account draws a stable host from a weighted pool of plausible Linux machines via `auth.HostProfile` / `ProfileFor(accountKey)` (cc-core `auth/hostprofile.go`), sha256-anchored exactly like `DeviceIDFor`, and persisted to the credential file (`host_profile`) on first sidecar touch via `EnsureHostProfile`. Without this, N distinct accounts would all advertise the single captured machine (Arch/konsole/zsh) — "many users, one identical rare box" is itself a signal. Scope is **Linux-only and sidecar-only**: `platform`/`arch`/`node_version`/`is_running_with_bun` and the chat-path `x-stainless-*` headers stay fixed (one ground-truth capture; the Bun runtime bundle moves with the CC release; no mac/win capture to model their different env structure).

GC runs every `sidecarGCInterval` (5 min) and evicts virtual sessions idle past `sidecarSessionIdleTTL` (30 min); heartbeats self-stop far sooner, at the 90s warm-window edge above. `Server.Shutdown` cancels every live session's context.

API-key credentials never trigger sidecars (the third-party-detection signal we're hiding only applies to OAuth subscription accounts).

### Codex path (`codex_proxy.go` + `codex_oauth_proxy.go`)

OpenAI-format requests on the Codex endpoint. **API-key credentials** forward to `api.openai.com` mostly verbatim; **OAuth (ChatGPT Plus/Pro/Team)** credentials forward to `chatgpt.com/backend-api/codex/responses` with the session/account headers the real Codex CLI sends. JWT parsing in cc-core `auth/codex_jwt.go` extracts `chatgpt_account_id` and `chatgpt_plan_type` from the id_token.

Three things the old description got wrong:

- **There is no chat↔responses translation.** OAuth credentials serve `/v1/responses` and `/v1/responses/compact` only (`codex_oauth_proxy.go:37`). A `/v1/chat/completions` request that lands on an OAuth credential does not get converted — the handler asks the retry loop to try a different credential, because API-key creds handle that shape fine. It only fails if no API key is available.
- **`/v1/models` is a union, not just the plan catalog.** cc-core `auth/codex_models.go` synthesizes the per-plan-tier catalog, but for API-key credentials the response merges that with a live upstream probe.
- **`codex_ws.force_http` is declared and never read.** It exists in `internal/config/config.go:27` with a doc comment describing a WS→HTTP bridge, and nothing consumes it. Setting it does nothing — either wire it or delete it.

The quota refresher's 5-minute interval is hard-coded at the `main.go` call site, not configurable.

**Two different portal probes, and they answer different questions.** `POST /auths/:id/codex-usage` (`FetchCodexUsage`) reads wham/usage — how much quota is left in the current window, minute-to-minute. `POST /auths/:id/codex-subscription` (`FetchCodexSubscription`, cc-core v0.8.66) reads `/backend-api/subscriptions` + `accounts/check` — what plan was bought, when the term started, whether it renews. Only the second can see **delinquency**, and that is the point of it: a delinquent account serves traffic normally until its grace period ends and then stops dead, so nothing in the quota view moves beforehand. Neither probe may touch credential health on failure. Three things to keep straight:

- The **derived** answers (`plan`, `free`, `at_risk`, deadlines) are computed server-side in `newCodexSubscriptionView` from cc-core's helpers and shipped to the SPA already decided. Do not re-derive them in TypeScript: "is it free" has two independent upstream sources (a gratis flag *and* a 100%-off promo), and "is it at risk" has to choose between grace-period end and term end.
- The view rides on the auth row (`codex_subscription`), not just the probe response, so a card renders a known billing risk on page load. But `Auth.CodexSubscription` is **in-memory only** — like `CodexUsage`, it is nil after a restart until something probes again. There is no scheduled refresh; the button is the only trigger.
- Billing requests are presented as a **browser XHR** (`browserUA` + `Sec-*`), unlike wham/usage which mirrors the Codex CLI. Leaving User-Agent unset is not neutral — Go substitutes `Go-http-client/…`, which on an OAuth subscription account is the loudest third-party tell there is.

> **Codex OAuth has not been smoke-tested against a real ChatGPT subscription token in production.** The auth-layer paths (token exchange, refresh, JWT) work; full request/response parity against `chatgpt.com/backend-api` is pending. If you change anything in this path, exercise both the API-key and OAuth branches.

### Codex WebSocket ingress (`codex_ws.go`) — per-turn billing is the subtle part

Opt-in (`codex_ws.enabled`). Real codex-tui speaks `/v1/responses` over a **WebSocket** (`GET` + Upgrade), one long-lived socket carrying many turns — `cc-core/codexws` is the upstream transport to `chatgpt.com/backend-api/codex/responses`. Two non-obvious invariants:

- **Bill per turn, asynchronously — not once at session close.** A WS session can run an hour and hundreds of turns. `pumpCodexWS` settles each turn's *delta* (`codexTurnDelta` = running total − already-billed) on every terminal event via `billCodexWSTurn`, pushed through a per-session buffered channel drained by one goroutine (matches sub2api) so a slow SaaS write never stalls forwarding; a full queue falls back to inline billing rather than dropping a charge. The auth's own token ledger (`usage.Record`, load-balancing weight) is folded in once at close with zero cost to avoid double-counting. Deferring to close made cost lag real upstream usage (quota % ticks up live while total cost sits still) and lost the whole session's billing on a mid-stream restart. `codexTurnDelta` is pure + regression-tested.
- **Session fair-share cap** (`client_max_sessions`, default 0 = unlimited). A WS session holds its pool slot for the socket's whole life, so a few heavy WS users can starve everyone else off a healthy fleet. The pre-upgrade gate uses `cc-core` `Pool.SessionsHeld` and refuses only slots this token does *not* already hold, so an established session is never torn down.

### SaaS / billing layer — `internal/saas/`, gated on `saas.enabled`

Present and fully wired in this fork despite what older notes claimed. `server.go:118` opens `saas.db`, builds the exchange-rate refresher, the Z-Pay gateway, the billing/invoice/inbox handlers, and the expiry sweeper. Routes mount on the **primary engine only** via `mountBillingRoutes`: `/api/wallet/*`, `/api/team/*`, `/api/webhooks/resend-inbound`; the admin-side `/api/{groups,orders,workspaces,invoices,inbox}` mount whenever `saasDB != nil`.

The proxy hot path has exactly two hooks: a **402 balance pre-check** (`proxy.go:145`) and **`SettleCharge`** at settle time (`proxy.go:1039`). Default is `saas.enabled: false`, and failure is soft — if `saas.db` won't open, the server logs and runs with billing off rather than refusing to boot. So an operator who never touches it sees exactly the legacy no-billing behaviour.

Workspace group charging (`internal/saas/db/workspace.go`) opens `BEGIN IMMEDIATE` on a dedicated `*sql.Conn` — modernc.org/sqlite has no per-Tx txlock option — so a cap's read-modify-write is serialized. A DEFERRED transaction would let concurrent readers see the same "already used" and overshoot the cap. `TestChargeMemberFirst_ConcurrentCapRace` pins this.

⏰ **`claude-sonnet-5` carries a dated introductory price in cc-core `pricing/pricing.go` that expires 2026-08-31** ($2/$10 → list $3/$15). Past that date the catalog undercharges every sonnet-5 request by 33% until it is bumped.

### Two ways to add an Anthropic OAuth credential

1. **Standard PKCE flow** — `buildAnthropicAuthURL` + browser redirect + `finishAnthropicLogin` token-exchange (`cc-core/auth/login.go`). This is what the admin panel's "Sign in with Claude" button does.
2. **Session cookie flow** — `cc-core/auth/login_session.go`. Operator pastes a `sk-ant-sid02-…` `sessionKey` cookie + a mandatory proxy URL; server drives `claude.com/cai/oauth/authorize` server-side under uTLS Chrome fingerprint, captures the 302 redirect, and reuses `finishAnthropicLogin` for token-exchange. Proxy is non-optional because driving `claude.com` from a server IP without one fails Cloudflare's checks and risks the underlying account.

Both paths produce the same `auth.Auth` and go through `Pool.AddOAuth`. If you change the token-exchange logic in `finishAnthropicLogin`, both flows are affected.

### Capture archive — lives in `cc-core/crack/`

Recorded real-client traffic, ground truth for every fingerprint constant. **Consolidated into `cc-core/crack/` (v0.8.19)** so the captures sit next to the constants they pin — this repo no longer carries its own `crack/`. Organized by client; only the latest capture of each is kept (older in git history). See `cc-core/crack/README.md`.

- `cc-core/crack/cc2220/` — Claude Code live-session capture. The running target is **2.1.220** (`SPEC.md` = authoritative constants + diff; `rows/` = structurally-redacted requests).
- `cc-core/crack/cc2214/` — kept as the login/bootstrap baseline (`code_triggers` was added off this one).
- `cc-core/crack/codex/` — ChatGPT/Codex CLI capture (`codex-tui/0.144.4`, `mimicry.CodexCLIVersion`).
- `cc-core/crack/{apikey,login,oauth}/` — auth-flow captures. `COMPARE.md` diffs across them.
- `cc-core/crack/scripts/` — `extract_live.py` (structural redaction), `split.py`/`gen.py`/`sanitize.py`.

A stale `crack/scripts/redaction_map.json` may still sit in this repo's working tree. It is untracked and gitignored (it maps real captured values, so it must stay that way) — a pre-consolidation leftover, safe to delete.

**Bumping the CC version target is now a cc-core change**: re-capture → `extract_live.py` → update `cc-core/crack/cc<ver>/SPEC.md` and the constants in `cc-core/{mimicry,sidecar}` → tag a cc-core release → bump the dependency here (and in hypitoken).

## Conventions worth knowing

- **bun, not npm** — every JS toolchain invocation in this repo uses bun. `npx` will technically work but the lockfile is `bun.lock`.
- **All identity derivation is content-addressed**, no random UUIDs except `X-Client-Request-Id` and the internal `event_id` field. If you need a new stable identifier, derive it from `accountKey` (or `accountKey + clientToken` if it should differ across downstream users).
- **OAuth credential file fields are append-only** — `parseFile` in `cc-core/auth/oauth.go` tolerates missing fields with sensible fallbacks; new fields go through the `_ = raw["new_field"].(...)` pattern so old credential files keep loading.
- **Per-provider stickiness uses `auth.NormalizeProvider(provider) + "|" + clientToken`** as the key — Claude and Codex share a token but not a slot. Don't collapse this.
- **Hop-by-hop headers + ingress headers are stripped before forwarding** (`hopHeaders` map and `stripIngressHeaders` in `proxy.go`). This is critical when behind Cloudflare Tunnel — `Cdn-Loop: cloudflare` triggers CF's loop-prevention WAF on `api.anthropic.com`. Don't loosen this filter.
- **Tests** — the sidecar suite is now **`cc-core/sidecar/sidecar_test.go`**, not in this repo. It runs against a live `httptest.Server` and exercises real timing (several seconds of wall clock for the bootstrap suite). When adding a bootstrap step, extend `TestBootstrapFiresAllStepsWithCorrectUA`'s `wants` map — it asserts both directions: every expected path was hit with the right `User-Agent` and `Anthropic-Beta`, **and** no unexpected path was hit.
- **Config zero values are not "unlimited"** — `applyDefaults` (`internal/config/config.go`) rewrites `log_retention_days`/`client_max_concurrent`/`client_rpm` from `0` to 90/15/60, so the "0 = keep forever / unlimited" promised by the field comments and `config.example.yaml` is unreachable. Either honour it or fix the comments; don't document around it.
- **`README.md` is substantially stale** and should not be trusted as a spec: it still describes `/admin/` (really `admin_path`, default `/mgmt-console`), an `access_tokens` config key (doesn't exist — `tokens.json`), 30-day log retention (90), a ~10-minute idle window (5), 4 retry attempts (`maxAttempts = 12`), and a Preact/CDN single-page panel with read-only API keys (React 18 + Vite SPA, API keys are CRUD-able). `install.sh`'s next-steps text repeats the `access_tokens` error.
