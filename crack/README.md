# crack/ — capture archive

Recorded real-client traffic used as ground truth for the fingerprint constants
in the codebase. Organized by upstream client; only the **latest** capture per
client is kept (older versions live in git history).

```
crack/
  claude/   Claude Code (claude-cli) — current target 2.1.156. SPEC.md is the
            source of truth; rows/ are structurally-redacted representative
            requests (no conversation prose). Produced by scripts/extract_live.py.
  kiro/     Kiro / Amazon-Q CLI capture — rows/ + docs/ + raw/, plus login/ for
            the Cognito PKCE login flow. Produced by scripts/split.py + gen.py.
  scripts/  Tooling (see scripts/README.md).
  (codex/   ChatGPT/Codex CLI — to be added.)
```

## Why claude/ has no raw dump

`crack/claude/` keeps only redacted `rows/` because a live Claude Code session
capture contains private conversation content. `scripts/extract_live.py` walks
each body and keeps only the fingerprint-bearing **structure** (keys, block
types, `cache_control`, versions, betas, env, metadata shape), replacing prose /
code / identity values with `<redacted …>` placeholders. The raw whistle dump is
never committed. `crack/kiro/` keeps its raw dump (no private chat content) so it
stays re-processable.

## Refresh flows

Claude (from a fresh whistle dump, not committed):

```bash
python3 crack/scripts/extract_live.py /path/to/whistle-dump.json   # → crack/claude/rows/
# then update crack/claude/SPEC.md to match the new version
```

Kiro:

```bash
# drop the dump at crack/kiro/raw/kiro-session-full.json (or kiro/login/raw/…)
python3 crack/scripts/split.py kiro          # raw → rows
python3 crack/scripts/sanitize.py            # in-place redact tokens / IDs
python3 crack/scripts/gen.py kiro            # rows → docs
```
