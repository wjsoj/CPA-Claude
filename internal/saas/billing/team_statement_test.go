package billing

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/wjsoj/CPA-Claude/internal/config"
	"github.com/wjsoj/CPA-Claude/internal/saas/db"
	"github.com/wjsoj/CPA-Claude/internal/statement"
	"github.com/wjsoj/CPA-Claude/internal/tokenmask"
	"github.com/wjsoj/cc-core/requestlog"
)

type teamStatementResp struct {
	Workspace struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	} `json:"workspace"`
	From          string  `json:"from"`
	To            string  `json:"to"`
	Timezone      string  `json:"timezone"`
	CNYPerUSD     float64 `json:"cny_per_usd"`
	Requests      int64   `json:"requests"`
	BilledCNY     float64 `json:"billed_cny"`
	UnitemisedCNY float64 `json:"unitemised_cny"`
	ChargedCNY    float64 `json:"charged_cny"`
	MemberCount   int     `json:"member_count"`
	DetailLines   int64   `json:"detail_lines"`
	Truncated     bool    `json:"truncated"`
	Partial       bool    `json:"partial"`
	ByMember      []struct {
		Masked            string  `json:"masked"`
		Label             string  `json:"label"`
		Requests          int64   `json:"requests"`
		BilledCNY         float64 `json:"billed_cny"`
		Share             float64 `json:"share"`
		PoolLedgerCNY     float64 `json:"pool_ledger_cny"`
		PersonalLedgerCNY float64 `json:"personal_ledger_cny"`
	} `json:"by_member"`
	ByModel []struct {
		Model     string  `json:"model"`
		Requests  int64   `json:"requests"`
		BilledCNY float64 `json:"billed_cny"`
	} `json:"by_model"`
}

func teamStatement(t *testing.T, e *gin.Engine, tok string, body any) teamStatementResp {
	t.Helper()
	w := do(e, "POST", "/api/team/statement", tok, body)
	if w.Code != http.StatusOK {
		t.Fatalf("statement → %d (%s)", w.Code, w.Body.String())
	}
	var out teamStatementResp
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// (a) The group's headline must equal the sum of what each member would see on
// their own per-token statement. That document reads its range total off the
// same cube filter (masked token + day labels + Limit 1), so this recomputes it
// exactly that way and compares — if the group ever started summing something
// else (the fleet-wide Summary, say, or workspace_tx), the two would diverge
// and the invoice attachment would stop matching what members can verify.
func TestTeamStatementEqualsSumOfPerTokenStatements(t *testing.T) {
	e, _, logDir := newUsageTeam(t)
	now := time.Now().UTC().Add(-2 * time.Hour)
	writeLog(t, logDir, []requestlog.Record{
		rec(now, tokAdmin, "claude-opus-4-7", 3),
		rec(now, tokAdmin, "claude-opus-4-7", 1.5),
		rec(now, tokMember, "claude-sonnet-5", 7),
		// An outsider on the same relay: their spend must stay out of the team's.
		rec(now, "sk-other-9999999999999999999999999999zzzz", "claude-opus-4-7", 99),
	})
	today := day(now)

	got := teamStatement(t, e, tokAdmin, map[string]any{"from": today, "to": today})
	if got.CNYPerUSD != config.DefaultCNYPerUSD {
		t.Fatalf("rate = %v, want the compiled-in fallback %v", got.CNYPerUSD, config.DefaultCNYPerUSD)
	}
	rate := got.CNYPerUSD

	var want float64
	var wantReq int64
	for _, tok := range []string{tokAdmin, tokMember} {
		res, err := requestlog.Query(requestlog.Filter{
			Dir: logDir, ClientToken: tokenmask.Mask(tok),
			FromDay: today, ToDay: today, Limit: 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		want += res.Summary.BilledUSD * rate
		wantReq += res.Summary.Count
	}
	if !approx(got.BilledCNY, want) {
		t.Fatalf("group total = %v, sum of per-token statements = %v", got.BilledCNY, want)
	}
	if got.Requests != wantReq {
		t.Fatalf("group requests = %d, sum of per-token = %d", got.Requests, wantReq)
	}
	if !approx(got.BilledCNY, 11.5*rate) {
		t.Fatalf("group total = %v, want the team's 11.5 USD converted (not the outsider's 99)", got.BilledCNY)
	}

	// The member table has to add up to the headline, or a reader checking the
	// invoice against the rows lands on a discrepancy the document never
	// explains.
	var sum float64
	var share float64
	for _, m := range got.ByMember {
		sum += m.BilledCNY
		share += m.Share
	}
	if !approx(sum, got.BilledCNY) {
		t.Fatalf("by_member sums to %v, headline says %v", sum, got.BilledCNY)
	}
	if !approx(share, 1) {
		t.Fatalf("member shares sum to %v, want 1", share)
	}
	if got.MemberCount != 2 {
		t.Fatalf("member_count = %d, want 2", got.MemberCount)
	}
	// And so does the model table.
	sum = 0
	for _, m := range got.ByModel {
		sum += m.BilledCNY
	}
	if !approx(sum, got.BilledCNY) {
		t.Fatalf("by_model sums to %v, headline says %v", sum, got.BilledCNY)
	}
}

// (b) A group statement shows other people's spend, so it needs the
// workspace-admin judgement — not merely possession of a token that happens to
// belong to the team.
func TestTeamStatementRequiresGroupAdmin(t *testing.T) {
	e, _, _ := newUsageTeam(t)
	for _, tc := range []struct {
		name, tok string
		want      int
	}{
		{"anonymous", "", http.StatusUnauthorized},
		{"plain member of this very team", tokMember, http.StatusForbidden},
		{"outsider", "sk-nobody-000000000000000000000000", http.StatusForbidden},
	} {
		for _, path := range []string{"/api/team/statement", "/api/team/statement.pdf"} {
			w := do(e, "POST", path, tc.tok, map[string]any{})
			if w.Code != tc.want {
				t.Errorf("%s on %s → %d, want %d (%s)", tc.name, path, w.Code, tc.want, w.Body.String())
			}
		}
	}
	if w := do(e, "POST", "/api/team/statement", tokAdmin, map[string]any{}); w.Code != http.StatusOK {
		t.Fatalf("admin → %d (%s)", w.Code, w.Body.String())
	}
}

// (c) Attribution is the whole point of a group listing: an amount nobody can
// assign to a person is not a reimbursement record. This drives the real
// fan-out and merge, then checks each line landed on the member who made it.
func TestTeamStatementDetailLinesCarryTheRightMember(t *testing.T) {
	e, d, logDir := newUsageTeam(t)
	now := time.Now().UTC().Add(-3 * time.Hour)
	writeLog(t, logDir, []requestlog.Record{
		rec(now, tokAdmin, "claude-opus-4-7", 3),
		rec(now.Add(time.Minute), tokMember, "claude-sonnet-5", 7),
		rec(now.Add(2*time.Minute), tokMember, "claude-sonnet-5", 1),
		rec(now, "sk-other-9999999999999999999999999999zzzz", "claude-opus-4-7", 99),
	})
	today := day(now)

	ctx := context.Background()
	ws, err := d.WorkspaceAdminFor(ctx, tokAdmin)
	if err != nil {
		t.Fatal(err)
	}
	gu, err := BuildGroupUsage(ctx, GroupUsageQuery{
		Wallets: d, LogDir: logDir, LogIndexed: true, WorkspaceID: ws.ID, FromDay: today, ToDay: today,
	})
	if err != nil {
		t.Fatal(err)
	}
	rate := config.DefaultCNYPerUSD
	lines, truncated, err := groupDetailLines(logDir, gu, requestlog.BucketLocation(), rate)
	if err != nil {
		t.Fatal(err)
	}
	if truncated {
		t.Fatal("3 rows is not truncated")
	}
	g := struct {
		Lines     []statement.Line
		CNYPerUSD float64
		BilledCNY float64
	}{lines, rate, gu.Total.BilledUSD * rate}
	if len(g.Lines) != 3 {
		t.Fatalf("got %d lines, want the team's 3 (and not the outsider's)", len(g.Lines))
	}
	byMember := map[string][]float64{}
	for _, ln := range g.Lines {
		if ln.Member == "" {
			t.Fatalf("line %+v has no member attribution", ln)
		}
		byMember[ln.Member] = append(byMember[ln.Member], ln.BilledCNY)
	}
	adminLines := byMember[tokenmask.Mask(tokAdmin)]
	memberLines := byMember[tokenmask.Mask(tokMember)]
	if len(adminLines) != 1 || !approx(adminLines[0], 3*rate) {
		t.Fatalf("admin's lines = %v, want one at %v", adminLines, 3*rate)
	}
	if len(memberLines) != 2 {
		t.Fatalf("member's lines = %v, want two", memberLines)
	}
	if !approx(memberLines[0]+memberLines[1], 8*rate) {
		t.Fatalf("member's lines sum to %v, want %v", memberLines[0]+memberLines[1], 8*rate)
	}
	// Chronological, so the page reads forward.
	for i := 1; i < len(g.Lines); i++ {
		if g.Lines[i].TS.Before(g.Lines[i-1].TS) {
			t.Fatalf("lines not in chronological order: %v", g.Lines)
		}
	}
	// The listing is a listing; the totals come from the cube either way.
	if !approx(g.BilledCNY, 11*rate) {
		t.Fatalf("range total = %v, want %v", g.BilledCNY, 11*rate)
	}

	// And the PDF renders end to end with those rows on it.
	w := do(e, "POST", "/api/team/statement.pdf", tokAdmin,
		map[string]any{"from": today, "to": today, "detail": "full"})
	if w.Code != http.StatusOK {
		t.Fatalf("pdf → %d (%s)", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/pdf" {
		t.Fatalf("content-type = %q", ct)
	}
	if w.Body.Len() < 1000 {
		t.Fatalf("pdf is %d bytes", w.Body.Len())
	}
}

// (d) A range with no traffic must still render — a team created this morning
// exporting last month is not an error — and when the ledger holds a debit the
// log cannot account for, the document has to carry the gap rather than close
// on a silent ¥0.00.
func TestTeamStatementEmptyRangeStillRendersAndReconciles(t *testing.T) {
	e, d, _ := newUsageTeam(t)
	ctx := context.Background()
	// A charge on the member's own wallet with no request-log row behind it:
	// exactly what a retention prune or a lost log line leaves behind.
	if _, err := d.AddBalance(ctx, tokMember, db.TxKindTopup, 10, "seed", "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := d.AddBalance(ctx, tokMember, db.TxKindCharge, -2, "ref", "", true); err != nil {
		t.Fatal(err)
	}
	today := day(time.Now().UTC())

	got := teamStatement(t, e, tokAdmin, map[string]any{"from": today, "to": today})
	if got.Requests != 0 || got.BilledCNY != 0 {
		t.Fatalf("empty range reports %d requests / %v", got.Requests, got.BilledCNY)
	}
	rate := got.CNYPerUSD
	if !approx(got.UnitemisedCNY, 2*rate) {
		t.Fatalf("unitemised = %v, want the ledger-only charge %v", got.UnitemisedCNY, 2*rate)
	}
	if !approx(got.ChargedCNY, 2*rate) {
		t.Fatalf("charged = %v, want %v", got.ChargedCNY, 2*rate)
	}
	// The member row carries the personal half of that debit, so the console
	// can show who it belonged to.
	var personal float64
	for _, m := range got.ByMember {
		personal += m.PersonalLedgerCNY
	}
	if !approx(personal, 2*rate) {
		t.Fatalf("per-member personal spend = %v, want %v", personal, 2*rate)
	}
	if got.MemberCount == 0 {
		t.Fatal("an empty range still has a roster")
	}

	// And the PDF renders.
	w := do(e, "POST", "/api/team/statement.pdf", tokAdmin, map[string]any{"from": today, "to": today})
	if w.Code != http.StatusOK {
		t.Fatalf("pdf on an empty range → %d (%s)", w.Code, w.Body.String())
	}
	if w.Body.Len() < 1000 {
		t.Fatalf("pdf is %d bytes", w.Body.Len())
	}
}

// The window contract is the one /api/team/usage already publishes, and a
// timestamp is refused rather than silently costing a full row scan.
func TestTeamStatementWindowContract(t *testing.T) {
	e, _, _ := newUsageTeam(t)
	for _, body := range []map[string]any{
		{"from": "2026-08-01T00:00:00Z"},
		{"from": "2026-08-10", "to": "2026-08-01"},
		{"detail": "everything"},
		{"target_cny": 500},
	} {
		if w := do(e, "POST", "/api/team/statement", tokAdmin, body); w.Code != http.StatusBadRequest {
			t.Errorf("%v → %d, want 400 (%s)", body, w.Code, w.Body.String())
		}
	}
	// No body at all is a valid request: the default window, summary detail.
	if w := do(e, "POST", "/api/team/statement", tokAdmin, nil); w.Code != http.StatusOK {
		t.Fatalf("empty body → %d (%s)", w.Code, w.Body.String())
	}
}

// The preview must report the row count a full export would print without
// collecting the rows — otherwise the dialog cannot warn about truncation until
// after the download.
func TestTeamStatementPreviewReportsListingSizeWithoutCollecting(t *testing.T) {
	e, _, logDir := newUsageTeam(t)
	now := time.Now().UTC().Add(-time.Hour)
	recs := make([]requestlog.Record, 0, 5)
	for i := 0; i < 5; i++ {
		recs = append(recs, rec(now.Add(time.Duration(i)*time.Minute), tokMember, "m", 1))
	}
	writeLog(t, logDir, recs)
	today := day(now)

	got := teamStatement(t, e, tokAdmin, map[string]any{"from": today, "to": today, "detail": "full"})
	if got.DetailLines != 5 {
		t.Fatalf("detail_lines = %d, want 5", got.DetailLines)
	}
	if got.Truncated {
		t.Fatal("5 rows is not truncated")
	}
	if statement.MaxDetailLines < 5 {
		t.Skip("cap lowered under the fixture")
	}
}

// The listing is capped for the document as a whole, and the rows it keeps are
// the newest. Both halves matter: a per-member cap would produce a PDF with
// tens of thousands of rows, and keeping the oldest would open the listing on
// the far edge of the range while the heading promises "最近".
//
// trimNewest is what enforces it after every merge, which is also what keeps
// peak memory at a few thousand rows instead of the cap per member.
func TestTrimNewestKeepsTheNewestUpToTheCap(t *testing.T) {
	base := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	lines := make([]statement.Line, 0, statement.MaxDetailLines+10)
	for i := 0; i < statement.MaxDetailLines+10; i++ {
		lines = append(lines, statement.Line{TS: base.Add(time.Duration(i) * time.Minute)})
	}
	trimNewest(&lines)
	if len(lines) != statement.MaxDetailLines {
		t.Fatalf("kept %d lines, want the cap %d", len(lines), statement.MaxDetailLines)
	}
	oldestKept := lines[0].TS
	for _, ln := range lines {
		if ln.TS.Before(oldestKept) {
			oldestKept = ln.TS
		}
	}
	// The ten oldest are exactly what must have been dropped.
	if want := base.Add(10 * time.Minute); !oldestKept.Equal(want) {
		t.Fatalf("oldest kept row is %v, want %v — the cap dropped the wrong end", oldestKept, want)
	}

	// Under the cap it is a no-op, order included: the merge relies on that to
	// stay cheap when a team has ordinary traffic.
	short := []statement.Line{{TS: base.Add(time.Hour)}, {TS: base}}
	trimNewest(&short)
	if len(short) != 2 || !short[0].TS.Equal(base.Add(time.Hour)) {
		t.Fatalf("under-cap slice was disturbed: %v", short)
	}
}

// A team big enough to overflow the listing must still get a correct document:
// the cap bites, it says so, and the totals — which come from the cube, not
// from the rows — stay the range's own. This is the case a per-member cap would
// get wrong by printing MaxDetailLines rows per person.
func TestTeamStatementListingCapIsPerDocument(t *testing.T) {
	_, d, logDir := newUsageTeam(t)
	now := time.Now().UTC().Add(-6 * time.Hour)
	n := statement.MaxDetailLines/2 + 100 // per member, so the pair overflows
	recs := make([]requestlog.Record, 0, 2*n)
	for i := 0; i < n; i++ {
		at := now.Add(time.Duration(i) * time.Second)
		recs = append(recs, rec(at, tokAdmin, "claude-opus-4-7", 0.01))
		recs = append(recs, rec(at, tokMember, "claude-sonnet-5", 0.01))
	}
	writeLog(t, logDir, recs)
	today := day(now)

	ctx := context.Background()
	ws, err := d.WorkspaceAdminFor(ctx, tokAdmin)
	if err != nil {
		t.Fatal(err)
	}
	gu, err := BuildGroupUsage(ctx, GroupUsageQuery{
		Wallets: d, LogDir: logDir, LogIndexed: true, WorkspaceID: ws.ID, FromDay: today, ToDay: today,
	})
	if err != nil {
		t.Fatal(err)
	}
	lines, truncated, err := groupDetailLines(logDir, gu, requestlog.BucketLocation(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != statement.MaxDetailLines {
		t.Fatalf("listing holds %d rows, want the document cap %d", len(lines), statement.MaxDetailLines)
	}
	if !truncated {
		t.Fatal("a listing shorter than the range must report itself truncated")
	}
	// Both members survive the cap: a merge that trimmed per member instead
	// would drop whoever finished last.
	seen := map[string]bool{}
	for _, ln := range lines {
		seen[ln.Member] = true
	}
	if len(seen) != 2 {
		t.Fatalf("cap left rows from %d members, want both", len(seen))
	}
	// The totals are the range's, untouched by truncation.
	if gu.Total.Count != int64(2*n) {
		t.Fatalf("range count = %d, want %d", gu.Total.Count, 2*n)
	}
}

// (e) Mode A, the pool half of the reconciliation. A team that funds a pool has
// most of its money in workspace_tx, and reading only the personal ledger would
// report that money as missing — turning a correctly itemised statement into one
// that claims a large "未能明细化的消费".
func TestTeamStatementReconcilesBothLedgers(t *testing.T) {
	e, d, logDir := newUsageTeam(t)
	ctx := context.Background()
	ws, err := d.WorkspaceAdminFor(ctx, tokAdmin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.AdjustWorkspaceBalance(ctx, ws.ID, 100, db.TxKindTopup, "seed", ""); err != nil {
		t.Fatal(err)
	}
	// A cap of 4 splits a 10 USD charge into 4 from the pool and 6 from the
	// member's own wallet — the fallback workspace_tx never records.
	if err := d.UpdateMember(ctx, tokMember, nil, ptrF(4), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := d.AddBalance(ctx, tokMember, db.TxKindTopup, 50, "seed", "", true); err != nil {
		t.Fatal(err)
	}
	pool, personal, err := d.ChargeMemberFirst(ctx, tokMember, 10, "ref", "spend")
	if err != nil {
		t.Fatal(err)
	}
	if !approx(pool, 4) || !approx(personal, 6) {
		t.Fatalf("charge split = %v / %v, want 4 / 6", pool, personal)
	}
	// The log carries only part of it, so the gap is the rest — and the gap must
	// be measured against BOTH ledgers, not just the personal one.
	now := time.Now().UTC()
	writeLog(t, logDir, []requestlog.Record{rec(now, tokMember, "claude-opus-4-7", 1)})
	today := day(now)

	got := teamStatement(t, e, tokAdmin, map[string]any{"from": today, "to": today})
	rate := got.CNYPerUSD
	if !approx(got.BilledCNY, 1*rate) {
		t.Fatalf("itemised total = %v, want the log's 1 USD converted", got.BilledCNY)
	}
	if !approx(got.ChargedCNY, 10*rate) {
		t.Fatalf("charged = %v, want (4 pool + 6 personal) * %v", got.ChargedCNY, rate)
	}
	if !approx(got.UnitemisedCNY, 9*rate) {
		t.Fatalf("unitemised = %v, want the 9 USD the log cannot account for", got.UnitemisedCNY)
	}
	var poolSum, personalSum float64
	for _, m := range got.ByMember {
		poolSum += m.PoolLedgerCNY
		personalSum += m.PersonalLedgerCNY
	}
	if !approx(poolSum, 4*rate) || !approx(personalSum, 6*rate) {
		t.Fatalf("member ledger columns = %v pool / %v personal, want %v / %v",
			poolSum, personalSum, 4*rate, 6*rate)
	}
}

// (f) Masks are not identities. Every token of ten bytes or fewer masks to the
// same opaque string, so joining the ledger to the member rows by mask hands
// each such member the SUM of all of them: the statement's "区间实际扣款" and
// "未能明细化的消费" both inflate, and each member's personal column shows the
// other's money. On a reimbursement attachment that is the one direction the
// numbers must never move in.
func TestTeamStatementDoesNotDoubleCountCollidingMasks(t *testing.T) {
	gin.SetMode(gin.TestMode)
	d, err := db.Open(filepath.Join(t.TempDir(), "team.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	ctx := context.Background()
	ws, err := d.CreateWorkspace(ctx, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if err := d.AddMember(ctx, ws.ID, tokAdmin, db.WSRoleAdmin, 0, 0); err != nil {
		t.Fatal(err)
	}
	// Two operator-assigned short tokens: both mask to tokenmask.Opaque.
	shorts := map[string]float64{"sk-short1": 5, "sk-short2": 6}
	for tok, amt := range shorts {
		if err := d.AddMember(ctx, ws.ID, tok, db.WSRoleMember, 0, 0); err != nil {
			t.Fatal(err)
		}
		if _, err := d.AddBalance(ctx, tok, db.TxKindTopup, 100, "seed", "", false); err != nil {
			t.Fatal(err)
		}
		if _, err := d.AddBalance(ctx, tok, db.TxKindCharge, -amt, "ref", "", true); err != nil {
			t.Fatal(err)
		}
	}
	th := &TeamHandler{
		DB: d, LogDir: t.TempDir(), LogIndexed: true,
		Auth:        func(c *gin.Context) string { return c.GetHeader("X-Tok") },
		TokenExists: func(tok string) bool { return true },
	}
	e := gin.New()
	th.Routes(e.Group("/api/team"))

	today := day(time.Now().UTC())
	got := teamStatement(t, e, tokAdmin, map[string]any{"from": today, "to": today})
	rate := got.CNYPerUSD
	// 5 + 6, counted once each — not 11 twice.
	if !approx(got.ChargedCNY, 11*rate) {
		t.Fatalf("charged = %v, want %v (11 USD counted once)", got.ChargedCNY, 11*rate)
	}
	if !approx(got.UnitemisedCNY, 11*rate) {
		t.Fatalf("unitemised = %v, want %v", got.UnitemisedCNY, 11*rate)
	}
	var personal float64
	rows := 0
	for _, m := range got.ByMember {
		if m.Masked != tokenmask.Opaque {
			continue
		}
		rows++
		personal += m.PersonalLedgerCNY
		// Neither row may carry the pair's combined 11.
		if approx(m.PersonalLedgerCNY, 11*rate) {
			t.Fatalf("a colliding mask took the other member's charges too: %v", m.PersonalLedgerCNY)
		}
	}
	if rows != 2 {
		t.Fatalf("expected both short-token members in by_member, got %d", rows)
	}
	if !approx(personal, 11*rate) {
		t.Fatalf("member personal column sums to %v, want %v", personal, 11*rate)
	}
}

// (g) Rows written before cc-core v0.8.61 carry the charge in cost_usd alone.
// Retention is 90 days, so they sit in the same range as modern ones; reading
// the raw billed_usd prints them as free, and a reimbursement attachment that
// shows a paid request at ¥0.00 is a support ticket.
func TestTeamStatementDetailLinesPriceLegacyRows(t *testing.T) {
	_, d, logDir := newUsageTeam(t)
	now := time.Now().UTC().Add(-3 * time.Hour)
	writeLog(t, logDir, []requestlog.Record{
		rec(now, tokMember, "claude-sonnet-5", 2),
		legacyRec(now.Add(time.Minute), tokMember, "claude-sonnet-5", 3),
	})
	today := day(now)
	ctx := context.Background()
	ws, err := d.WorkspaceAdminFor(ctx, tokAdmin)
	if err != nil {
		t.Fatal(err)
	}
	gu, err := BuildGroupUsage(ctx, GroupUsageQuery{
		Wallets: d, LogDir: logDir, LogIndexed: true, WorkspaceID: ws.ID, FromDay: today, ToDay: today,
	})
	if err != nil {
		t.Fatal(err)
	}
	rate := config.DefaultCNYPerUSD
	lines, _, err := groupDetailLines(logDir, gu, requestlog.BucketLocation(), rate)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	var sum float64
	for _, ln := range lines {
		if ln.BilledCNY == 0 {
			t.Fatalf("a charged request printed as free: %+v", ln)
		}
		sum += ln.BilledCNY
	}
	if !approx(sum, 5*rate) {
		t.Fatalf("listing sums to %v, want %v", sum, 5*rate)
	}
}

// (h) Without the request-log index every table under the headline costs one
// full scan of the archive per member. Refusing is the honest answer; running
// for a minute and returning a document with an empty model table is not.
func TestTeamStatementRefusedWithoutLogIndex(t *testing.T) {
	_, d, logDir := newUsageTeam(t)
	th := &TeamHandler{
		DB: d, LogDir: logDir, // LogIndexed deliberately false
		Auth:        func(c *gin.Context) string { return c.GetHeader("X-Tok") },
		TokenExists: func(tok string) bool { return true },
	}
	e := gin.New()
	th.Routes(e.Group("/api/team"))
	for _, path := range []string{"/api/team/statement", "/api/team/statement.pdf"} {
		w := do(e, "POST", path, tokAdmin, map[string]any{})
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s → %d, want 503 (%s)", path, w.Code, w.Body.String())
		}
	}
}
