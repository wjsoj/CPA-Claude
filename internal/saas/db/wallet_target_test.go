package db

import (
	"context"
	"testing"
	"time"
)

// TotalPaidCNY is the ceiling a target-amount statement request must not
// exceed, so it has to count only real Alipay payments — not admin-granted
// or adjusted balance, and not orders that never actually settled.
func TestTotalPaidCNYSumsOnlyPaidOrders(t *testing.T) {
	ctx := context.Background()
	d := testDB(t)
	const tok = "sk-target-0001"
	now := time.Now()

	seedOrder(t, d, tok, "trade-paid-1", 100, OrderPaid, now.Add(-time.Hour))
	seedOrder(t, d, tok, "trade-paid-2", 50.5, OrderPaid, now.Add(-2*time.Hour))
	seedOrder(t, d, tok, "trade-pending", 999, OrderPending, now.Add(-time.Hour))
	seedOrder(t, d, tok, "trade-expired", 999, OrderExpired, now.Add(-time.Hour))
	seedOrder(t, d, tok, "trade-failed", 999, OrderFailed, now.Add(-time.Hour))
	// Someone else's paid order must not leak in.
	seedOrder(t, d, "sk-someone-else", "trade-other", 500, OrderPaid, now)

	// Admin-granted credit moves the wallet balance exactly like a topup
	// order would, but it is not a payment and must not count as one.
	if _, err := d.EnsureWallet(ctx, tok); err != nil {
		t.Fatalf("EnsureWallet: %v", err)
	}
	if _, err := d.AddBalance(ctx, tok, TxKindAdjust, 1000, "admin-grant", "", true); err != nil {
		t.Fatalf("AddBalance: %v", err)
	}

	got, err := d.TotalPaidCNY(ctx, tok)
	if err != nil {
		t.Fatalf("TotalPaidCNY: %v", err)
	}
	if got != 150.5 {
		t.Errorf("TotalPaidCNY = %v, want 150.5 (only the two paid orders)", got)
	}
}

func TestTotalPaidCNYEmptyForUnknownToken(t *testing.T) {
	ctx := context.Background()
	d := testDB(t)
	got, err := d.TotalPaidCNY(ctx, "sk-nobody")
	if err != nil {
		t.Fatalf("TotalPaidCNY: %v", err)
	}
	if got != 0 {
		t.Errorf("TotalPaidCNY = %v, want 0", got)
	}
}

func seedOrder(t *testing.T, d *DB, token, tradeNo string, cny float64, status string, paidAt time.Time) {
	t.Helper()
	if _, err := d.ExecContext(context.Background(),
		`INSERT INTO alipay_orders (out_trade_no, token, cny_amount, usd_credit, rate, status, trade_no, qr_code, created_at, paid_at)
		 VALUES (?, ?, ?, 0, 1, ?, '', '', ?, ?)`,
		tradeNo, token, cny, status, paidAt.Unix(), paidAt.Unix()); err != nil {
		t.Fatalf("seed order: %v", err)
	}
}
