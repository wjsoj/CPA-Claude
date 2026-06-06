# crack/claude — Claude Code 2.1.167 live capture

Captured 2026-06-06 from a real `claude-cli/2.1.167` OAuth session (whistle dump,
steady-state working session — bootstrap burst + chat + telemetry, **no fresh
login this round**). Supersedes the 2.1.158 ground truth for the fingerprint
constants.

- **`SPEC.md`** — the authoritative 2.1.158 → 2.1.167 diff + edit checklist. Read
  this first; it is what `cc-core/{mimicry,sidecar,auth}` and hypitoken's vendored
  copies are pinned against. The full OAuth-login flow (unchanged this round) is
  documented in the 2.1.158 SPEC in git history; SPEC §7 explains why it was not
  re-captured.
- **`rows/`** — structurally-redacted representative requests, one per endpoint
  class: startup (`01-startup_eval_sdk`, `02-startup_grove`, `03-startup_bootstrap`,
  `04-startup_penguin`), then chat/telemetry (`05-v1_messages`, `06-count_tokens`,
  `07/08-event_logging`, `09-datadog`, `10-releases`). Produced by
  `crack/scripts/extract_live.py`.

## Privacy note

`extract_live.py` keeps only fingerprint-bearing **structure** — keys, block types,
`cache_control`, versions, betas, env, metadata shape, the per-call User-Agent
matrix, and the OAuth request-param/response **key names** + non-secret values
(scope, token_type, expires_in, has_claude_max, organization_type, …). Every secret
or identity **value** is masked:

- OAuth secrets by key (`code`, `code_verifier`, `state`, `access_token`,
  `refresh_token`, `token_uuid`, …),
- plus a universal regex scrub of any UUID / email / sha256 hash across headers,
  URLs, and bodies (the public Claude Code client_id `9d1c250a-…` is whitelisted
  and kept verbatim, since it is a documented constant).

The raw dump is never committed. Note: unlike `crack/kiro/`, the Claude flow does
**not** run `sanitize.py` — `extract_live.py` is self-contained, and `sanitize.py`'s
host-profile rewrites (arch→generic, konsole→xterm) would corrupt the deliberately
**pinned** env fingerprint (§6 of SPEC).

## Re-extract from a fresh dump

```bash
python3 crack/scripts/extract_live.py /path/to/whistle-dump.json   # → crack/claude/rows/
# then update crack/claude/SPEC.md to match the new version
```

The whistle `get-data` API (`http://127.0.0.1:8899/cgi-bin/get-data?count=200&startTime=0`)
returns each request's full req/res body inline as a base64 field — pipe that JSON
straight into `extract_live.py`.
