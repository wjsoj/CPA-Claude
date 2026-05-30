# crack/codex

Live capture of the **Codex CLI** (`codex-tui`, the Rust TUI) talking to the ChatGPT
subscription backend. Ground truth for the OpenAI/ChatGPT OAuth fingerprint, parallel to
`crack/claude/` (Anthropic) and `crack/kiro/` (Amazon Q).

- `SPEC.md` — authoritative constant list + the `0.125.0 → 0.135.0` diff and the
  HTTP-vs-WebSocket transport note. **Read this first.**
- `rows/` — structurally-redacted representative requests (one per distinct endpoint).
  Secrets (Bearer JWT, cookies, account UUID, user id, email, workspace path, git origin)
  are replaced with placeholders; non-secret fingerprint values are verbatim.

Capture target: `codex-tui/0.135.0` on a ChatGPT **Pro** plan, 2026-05-30, via Whistle.

Not captured this round: the OAuth PKCE login round-trip (outside the rolling buffer) and the
WebSocket frame bodies carrying the Responses payload (Whistle did not record WS frames).
