# CPA-Claude

A native Claude (Anthropic) API proxy focused on one job: fan out client
requests across multiple Claude OAuth credentials and API keys, with per-user
sticky slot allocation, per-credential proxies, persistent usage tracking, and
automatic API-key fallback.

> **Credit.** This project is a heavily simplified and refocused derivative of
> [**CLIProxyAPI**](https://github.com/router-for-me/CLIProxyAPI) (MIT). The
> original supports many AI providers (Gemini, Codex/OpenAI, Qwen, Kimi,
> iFlow, Antigravity, Vertex, Claude, …) and ships a management UI, TUI,
> translator layer, and multi-protocol entry points. CPA-Claude keeps **only**
> the Claude passthrough, adds slot-based concurrency, per-credential
> SOCKS/HTTP proxies, and persistent usage tracking, and drops everything
> else. The Anthropic OAuth refresh flow and the uTLS Chrome transport were
> borrowed from the upstream project — huge thanks to its authors.

## Features

- **Native Claude only** — no protocol translation, pure passthrough to
  `api.anthropic.com/v1/messages` (SSE streaming preserved).
- **Slot-based concurrency per OAuth file** — each OAuth credential has a
  `max_concurrent` cap. A client session that makes a request within the last
  10 minutes (configurable) occupies one slot. Idle clients release their
  slot automatically.
- **Per-credential upstream proxy** — every OAuth file may set its own
  `proxy_url`, useful when different accounts must egress from different IPs.
- **Persistent usage tracking** — input/output/cache-read/cache-write token
  counts per credential survive restarts in `state.json`.
- **API-key fallback** — when every OAuth credential is saturated, quota-
  exceeded, or dead, requests fall through to the unlimited API-key pool.
- **Per-client weekly budgets** — each access token can have a `weekly_usd`
  cap. Spend is tracked by ISO week and costed with a built-in pricing
  table covering Haiku 4.5, Opus 4.6 and Sonnet 4.6 (cache-read and
  cache-create priced separately). Exceeded → 429 until the next Monday.
- **uTLS (Chrome)** — bypasses Anthropic's TLS fingerprinting.
- **Proxy schemes** — `http://`, `https://`, `socks5://`, `socks5h://` all
  supported, both with and without uTLS.

## Install

One-liner (Linux/macOS, amd64/arm64) — installs to `/usr/local/bin`:

```bash
curl -fsSL https://raw.githubusercontent.com/wjsoj/CPA-Claude/main/install.sh | bash
```

Pin a version or change prefix:

```bash
curl -fsSL https://raw.githubusercontent.com/wjsoj/CPA-Claude/main/install.sh \
  | bash -s -- --version v0.1.0 --prefix ~/.local
```

Re-run the same command to upgrade.

From source:

```bash
go install github.com/wjsoj/CPA-Claude/cmd/server@latest
# binary will be named "server"; rename to cpa-claude or adjust PATH usage.
```

## Quick start

```bash
cp config.example.yaml config.yaml
# edit config.yaml, populate ./auths/*.json, then:
cpa-claude -config config.yaml
```

## OAuth credential file format

`./auths/<any-name>.json`:

```json
{
  "type": "claude",
  "access_token": "sk-ant-oat01-...",
  "refresh_token": "sk-ant-ort01-...",
  "expired": "2026-05-01T12:00:00Z",
  "email": "alice@example.com",
  "label": "alice",
  "proxy_url": "http://10.0.0.1:3128",
  "max_concurrent": 5,
  "disabled": false
}
```

- `access_token` is auto-refreshed on every request if within 5 minutes of
  `expired`. The refreshed token (and new `expired`) is written back to the
  file atomically.
- `max_concurrent: 0` means unlimited for this OAuth (not usually what you
  want — set it to match the account's practical parallelism).

## Endpoints

| Method | Path                          | Notes                           |
| ------ | ----------------------------- | ------------------------------- |
| POST   | `/v1/messages`                | Streaming (`stream:true`) OK    |
| POST   | `/v1/messages/count_tokens`   | Pass-through                    |
| GET    | `/status`                     | Auth pool + usage snapshot      |
| GET    | `/healthz`                    | Liveness                        |
| GET    | `/admin/`                     | Web admin panel (if configured) |

## Admin panel

Set `admin_token` in `config.yaml` and open `http://<host>:<port>/admin/`.
The panel is a single embedded HTML page (Preact + Tailwind via CDN) that
lets you:

- view every OAuth / API-key credential with live slot usage, quota status,
  expiry, and accumulated token usage;
- toggle disabled, edit `max_concurrent` and `proxy_url`, rename labels;
- force-refresh an OAuth token, clear a quota-exceeded flag;
- upload a new OAuth JSON file or delete one.
- **Sign in with Claude** — initiates the Anthropic OAuth flow (PKCE) in
  a new tab, lets you paste the callback URL back, exchanges the code
  for tokens, and saves a new credential file. Optional proxy for the
  token exchange.

API keys are read-only in v1 — edit `config.yaml` and restart to change
them.

## Compatibility with upstream CLIProxyAPI

OAuth JSON files produced by the original
[CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) — both the
manual `claude setup-token` output and files created by its own login
flow — drop into `auth_dir` unchanged. Extra keys (`id_token`,
`last_refresh`, etc.) are preserved on save. The only caveat: files
imported from upstream have no `max_concurrent`, so they load as
unlimited (`∞`). Set a cap in the admin panel if you want slot-based
routing to take effect.

Clients authenticate with `Authorization: Bearer <token>` matching one of the
`access_tokens` in `config.yaml`. Each distinct token is one "client session"
and occupies one OAuth slot while active.

## Routing / fallback semantics

On each request for a client token:

1. If that client already has a sticky OAuth assignment and that OAuth is
   healthy (not disabled, not quota-exceeded), reuse it.
2. Otherwise pick the healthy OAuth with the fewest active sessions that
   still has spare `max_concurrent` capacity.
3. If no OAuth has capacity, pick any usable API key.
4. If the upstream returns 401/403/429/529, the credential is flagged
   (quota-exceeded where applicable) and the request is retried on a
   different credential (up to 4 attempts total).

## License

MIT. See [LICENSE](LICENSE) for the full text and the attribution note to
[CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI).
