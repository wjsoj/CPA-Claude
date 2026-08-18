package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---- helpers ----

// seedPaidCNY gives token an invoiceable pool by inserting a paid order. The
// USD credit is irrelevant to invoicing (a fapiao is always denominated in the
// yuan that were actually paid), so it is left arbitrary.
func seedPaidCNY(t *testing.T, d *DB, token string, cny float64) {
	t.Helper()
	no := fmt.Sprintf("ord-%s-%d", token, time.Now().UnixNano())
	if _, err := d.ExecContext(context.Background(),
		`INSERT INTO alipay_orders (out_trade_no, token, cny_amount, usd_credit, rate, status, trade_no, qr_code, created_at, paid_at)
		 VALUES (?, ?, ?, 1, 7.15, ?, '', '', ?, ?)`,
		no, token, cny, OrderPaid, time.Now().Unix(), time.Now().Unix()); err != nil {
		t.Fatalf("seed paid order: %v", err)
	}
}

// seedInvoiceTeam builds a workspace whose members each have `cny` of paid
// orders behind them. The first token is the admin. Returns the workspace id.
func seedInvoiceTeam(t *testing.T, d *DB, tokens []string, cny []float64) int64 {
	t.Helper()
	ctx := context.Background()
	ws, err := d.CreateWorkspace(ctx, "acme")
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	for i, tok := range tokens {
		role := WSRoleMember
		if i == 0 {
			role = WSRoleAdmin
		}
		if err := d.AddMember(ctx, ws.ID, tok, role, 0, 0); err != nil {
			t.Fatalf("AddMember %s: %v", tok, err)
		}
		if cny[i] > 0 {
			seedPaidCNY(t, d, tok, cny[i])
		}
	}
	return ws.ID
}

func availCNY(t *testing.T, d *DB, token string) float64 {
	t.Helper()
	s, err := d.InvoiceableCNY(context.Background(), token)
	if err != nil {
		t.Fatalf("InvoiceableCNY(%s): %v", token, err)
	}
	return s.AvailableCNY
}

func testTitle() InvoiceTitle {
	return InvoiceTitle{Name: "北京某某科技有限公司", TaxNo: "91110108MA01ABCDEF"}
}

func createTeamInvoice(t *testing.T, d *DB, wsID int64, admin string, cny float64) *Invoice {
	t.Helper()
	inv, err := d.CreateWorkspaceInvoice(context.Background(), wsID, admin, cny, testTitle(), "fin@example.com")
	if err != nil {
		t.Fatalf("CreateWorkspaceInvoice(%.2f): %v", cny, err)
	}
	return inv
}

// ---- tests ----

// The filing admin is charged their allocation, not the whole invoice.
//
// A team invoice is stored under the admin's token (so the audit trail knows
// who asked) AND itemised in invoice_allocations. If InvoiceableCNY's personal
// leg forgets `workspace_id = 0`, both are counted against the admin: they lose
// the group's entire invoice amount out of their own pool while their fellow
// members lose their share on top. Nothing else in the system notices — the
// numbers merely come out too small.
func TestTeamInvoiceDoesNotDoubleChargeTheFilingAdmin(t *testing.T) {
	ctx := context.Background()
	d := testDB(t)
	admin, member := "sk-team-admin-aaaaaaaaaa", "sk-team-member-bbbbbbbbb"
	wsID := seedInvoiceTeam(t, d, []string{admin, member}, []float64{300, 100})

	// 350 > the admin's own 300, so it spills onto the member: admin 300,
	// member 50.
	createTeamInvoice(t, d, wsID, admin, 350)

	s, err := d.InvoiceableCNY(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	// Locked, not just available: available floors at 0, so a double charge
	// would hide behind the floor on an admin whose share exhausts their pool.
	if !approx(s.LockedCNY, 300) {
		t.Errorf("admin locked = %.2f, want 300 (their allocation) — 650 means the face value was counted on top of it", s.LockedCNY)
	}
	if !approx(s.AvailableCNY, 0) {
		t.Errorf("admin available = %.2f, want 0", s.AvailableCNY)
	}
	if got := availCNY(t, d, member); !approx(got, 50) {
		t.Errorf("member available = %.2f, want 50 (100 paid, 50 allocated)", got)
	}

	// The same bug with room to spare, where it does reach AvailableCNY: a
	// second, smaller invoice fitting entirely inside one member's pool.
	d2 := testDB(t)
	a2 := "sk-solo-admin-aaaaaaaaaaa"
	ws2 := seedInvoiceTeam(t, d2, []string{a2}, []float64{300})
	createTeamInvoice(t, d2, ws2, a2, 150)
	if got := availCNY(t, d2, a2); !approx(got, 150) {
		t.Errorf("sole-member admin available = %.2f, want 150 (300 paid, 150 allocated)", got)
	}
}

// A member's personal quota really shrinks by their allocation, and the next
// personal request is refused on the reduced figure — the allocation is not a
// display-only annotation.
func TestTeamInvoiceConsumesMemberPersonalQuota(t *testing.T) {
	ctx := context.Background()
	d := testDB(t)
	admin, member := "sk-consume-admin-aaaaaaa", "sk-consume-member-bbbbbb"
	wsID := seedInvoiceTeam(t, d, []string{admin, member}, []float64{30, 100})

	createTeamInvoice(t, d, wsID, admin, 100) // admin 30, member 70

	if got := availCNY(t, d, member); !approx(got, 30) {
		t.Fatalf("member available = %.2f, want 30", got)
	}
	// Over the remainder → refused.
	if _, err := d.CreateInvoice(ctx, member, 30.01, testTitle(), "m@example.com"); !errors.Is(err, ErrInsufficientInvoiceable) {
		t.Fatalf("personal invoice over remaining quota: err = %v, want ErrInsufficientInvoiceable", err)
	}
	// Exactly the remainder → allowed.
	if _, err := d.CreateInvoice(ctx, member, 30, testTitle(), "m@example.com"); err != nil {
		t.Fatalf("personal invoice for exactly the remainder: %v", err)
	}
	if got := availCNY(t, d, member); !approx(got, 0) {
		t.Errorf("member available after personal invoice = %.2f, want 0", got)
	}
}

// Rejecting a team invoice returns every member's quota. Allocation rows stay
// on disk as the audit record; it is the status join that releases them.
func TestRejectedTeamInvoiceReleasesMemberQuota(t *testing.T) {
	ctx := context.Background()
	d := testDB(t)
	admin, member := "sk-reject-admin-aaaaaaaa", "sk-reject-member-bbbbbbb"
	wsID := seedInvoiceTeam(t, d, []string{admin, member}, []float64{100, 100})

	inv := createTeamInvoice(t, d, wsID, admin, 150)
	if got := availCNY(t, d, member); !approx(got, 50) {
		t.Fatalf("member available while pending = %.2f, want 50", got)
	}

	if _, err := d.MarkInvoiceRejected(ctx, inv.ID, "wrong title"); err != nil {
		t.Fatalf("MarkInvoiceRejected: %v", err)
	}
	if got := availCNY(t, d, admin); !approx(got, 100) {
		t.Errorf("admin available after reject = %.2f, want 100", got)
	}
	if got := availCNY(t, d, member); !approx(got, 100) {
		t.Errorf("member available after reject = %.2f, want 100", got)
	}
	// The audit trail survives the release.
	allocs, err := d.InvoiceAllocations(ctx, inv.ID)
	if err != nil {
		t.Fatalf("InvoiceAllocations: %v", err)
	}
	if len(allocs) != 2 {
		t.Errorf("allocations after reject = %d rows, want 2 (rows are immutable)", len(allocs))
	}
}

// An issued invoice keeps consuming quota — only rejection releases it.
func TestIssuedTeamInvoiceKeepsConsumingQuota(t *testing.T) {
	ctx := context.Background()
	d := testDB(t)
	admin := "sk-issued-admin-aaaaaaaa"
	wsID := seedInvoiceTeam(t, d, []string{admin}, []float64{100})

	inv := createTeamInvoice(t, d, wsID, admin, 60)
	if _, err := d.MarkInvoiceIssued(ctx, inv.ID, "/tmp/x.pdf", ""); err != nil {
		t.Fatalf("MarkInvoiceIssued: %v", err)
	}
	s, err := d.InvoiceableCNY(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	if !approx(s.IssuedCNY, 60) || !approx(s.LockedCNY, 0) || !approx(s.AvailableCNY, 40) {
		t.Errorf("summary = locked %.2f issued %.2f avail %.2f, want 0/60/40", s.LockedCNY, s.IssuedCNY, s.AvailableCNY)
	}
}

// The per-member breakdown must add up to the invoice's face value exactly.
// A finance team reads the itemised lines and the total side by side; a
// one-fen gap between them is the fastest way to lose their trust in the whole
// document.
func TestTeamInvoiceAllocationsSumToFaceValue(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		paid   []float64
		amount float64
	}{
		{"three even thirds of 100", []float64{33.34, 33.33, 33.33}, 100},
		{"partial last member", []float64{40, 40, 40}, 100},
		{"exact single member", []float64{100, 5, 5}, 100},
		{"awkward fen", []float64{0.07, 0.07, 0.07}, 0.21},
		{"whole pool to the fen", []float64{12.34, 56.78, 9.01}, 78.13},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := testDB(t)
			toks := make([]string, len(tc.paid))
			for i := range tc.paid {
				toks[i] = fmt.Sprintf("sk-sum-%s-%d-xxxxxxxx", tc.name[:5], i)
			}
			wsID := seedInvoiceTeam(t, d, toks, tc.paid)
			inv := createTeamInvoice(t, d, wsID, toks[0], tc.amount)

			allocs, err := d.InvoiceAllocations(ctx, inv.ID)
			if err != nil {
				t.Fatal(err)
			}
			var sum float64
			for _, a := range allocs {
				if a.CNYAmount <= 0 {
					t.Errorf("allocation for %s is %.4f — zero/negative shares must not be recorded", a.Token, a.CNYAmount)
				}
				sum += a.CNYAmount
			}
			if diff := sum - tc.amount; diff > 1e-9 || diff < -1e-9 {
				t.Fatalf("allocations sum to %.4f, invoice face value is %.2f", sum, tc.amount)
			}
		})
	}
}

// Greedy fill in member join order: the earliest members' payments are the ones
// invoiced. The order is part of the contract — the summary preview shows the
// same sequence, so what the admin sees is what they get.
//
// The tokens are deliberately in REVERSE lexical order to their join order, and
// the join timestamps are forced apart. Members added in one batch share a
// created_at (AddMember stamps whole Unix seconds), so a test that adds them
// back-to-back only ever exercises the `token ASC` tiebreak and would pass
// identically against an implementation that sorted by token alone.
func TestTeamInvoiceAllocatesInMemberJoinOrder(t *testing.T) {
	ctx := context.Background()
	d := testDB(t)
	first, second, third := "sk-order-z-zzzzzzzzzzzz", "sk-order-m-mmmmmmmmmmmm", "sk-order-a-aaaaaaaaaaaa"
	wsID := seedInvoiceTeam(t, d, []string{first, second, third}, []float64{50, 50, 50})
	for i, tok := range []string{first, second, third} {
		if _, err := d.ExecContext(ctx,
			`UPDATE workspace_members SET created_at = ? WHERE workspace_id = ? AND token = ?`,
			1700000000+i*3600, wsID, tok); err != nil {
			t.Fatalf("set join time for %s: %v", tok, err)
		}
	}

	inv := createTeamInvoice(t, d, wsID, first, 75)
	allocs, err := d.InvoiceAllocations(ctx, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(allocs) != 2 {
		t.Fatalf("allocations = %d, want 2 (first filled, second partial, third untouched)", len(allocs))
	}
	if allocs[0].Token != first || !approx(allocs[0].CNYAmount, 50) {
		t.Errorf("first allocation = %s %.2f, want %s 50 — allocation followed token order, not join order",
			allocs[0].Token, allocs[0].CNYAmount, first)
	}
	if allocs[1].Token != second || !approx(allocs[1].CNYAmount, 25) {
		t.Errorf("second allocation = %s %.2f, want %s 25", allocs[1].Token, allocs[1].CNYAmount, second)
	}
	if got := availCNY(t, d, third); !approx(got, 50) {
		t.Errorf("last-joined member available = %.2f, want 50 (untouched)", got)
	}
	// The preview the admin saw must list them in the same sequence, or the
	// breakdown they approve is not the one they get.
	s, err := d.WorkspaceInvoiceableCNY(ctx, wsID)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Members) != 3 || s.Members[0].Token != first || s.Members[1].Token != second || s.Members[2].Token != third {
		t.Errorf("summary member order = %+v, want join order [%s %s %s]", s.Members, first, second, third)
	}
}

// Concurrency: N admins (or N double-clicks) racing to file team invoices must
// never collectively issue more than the group ever paid. Mirrors
// TestChargeMemberFirst_ConcurrentCapRace — this read-modify-write over every
// member's quota is exactly the shape BEGIN IMMEDIATE exists to serialize.
func TestCreateWorkspaceInvoice_ConcurrentNoOverIssue(t *testing.T) {
	ctx := context.Background()
	d := testDB(t)
	toks := []string{"sk-race-a-aaaaaaaaaaaaa", "sk-race-b-bbbbbbbbbbbbb"}
	wsID := seedInvoiceTeam(t, d, toks, []float64{50, 50}) // 100 total

	const n = 10
	var wg sync.WaitGroup
	var mu sync.Mutex
	var issued float64
	var refused int
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// 10 × ¥20 against a ¥100 pool: exactly 5 must win.
			inv, err := d.CreateWorkspaceInvoice(ctx, wsID, toks[0], 20, testTitle(), "f@example.com")
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if errors.Is(err, ErrInsufficientInvoiceable) {
					refused++
					return
				}
				t.Errorf("CreateWorkspaceInvoice: %v", err)
				return
			}
			issued += inv.CNYAmount
		}()
	}
	wg.Wait()

	if issued > 100+invoiceEpsilon {
		t.Fatalf("issued ¥%.2f against a ¥100 pool — quota race not serialized", issued)
	}
	if !approx(issued, 100) || refused != 5 {
		t.Fatalf("issued ¥%.2f in %d invoices (refused %d), want ¥100 / 5 refused", issued, n-refused, refused)
	}
	// And the pool is genuinely spent, per member.
	for _, tok := range toks {
		if got := availCNY(t, d, tok); !approx(got, 0) {
			t.Errorf("%s available = %.2f, want 0", tok, got)
		}
	}
}

// A refused request must leave nothing behind. The invoice row and its
// allocations go in as one transaction, so a shortfall discovered mid-way
// cannot leave a phantom pending invoice quietly holding everyone's quota.
func TestCreateWorkspaceInvoiceInsufficientLeavesNoRows(t *testing.T) {
	ctx := context.Background()
	d := testDB(t)
	admin, member := "sk-short-admin-aaaaaaaa", "sk-short-member-bbbbbbb"
	wsID := seedInvoiceTeam(t, d, []string{admin, member}, []float64{10, 10})

	_, err := d.CreateWorkspaceInvoice(ctx, wsID, admin, 25, testTitle(), "f@example.com")
	if !errors.Is(err, ErrInsufficientInvoiceable) {
		t.Fatalf("err = %v, want ErrInsufficientInvoiceable", err)
	}
	// The message has to carry the real group total, or the admin is left
	// guessing what amount would work.
	if want := "available 20.00"; err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("error %q does not report the true available total (%q)", err, want)
	}

	var invoices, allocs int
	if err := d.QueryRowContext(ctx, `SELECT COUNT(*) FROM invoices`).Scan(&invoices); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRowContext(ctx, `SELECT COUNT(*) FROM invoice_allocations`).Scan(&allocs); err != nil {
		t.Fatal(err)
	}
	if invoices != 0 || allocs != 0 {
		t.Fatalf("after a refused request: %d invoice rows, %d allocation rows — want 0/0", invoices, allocs)
	}
	// Quota untouched.
	for _, tok := range []string{admin, member} {
		if got := availCNY(t, d, tok); !approx(got, 10) {
			t.Errorf("%s available = %.2f, want 10", tok, got)
		}
	}
}

// The workspace summary is the sum of its members' individual pools, and its
// member list is in the same order allocation will consume them.
func TestWorkspaceInvoiceableCNY(t *testing.T) {
	ctx := context.Background()
	d := testDB(t)
	a, b := "sk-wssum-a-aaaaaaaaaaaa", "sk-wssum-b-bbbbbbbbbbbb"
	wsID := seedInvoiceTeam(t, d, []string{a, b}, []float64{80, 20})

	createTeamInvoice(t, d, wsID, a, 90) // a: 80, b: 10

	s, err := d.WorkspaceInvoiceableCNY(ctx, wsID)
	if err != nil {
		t.Fatal(err)
	}
	if !approx(s.Total.PaidCNY, 100) || !approx(s.Total.LockedCNY, 90) || !approx(s.Total.AvailableCNY, 10) {
		t.Errorf("total = paid %.2f locked %.2f avail %.2f, want 100/90/10",
			s.Total.PaidCNY, s.Total.LockedCNY, s.Total.AvailableCNY)
	}
	if len(s.Members) != 2 || s.Members[0].Token != a || s.Members[1].Token != b {
		t.Fatalf("members = %+v, want [%s %s] in join order", s.Members, a, b)
	}
	if !approx(s.Members[0].AvailableCNY, 0) || !approx(s.Members[1].AvailableCNY, 10) {
		t.Errorf("member avail = %.2f / %.2f, want 0 / 10",
			s.Members[0].AvailableCNY, s.Members[1].AvailableCNY)
	}
}

// A member's own history shows the team invoices that spent their quota —
// including for the filing admin, whose personal history must NOT report the
// face value as their own spend.
func TestListInvoicesByTokenIncludesTeamInvoices(t *testing.T) {
	ctx := context.Background()
	d := testDB(t)
	admin, member := "sk-hist-admin-aaaaaaaaa", "sk-hist-member-bbbbbbbb"
	wsID := seedInvoiceTeam(t, d, []string{admin, member}, []float64{100, 100})
	createTeamInvoice(t, d, wsID, admin, 150) // admin 100, member 50

	for _, tc := range []struct {
		token string
		want  float64
	}{{admin, 100}, {member, 50}} {
		rows, err := d.ListInvoicesByToken(ctx, tc.token, 100)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 {
			t.Fatalf("%s: %d rows, want 1", tc.token, len(rows))
		}
		r := rows[0]
		if !r.IsTeam() || r.WorkspaceID != wsID || r.WorkspaceName != "acme" {
			t.Errorf("%s: scope = ws %d %q, want team ws %d acme", tc.token, r.WorkspaceID, r.WorkspaceName, wsID)
		}
		if !approx(r.CNYAmount, 150) {
			t.Errorf("%s: face value = %.2f, want 150", tc.token, r.CNYAmount)
		}
		if !approx(r.AllocatedCNY, tc.want) {
			t.Errorf("%s: allocated = %.2f, want %.2f", tc.token, r.AllocatedCNY, tc.want)
		}
	}
}

// allocateInvoice, exercised directly. Everything above reaches it through
// CreateWorkspaceInvoice, which can only ever hand it two-decimal avail values;
// this is the only place the function's own contract — greedy in order, no
// zero shares, shares sum to the face value — can be pinned against inputs the
// DB layer does not currently produce.
func TestAllocateInvoiceDirect(t *testing.T) {
	members := func(n int) []invoiceMember {
		out := make([]invoiceMember, n)
		for i := range out {
			out[i] = invoiceMember{Token: fmt.Sprintf("tok-%d", i)}
		}
		return out
	}
	for _, tc := range []struct {
		name  string
		avail []float64
		cny   float64
		want  []float64 // per member, in order; trailing untouched members omitted
	}{
		{"first member covers it", []float64{100, 100}, 40, []float64{40}},
		{"spills to the second", []float64{50, 50, 50}, 75, []float64{50, 25}},
		{"exactly the pool", []float64{10, 20}, 30, []float64{10, 20}},
		// A zero-balance member in the middle must be skipped outright, not
		// recorded as a ¥0.00 line on the fapiao's breakdown.
		{"skips empty members", []float64{10, 0, 10}, 15, []float64{10, 0, 5}},
		{"leading empty member", []float64{0, 25}, 20, []float64{0, 20}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := allocateInvoice(members(len(tc.avail)), tc.avail, tc.cny)
			var sum float64
			for _, a := range got {
				if a.CNYAmount <= 0 {
					t.Errorf("recorded a non-positive share %.4f for %s", a.CNYAmount, a.Token)
				}
				sum += a.CNYAmount
			}
			if !approx(sum, tc.cny) {
				t.Fatalf("shares sum to %.4f, face value is %.2f", sum, tc.cny)
			}
			byTok := map[string]float64{}
			for _, a := range got {
				byTok[a.Token] = a.CNYAmount
			}
			for i, want := range tc.want {
				if got := byTok[fmt.Sprintf("tok-%d", i)]; !approx(got, want) {
					t.Errorf("member %d got %.4f, want %.4f", i, got, want)
				}
			}
		})
	}
}

// The residue guard, reached the only way it can be: an avail carrying more
// than two decimals, which round2 then shaves off every share. Unreachable from
// CreateWorkspaceInvoice today (invoiceSummaryFor already rounds), so this
// input is synthetic on purpose — it is the regression that would appear if a
// sub-fen paid amount or a pro-rata split ever fed this function.
func TestAllocateInvoiceAbsorbsResidue(t *testing.T) {
	members := []invoiceMember{{Token: "a"}, {Token: "b"}, {Token: "c"}}
	// Each take rounds down to 0.33, so the greedy shares total 0.99 against a
	// ¥1.00 face value. Without the correction the breakdown is a fen short.
	got := allocateInvoice(members, []float64{0.333, 0.333, 0.334}, 1.00)
	if len(got) != 3 {
		t.Fatalf("allocations = %+v, want 3", got)
	}
	var sum float64
	for _, a := range got {
		sum += a.CNYAmount
	}
	if !approx(sum, 1.00) {
		t.Fatalf("shares sum to %.4f, want exactly 1.00 — the residue was not absorbed", sum)
	}
	if !approx(got[2].CNYAmount, 0.34) {
		t.Errorf("last share = %.4f, want 0.34 (0.33 + the 0.01 residue)", got[2].CNYAmount)
	}
}

// signedRound2 exists for the residue correction's negative direction, which
// round2 gets wrong: round2 adds 0.5 unconditionally, so a negative input is
// rounded toward zero — -0.015 would land on -0.01 instead of -0.02. Cases are
// chosen away from exact .xx5 ties, where the float64 literal itself decides
// the answer (1.005*100 is 100.4999…, so round2 already reads it as 1.00);
// that is round2's long-standing behaviour across the whole billing layer and
// not something the sign wrapper is meant to change.
func TestSignedRound2(t *testing.T) {
	for _, tc := range []struct{ in, want float64 }{
		{0.014, 0.01}, {0.016, 0.02}, {-0.014, -0.01}, {-0.016, -0.02},
		{0, 0}, {-1.234, -1.23}, {1.236, 1.24},
	} {
		if got := signedRound2(tc.in); !approx(got, tc.want) {
			t.Errorf("signedRound2(%.4f) = %.4f, want %.4f", tc.in, got, tc.want)
		}
	}
}

// A personal invoice keeps reporting its own face value as the allocation, so
// the two scopes render through one code path.
func TestListInvoicesByTokenPersonalAllocationIsFaceValue(t *testing.T) {
	ctx := context.Background()
	d := testDB(t)
	tok := "sk-personal-aaaaaaaaaaaa"
	seedPaidCNY(t, d, tok, 50)
	if _, err := d.CreateInvoice(ctx, tok, 20, testTitle(), "p@example.com"); err != nil {
		t.Fatal(err)
	}
	rows, err := d.ListInvoicesByToken(ctx, tok, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].IsTeam() || !approx(rows[0].AllocatedCNY, 20) || rows[0].WorkspaceName != "" {
		t.Fatalf("personal row = %+v, want 1 personal row allocated 20", rows)
	}
}

// TestRekeyTokenMovesInvoiceState pins the invoice trio to the rekey.
//
// The quota is computed as paid(alipay_orders) − pending − issued(invoices +
// invoice_allocations). alipay_orders always moved with the token, so leaving
// the invoice rows behind did not merely lose history — it handed the rotated
// token its whole paid amount back as fresh quota, letting everything already
// invoiced be invoiced a second time.
func TestRekeyTokenMovesInvoiceState(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	const admin = "sk-rekey-inv-admin-xxxxxxxx"
	const member = "sk-rekey-inv-member-yyyyyyy"
	const newTok = "sk-rekey-inv-member-new-zzzz"

	// Member has 100 paid: 40 goes to a personal invoice. The team invoice is
	// sized to overflow the admin's own 10, so 20 of it lands on the member as
	// an allocation row. 40 should remain invoiceable — before and after.
	ws := seedInvoiceTeam(t, d, []string{admin, member}, []float64{10, 100})
	if _, err := d.CreateInvoice(ctx, member, 40, testTitle(), "who@example.com"); err != nil {
		t.Fatalf("CreateInvoice: %v", err)
	}
	createTeamInvoice(t, d, ws, admin, 30)
	before := availCNY(t, d, member)

	rep, err := d.RekeyToken(ctx, member, newTok)
	if err != nil {
		t.Fatalf("RekeyToken: %v", err)
	}
	if rep.InvoiceRowsAffected != 1 {
		t.Fatalf("invoices moved = %d, want 1", rep.InvoiceRowsAffected)
	}
	if rep.InvoiceAllocRowsAffected != 1 {
		t.Fatalf("invoice_allocations moved = %d, want 1", rep.InvoiceAllocRowsAffected)
	}
	// CreateInvoice books the header into the member's shortlist as a side
	// effect; the team invoice's went to the filing admin instead.
	if rep.InvoiceTitleRowsAffected != 1 {
		t.Fatalf("invoice_titles moved = %d, want 1", rep.InvoiceTitleRowsAffected)
	}

	// The quota is carried over exactly, not reset to the paid total.
	if after := availCNY(t, d, newTok); !approx(after, before) {
		t.Fatalf("quota after rekey = %.2f, want %.2f (the pre-rekey figure)", after, before)
	}
	if got := availCNY(t, d, member); !approx(got, 0) {
		t.Fatalf("old token still has %.2f of quota after rekey, want 0", got)
	}

	// History and the saved-title shortlist follow the token too.
	invs, err := d.ListInvoicesByToken(ctx, newTok, 50)
	if err != nil {
		t.Fatalf("ListInvoicesByToken: %v", err)
	}
	if len(invs) != 2 {
		t.Fatalf("new token sees %d invoices, want 2 (one personal, one team share)", len(invs))
	}
	titles, err := d.ListInvoiceTitles(ctx, newTok, "", 0)
	if err != nil {
		t.Fatalf("ListInvoiceTitles: %v", err)
	}
	if len(titles) == 0 {
		t.Fatal("saved invoice titles did not follow the rotated token")
	}
}

// A destination carrying invoice state is refused before anything is touched,
// matching how an occupied wallet or workspace membership is handled — the
// UNIQUE(token, name) on invoice_titles would otherwise surface as a
// constraint error from inside the transaction.
func TestRekeyTokenRefusesOccupiedInvoiceState(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	const old = "sk-rekey-occupied-old-xxxxxx"
	const dst = "sk-rekey-occupied-dst-yyyyyy"
	seedPaidCNY(t, d, old, 100)
	seedPaidCNY(t, d, dst, 50)
	if _, err := d.CreateInvoice(ctx, dst, 10, testTitle(), "who@example.com"); err != nil {
		t.Fatalf("CreateInvoice: %v", err)
	}
	if _, err := d.RekeyToken(ctx, old, dst); err == nil {
		t.Fatal("rekey onto a token with existing invoices should be refused")
	}
	// Nothing moved: the source keeps its full quota.
	if got := availCNY(t, d, old); !approx(got, 100) {
		t.Fatalf("source quota = %.2f after a refused rekey, want 100", got)
	}
}

// TestRekeyTokenCountsUnderWriteLock is the regression for the bug that made
// reset unusable on exactly the tokens that needed it.
//
// The row counts used to be read before BeginTx. A charge settling in the gap
// left the UPDATE touching one row more than had been counted, the
// conservation check called that corruption, and the rotation was refused —
// deterministically, for any token with live traffic.
//
// The interleaving is forced rather than raced: a competing writer holds the
// write lock, RekeyToken blocks at BEGIN, and the writer commits a new ledger
// row before letting go. Counting inside the lock sees that row; counting
// outside it does not.
func TestRekeyTokenCountsUnderWriteLock(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	const old = "sk-rekey-race-old-xxxxxxxxxx"
	const newTok = "sk-rekey-race-new-yyyyyyyyyy"
	if _, err := d.AddBalance(ctx, old, TxKindTopup, 10, "seed", "seed", true); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Competing writer takes the write lock and holds it.
	blocker, err := d.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	if _, err := blocker.ExecContext(ctx,
		`INSERT INTO wallet_tx (token, kind, amount_usd, ref, note, created_at)
		 VALUES (?, ?, ?, '', '', ?)`,
		old, TxKindCharge, -0.5, time.Now().Unix()); err != nil {
		t.Fatalf("competing insert: %v", err)
	}

	done := make(chan *RekeyTokenReport, 1)
	errc := make(chan error, 1)
	go func() {
		rep, err := d.RekeyToken(ctx, old, newTok)
		if err != nil {
			errc <- err
			return
		}
		done <- rep
	}()

	// Let the rekey reach BEGIN and block there, then release the row it
	// must account for.
	time.Sleep(300 * time.Millisecond)
	if err := blocker.Commit(); err != nil {
		t.Fatalf("blocker commit: %v", err)
	}

	select {
	case err := <-errc:
		t.Fatalf("rekey refused a token that was merely busy: %v", err)
	case rep := <-done:
		// Two ledger rows: the seed topup and the charge the competing
		// writer committed while the rekey waited.
		if rep.WalletTxRowsAffected != 2 {
			t.Fatalf("wallet_tx moved = %d, want 2 (the row written during the wait must move too)",
				rep.WalletTxRowsAffected)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("rekey never returned")
	}

	var leftBehind int64
	if err := d.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM wallet_tx WHERE token = ?`, old).Scan(&leftBehind); err != nil {
		t.Fatal(err)
	}
	if leftBehind != 0 {
		t.Fatalf("%d ledger rows stranded on the old token", leftBehind)
	}
}
