# crack/scripts/

Helper scripts for the capture archive — keep separate from the capture data
itself (`raw/`, `oauth/`, `apikey/`, `login/`).

## Files

| Script | Purpose |
|---|---|
| `split.py <mode>` | Decode `crack/raw/<mode>-session-full.json` into `crack/<mode>/rows/NN-METHOD-host_path.json`. `<mode>` ∈ `oauth` / `apikey` / `login`. The `login` mode additionally filters down to only the 12 login-flow requests. |
| `gen.py <mode>` | Render the per-row JSONs as per-request markdown under `crack/<mode>/docs/`. Embeds NOTES + EXTRA depth tables specific to each mode. |
| `sanitize.py` | Idempotent in-place redaction across **every** json/md under `crack/` (excluding `archive/`). Replaces tokens, UUIDs, emails, device id, OAuth `code/state/verifier/challenge`, CF cookies, hostname, paths, etc. Run after any new capture import. |

All three scripts anchor paths to their own location (`os.path.dirname(__file__)/..`),
so you can run them from any cwd:

```bash
python3 crack/scripts/split.py oauth
python3 crack/scripts/sanitize.py
python3 crack/scripts/gen.py oauth
```

A typical refresh flow after dumping a new whistle session:

```bash
# 1. drop the new whistle export at crack/raw/<mode>-session-full.json
python3 crack/scripts/split.py <mode>     # raw → rows
python3 crack/scripts/sanitize.py         # in-place redact tokens / IDs
python3 crack/scripts/gen.py <mode>       # rows → docs
```
