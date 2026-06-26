package db

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
)

func testDB(t *testing.T) *DB {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// seedWorkspaceMember creates a workspace with `pool` USD, adds `token` as a
// member with the given caps, and gives the token `personal` USD personal
// balance. Returns the workspace id.
func seedWorkspaceMember(t *testing.T, d *DB, token string, pool, personal, dailyCap, monthlyCap float64) int64 {
	t.Helper()
	ctx := context.Background()
	ws, err := d.CreateWorkspace(ctx, "team")
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	if pool != 0 {
		if _, err := d.AdjustWorkspaceBalance(ctx, ws.ID, pool, TxKindTopup, "seed", "seed"); err != nil {
			t.Fatalf("seed pool: %v", err)
		}
	}
	if err := d.AddMember(ctx, ws.ID, token, WSRoleMember, dailyCap, monthlyCap); err != nil {
		t.Fatalf("AddMember: %v", err)
	}
	if _, err := d.EnsureWallet(ctx, token); err != nil {
		t.Fatalf("EnsureWallet: %v", err)
	}
	if personal != 0 {
		if _, err := d.AddBalance(ctx, token, TxKindTopup, personal, "seed", "seed", true); err != nil {
			t.Fatalf("seed personal: %v", err)
		}
	}
	return ws.ID
}

func poolBal(t *testing.T, d *DB, id int64) float64 {
	t.Helper()
	w, err := d.GetWorkspace(context.Background(), id)
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}
	return w.BalanceUSD
}

func personalBal(t *testing.T, d *DB, token string) float64 {
	t.Helper()
	b, err := d.GetBalance(context.Background(), token)
	if err != nil {
		t.Fatalf("GetBalance: %v", err)
	}
	return b
}

const eps = 1e-9

func approx(a, b float64) bool { return a-b < eps && b-a < eps }

func TestChargeMemberFirst_PoolThenPersonal(t *testing.T) {
	d := testDB(t)
	tok := "sk-pool-then-personal-xxxx"
	id := seedWorkspaceMember(t, d, tok, 10, 100, 0, 0)
	pool, personal, err := d.ChargeMemberFirst(context.Background(), tok, 3, "r", "n")
	if err != nil {
		t.Fatal(err)
	}
	if !approx(pool, 3) || !approx(personal, 0) {
		t.Fatalf("split = pool %.4f personal %.4f, want 3/0", pool, personal)
	}
	if got := poolBal(t, d, id); !approx(got, 7) {
		t.Fatalf("pool balance = %.4f, want 7", got)
	}
	if got := personalBal(t, d, tok); !approx(got, 100) {
		t.Fatalf("personal balance = %.4f, want 100", got)
	}
}

func TestChargeMemberFirst_PoolDrain(t *testing.T) {
	d := testDB(t)
	tok := "sk-pool-drain-xxxxxxxxxxx"
	id := seedWorkspaceMember(t, d, tok, 2, 100, 0, 0)
	pool, personal, err := d.ChargeMemberFirst(context.Background(), tok, 5, "r", "n")
	if err != nil {
		t.Fatal(err)
	}
	if !approx(pool, 2) || !approx(personal, 3) {
		t.Fatalf("split = pool %.4f personal %.4f, want 2/3", pool, personal)
	}
	if got := poolBal(t, d, id); !approx(got, 0) {
		t.Fatalf("pool balance = %.4f, want 0", got)
	}
	if got := personalBal(t, d, tok); !approx(got, 97) {
		t.Fatalf("personal balance = %.4f, want 97", got)
	}
}

func TestChargeMemberFirst_DailyCap(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	tok := "sk-daily-cap-xxxxxxxxxxxxx"
	id := seedWorkspaceMember(t, d, tok, 100, 100, 5, 0)
	// First $4 fully from pool.
	if p, _, err := d.ChargeMemberFirst(ctx, tok, 4, "r", "n"); err != nil || !approx(p, 4) {
		t.Fatalf("first charge pool=%.4f err=%v, want 4", p, err)
	}
	// Next $3: cap leaves $1 of pool, $2 spills to personal.
	pool, personal, err := d.ChargeMemberFirst(ctx, tok, 3, "r", "n")
	if err != nil {
		t.Fatal(err)
	}
	if !approx(pool, 1) || !approx(personal, 2) {
		t.Fatalf("split = pool %.4f personal %.4f, want 1/2", pool, personal)
	}
	if got := poolBal(t, d, id); !approx(got, 95) {
		t.Fatalf("pool balance = %.4f, want 95", got)
	}
	if got := personalBal(t, d, tok); !approx(got, 98) {
		t.Fatalf("personal balance = %.4f, want 98", got)
	}
}

func TestChargeMemberFirst_MonthlyCap(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	tok := "sk-monthly-cap-xxxxxxxxxxx"
	id := seedWorkspaceMember(t, d, tok, 100, 100, 0, 6)
	if p, _, err := d.ChargeMemberFirst(ctx, tok, 5, "r", "n"); err != nil || !approx(p, 5) {
		t.Fatalf("first charge pool=%.4f err=%v, want 5", p, err)
	}
	pool, personal, err := d.ChargeMemberFirst(ctx, tok, 4, "r", "n")
	if err != nil {
		t.Fatal(err)
	}
	if !approx(pool, 1) || !approx(personal, 3) {
		t.Fatalf("split = pool %.4f personal %.4f, want 1/3", pool, personal)
	}
	if got := poolBal(t, d, id); !approx(got, 94) {
		t.Fatalf("pool balance = %.4f, want 94", got)
	}
}

func TestChargeMemberFirst_NoMembership(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	tok := "sk-no-membership-xxxxxxxxx"
	if _, err := d.EnsureWallet(ctx, tok); err != nil {
		t.Fatal(err)
	}
	if _, err := d.AddBalance(ctx, tok, TxKindTopup, 50, "seed", "seed", true); err != nil {
		t.Fatal(err)
	}
	pool, personal, err := d.ChargeMemberFirst(ctx, tok, 7, "r", "n")
	if err != nil {
		t.Fatal(err)
	}
	if !approx(pool, 0) || !approx(personal, 7) {
		t.Fatalf("split = pool %.4f personal %.4f, want 0/7", pool, personal)
	}
	if got := personalBal(t, d, tok); !approx(got, 43) {
		t.Fatalf("personal balance = %.4f, want 43", got)
	}
}

func TestChargeMemberFirst_Disabled(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	tok := "sk-disabled-ws-xxxxxxxxxxx"
	id := seedWorkspaceMember(t, d, tok, 100, 50, 0, 0)
	if err := d.UpdateWorkspace(ctx, id, nil, ptr(true)); err != nil {
		t.Fatal(err)
	}
	pool, personal, err := d.ChargeMemberFirst(ctx, tok, 8, "r", "n")
	if err != nil {
		t.Fatal(err)
	}
	if !approx(pool, 0) || !approx(personal, 8) {
		t.Fatalf("split = pool %.4f personal %.4f, want 0/8 (disabled→personal)", pool, personal)
	}
	if got := poolBal(t, d, id); !approx(got, 100) {
		t.Fatalf("pool untouched expected 100, got %.4f", got)
	}
}

// TestChargeMemberFirst_ConcurrentCapRace is the key correctness test: with a
// daily cap of $5 and ten concurrent $1 charges, the pool must give up EXACTLY
// $5 (cap), never more — proving BEGIN IMMEDIATE serializes the cap
// read-modify-write. A DEFERRED transaction would let several readers observe
// the same "already used" and overshoot.
func TestChargeMemberFirst_ConcurrentCapRace(t *testing.T) {
	d := testDB(t)
	tok := "sk-cap-race-xxxxxxxxxxxxxx"
	id := seedWorkspaceMember(t, d, tok, 100, 100, 5, 0)

	const n = 10
	var wg sync.WaitGroup
	var mu sync.Mutex
	var poolSum float64
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, _, err := d.ChargeMemberFirst(context.Background(), tok, 1, "r", "n")
			if err != nil {
				t.Errorf("charge: %v", err)
				return
			}
			mu.Lock()
			poolSum += p
			mu.Unlock()
		}()
	}
	wg.Wait()

	if poolSum > 5+eps {
		t.Fatalf("pool gave up %.4f > cap 5 — cap race not serialized", poolSum)
	}
	if !approx(poolSum, 5) {
		t.Fatalf("pool gave up %.4f, want exactly 5", poolSum)
	}
	if got := poolBal(t, d, id); !approx(got, 95) {
		t.Fatalf("pool balance = %.4f, want 95", got)
	}
}

// TestChargeMemberFirst_ConcurrentPoolDrain proves the pool never goes negative
// under concurrency: pool=$5, ten concurrent $1 charges → exactly $5 from pool,
// the rest from personal, pool floored at 0.
func TestChargeMemberFirst_ConcurrentPoolDrain(t *testing.T) {
	d := testDB(t)
	tok := "sk-pool-race-xxxxxxxxxxxxx"
	id := seedWorkspaceMember(t, d, tok, 5, 100, 0, 0)

	const n = 10
	var wg sync.WaitGroup
	var mu sync.Mutex
	var poolSum float64
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, _, err := d.ChargeMemberFirst(context.Background(), tok, 1, "r", "n")
			if err != nil {
				t.Errorf("charge: %v", err)
				return
			}
			mu.Lock()
			poolSum += p
			mu.Unlock()
		}()
	}
	wg.Wait()

	if !approx(poolSum, 5) {
		t.Fatalf("pool gave up %.4f, want exactly 5", poolSum)
	}
	if got := poolBal(t, d, id); got < -eps || !approx(got, 0) {
		t.Fatalf("pool balance = %.4f, want 0 (never negative)", got)
	}
}

func TestRekeyTokenWorkspaceLinkage(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	old := "sk-rekey-old-xxxxxxxxxxxxx"
	id := seedWorkspaceMember(t, d, old, 100, 100, 7, 0)
	if _, _, err := d.ChargeMemberFirst(ctx, old, 3, "r", "n"); err != nil {
		t.Fatal(err)
	}
	newTok := "sk-rekey-new-yyyyyyyyyyyyy"
	rep, err := d.RekeyToken(ctx, old, newTok)
	if err != nil {
		t.Fatalf("RekeyToken: %v", err)
	}
	if rep.MemberRowsAffected != 1 {
		t.Fatalf("member rows moved = %d, want 1", rep.MemberRowsAffected)
	}
	if rep.WorkspaceTxRowsAffected != 1 {
		t.Fatalf("workspace_tx rows moved = %d, want 1 (one charge)", rep.WorkspaceTxRowsAffected)
	}
	// Old token no longer a member; new token is, under the same workspace.
	if _, err := d.MemberFor(ctx, old); err == nil {
		t.Fatal("old token still a member after rekey")
	}
	m, err := d.MemberFor(ctx, newTok)
	if err != nil || m.WorkspaceID != id {
		t.Fatalf("new token membership = %+v err=%v, want workspace %d", m, err, id)
	}
	// Per-member period spend follows the token (used for caps).
	day, _ := d.MemberPeriodPoolSpend(ctx, newTok)
	if !approx(day, 3) {
		t.Fatalf("rekeyed member day spend = %.4f, want 3", day)
	}
}

func TestCreditPaidOrderToWorkspaceIdempotent(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	ws, err := d.CreateWorkspace(ctx, "team")
	if err != nil {
		t.Fatal(err)
	}
	if err := d.CreateOrder(ctx, AlipayOrder{
		OutTradeNo: "CPA-ws-order-1", Token: "sk-admin", WorkspaceID: ws.ID,
		CNYAmount: 72, USDCredit: 10, Rate: 7.2,
	}); err != nil {
		t.Fatal(err)
	}
	// Concurrent double-credit: exactly one should succeed.
	var wg sync.WaitGroup
	var okCount, notPending int32
	var mu sync.Mutex
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := d.CreditPaidOrderToWorkspace(ctx, "CPA-ws-order-1", "T1", ws.ID, 10, "ref", "note")
			mu.Lock()
			if err == nil {
				okCount++
			} else if err == ErrOrderNotPending {
				notPending++
			} else {
				t.Errorf("unexpected err: %v", err)
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	if okCount != 1 || notPending != 1 {
		t.Fatalf("idempotency broken: ok=%d notPending=%d", okCount, notPending)
	}
	if got := poolBal(t, d, ws.ID); !approx(got, 10) {
		t.Fatalf("pool credited %.4f, want 10 (exactly once)", got)
	}
	// And the order is readable with its workspace_id (migration v4 column).
	o, err := d.GetOrder(ctx, "CPA-ws-order-1")
	if err != nil || o.WorkspaceID != ws.ID {
		t.Fatalf("order workspace_id = %d err=%v, want %d", o.WorkspaceID, err, ws.ID)
	}
}

func TestAddMemberUniqueConflict(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	ws1, _ := d.CreateWorkspace(ctx, "a")
	ws2, _ := d.CreateWorkspace(ctx, "b")
	tok := "sk-dup-member-xxxxxxxxxxxx"
	if err := d.AddMember(ctx, ws1.ID, tok, WSRoleMember, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := d.AddMember(ctx, ws2.ID, tok, WSRoleMember, 0, 0); err != ErrMemberExists {
		t.Fatalf("second AddMember err = %v, want ErrMemberExists", err)
	}
}

func ptr[T any](v T) *T { return &v }
