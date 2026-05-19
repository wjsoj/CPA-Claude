package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Wallet tx kinds. Stored as TEXT so admin-side reads can switch on the
// literal without a Go-side enum round-trip.
const (
	TxKindTopup  = "topup"
	TxKindCharge = "charge"
	TxKindAdjust = "adjust"
	TxKindRefund = "refund"
)

// ErrInsufficientBalance is returned by AddBalance when a negative delta
// would push the wallet below zero and allowNegative is false.
var ErrInsufficientBalance = errors.New("insufficient balance")

// Wallet is the per-token wallet row.
type Wallet struct {
	Token      string
	BalanceUSD float64
	GroupID    int64
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type WalletTx struct {
	ID        int64
	Token     string
	Kind      string
	AmountUSD float64
	Ref       string
	Note      string
	CreatedAt time.Time
}

// EnsureWallet creates the wallet row for a token if it doesn't exist yet,
// assigning it to the default pricing group. Idempotent — safe to call on
// every authenticated request without measurable overhead (PRIMARY KEY
// upsert is O(1) in SQLite).
//
// Returns the (possibly freshly-created) wallet row.
func (db *DB) EnsureWallet(ctx context.Context, token string) (*Wallet, error) {
	w, err := db.GetWallet(ctx, token)
	if err == nil {
		return w, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	def, err := db.DefaultGroup(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	if _, err := db.ExecContext(ctx,
		`INSERT OR IGNORE INTO wallets (token, balance_usd, group_id, created_at, updated_at) VALUES (?, 0, ?, ?, ?)`,
		token, def.ID, now, now); err != nil {
		return nil, err
	}
	return db.GetWallet(ctx, token)
}

func (db *DB) GetWallet(ctx context.Context, token string) (*Wallet, error) {
	row := db.QueryRowContext(ctx,
		`SELECT token, balance_usd, group_id, created_at, updated_at FROM wallets WHERE token = ?`, token)
	var w Wallet
	var c, u int64
	if err := row.Scan(&w.Token, &w.BalanceUSD, &w.GroupID, &c, &u); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	w.CreatedAt = time.Unix(c, 0)
	w.UpdatedAt = time.Unix(u, 0)
	return &w, nil
}

func (db *DB) GetBalance(ctx context.Context, token string) (float64, error) {
	var bal float64
	err := db.QueryRowContext(ctx, `SELECT balance_usd FROM wallets WHERE token = ?`, token).Scan(&bal)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return bal, err
}

// AddBalance applies a signed delta and records a wallet_tx row in one
// transaction. allowNegative=false rejects writes that would push the
// balance below zero (typical for charge); allowNegative=true is for admin
// adjustments.
func (db *DB) AddBalance(ctx context.Context, token, kind string, deltaUSD float64, ref, note string, allowNegative bool) (newBal float64, err error) {
	if _, err := db.EnsureWallet(ctx, token); err != nil {
		return 0, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var bal float64
	if err := tx.QueryRowContext(ctx, `SELECT balance_usd FROM wallets WHERE token = ?`, token).Scan(&bal); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	bal += deltaUSD
	if bal < 0 && !allowNegative {
		return 0, ErrInsufficientBalance
	}
	now := time.Now().Unix()
	if _, err := tx.ExecContext(ctx, `UPDATE wallets SET balance_usd = ?, updated_at = ? WHERE token = ?`, bal, now, token); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO wallet_tx (token, kind, amount_usd, ref, note, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		token, kind, deltaUSD, ref, note, now); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return bal, nil
}

// ListWalletTx returns recent transactions for a token, newest first.
func (db *DB) ListWalletTx(ctx context.Context, token string, limit int) ([]*WalletTx, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := db.QueryContext(ctx,
		`SELECT id, token, kind, amount_usd, ref, note, created_at FROM wallet_tx WHERE token = ? ORDER BY id DESC LIMIT ?`,
		token, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*WalletTx
	for rows.Next() {
		var t WalletTx
		var c int64
		if err := rows.Scan(&t.ID, &t.Token, &t.Kind, &t.AmountUSD, &t.Ref, &t.Note, &c); err != nil {
			return nil, err
		}
		t.CreatedAt = time.Unix(c, 0)
		out = append(out, &t)
	}
	return out, rows.Err()
}

// SetWalletGroup reassigns a token to a different pricing group. Used by
// the admin panel when an operator moves a token between groups.
func (db *DB) SetWalletGroup(ctx context.Context, token string, groupID int64) error {
	if _, err := db.GetGroup(ctx, groupID); err != nil {
		return err
	}
	if _, err := db.EnsureWallet(ctx, token); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx,
		`UPDATE wallets SET group_id = ?, updated_at = ? WHERE token = ?`,
		groupID, time.Now().Unix(), token)
	return err
}

// FleetWalletTotals is the aggregate summary for the operator dashboard.
type FleetWalletTotals struct {
	UserPaidUSD float64 // sum of -amount_usd, kind='charge'
	TopupsUSD   float64 // sum of  amount_usd, kind='topup'
	ChargeCount int64
}

func (db *DB) FleetTotals(ctx context.Context) (*FleetWalletTotals, error) {
	row := db.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN kind='charge' THEN -amount_usd ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN kind='topup'  THEN  amount_usd ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN kind='charge' THEN 1 ELSE 0 END), 0)
		FROM wallet_tx`)
	var t FleetWalletTotals
	if err := row.Scan(&t.UserPaidUSD, &t.TopupsUSD, &t.ChargeCount); err != nil {
		return nil, err
	}
	return &t, nil
}

// RekeyTokenReport tells the caller exactly what was migrated.
type RekeyTokenReport struct {
	WalletRowsAffected     int64
	WalletTxRowsAffected   int64
	OrdersRowsAffected     int64
	OldBalanceUSD          float64
	NewBalanceUSDAfterMove float64
	BackupPath             string
}

// RekeyToken migrates all wallet-side state from oldToken to newToken
// inside a single transaction (the wallets row, all wallet_tx ledger
// entries, and all alipay_orders). Used by admin token-reset to keep
// history attached to a rotated token.
//
// Safety invariants (this is production billing data):
//
//   - Pre-mutation backup via SQLite `VACUUM INTO` to a timestamped
//     .bak file. If the backup fails, the rekey aborts before touching
//     any row.
//   - All UPDATEs run inside one BEGIN..COMMIT. WAL + synchronous=FULL
//     guarantees all-or-nothing on power loss.
//   - Conservation check: wallet_tx + alipay_orders rows-affected must
//     match the pre-mutation counts; mismatch rolls back.
//   - Post-commit readback verifies the new wallet row's balance equals
//     the pre-mutation balance; mismatch returns an error so the
//     operator can restore from the backup file.
//   - Refuses if newToken already has a wallet (would either violate
//     PK or silently merge balances).
func (db *DB) RekeyToken(ctx context.Context, oldToken, newToken string) (*RekeyTokenReport, error) {
	if oldToken == "" || newToken == "" || oldToken == newToken {
		return nil, errors.New("oldToken and newToken must differ and be non-empty")
	}
	rep := &RekeyTokenReport{}
	var hadWallet bool
	if err := db.QueryRowContext(ctx,
		`SELECT balance_usd FROM wallets WHERE token = ?`, oldToken).Scan(&rep.OldBalanceUSD); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	} else {
		hadWallet = true
	}
	var oldTxCount, oldOrderCount, dstWalletCount int64
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM wallet_tx WHERE token = ?`, oldToken).Scan(&oldTxCount); err != nil {
		return nil, err
	}
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM alipay_orders WHERE token = ?`, oldToken).Scan(&oldOrderCount); err != nil {
		return nil, err
	}
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM wallets WHERE token = ?`, newToken).Scan(&dstWalletCount); err != nil {
		return nil, err
	}
	if dstWalletCount > 0 {
		return nil, errors.New("destination token already has a wallet; refusing to overwrite")
	}

	if db.path != "" {
		bk := db.path + ".pre-rekey-" + time.Now().UTC().Format("20060102-150405") + ".bak"
		if _, err := db.ExecContext(ctx, `VACUUM INTO ?`, bk); err != nil {
			return nil, fmt.Errorf("pre-rekey backup failed (refusing to mutate): %w", err)
		}
		rep.BackupPath = bk
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if hadWallet {
		res, err := tx.ExecContext(ctx,
			`UPDATE wallets SET token = ?, updated_at = ? WHERE token = ?`,
			newToken, time.Now().Unix(), oldToken)
		if err != nil {
			return nil, err
		}
		rep.WalletRowsAffected, _ = res.RowsAffected()
		if rep.WalletRowsAffected != 1 {
			return nil, fmt.Errorf("wallets rekey expected 1 row, got %d", rep.WalletRowsAffected)
		}
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE wallet_tx SET token = ? WHERE token = ?`, newToken, oldToken)
	if err != nil {
		return nil, err
	}
	rep.WalletTxRowsAffected, _ = res.RowsAffected()
	if rep.WalletTxRowsAffected != oldTxCount {
		return nil, fmt.Errorf("wallet_tx conservation broken: pre=%d post=%d", oldTxCount, rep.WalletTxRowsAffected)
	}
	res, err = tx.ExecContext(ctx,
		`UPDATE alipay_orders SET token = ? WHERE token = ?`, newToken, oldToken)
	if err != nil {
		return nil, err
	}
	rep.OrdersRowsAffected, _ = res.RowsAffected()
	if rep.OrdersRowsAffected != oldOrderCount {
		return nil, fmt.Errorf("alipay_orders conservation broken: pre=%d post=%d", oldOrderCount, rep.OrdersRowsAffected)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	if hadWallet {
		if err := db.QueryRowContext(ctx,
			`SELECT balance_usd FROM wallets WHERE token = ?`, newToken).Scan(&rep.NewBalanceUSDAfterMove); err != nil {
			return rep, fmt.Errorf("post-commit balance readback failed: %w", err)
		}
		// Both reads pull the same scalar untouched by arithmetic; exact
		// equality is the right check here.
		if rep.NewBalanceUSDAfterMove != rep.OldBalanceUSD {
			return rep, fmt.Errorf("post-commit balance mismatch: pre=%.10f post=%.10f (backup at %s)",
				rep.OldBalanceUSD, rep.NewBalanceUSDAfterMove, rep.BackupPath)
		}
	}
	return rep, nil
}
