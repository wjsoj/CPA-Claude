package db

import (
	"context"
	"testing"
	"time"
)

// The two *SpendBetween queries are the only ledger source behind a group
// statement's "区间实际扣款" and each member's personal column. Both of their
// WHERE clauses are load-bearing in a way no amount is: the workspace predicate
// keeps another team's charges out of this team's reimbursement document, and
// the time predicate is the only thing making the figure describe the range the
// caller asked for rather than all of history. Neither shows up as an error
// when it is wrong — it shows up as a plausible, larger number.

// chargeAt writes a charge row directly so the test can place it on either side
// of a window edge. The production writers stamp time.Now(), which is exactly
// what a boundary test cannot use.
func chargeAt(t *testing.T, d *DB, table, token string, wsID int64, usd float64, at time.Time) {
	t.Helper()
	var err error
	switch table {
	case "workspace_tx":
		_, err = d.ExecContext(context.Background(),
			`INSERT INTO workspace_tx (workspace_id, token, kind, amount_usd, ref, note, created_at)
			 VALUES (?, ?, 'charge', ?, 'ref', '', ?)`, wsID, token, -usd, at.Unix())
	case "wallet_tx":
		_, err = d.ExecContext(context.Background(),
			`INSERT INTO wallet_tx (token, kind, amount_usd, ref, note, created_at)
			 VALUES (?, 'charge', ?, 'ref', '', ?)`, token, -usd, at.Unix())
	default:
		t.Fatalf("unknown table %q", table)
	}
	if err != nil {
		t.Fatalf("insert %s: %v", table, err)
	}
}

func newWorkspaceWithMember(t *testing.T, d *DB, name, token string) int64 {
	t.Helper()
	ctx := context.Background()
	ws, err := d.CreateWorkspace(ctx, name)
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	if err := d.AddMember(ctx, ws.ID, token, WSRoleMember, 0, 0); err != nil {
		t.Fatalf("AddMember: %v", err)
	}
	if _, err := d.EnsureWallet(ctx, token); err != nil {
		t.Fatalf("EnsureWallet: %v", err)
	}
	return ws.ID
}

func TestMemberSpendBetweenScopesToWorkspaceAndWindow(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()

	const (
		mine   = "sk-mine-00000000000000000000000000"
		theirs = "sk-thrs-00000000000000000000000000"
	)
	ours := newWorkspaceWithMember(t, d, "ours", mine)
	other := newWorkspaceWithMember(t, d, "theirs", theirs)

	from := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 1) // half-open [from, to)

	for _, tc := range []struct {
		name  string
		table string
	}{
		{"pool ledger", "workspace_tx"},
		{"personal ledger", "wallet_tx"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// One charge on each side of both edges, plus two inside.
			chargeAt(t, d, tc.table, mine, ours, 1, from.Add(-time.Second)) // before
			chargeAt(t, d, tc.table, mine, ours, 2, from)                   // inclusive start
			chargeAt(t, d, tc.table, mine, ours, 4, to.Add(-time.Second))   // last instant
			chargeAt(t, d, tc.table, mine, ours, 8, to)                     // exclusive end
			// And the same member's amount charged under a different team.
			chargeAt(t, d, tc.table, theirs, other, 16, from.Add(time.Hour))

			var (
				got map[string]float64
				err error
			)
			if tc.table == "workspace_tx" {
				got, err = d.MemberPoolSpendBetween(ctx, ours, from, to)
			} else {
				got, err = d.MemberPersonalSpendBetween(ctx, ours, from, to)
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 {
				t.Fatalf("got %v, want only this workspace's own member", got)
			}
			// 2 + 4: the start instant is in, the end instant is not, and the
			// other team's 16 never appears.
			if v := got[mine]; v != 6 {
				t.Fatalf("summed %v, want 6 (2 at the start edge + 4 at the last instant)", v)
			}
			if _, ok := got[theirs]; ok {
				t.Fatalf("another workspace's member leaked in: %v", got)
			}
		})
	}
}

// A member of no workspace has personal charges too; they must not be pulled in
// by the join just because they exist in wallet_tx.
func TestMemberPersonalSpendBetweenIgnoresNonMembers(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	const (
		member  = "sk-memb-00000000000000000000000000"
		outside = "sk-outs-00000000000000000000000000"
	)
	ws := newWorkspaceWithMember(t, d, "acme", member)
	if _, err := d.EnsureWallet(ctx, outside); err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 1)
	chargeAt(t, d, "wallet_tx", member, ws, 3, from.Add(time.Hour))
	chargeAt(t, d, "wallet_tx", outside, 0, 99, from.Add(time.Hour))

	got, err := d.MemberPersonalSpendBetween(ctx, ws, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[member] != 3 {
		t.Fatalf("got %v, want only the member's own 3", got)
	}
}

// Only charges count. A top-up is a positive amount_usd row in the same table,
// and summing it would report money added as money spent.
func TestMemberSpendBetweenCountsChargesOnly(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	const tok = "sk-kind-00000000000000000000000000"
	ws := newWorkspaceWithMember(t, d, "acme", tok)
	from := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 1)

	chargeAt(t, d, "wallet_tx", tok, ws, 5, from.Add(time.Hour))
	if _, err := d.ExecContext(ctx,
		`INSERT INTO wallet_tx (token, kind, amount_usd, ref, note, created_at)
		 VALUES (?, 'topup', 100, 'ref', '', ?)`, tok, from.Add(2*time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	got, err := d.MemberPersonalSpendBetween(ctx, ws, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if got[tok] != 5 {
		t.Fatalf("summed %v, want just the 5 charged", got[tok])
	}
}
