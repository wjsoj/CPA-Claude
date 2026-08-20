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

	// Registers the cgo-free "sqlite" driver used by every Open below.
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
// _txlock=immediate: every BeginTx opens BEGIN IMMEDIATE rather than Go's
// default BEGIN DEFERRED. This is load-bearing, not a tuning knob. A deferred
// transaction takes a read lock on its first SELECT and only asks for the write
// lock at its first write; when another connection already holds the write
// lock, SQLite fails that upgrade with SQLITE_BUSY *immediately and
// deliberately ignores busy_timeout* — backing off would deadlock two readers
// both waiting to upgrade. So busy_timeout cannot help a deferred read→write
// transaction, which is exactly the shape of Charge (read balance, then write
// balance + ledger row). The sibling fork ran this configuration into ~29% of
// all charge attempts failing under normal concurrency, with no retry anywhere:
// requests served, never billed. With IMMEDIATE the write lock is taken at
// BEGIN, so contention waits on busy_timeout instead of erroring out.
//
// Every BeginTx site in this module writes, so nothing pays for the
// serialization needlessly; plain Query/Exec outside a transaction is
// unaffected. busy_timeout is 10s (was 5s) purely for headroom.
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
	dsn := fmt.Sprintf("file:%s?_txlock=immediate&_pragma=foreign_keys(1)&_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)&_pragma=cache_size(-65536)", path)
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
