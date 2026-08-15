package db

import (
	"context"
	"testing"
	"time"
)

// ChargedUSDBetween is what lets a statement notice that the request log lost
// rows the ledger still holds, so it has to see both ledgers and respect the
// window.
func TestChargedUSDBetweenSpansBothLedgers(t *testing.T) {
	ctx := context.Background()
	d := testDB(t)
	const tok = "sk-recon-0001"

	// A real workspace, because a shared-pool charge is FK'd to one.
	wsID := seedWorkspaceMember(t, d, tok, 100, 100, 0, 0)
	now := time.Now()
	inWindow := now.Add(-2 * time.Hour)
	outWindow := now.Add(-72 * time.Hour)

	// Personal wallet charges: one inside the window, one well before it.
	seedWalletTx(t, d, tok, "charge", -3, inWindow)
	seedWalletTx(t, d, tok, "charge", -99, outWindow)
	// A top-up must never count as spend, however large.
	seedWalletTx(t, d, tok, "topup", 500, inWindow)
	// Shared-pool charge for the same member, inside the window. A workspace
	// member's spend lands here first, so omitting this table would report a
	// team member as having spent almost nothing.
	seedWorkspaceTx(t, d, wsID, tok, "charge", -4, inWindow)

	got, err := d.ChargedUSDBetween(ctx, tok, now.Add(-24*time.Hour), now)
	if err != nil {
		t.Fatalf("ChargedUSDBetween: %v", err)
	}
	if got != 7 {
		t.Errorf("charged = %v, want 7 (3 personal + 4 pool; the topup and the older charge are excluded)", got)
	}

	// A window containing nothing yields zero rather than an error.
	empty, err := d.ChargedUSDBetween(ctx, tok, now.Add(-time.Minute), now)
	if err != nil {
		t.Fatalf("ChargedUSDBetween (empty): %v", err)
	}
	if empty != 0 {
		t.Errorf("empty window = %v, want 0", empty)
	}

	// Another token's ledger must not leak in.
	other, err := d.ChargedUSDBetween(ctx, "sk-someone-else", now.Add(-24*time.Hour), now)
	if err != nil {
		t.Fatalf("ChargedUSDBetween (other): %v", err)
	}
	if other != 0 {
		t.Errorf("other token = %v, want 0", other)
	}
}

// The window is half-open, matching how the statement resolves its day bounds.
// An inclusive upper edge would double-count a charge landing exactly on the
// boundary between two consecutive monthly statements.
func TestChargedUSDBetweenIsHalfOpen(t *testing.T) {
	ctx := context.Background()
	d := testDB(t)
	const tok = "sk-recon-0002"
	if _, err := d.EnsureWallet(ctx, tok); err != nil {
		t.Fatalf("EnsureWallet: %v", err)
	}
	start := time.Now().Add(-time.Hour).Truncate(time.Second)
	end := start.Add(time.Hour)

	seedWalletTx(t, d, tok, "charge", -1, start) // on the inclusive edge
	seedWalletTx(t, d, tok, "charge", -1, end)   // on the exclusive edge

	got, err := d.ChargedUSDBetween(ctx, tok, start, end)
	if err != nil {
		t.Fatalf("ChargedUSDBetween: %v", err)
	}
	if got != 1 {
		t.Errorf("charged = %v, want 1 — start is inclusive, end exclusive", got)
	}
}

func seedWalletTx(t *testing.T, d *DB, tok, kind string, amt float64, at time.Time) {
	t.Helper()
	if _, err := d.ExecContext(context.Background(),
		`INSERT INTO wallet_tx (token, kind, amount_usd, ref, note, created_at) VALUES (?,?,?,'','',?)`,
		tok, kind, amt, at.Unix()); err != nil {
		t.Fatalf("seed wallet_tx: %v", err)
	}
}

func seedWorkspaceTx(t *testing.T, d *DB, wsID int64, tok, kind string, amt float64, at time.Time) {
	t.Helper()
	if _, err := d.ExecContext(context.Background(),
		`INSERT INTO workspace_tx (workspace_id, token, kind, amount_usd, ref, note, created_at) VALUES (?,?,?,?,'','',?)`,
		wsID, tok, kind, amt, at.Unix()); err != nil {
		t.Fatalf("seed workspace_tx: %v", err)
	}
}
