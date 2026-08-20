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

// ChargedUSDBetween sums everything actually debited for a token over
// [from, to), across both ledgers.
//
// It exists so a usage statement can be reconciled against the money rather
// than trusting the request log alone. The two records are written by different
// code on different paths: the debit is a transaction against the wallet, while
// the itemised row is a separate append to the request log. A crash, a disk
// problem, or a retention prune can lose the second while the first stands —
// and then a statement built only from the log silently under-reports what the
// customer was charged, which is the one direction a spend record must never be
// wrong in.
//
// Both tables are summed because ChargeMemberFirst debits a workspace member's
// shared pool (workspace_tx) before their personal wallet (wallet_tx); reading
// only wallet_tx would report a team member's spend as near zero. Amounts are
// stored negative for charges, so the sums are negated back to positive.
//
// The window is half-open: from inclusive, to exclusive, matching how the
// statement's day bounds are resolved.
func (db *DB) ChargedUSDBetween(ctx context.Context, token string, from, to time.Time) (float64, error) {
	if token == "" {
		return 0, nil
	}
	lo, hi := from.Unix(), to.Unix()
	var personal, pool float64
	if err := db.QueryRowContext(ctx,
		`SELECT COALESCE(-SUM(amount_usd), 0) FROM wallet_tx
		 WHERE token = ? AND kind = 'charge' AND created_at >= ? AND created_at < ?`,
		token, lo, hi).Scan(&personal); err != nil {
		return 0, err
	}
	if err := db.QueryRowContext(ctx,
		`SELECT COALESCE(-SUM(amount_usd), 0) FROM workspace_tx
		 WHERE token = ? AND kind = 'charge' AND created_at >= ? AND created_at < ?`,
		token, lo, hi).Scan(&pool); err != nil {
		return 0, err
	}
	return personal + pool, nil
}

// TotalPaidCNY sums alipay_orders.cny_amount for this token's paid orders —
// what the account has actually paid via Alipay, in yuan.
//
// This is deliberately narrower than lifetime spend or the wallet balance:
// it excludes admin-granted or adjusted credit (wallet_tx kind='adjust'),
// counting only orders that reached status='paid'. It exists as the ceiling
// a target-amount usage statement is checked against — that feature lets a
// token holder generate a statement whose line items sum to a figure they
// name, and the one thing standing between that and manufacturing a bigger
// "consumption" total than they ever paid for is refusing to let the target
// exceed money that genuinely changed hands.
func (db *DB) TotalPaidCNY(ctx context.Context, token string) (float64, error) {
	if token == "" {
		return 0, nil
	}
	var total float64
	if err := db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(cny_amount), 0) FROM alipay_orders WHERE token = ? AND status = ?`,
		token, OrderPaid).Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
}

// AllWallets returns every wallet keyed by token.
//
// For callers that need a balance for each of many tokens at once. The rows
// are one per token by definition, so this is the same amount of data a loop
// of GetWallet would have fetched, minus a round trip per token: the admin
// summary was issuing two queries per client and rebuilding the same handful
// of pricing groups over and over.
func (db *DB) AllWallets(ctx context.Context) (map[string]*Wallet, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT token, balance_usd, group_id, created_at, updated_at FROM wallets`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]*Wallet)
	for rows.Next() {
		var w Wallet
		var created, updated int64
		if err := rows.Scan(&w.Token, &w.BalanceUSD, &w.GroupID, &created, &updated); err != nil {
			return nil, err
		}
		w.CreatedAt = time.Unix(created, 0)
		w.UpdatedAt = time.Unix(updated, 0)
		out[w.Token] = &w
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
	WalletRowsAffected       int64
	WalletTxRowsAffected     int64
	OrdersRowsAffected       int64
	MemberRowsAffected       int64
	WorkspaceTxRowsAffected  int64
	InvoiceRowsAffected      int64
	InvoiceAllocRowsAffected int64
	InvoiceTitleRowsAffected int64
	OldBalanceUSD            float64
	NewBalanceUSDAfterMove   float64
	BackupPath               string
}

// RekeyToken migrates all wallet-side state from oldToken to newToken
// inside a single transaction (the wallets row, all wallet_tx ledger
// entries, all alipay_orders, the workspace membership + pool ledger
// attribution, and the invoice trio). Used by admin token-reset to keep
// history attached to a rotated token.
//
// Every table keyed on the token has to move together, and the invoice
// trio is the sharpest case: invoiceSummaryFor computes the remaining
// quota as paid(alipay_orders) − pending − issued(invoices +
// invoice_allocations). Moving the orders while leaving the invoices
// behind hands the rotated token its full paid amount as fresh quota,
// so everything already invoiced can be invoiced a second time.
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

	// The backup is taken before the write lock: VACUUM INTO cannot run
	// inside a transaction. A charge landing in the gap is settled against
	// the old token and would be absent from the backup, which is the same
	// exposure the backup always had — it is a restore point, not a
	// snapshot of the exact pre-mutation state.
	if db.path != "" {
		bk := fmt.Sprintf("%s.pre-rekey-%s.bak", db.path, time.Now().UTC().Format("20060102-150405.000000000"))
		if _, err := db.ExecContext(ctx, `VACUUM INTO ?`, bk); err != nil {
			return nil, fmt.Errorf("pre-rekey backup failed (refusing to mutate): %w", err)
		}
		rep.BackupPath = bk
	}

	// Everything below — the counts, the guards and the UPDATEs — runs inside
	// one transaction. The module opens with _txlock=immediate, so BeginTx
	// takes the write lock at BEGIN rather than on first write; no other
	// writer can interleave once we are past this line.
	//
	// The counts MUST be taken inside that lock. They were read before
	// BeginTx until 2026-08-18, and on a token carrying live traffic the
	// result was that reset could essentially never succeed: a charge
	// settling between the pre-count and the UPDATE made the UPDATE touch
	// one row more than had been counted, the conservation check read that
	// as data corruption, and the rotation was refused with "wallet_tx
	// conservation broken: pre=86695 post=86696". The busier the token, the
	// more reliably its owner could not rotate a leaked key — the one
	// situation the feature exists for.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var hadWallet bool
	if err := tx.QueryRowContext(ctx,
		`SELECT balance_usd FROM wallets WHERE token = ?`, oldToken).Scan(&rep.OldBalanceUSD); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	} else {
		hadWallet = true
	}

	// Source row counts, and the destination-occupied guards. A freshly
	// minted token collides with nothing, but a caller passing an in-use
	// token deserves a clean refusal rather than a constraint error thrown
	// from the middle of the transaction.
	var oldTxCount, oldOrderCount, oldMemberCount, oldWsTxCount int64
	var oldInvoiceCount, oldAllocCount, oldTitleCount int64
	var dstWalletCount, dstMemberCount int64
	var dstInvoiceCount, dstAllocCount, dstTitleCount int64
	for _, q := range []struct {
		table string
		token string
		into  *int64
	}{
		{"wallet_tx", oldToken, &oldTxCount},
		{"alipay_orders", oldToken, &oldOrderCount},
		{"workspace_members", oldToken, &oldMemberCount},
		{"workspace_tx", oldToken, &oldWsTxCount},
		{"invoices", oldToken, &oldInvoiceCount},
		{"invoice_allocations", oldToken, &oldAllocCount},
		{"invoice_titles", oldToken, &oldTitleCount},
		{"wallets", newToken, &dstWalletCount},
		{"workspace_members", newToken, &dstMemberCount},
		{"invoices", newToken, &dstInvoiceCount},
		{"invoice_allocations", newToken, &dstAllocCount},
		{"invoice_titles", newToken, &dstTitleCount},
	} {
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(1) FROM `+q.table+` WHERE token = ?`, q.token).Scan(q.into); err != nil {
			return nil, err
		}
	}
	if dstWalletCount > 0 {
		return nil, errors.New("destination token already has a wallet; refusing to overwrite")
	}
	if dstMemberCount > 0 {
		return nil, errors.New("destination token already belongs to a workspace; refusing to overwrite")
	}
	if dstInvoiceCount > 0 || dstAllocCount > 0 || dstTitleCount > 0 {
		return nil, errors.New("destination token already has invoice state; refusing to overwrite")
	}

	now := time.Now().Unix()
	if hadWallet {
		res, err := tx.ExecContext(ctx,
			`UPDATE wallets SET token = ?, updated_at = ? WHERE token = ?`,
			newToken, now, oldToken)
		if err != nil {
			return nil, err
		}
		rep.WalletRowsAffected, _ = res.RowsAffected()
		if rep.WalletRowsAffected != 1 {
			return nil, fmt.Errorf("wallets rekey expected 1 row, got %d", rep.WalletRowsAffected)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE workspace_members SET updated_at = ? WHERE token = ?`, now, oldToken); err != nil {
		return nil, err
	}
	for _, u := range []struct {
		table string
		pre   int64
		into  *int64
	}{
		{"wallet_tx", oldTxCount, &rep.WalletTxRowsAffected},
		{"alipay_orders", oldOrderCount, &rep.OrdersRowsAffected},
		{"workspace_members", oldMemberCount, &rep.MemberRowsAffected},
		{"workspace_tx", oldWsTxCount, &rep.WorkspaceTxRowsAffected},
		{"invoices", oldInvoiceCount, &rep.InvoiceRowsAffected},
		{"invoice_allocations", oldAllocCount, &rep.InvoiceAllocRowsAffected},
		{"invoice_titles", oldTitleCount, &rep.InvoiceTitleRowsAffected},
	} {
		// u.table comes from the fixed literal list above, never from input;
		// both values stay parameterized.
		res, err := tx.ExecContext(ctx,
			`UPDATE `+u.table+` SET token = ? WHERE token = ?`, newToken, oldToken) //nolint:gosec // G202: table name is a compile-time literal, not user data
		if err != nil {
			return nil, err
		}
		*u.into, _ = res.RowsAffected()
		if *u.into != u.pre {
			return nil, fmt.Errorf("%s conservation broken: pre=%d post=%d", u.table, u.pre, *u.into)
		}
	}

	if hadWallet {
		// Read the moved balance back while the lock is still held, so the
		// comparison cannot pick up a charge that settled against the new
		// token the instant the rotation became visible.
		if err := tx.QueryRowContext(ctx,
			`SELECT balance_usd FROM wallets WHERE token = ?`, newToken).Scan(&rep.NewBalanceUSDAfterMove); err != nil {
			return nil, fmt.Errorf("balance readback failed: %w", err)
		}
		// Both reads pull the same scalar untouched by arithmetic; exact
		// equality is the right check here.
		if rep.NewBalanceUSDAfterMove != rep.OldBalanceUSD {
			return nil, fmt.Errorf("balance mismatch: pre=%.10f post=%.10f",
				rep.OldBalanceUSD, rep.NewBalanceUSDAfterMove)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return rep, nil
}
