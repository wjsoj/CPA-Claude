# Disaster Recovery — CPA-Claude

If the server is lost beyond recovery, you can rebuild CPA-Claude from **the
project code + the newest object in the `apibackup` bucket**. Client tokens,
upstream credentials, and any wallet data survive.

## What gets backed up (daily, encrypted, off-host)

A daily systemd timer runs `cpa-claude backup --config <cfg>`, which uploads
`apibackup/cpa-claude/YYYY-MM-DD.tar.gz.enc` (X25519 sealed; rolling 7 days):

- `tokens.json` — client API tokens **(critical — all client access)**
- `auths/` — upstream OAuth/API credentials (refresh_tokens; OAuth dies if lost)
- `config.yaml`
- `saas.db` (+ `saas.db.jwt_secret`) — wallet/orders, if the SaaS DB is present
- `secrets/` (if present), `state.json`, `monitor.json` — best-effort

## Prerequisites (kept OFFLINE, never on the server)

1. The **X25519 private key** (the `private` half from `cpa-claude backup keygen`).
2. A **read-only restore S3 key** (GetObject + ListBucket), separate from the
   server's write-only key.

## Recovery steps

```sh
# 1. Install the binary (don't start the service yet).
curl -fsSL https://gh-proxy.com/raw.githubusercontent.com/wjsoj/CPA-Claude/main/install.sh \
  | bash -s -- --version vX.Y.Z --force

# 2. Recreate the config dir.
mkdir -p ~/.config/cpa-claude

# 3. Restore the newest backup (read creds + private key passed on the CLI so
#    they never persist on the recovered host).
cpa-claude restore \
  --config ~/.config/cpa-claude/config.yaml \   # only needs backup.s3.{endpoint,region,bucket,prefix}
  --date latest \
  --identity /path/to/offline/private-key \
  --s3-access-key-id <RESTORE_KEY_ID> \
  --s3-secret-key <RESTORE_KEY_SECRET> \
  --dest ~/.config/cpa-claude/

# 4. Switch config back to the WRITE-ONLY server S3 key (+ recipient_pubkey).

# 5. Start it.
sudo systemctl enable --now cpa-claude.service cpa-claude-backup.timer
```

## Verify

- `systemctl status cpa-claude` — active.
- Admin panel `/mgmt-console` lists the restored client tokens.
- Upstream OAuth credentials **refresh** without re-auth (auths/ restored).
- `systemctl list-timers | grep cpa-claude-backup` — next run scheduled.

## After recovery

Rotate every credential briefly on the recovered host (restore S3 key, and per
security policy the upstream OAuth creds).

## Operational notes

- Generate the keypair once: `cpa-claude backup keygen`. Public → config
  `backup.recipient_pubkey`; private → offline (≥2 safe places —
  **losing it makes all backups unrecoverable**).
- Manual backup now: `cpa-claude backup --config <cfg>`.
