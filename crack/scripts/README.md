# crack/scripts/

Helper scripts for the capture archive — kept separate from the capture data
itself (`claude/`, `kiro/`).

## Files

| Script | Purpose |
|---|---|
| `extract_live.py <dump.json> [outdir]` | **Claude** tool. Structurally-redacting extractor for a live Claude Code session dump (whose bodies contain private conversation content). Keeps only fingerprint structure — keys, block types, `cache_control`, versions, betas, env, metadata shape — replacing prose/code/identity with placeholders. Default outdir `crack/claude/rows/`. The source dump is never committed. |
| `split.py <mode>` | **Kiro** tool. Decode a Kiro whistle dump into `rows/`. `<mode>` ∈ `kiro` (reads `crack/kiro/raw/kiro-session-full.json`) / `kiro-login` (reads `crack/kiro/login/raw/kiro-login-session-full.json`, filtered by rowId ≥ `KIRO_LOGIN_START_ROWID`). |
| `gen.py <mode>` | Render the Kiro per-row JSONs as per-request markdown under `crack/kiro/.../docs/`. (Retains note dictionaries for retired Anthropic modes as reference; data-driven by the mode arg.) |
| `sanitize.py` | Idempotent in-place redaction across **every** json/md under `crack/`. Replaces tokens, UUIDs, emails, device id, OAuth/Cognito `code/state/verifier/challenge`, AWS STS creds, CF cookies, hostname, paths, etc. The literal secret→placeholder map lives in `redaction_map.json` (gitignored). Run after any new capture import. |

All scripts anchor paths to their own location (`os.path.dirname(__file__)/..`),
so you can run them from any cwd.

## Refresh flows

Claude (fresh whistle dump, not committed):

```bash
python3 crack/scripts/extract_live.py /path/to/whistle-dump.json   # → crack/claude/rows/
```

Kiro:

```bash
# drop the dump at crack/kiro/raw/kiro-session-full.json
python3 crack/scripts/split.py kiro       # raw → rows
python3 crack/scripts/sanitize.py         # in-place redact tokens / IDs
python3 crack/scripts/gen.py kiro         # rows → docs
```
