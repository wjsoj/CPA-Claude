# OAuth Request Audit

Local-only audit tooling for comparing captured Claude Code OAuth request
samples with the current Go implementation.

The tool:

- reads `crack/oauth/rows/*.json`
- reads selected source files under `internal/server` and `internal/auth`
- generates a Markdown report under `reports/`
- does not contact upstream services
- does not read live credentials

Run from the repository root:

```bash
go run ./audit/oauth-request-audit
```

Optional flags:

```bash
go run ./audit/oauth-request-audit \
  -root . \
  -out audit/oauth-request-audit/reports/oauth-request-audit.md
```

This folder is intentionally self-contained so it can be removed later without
touching the proxy implementation.
