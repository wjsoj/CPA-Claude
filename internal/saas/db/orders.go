package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const (
	OrderPending = "pending"
	OrderPaid    = "paid"
	OrderExpired = "expired"
	OrderFailed  = "failed"
)

// ErrOrderNotPending is returned by CreditPaidOrder when the order is no
// longer pending — i.e. a concurrent webhook already credited it. Callers
// should treat it as a successful no-op.
var ErrOrderNotPending = errors.New("order not pending")

type AlipayOrder struct {
	OutTradeNo string
	Token      string
	CNYAmount  float64
	USDCredit  float64
	Rate       float64
	Status     string
	TradeNo    string
	QRCode     string
	CreatedAt  time.Time
	PaidAt     time.Time
}

const orderCols = `out_trade_no, token, cny_amount, usd_credit, rate, status, trade_no, qr_code, created_at, paid_at`

func scanOrder(row interface{ Scan(...any) error }) (*AlipayOrder, error) {
	var o AlipayOrder
	var c, p int64
	if err := row.Scan(&o.OutTradeNo, &o.Token, &o.CNYAmount, &o.USDCredit, &o.Rate, &o.Status, &o.TradeNo, &o.QRCode, &c, &p); err != nil {
		return nil, err
	}
	o.CreatedAt = time.Unix(c, 0)
	if p > 0 {
		o.PaidAt = time.Unix(p, 0)
	}
	return &o, nil
}

func (db *DB) CreateOrder(ctx context.Context, o AlipayOrder) error {
	o.CreatedAt = time.Now()
	_, err := db.ExecContext(ctx, `INSERT INTO alipay_orders
		(out_trade_no, token, cny_amount, usd_credit, rate, status, trade_no, qr_code, created_at, paid_at)
		VALUES (?, ?, ?, ?, ?, ?, '', ?, ?, 0)`,
		o.OutTradeNo, o.Token, o.CNYAmount, o.USDCredit, o.Rate, OrderPending, o.QRCode, o.CreatedAt.Unix())
	return err
}

func (db *DB) GetOrder(ctx context.Context, outTradeNo string) (*AlipayOrder, error) {
	row := db.QueryRowContext(ctx, `SELECT `+orderCols+` FROM alipay_orders WHERE out_trade_no = ?`, outTradeNo)
	o, err := scanOrder(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return o, err
}

// CreditPaidOrder atomically marks the order paid and credits the wallet.
// Idempotent: a second concurrent call sees 0 rows affected on the order
// UPDATE and returns ErrOrderNotPending without touching the wallet.
func (db *DB) CreditPaidOrder(ctx context.Context, outTradeNo, tradeNo, token string, usdCredit float64, ref, note string) (float64, error) {
	if _, err := db.EnsureWallet(ctx, token); err != nil {
		return 0, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	now := time.Now().Unix()

	res, err := tx.ExecContext(ctx,
		`UPDATE alipay_orders SET status = ?, trade_no = ?, paid_at = ? WHERE out_trade_no = ? AND status = ?`,
		OrderPaid, tradeNo, now, outTradeNo, OrderPending)
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, ErrOrderNotPending
	}

	var bal float64
	if err := tx.QueryRowContext(ctx, `SELECT balance_usd FROM wallets WHERE token = ?`, token).Scan(&bal); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	bal += usdCredit
	if _, err := tx.ExecContext(ctx, `UPDATE wallets SET balance_usd = ?, updated_at = ? WHERE token = ?`, bal, now, token); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO wallet_tx (token, kind, amount_usd, ref, note, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		token, TxKindTopup, usdCredit, ref, note, now); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return bal, nil
}

func (db *DB) MarkOrderExpired(ctx context.Context, outTradeNo string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE alipay_orders SET status = ? WHERE out_trade_no = ? AND status = ?`,
		OrderExpired, outTradeNo, OrderPending)
	return err
}

// ExpirePendingOrdersBefore deletes every pending order created before
// cutoff. Returns the row count removed.
//
// Was originally an UPDATE → 'expired'; switched to outright DELETE so
// stale orders don't clutter the user's recharge history. The wallet
// ledger (wallet_tx) is the audit trail for *credited* top-ups; orders
// that never paid carry no financial state so dropping them entirely is
// safe. Late notify hits for a dropped order go through the
// applyNotification "order not found" terminal branch and ACK without
// effect.
func (db *DB) ExpirePendingOrdersBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := db.ExecContext(ctx,
		`DELETE FROM alipay_orders WHERE status = ? AND created_at < ?`,
		OrderPending, cutoff.Unix())
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// DeletePendingOrder removes a single pending order owned by the given
// token. Refuses to delete orders in any other status — paid/expired/
// failed states either carry financial reconciliation we must preserve
// (paid) or are about to be swept by ExpirePendingOrdersBefore anyway
// (expired/failed). Returns ErrNotFound when no matching pending row
// existed (already paid, already swept, or never owned by this token).
func (db *DB) DeletePendingOrder(ctx context.Context, outTradeNo, token string) error {
	res, err := db.ExecContext(ctx,
		`DELETE FROM alipay_orders WHERE out_trade_no = ? AND token = ? AND status = ?`,
		outTradeNo, token, OrderPending)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// CountPendingOrders returns the number of pending orders for a token.
func (db *DB) CountPendingOrders(ctx context.Context, token string) (int, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM alipay_orders WHERE token = ? AND status = ?`,
		token, OrderPending).Scan(&n)
	return n, err
}

func (db *DB) ListOrders(ctx context.Context, token string, limit int) ([]*AlipayOrder, error) {
	if limit <= 0 {
		limit = 50
	}
	// Hide expired/failed orders from the user-facing list — they
	// carry no financial state and the sweeper deletes new ones
	// outright; this filter also hides rows that were marked expired
	// by the pre-DELETE sweeper version still living in the DB.
	rows, err := db.QueryContext(ctx,
		`SELECT `+orderCols+` FROM alipay_orders WHERE token = ? AND status NOT IN ('expired','failed') ORDER BY created_at DESC LIMIT ?`,
		token, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*AlipayOrder
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (db *DB) ListAllOrders(ctx context.Context, limit int) ([]*AlipayOrder, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := db.QueryContext(ctx,
		`SELECT `+orderCols+` FROM alipay_orders ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*AlipayOrder
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
