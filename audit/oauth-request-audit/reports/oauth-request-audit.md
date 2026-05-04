# OAuth Request Audit

Generated: 2026-05-04T13:17:19+08:00

Scope: local static audit only. The tool reads `crack/oauth/rows` and selected Go source files; it does not contact upstream services.

## Golden OAuth Flow

| idx | method | url | phase guess | request shape |
|---:|---|---|---|---|
| 1 | POST | `/api/eval/sdk-zAZezfDKGoZuXXKe` | GrowthBook bootstrap | attributes{13}, forcedFeatures[0], forcedVariations{0}, url |
| 2 | GET | `/api/oauth/account/settings` | OAuth account bootstrap | empty |
| 3 | GET | `/api/claude_code_grove` | Claude Code bootstrap | empty |
| 4 | GET | `/api/claude_cli/bootstrap` | Claude Code bootstrap | empty |
| 5 | GET | `/api/claude_code_penguin_mode` | Claude Code bootstrap | empty |
| 6 | POST | `/v1/messages?beta=true` | quota probe | max_tokens, messages[1], metadata{1}, model |
| 7 | POST | `http://localhost:8080/answer/api/v1/mcp` | MCP discovery | id, jsonrpc, method, params{3} |
| 8 | GET | `/mcp-registry/v0/servers?version=latest&limit=100&visibility=commercial%2Cgsuite%2Centerprise%2Chealth` | MCP discovery | empty |
| 9 | GET | `/v1/mcp_servers?limit=1000` | MCP discovery | empty |
| 10 | GET | `/claude-code-releases/latest` | release check | empty |
| 11 | CONNECT | `registry.npmmirror.com:443` | other | empty |
| 12 | GET | `/mcp-registry/v0/servers?version=latest&limit=100&visibility=commercial%2Cgsuite%2Centerprise%2Chealth&cursor=com.crypto.mcp%2Fcrypto-com%3A1.0.0` | MCP discovery | empty |
| 13 | GET | `/mcp-registry/v0/servers?version=latest&limit=100&visibility=commercial%2Cgsuite%2Centerprise%2Chealth&cursor=io.customer%2Fmcp%3A1.0.0` | MCP discovery | empty |
| 14 | POST | `/api/event_logging/v2/batch` | event logging | events[99] |
| 15 | CONNECT | `registry.npmmirror.com:443` | other | empty |
| 16 | POST | `/api/v2/logs` | Datadog logging | JSON array |
| 17 | POST | `/v1/messages?beta=true` | business message | context_management{1}, diagnostics{1}, max_tokens, messages[19], metadata{1}, model, output_config{1}, stream, system[4], thinking{1}, tools[8] |
| 18 | POST | `/v1/messages?beta=true` | business message | context_management{1}, diagnostics{1}, max_tokens, messages[21], metadata{1}, model, output_config{1}, stream, system[4], thinking{1}, tools[8] |
| 19 | POST | `/api/event_logging/v2/batch` | event logging | events[43] |
| 20 | POST | `/v1/messages?beta=true` | business message | context_management{1}, diagnostics{1}, max_tokens, messages[23], metadata{1}, model, output_config{1}, stream, system[4], thinking{1}, tools[8] |
| 21 | POST | `/api/v2/logs` | Datadog logging | JSON array |
| 22 | POST | `/v1/messages?beta=true` | business message | context_management{1}, diagnostics{1}, max_tokens, messages[25], metadata{1}, model, output_config{1}, stream, system[4], thinking{1}, tools[9] |
| 23 | POST | `/api/event_logging/v2/batch` | event logging | events[30] |
| 24 | POST | `/api/v2/logs` | Datadog logging | JSON array |
| 25 | POST | `/api/event_logging/v2/batch` | event logging | events[8] |
| 26 | POST | `/v1/messages?beta=true` | business message | context_management{1}, diagnostics{1}, max_tokens, messages[27], metadata{1}, model, output_config{1}, stream, system[4], thinking{1}, tools[9] |
| 27 | POST | `/api/event_logging/v2/batch` | event logging | events[16] |
| 28 | POST | `/api/v2/logs` | Datadog logging | JSON array |
| 29 | CONNECT | `registry.npmmirror.com:443` | other | empty |
| 30 | POST | `/api/event_logging/v2/batch` | event logging | events[4] |
| 31 | CONNECT | `registry.npmmirror.com:443` | other | empty |
| 32 | POST | `/api/event_logging/v2/batch` | event logging | empty |

## Key Field Matrix

### Row 01 `POST /api/eval/sdk-zAZezfDKGoZuXXKe`

- Headers: `authorization`=Bearer sk-ant-oat01-REDACTED; `user-agent`=Bun/1.3.14; `anthropic-beta`=oauth-2025-04-20; `accept`=*/*; `accept-encoding`=gzip, br; `connection`=keep-alive
- Body: attributes{13}, forcedFeatures[0], forcedVariations{0}, url

### Row 02 `GET /api/oauth/account/settings`

- Headers: `authorization`=Bearer sk-ant-oat01-REDACTED; `user-agent`=claude-code/2.1.126; `anthropic-beta`=oauth-2025-04-20; `accept`=application/json, text/plain, */*; `accept-encoding`=gzip, br; `connection`=close
- Body: empty

### Row 03 `GET /api/claude_code_grove`

- Headers: `authorization`=Bearer sk-ant-oat01-REDACTED; `user-agent`=claude-cli/2.1.126 (external, cli); `anthropic-beta`=oauth-2025-04-20; `accept`=application/json, text/plain, */*; `accept-encoding`=gzip, br; `connection`=close
- Body: empty

### Row 04 `GET /api/claude_cli/bootstrap`

- Headers: `authorization`=Bearer sk-ant-oat01-REDACTED; `user-agent`=claude-code/2.1.126; `anthropic-beta`=oauth-2025-04-20; `accept`=application/json, text/plain, */*; `accept-encoding`=gzip, br; `connection`=close
- Body: empty

### Row 05 `GET /api/claude_code_penguin_mode`

- Headers: `authorization`=Bearer sk-ant-oat01-REDACTED; `user-agent`=axios/1.13.6; `anthropic-beta`=oauth-2025-04-20; `accept`=application/json, text/plain, */*; `accept-encoding`=gzip, br; `connection`=close
- Body: empty

### Row 06 `POST /v1/messages?beta=true`

- Headers: `authorization`=Bearer sk-ant-oat01-REDACTED; `user-agent`=claude-cli/2.1.126 (external, cli); `anthropic-beta`=oauth-2025-04-20,interleaved-thinking-2025-05-14,redact-thinking-2026-02-12,context-management-20...; `anthropic-version`=2023-06-01; `x-claude-code-session-id`=d85790bb-6261-43c0-982d-550eb177c8d5; `x-client-request-id`=e7aa2abd-83bd-46a1-86b2-dbcb23169e3b; `accept`=application/json; `accept-encoding`=gzip, br; `connection`=keep-alive
- Body: `model`=claude-haiku-4-5-20251001; `messages`=1; `metadata`
- `metadata.user_id`: device_id=000000000000000000000...; account_uuid=00000000-0000-0000-0000-000000000001; session_id=d85790bb-6261-43c0-982d-550eb177c8d5

### Row 09 `GET /v1/mcp_servers?limit=1000`

- Headers: `authorization`=Bearer sk-ant-oat01-REDACTED; `user-agent`=axios/1.13.6; `anthropic-beta`=mcp-servers-2025-12-04; `anthropic-version`=2023-06-01; `accept`=application/json, text/plain, */*; `accept-encoding`=gzip, br; `connection`=close
- Body: empty

### Row 10 `GET /claude-code-releases/latest`

- Headers: `user-agent`=axios/1.13.6; `accept`=application/json, text/plain, */*; `accept-encoding`=gzip, br; `connection`=close
- Body: empty

### Row 14 `POST /api/event_logging/v2/batch`

- Headers: `authorization`=Bearer sk-ant-oat01-REDACTED; `user-agent`=claude-code/2.1.126; `anthropic-beta`=oauth-2025-04-20; `x-service-name`=claude-code; `accept`=application/json, text/plain, */*; `accept-encoding`=gzip, br; `connection`=close
- Body: events[99]

### Row 16 `POST /api/v2/logs`

- Headers: `user-agent`=axios/1.13.6; `accept`=application/json, text/plain, */*; `accept-encoding`=gzip, br; `connection`=close
- Body: JSON array

### Row 17 `POST /v1/messages?beta=true`

- Headers: `authorization`=Bearer sk-ant-oat01-REDACTED; `user-agent`=claude-cli/2.1.126 (external, cli); `anthropic-beta`=claude-code-20250219,oauth-2025-04-20,context-1m-2025-08-07,interleaved-thinking-2025-05-14,redac...; `anthropic-version`=2023-06-01; `x-claude-code-session-id`=0d7d3701-b10d-49a7-9324-8189c8c54152; `x-client-request-id`=162f9d16-8f25-4e2c-81f8-1ad89452fa1c; `accept`=application/json; `accept-encoding`=gzip, br; `connection`=keep-alive
- Body: `model`=claude-opus-4-7; `system`=4; `tools`=8; `messages`=19; `metadata`; `thinking`; `context_management`; `output_config`; `diagnostics`; `stream`
- `metadata.user_id`: device_id=000000000000000000000...; account_uuid=00000000-0000-0000-0000-000000000001; session_id=0d7d3701-b10d-49a7-9324-8189c8c54152

## Current Implementation Signals

| field group | implementation source | source category |
|---|---|---|
| Claude CLI version / UA / Stainless headers / beta list | `internal/server/fingerprint.go:CLICurrentVersion` | hard-coded constants |
| `/v1/messages` OAuth auth + Anthropic headers | `applyAnthropicHeaders` | generated defaults plus client header passthrough |
| billing header `cc_version` / `cch` | `signBillingHeaderCCH` | derived from request body with local algorithm |
| `metadata.user_id.device_id` | `DeviceIDFor` | derived locally from account key, not read from a real machine-id store |
| bootstrap sidecars | `realBootstrapSteps` | generated async from a hard-coded schedule |
| GrowthBook account attributes | `buildGrowthBookBody` | mixed: OAuth file values plus hard-coded plan/tier/platform |
| event_logging / Datadog bodies | `buildHeartbeatBody` | low-fidelity generated heartbeat |
| OAuth account UUID persistence | `account_uuid` | parsed from credential JSON when present |

## Findings

### Flow ordering (high)

- Evidence: Golden capture sends bootstrap/quota/telemetry before row 17 business `/v1/messages`; current `proxy.go` calls `sidecar.Notify` inside the business request path and `sidecar.go` launches goroutines.
- Audit interpretation: A local mock diff should verify whether first business traffic is observed before the synthetic bootstrap. If yes, the implementation is not reproducing the observed OAuth flow.

### Body shape (high)

- Evidence: Row 17 has OAuth-style `tools` count 8, `system` count 4, `diagnostics`, and `ToolSearch`; current mimicry skips full rewrite when the incoming body already has a Claude Code system prefix.
- Audit interpretation: A downstream API-key-shaped Claude Code request can remain API-key-shaped while receiving OAuth headers.

### Identity (medium)

- Evidence: `DeviceIDFor` derives device id from account key; row captures describe machine-id-derived device id.
- Audit interpretation: This is a provenance mismatch. For audit use, mark it as locally synthesized rather than authentic.

### Account attributes (medium)

- Evidence: `buildGrowthBookBody` hard-codes `subscriptionType=max` and `rateLimitTier=default_claude_max_20x`.
- Audit interpretation: Those fields should be treated as unverified unless sourced from a real bootstrap/profile response.

### Telemetry fidelity (medium)

- Evidence: Row 14 contains a 99-event startup batch; current heartbeat emits one generated event per tick.
- Audit interpretation: This is useful for detecting implementation drift, but it is not a strict reproduction of captured telemetry.

## Recommended Safe Next Steps

1. Add a mock upstream transport mode that records the actual outbound requests produced by `server.forward` without contacting real upstream hosts.
2. Feed representative client requests into the proxy and compare actual outbound rows against this report's golden row summaries.
3. Extend this tool to ingest the mock-captured outbound rows under `audit/oauth-request-audit/captures/` and produce a three-way diff: golden capture vs implementation source vs actual outbound request.
4. Keep generated reports under `audit/oauth-request-audit/reports/` so the whole audit directory can be removed cleanly.
