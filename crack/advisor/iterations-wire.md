# Advisor API: Wire Shape of `iterations` Field

Captured from two real advisor tool calls in a Whistle session on 2026-05-05.
Both requests: `POST https://api.anthropic.com/v1/messages?beta=true`
Model: `claude-sonnet-4-6`
User-Agent: `claude-cli/2.1.128 (external, cli)`
Key `anthropic-beta` flags: `advisor-tool-2026-03-01`, `prompt-caching-scope-2026-01-05`, `cache-diagnosis-2026-04-07`, `interleaved-thinking-2025-05-14`

---

## Row 037 (`1777985876722-037`)

### `message_start` → `message.usage`

```json
{
  "input_tokens": 1,
  "cache_creation_input_tokens": 1471,
  "cache_read_input_tokens": 23994,
  "cache_creation": {
    "ephemeral_5m_input_tokens": 0,
    "ephemeral_1h_input_tokens": 1471
  },
  "output_tokens": 37,
  "service_tier": "standard",
  "inference_geo": "not_available"
}
```

### `message_delta` → `usage` (final, cumulative)

```json
{
  "input_tokens": 2,
  "cache_creation_input_tokens": 2206,
  "cache_read_input_tokens": 49459,
  "output_tokens": 314,
  "server_tool_use": {
    "web_search_requests": 0,
    "web_fetch_requests": 0
  },
  "iterations": [
    {
      "input_tokens": 1,
      "output_tokens": 85,
      "cache_read_input_tokens": 23994,
      "cache_creation_input_tokens": 1471,
      "cache_creation": {
        "ephemeral_5m_input_tokens": 0,
        "ephemeral_1h_input_tokens": 1471
      },
      "type": "message"
    },
    {
      "input_tokens": 36300,
      "output_tokens": 2197,
      "cache_read_input_tokens": 0,
      "cache_creation_input_tokens": 0,
      "cache_creation": {
        "ephemeral_5m_input_tokens": 0,
        "ephemeral_1h_input_tokens": 0
      },
      "type": "advisor_message",
      "model": "claude-opus-4-7"
    },
    {
      "input_tokens": 1,
      "output_tokens": 229,
      "cache_read_input_tokens": 25465,
      "cache_creation_input_tokens": 735,
      "cache_creation": {
        "ephemeral_5m_input_tokens": 735,
        "ephemeral_1h_input_tokens": 0
      },
      "type": "message"
    }
  ]
}
```

### Content blocks in response

| Index | Type | Name / ID |
|-------|------|-----------|
| 0 | `thinking` | — |
| 1 | `text` | — |
| 2 | `server_tool_use` | `advisor` / `srvtoolu_01YW3qZTpZtePHWPWh5jzFmK` |
| 3 | `advisor_tool_result` | — |
| 4 | `text` | — |
| 5 | `tool_use` | `Read` / `toolu_01MKRCySALWKncDVr6mbbTZT` |
| 6 | `tool_use` | `Read` / `toolu_01EtPHH6uJzMRyyiiqn5P1xQ` |
| 7 | `tool_use` | `Read` / `toolu_01LMN9b6YfkrgRPKKehSFAy1` |

---

## Row 052 (`1777985937315-052`)

### `message_start` → `message.usage`

```json
{
  "input_tokens": 1,
  "cache_creation_input_tokens": 1478,
  "cache_read_input_tokens": 40126,
  "cache_creation": {
    "ephemeral_5m_input_tokens": 0,
    "ephemeral_1h_input_tokens": 1478
  },
  "output_tokens": 23,
  "service_tier": "standard",
  "inference_geo": "not_available"
}
```

### `message_delta` → `usage` (final, cumulative)

```json
{
  "input_tokens": 2,
  "cache_creation_input_tokens": 2845,
  "cache_read_input_tokens": 81730,
  "output_tokens": 1063,
  "server_tool_use": {
    "web_search_requests": 0,
    "web_fetch_requests": 0
  },
  "iterations": [
    {
      "input_tokens": 1,
      "output_tokens": 96,
      "cache_read_input_tokens": 40126,
      "cache_creation_input_tokens": 1478,
      "cache_creation": {
        "ephemeral_5m_input_tokens": 0,
        "ephemeral_1h_input_tokens": 1478
      },
      "type": "message"
    },
    {
      "input_tokens": 55316,
      "output_tokens": 5498,
      "cache_read_input_tokens": 0,
      "cache_creation_input_tokens": 0,
      "cache_creation": {
        "ephemeral_5m_input_tokens": 0,
        "ephemeral_1h_input_tokens": 0
      },
      "type": "advisor_message",
      "model": "claude-opus-4-7"
    },
    {
      "input_tokens": 1,
      "output_tokens": 967,
      "cache_read_input_tokens": 41604,
      "cache_creation_input_tokens": 1367,
      "cache_creation": {
        "ephemeral_5m_input_tokens": 1367,
        "ephemeral_1h_input_tokens": 0
      },
      "type": "message"
    }
  ]
}
```

### Content blocks in response

| Index | Type | Name / ID |
|-------|------|-----------|
| 0 | `thinking` | — |
| 1 | `text` | — |
| 2 | `server_tool_use` | `advisor` / `srvtoolu_01FD1KsbjBxCceY1qMa5DWMs` |
| 3 | `advisor_tool_result` | — |
| 4 | `text` | — |

---

## Key Findings

### `iterations` array structure

Each `iterations` entry is always one of two `type` values:

1. **`"type": "message"`** — a turn by the outer `claude-sonnet-4-6` model (the one the client directly called). Appears as the first and last iteration in both captures.
2. **`"type": "advisor_message"`** — a sub-call to a stronger model. Both captures show `"model": "claude-opus-4-7"`. Has an extra `"model"` field that `"message"` entries do not have.

### Fields present in `advisor_message` entries (vs `message` entries)

| Field | `message` | `advisor_message` |
|-------|-----------|-------------------|
| `input_tokens` | yes | yes |
| `output_tokens` | yes | yes |
| `cache_read_input_tokens` | yes | yes |
| `cache_creation_input_tokens` | yes | yes |
| `cache_creation` | yes | yes |
| `type` | yes | yes |
| `model` | **absent** | **present** (`"claude-opus-4-7"`) |

### `cache_creation` nesting

`cache_creation` is a **nested object** with two sub-fields:
```json
"cache_creation": {
  "ephemeral_5m_input_tokens": <number>,
  "ephemeral_1h_input_tokens": <number>
}
```

This is true in **both** the `message_start` usage AND in every `iterations` entry.
They are NOT flat fields — they are always nested under the `cache_creation` key.

### Top-level `message_delta` usage vs per-iteration breakdown

The `message_delta` usage is a **summary** (NOT a simple sum of iterations):

- `input_tokens` in `message_delta` is the residual (typically 1 or 2) after cache hits
- `cache_creation_input_tokens` and `cache_read_input_tokens` are the outer model's cumulative totals across its own turns only
- `output_tokens` is the outer model's cumulative output only (NOT including the advisor sub-call output)
- The `iterations` array provides the per-turn breakdown including the advisor sub-call

**Row 037 token accounting:**
- Outer model iterations (type=message): output_tokens = 85 + 229 = 314 ✓ (matches message_delta.output_tokens)
- Advisor sub-call: input=36300, output=2197 (billed separately via iterations)

**Row 052 token accounting:**
- Outer model iterations: output_tokens = 96 + 967 = 1063 ✓ (matches message_delta.output_tokens)
- Advisor sub-call: input=55316, output=5498

### `server_tool_use` block wire shape

The `server_tool_use` block in `content_block_start` has:
```json
{
  "type": "server_tool_use",
  "id": "srvtoolu_01...",
  "name": "advisor",
  "input": {}
}
```

`input` is always an empty object `{}` at the time of `content_block_start`. The actual advisor content is streamed as `content_block_delta` events and then a synthetic `advisor_tool_result` block is returned (type=`advisor_tool_result`) in the same response before the final `text` block.

### Notable: `advisor_message` has zero cache tokens

In both captures, the `advisor_message` iteration shows:
```json
"cache_read_input_tokens": 0,
"cache_creation_input_tokens": 0,
"cache_creation": { "ephemeral_5m_input_tokens": 0, "ephemeral_1h_input_tokens": 0 }
```

The advisor (Opus) sub-call does NOT benefit from the outer model's prompt cache. It receives a large context (36K and 55K input tokens respectively) but starts cache-cold.

### CC version in this capture

`claude-cli/2.1.128` (not 2.1.126 — version has incremented since earlier captures in `crack/`).
