package db

import (
	"context"
	"testing"
	"time"
)

// A total taken over a page is not a total.
//
// The operator's revenue figures used to be summed from whichever paid orders
// happened to be on the page the panel had just fetched, so they silently
// stopped counting anything older than the page cap — and the cap is reached
// long before the archive is.
func TestPaidOrderTotalsCoverEveryPaidOrderNotJustAPage(t *testing.T) {
	ctx := context.Background()
	d := testDB(t)
	now := time.Now()

	// 12 paid orders of ¥10 / $1.40 spread over 12 hours, plus noise that must
	// not count towards revenue. The USD credit is seeded distinct from the CNY
	// amount so the two sums cannot be swapped without the test noticing —
	// "Σ USD credited" reporting yuan would otherwise pass.
	seed := func(no string, cny, usd float64, status string, at time.Time) {
		t.Helper()
		if _, err := d.ExecContext(ctx,
			`INSERT INTO alipay_orders (out_trade_no, token, cny_amount, usd_credit, rate, status, trade_no, qr_code, created_at, paid_at)
			 VALUES (?, 'sk-payer-0001', ?, ?, 7.15, ?, '', '', ?, ?)`,
			no, cny, usd, status, at.Unix(), at.Unix()); err != nil {
			t.Fatalf("seed order: %v", err)
		}
	}
	for i := range 12 {
		seed("paid-"+string(rune('a'+i)), 10, 1.40, OrderPaid, now.Add(-time.Duration(i)*time.Hour))
	}
	seed("pending-1", 999, 999, OrderPending, now)
	seed("expired-1", 999, 999, OrderExpired, now)

	cny, usd, count, err := d.PaidOrderTotals(ctx)
	if err != nil {
		t.Fatalf("PaidOrderTotals: %v", err)
	}
	if count != 12 {
		t.Errorf("count = %d, want 12 paid orders", count)
	}
	if cny < 119.99 || cny > 120.01 {
		t.Errorf("cny = %v, want ¥120 — unpaid orders must not count", cny)
	}
	if usd < 16.79 || usd > 16.81 {
		t.Errorf("usd = %v, want $16.80 (12 × $1.40) — this is usd_credit, not the yuan amount", usd)
	}

	// A page far smaller than the archive: summing it would report ¥30.
	page, err := d.ListAllOrders(ctx, OrderPaid, 3)
	if err != nil {
		t.Fatalf("ListAllOrders: %v", err)
	}
	if len(page) != 3 {
		t.Fatalf("page = %d rows, want the 3 asked for", len(page))
	}
	var pageSum float64
	for _, o := range page {
		pageSum += o.CNYAmount
	}
	if pageSum >= cny {
		t.Errorf("page sum %v is not below the true total %v — the fixture cannot detect the bug", pageSum, cny)
	}
}

// The status filter has to run in SQL. Applied after LIMIT it selects the
// newest N orders of any status and then discards the non-matching ones, so a
// request for N paid orders comes back with however many of the last N orders
// were paid.
func TestListAllOrdersFiltersStatusBeforeTheLimit(t *testing.T) {
	ctx := context.Background()
	d := testDB(t)
	now := time.Now()

	// The 5 newest orders are all pending; the paid ones are older.
	for i := range 5 {
		seedOrder(t, d, "sk-payer-0001", "pending-"+string(rune('a'+i)), 10, OrderPending,
			now.Add(-time.Duration(i)*time.Minute))
	}
	for i := range 4 {
		seedOrder(t, d, "sk-payer-0001", "paid-"+string(rune('a'+i)), 10, OrderPaid,
			now.Add(-time.Duration(i+10)*time.Hour))
	}

	got, err := d.ListAllOrders(ctx, OrderPaid, 3)
	if err != nil {
		t.Fatalf("ListAllOrders: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d rows, want 3 paid ones — filtering after LIMIT would have returned 0", len(got))
	}
	for _, o := range got {
		if o.Status != OrderPaid {
			t.Errorf("row %s has status %q, want only paid", o.OutTradeNo, o.Status)
		}
	}

	// No filter still returns the newest of any status.
	all, err := d.ListAllOrders(ctx, "", 3)
	if err != nil {
		t.Fatalf("ListAllOrders(no filter): %v", err)
	}
	if len(all) != 3 || all[0].Status != OrderPending {
		t.Errorf("unfiltered newest = %d rows starting %q, want 3 starting with a pending one", len(all), all[0].Status)
	}
}
