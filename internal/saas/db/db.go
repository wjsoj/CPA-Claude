// Package db opens the SQLite store used by the SaaS layer. It is the
// single source of truth for pricing groups, per-token wallets, the wallet
// ledger, and Z-Pay/Alipay orders. The proxy hot-path's debit happens
// inside one of this package's transactions so the balance can never drift
// from the ledger.
//
// Tokens (not users) are the primary identity here — the CPA-Claude project
// has no concept of an end-user separate from the access token. Every
// wallet row is keyed on the token string.
package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// DB wraps *sql.DB with domain helpers (groups, wallets, orders, ...).
type DB struct {
	*sql.DB
	path string
}

// Open opens (or creates) the SQLite file at path with WAL enabled and runs
// any pending migrations.
//
// synchronous=FULL costs one extra fsync per commit but is the only setting
// that survives raw power loss without losing committed wallet rows.
// CPA-Claude traffic is nowhere near the throughput where the difference
// would matter.
//
// File mode is force-chmoded to 0600 after open so the wallet ledger is
// not world-readable even if the filesystem default umask was lax.
func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	// cache_size is negative => KiB of page cache (here 64 MiB). A larger
	// cache keeps the hot b-tree pages (wallets, indexes, recent wallet_tx)
	// resident so reads and the per-charge cap SUMs avoid re-reading pages
	// from the OS cache. Cheap win as wallet_tx grows into the millions.
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)&_pragma=cache_size(-65536)", path)
	sdb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	sdb.SetMaxOpenConns(8)
	sdb.SetMaxIdleConns(4)
	if err := sdb.Ping(); err != nil {
		return nil, err
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Chmod(path+suffix, 0o600)
	}
	db := &DB{DB: sdb, path: path}
	if err := db.migrate(); err != nil {
		_ = sdb.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return db, nil
}

func (db *DB) Path() string { return db.path }
