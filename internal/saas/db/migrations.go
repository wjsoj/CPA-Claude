package db

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	log "github.com/sirupsen/logrus"
)

// ErrNotFound is returned when a single-row lookup misses.
var ErrNotFound = errors.New("not found")

// migrations is append-only. Each entry is a complete schema delta; never
// reorder or rewrite a previous entry — only append.
var migrations = []string{
	// 1: initial schema. Defaults match the operator-requested values —
	// claude=1/20 (0.05), codex=1/80 (0.0125). The seed row is created
	// with those values so a freshly-installed instance is ready to bill
	// without an admin first having to touch the pricing-groups table.
	`
CREATE TABLE pricing_groups (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    name                TEXT NOT NULL UNIQUE,
    description         TEXT NOT NULL DEFAULT '',
    codex_multiplier    REAL NOT NULL DEFAULT 0.0125,
    claude_multiplier   REAL NOT NULL DEFAULT 0.05,
    credential_group    TEXT NOT NULL DEFAULT '',
    is_default          INTEGER NOT NULL DEFAULT 0,
    created_at          INTEGER NOT NULL,
    updated_at          INTEGER NOT NULL
);
INSERT INTO pricing_groups (name, description, codex_multiplier, claude_multiplier, is_default, created_at, updated_at)
VALUES ('default', 'Default pricing group', 0.0125, 0.05, 1, strftime('%s','now'), strftime('%s','now'));

CREATE TABLE wallets (
    token         TEXT PRIMARY KEY,
    balance_usd   REAL NOT NULL DEFAULT 0,
    group_id      INTEGER NOT NULL,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL,
    FOREIGN KEY (group_id) REFERENCES pricing_groups(id)
);

CREATE TABLE wallet_tx (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    token       TEXT NOT NULL,
    kind        TEXT NOT NULL,
    amount_usd  REAL NOT NULL,
    ref         TEXT NOT NULL DEFAULT '',
    note        TEXT NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL
);
CREATE INDEX idx_wallet_tx_token ON wallet_tx(token, created_at);

CREATE TABLE alipay_orders (
    out_trade_no    TEXT PRIMARY KEY,
    token           TEXT NOT NULL,
    cny_amount      REAL NOT NULL,
    usd_credit      REAL NOT NULL,
    rate            REAL NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending',
    trade_no        TEXT NOT NULL DEFAULT '',
    qr_code         TEXT NOT NULL DEFAULT '',
    created_at      INTEGER NOT NULL,
    paid_at         INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_alipay_orders_token ON alipay_orders(token, created_at);
`,
	// 2: invoicing. Users with paid Alipay orders accumulate "invoiceable
	// CNY" — they can request a fapiao up to that running total minus what
	// they've already invoiced. Admin reviews requests and uploads a PDF,
	// at which point we email the user the PDF as an attachment via Resend
	// and they can download it again from the status page (token-gated).
	//
	// invoice_titles persists per-token rolling shortlist of company
	// headers + tax info so the user doesn't retype on every request. The
	// (token, name) compound key lets the same operator reuse the same
	// company across rotating tokens and lets unrelated tokens use the
	// same company name independently.
	`
CREATE TABLE invoice_titles (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    token        TEXT NOT NULL,
    name         TEXT NOT NULL,
    tax_no       TEXT NOT NULL DEFAULT '',
    address      TEXT NOT NULL DEFAULT '',
    phone        TEXT NOT NULL DEFAULT '',
    bank         TEXT NOT NULL DEFAULT '',
    bank_account TEXT NOT NULL DEFAULT '',
    last_used_at INTEGER NOT NULL DEFAULT 0,
    created_at   INTEGER NOT NULL,
    UNIQUE(token, name)
);
CREATE INDEX idx_invoice_titles_token ON invoice_titles(token, last_used_at DESC);

CREATE TABLE invoices (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    token          TEXT NOT NULL,
    cny_amount     REAL NOT NULL,
    title_name     TEXT NOT NULL,
    title_snapshot TEXT NOT NULL,                    -- frozen JSON copy of the title row at request time
    contact_email  TEXT NOT NULL,
    status         TEXT NOT NULL DEFAULT 'pending',  -- pending | issued | rejected
    pdf_path       TEXT NOT NULL DEFAULT '',
    note           TEXT NOT NULL DEFAULT '',         -- admin-supplied reason on reject / free-form on issue
    created_at     INTEGER NOT NULL,
    issued_at      INTEGER NOT NULL DEFAULT 0,
    rejected_at    INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_invoices_token ON invoices(token, created_at DESC);
CREATE INDEX idx_invoices_status ON invoices(status, created_at DESC);
`,
	// 3: inbound email inbox. Resend pushes one webhook per incoming mail
	// to the receiving domain. We persist enough metadata to render an
	// admin inbox view without re-hitting Resend's API on every list load.
	// body_html / body_text / attachments are fetched lazily (after the
	// webhook returns) via Resend's Received Emails API and patched in;
	// fetched_at=0 means "metadata only, body not yet pulled".
	`
CREATE TABLE inbound_emails (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    resend_email_id   TEXT NOT NULL UNIQUE,         -- data.email_id from the webhook
    message_id        TEXT NOT NULL DEFAULT '',      -- RFC Message-ID
    from_addr         TEXT NOT NULL DEFAULT '',
    to_addrs          TEXT NOT NULL DEFAULT '[]',    -- JSON array
    cc_addrs          TEXT NOT NULL DEFAULT '[]',    -- JSON array
    subject           TEXT NOT NULL DEFAULT '',
    received_at       INTEGER NOT NULL,              -- unix seconds
    body_html         TEXT NOT NULL DEFAULT '',
    body_text         TEXT NOT NULL DEFAULT '',
    attachments       TEXT NOT NULL DEFAULT '[]',    -- JSON array of {id,filename,content_type,content_disposition,content_id}
    raw_event         TEXT NOT NULL DEFAULT '',      -- full webhook event JSON for forensics
    fetched_at        INTEGER NOT NULL DEFAULT 0,    -- 0 = body not fetched yet
    read_at           INTEGER NOT NULL DEFAULT 0,    -- 0 = unread
    created_at        INTEGER NOT NULL
);
CREATE INDEX idx_inbound_emails_received ON inbound_emails(received_at DESC);
CREATE INDEX idx_inbound_emails_read ON inbound_emails(read_at, received_at DESC);
`,
}

func (db *DB) migrate() error {
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_version (version INTEGER PRIMARY KEY)`); err != nil {
		return err
	}
	var current int
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM schema_version`).Scan(&current); err != nil {
		return err
	}
	target := len(migrations)
	if target == current {
		log.Infof("saas-db: schema up to date at v%d", current)
		return nil
	}
	if target < current {
		return fmt.Errorf("saas-db: binary supports v%d but DB is at v%d — refusing to downgrade", target, current)
	}
	if current > 0 && db.path != "" {
		if err := backupDBFile(db.path, current); err != nil {
			log.Warnf("saas-db: backup before migrate failed (continuing): %v", err)
		}
	}
	for i, sqlText := range migrations {
		v := i + 1
		if v <= current {
			continue
		}
		log.Infof("saas-db: applying migration v%d…", v)
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, sqlText); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %d: %w", v, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_version(version) VALUES(?)`, v); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %d (record): %w", v, err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	log.Infof("saas-db: migrated v%d → v%d", current, target)
	return nil
}

func backupDBFile(path string, fromVersion int) error {
	stamp := time.Now().UTC().Format("20060102T150405Z")
	dst := fmt.Sprintf("%s.backup-v%d-%s", path, fromVersion, stamp)
	if err := copyFile(path, dst); err != nil {
		return err
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		src := path + suffix
		if _, err := os.Stat(src); err == nil {
			_ = copyFile(src, dst+suffix)
		}
	}
	log.Infof("saas-db: pre-migrate backup → %s", dst)
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
