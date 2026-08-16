package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/wjsoj/CPA-Claude/internal/config"
	saasdb "github.com/wjsoj/CPA-Claude/internal/saas/db"
	"github.com/wjsoj/cc-core/auth"
	"github.com/wjsoj/cc-core/clienttoken"
	"github.com/wjsoj/cc-core/requestlog"
)

const testToken = "sk-test-statement-0123456789abcdef"

// writeLog lays down a requests-YYYY-MM-DD.jsonl the query path will pick up.
func writeLog(t *testing.T, dir string, day time.Time, recs []requestlog.Record) {
	t.Helper()
	name := filepath.Join(dir, "requests-"+day.UTC().Format("2006-01-02")+".jsonl")
	f, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, r := range recs {
		if err := enc.Encode(r); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}
}

// statementFixture builds a handler over a temp log dir holding three days of
// traffic for one token, plus a row belonging to somebody else.
func statementFixture(t *testing.T) (*gin.Engine, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dir := t.TempDir()
	masked := maskToken(testToken)
	loc := requestlog.BucketLocation()
	today := time.Now().In(loc)

	// One request per day for three consecutive days, $1 each, and one $100
	// row for a different token that must never appear in our totals.
	for i := range 3 {
		day := today.AddDate(0, 0, -i)
		writeLog(t, dir, day, []requestlog.Record{
			{
				TS: day.Add(-2 * time.Hour), ClientToken: masked,
				Provider: "anthropic", Model: "claude-opus-4-7", AuthID: "a1", AuthKind: "oauth",
				Input: 1000, Output: 200, CacheRead: 50, CacheCreate: 10,
				CostUSD: 20, BilledUSD: 1, Multiplier: 0.05, CNYPerUSD: 7, Status: 200,
			},
			{
				TS: day.Add(-2 * time.Hour), ClientToken: "sk-oth…9999",
				Provider: "anthropic", Model: "claude-opus-4-7", AuthID: "a1", AuthKind: "oauth",
				CostUSD: 2000, BilledUSD: 100, CNYPerUSD: 7, Status: 200,
			},
		})
	}

	tokens := clienttoken.OpenInMemory()
	if err := tokens.Add(clienttoken.Token{
		Token: testToken, Name: "报销测试令牌", Group: "default",
	}); err != nil {
		t.Fatalf("add token: %v", err)
	}

	h := New(
		&config.Config{LogDir: dir, LogRetentionDays: 90},
		auth.NewPool(nil, nil, time.Minute, false, ""),
		nil, nil, tokens,
	)
	r := gin.New()
	r.POST("/status/api/statement", h.handleStatementPreview)
	r.POST("/status/api/statement.pdf", h.handleStatementPDF)
	return r, dir
}

func postJSON(t *testing.T, r *gin.Engine, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func dayLabel(offset int) string {
	return time.Now().In(requestlog.BucketLocation()).AddDate(0, 0, offset).Format("2006-01-02")
}

// The preview must total only the caller's own rows, and the range must exclude
// days outside it while the running total does not.
func TestStatementPreviewTotalsOnlyOwnRows(t *testing.T) {
	r, _ := statementFixture(t)

	w := postJSON(t, r, "/status/api/statement", statementBody{
		Token: testToken, From: dayLabel(-1), To: dayLabel(0),
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var p statementPreview
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Two of the three days, one $1 request each. The other token's $100 rows
	// share the same days and must contribute nothing.
	if p.Requests != 2 {
		t.Errorf("requests = %d, want 2", p.Requests)
	}
	// $1 a day at a stored rate of 7 → ¥7 a day.
	if p.BilledCNY != 14 {
		t.Errorf("billed_cny = %v, want 14 (the foreign rows must not count)", p.BilledCNY)
	}
	// The running total spans the retention window, so it sees all three days.
	if p.LifetimeRequests != 3 {
		t.Errorf("lifetime_requests = %d, want 3", p.LifetimeRequests)
	}
	if p.LifetimeBilledCNY != 21 {
		t.Errorf("lifetime_billed_cny = %v, want 21", p.LifetimeBilledCNY)
	}
	if p.LifetimeDays != 90 {
		t.Errorf("lifetime_days = %d, want the configured retention 90", p.LifetimeDays)
	}
	if len(p.ByModel) != 1 || p.ByModel[0].Model != "claude-opus-4-7" {
		t.Errorf("by_model = %+v, want one claude-opus-4-7 row", p.ByModel)
	}
}

// A legacy row carries the charged amount in cost_usd with billed_usd unset.
// Reading the raw field would report it as free.
func TestStatementCountsLegacyRows(t *testing.T) {
	r, dir := statementFixture(t)
	loc := requestlog.BucketLocation()
	today := time.Now().In(loc)
	writeLog(t, dir, today, []requestlog.Record{{
		TS: today.Add(-time.Hour), ClientToken: maskToken(testToken),
		Provider: "openai", Model: "gpt-5.6-sol", AuthID: "a2", AuthKind: "apikey",
		CostUSD: 7, Status: 200, // pre-v0.8.61: no BilledUSD, and no rate either
	}})

	w := postJSON(t, r, "/status/api/statement", statementBody{
		Token: testToken, From: dayLabel(0), To: dayLabel(0),
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var p statementPreview
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// ¥7 from the modern row, plus the legacy row's $7 at the fixture's
	// fallback rate of 0 (no billing handler) — so it contributes nothing to
	// the yuan total but must still be counted and disclosed.
	if p.BilledCNY != 7 {
		t.Errorf("billed_cny = %v, want 7", p.BilledCNY)
	}
	if p.Requests != 2 {
		t.Errorf("requests = %d, want 2 — the legacy row must still be counted", p.Requests)
	}
	if p.UnratedRequests != 1 {
		t.Errorf("unrated_requests = %d, want 1 — the rateless row must be disclosed", p.UnratedRequests)
	}
}

// Ownership is the only gate on these endpoints, so it has to hold.
func TestStatementRejectsUnknownToken(t *testing.T) {
	r, _ := statementFixture(t)
	for _, path := range []string{"/status/api/statement", "/status/api/statement.pdf"} {
		w := postJSON(t, r, path, statementBody{Token: "sk-not-a-real-token-xxxxxxxx"})
		if w.Code != http.StatusNotFound {
			t.Errorf("%s with an unknown token = %d, want 404", path, w.Code)
		}
		w = postJSON(t, r, path, statementBody{Token: ""})
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s with no token = %d, want 400", path, w.Code)
		}
	}
}

// The download must be a real PDF, offered as an attachment, and must never
// carry the full token — the file is meant to be handed to a third party.
func TestStatementPDFDownload(t *testing.T) {
	r, _ := statementFixture(t)
	w := postJSON(t, r, "/status/api/statement.pdf", statementBody{
		Token: testToken, From: dayLabel(-2), To: dayLabel(0),
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/pdf" {
		t.Errorf("content-type = %q, want application/pdf", ct)
	}
	cd := w.Header().Get("Content-Disposition")
	want := fmt.Sprintf("usage-statement-%s_%s.pdf", dayLabel(-2), dayLabel(0))
	if !bytes.Contains([]byte(cd), []byte(want)) {
		t.Errorf("content-disposition = %q, want it to name %q", cd, want)
	}
	body := w.Body.Bytes()
	if !bytes.HasPrefix(body, []byte("%PDF-")) {
		t.Fatal("response is not a PDF")
	}
	if bytes.Contains(body, []byte(testToken)) {
		t.Error("the full client token leaked into the exported document")
	}
}

// A range wider than the cap is refused rather than silently narrowed.
// The reconciliation's reason for existing: the ledger holds a debit the
// request log lost. That money must appear on the statement as its own labelled
// figure — never dropped, and never smeared across the surviving line items.
func TestStatementSurfacesLedgerGap(t *testing.T) {
	dir := t.TempDir()
	masked := maskToken(testToken)
	loc := requestlog.BucketLocation()
	today := time.Now().In(loc)

	// One surviving log row: $1 at a stored rate of 7 → ¥7.
	writeLog(t, dir, today, []requestlog.Record{{
		TS: today.Add(-2 * time.Hour), ClientToken: masked,
		Provider: "anthropic", Model: "claude-opus-4-7", AuthID: "a1", AuthKind: "oauth",
		CostUSD: 20, BilledUSD: 1, Multiplier: 0.05, CNYPerUSD: 7, Status: 200,
	}})

	tokens := clienttoken.OpenInMemory()
	if err := tokens.Add(clienttoken.Token{Token: testToken, Name: "对账测试", Group: "default"}); err != nil {
		t.Fatalf("add token: %v", err)
	}
	sdb, err := saasdb.Open(filepath.Join(t.TempDir(), "saas.db"))
	if err != nil {
		t.Fatalf("open saas db: %v", err)
	}
	t.Cleanup(func() { _ = sdb.Close() })
	ctx := context.Background()
	if _, err := sdb.EnsureWallet(ctx, testToken); err != nil {
		t.Fatalf("EnsureWallet: %v", err)
	}
	// The ledger says $3 was debited today. Two dollars of that have no log row
	// behind them — exactly the data-loss case.
	if _, err := sdb.ExecContext(ctx,
		`INSERT INTO wallet_tx (token, kind, amount_usd, ref, note, created_at) VALUES (?, 'charge', ?, '', '', ?)`,
		testToken, -3.0, today.Add(-90*time.Minute).Unix()); err != nil {
		t.Fatalf("seed charge: %v", err)
	}

	h := New(&config.Config{LogDir: dir, LogRetentionDays: 90},
		auth.NewPool(nil, nil, time.Minute, false, ""), nil, nil, tokens).
		WithSaaS(sdb, nil)
	r := gin.New()
	r.POST("/status/api/statement", h.handleStatementPreview)

	w := postJSON(t, r, "/status/api/statement", statementBody{
		Token: testToken, From: dayLabel(0), To: dayLabel(0),
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var p statementPreview
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Itemised stays at the ¥7 it can actually evidence...
	if p.BilledCNY != 7 {
		t.Errorf("billed_cny = %v, want 7 — the gap must not be folded into the itemised total", p.BilledCNY)
	}
	// ...the missing $2 converts at the range's own effective rate of 7...
	if p.UnitemisedCNY != 14 {
		t.Errorf("unitemised_cny = %v, want 14 ($2 unaccounted at the range rate of 7)", p.UnitemisedCNY)
	}
	// ...and the closing figure matches the money the ledger really moved.
	if p.ChargedCNY != 21 {
		t.Errorf("charged_cny = %v, want 21", p.ChargedCNY)
	}
	if p.Requests != 1 {
		t.Errorf("requests = %d, want 1 — no request may be invented for the gap", p.Requests)
	}
}

// When the log accounts for everything the ledger did, no gap is reported: a
// permanent "未能明细化 ¥0.00" line would be noise, and a spurious one would
// undermine the times it matters.
func TestStatementReportsNoGapWhenLedgerAgrees(t *testing.T) {
	dir := t.TempDir()
	loc := requestlog.BucketLocation()
	today := time.Now().In(loc)
	writeLog(t, dir, today, []requestlog.Record{{
		TS: today.Add(-2 * time.Hour), ClientToken: maskToken(testToken),
		Provider: "anthropic", Model: "claude-opus-4-7", AuthID: "a1", AuthKind: "oauth",
		CostUSD: 20, BilledUSD: 1, Multiplier: 0.05, CNYPerUSD: 7, Status: 200,
	}})
	tokens := clienttoken.OpenInMemory()
	if err := tokens.Add(clienttoken.Token{Token: testToken, Name: "对账测试", Group: "default"}); err != nil {
		t.Fatalf("add token: %v", err)
	}
	sdb, err := saasdb.Open(filepath.Join(t.TempDir(), "saas.db"))
	if err != nil {
		t.Fatalf("open saas db: %v", err)
	}
	t.Cleanup(func() { _ = sdb.Close() })
	ctx := context.Background()
	if _, err := sdb.EnsureWallet(ctx, testToken); err != nil {
		t.Fatalf("EnsureWallet: %v", err)
	}
	if _, err := sdb.ExecContext(ctx,
		`INSERT INTO wallet_tx (token, kind, amount_usd, ref, note, created_at) VALUES (?, 'charge', ?, '', '', ?)`,
		testToken, -1.0, today.Add(-90*time.Minute).Unix()); err != nil {
		t.Fatalf("seed charge: %v", err)
	}

	h := New(&config.Config{LogDir: dir, LogRetentionDays: 90},
		auth.NewPool(nil, nil, time.Minute, false, ""), nil, nil, tokens).
		WithSaaS(sdb, nil)
	r := gin.New()
	r.POST("/status/api/statement", h.handleStatementPreview)

	w := postJSON(t, r, "/status/api/statement", statementBody{
		Token: testToken, From: dayLabel(0), To: dayLabel(0),
	})
	var p statementPreview
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if p.UnitemisedCNY != 0 {
		t.Errorf("unitemised_cny = %v, want 0 when the ledger agrees", p.UnitemisedCNY)
	}
	if p.ChargedCNY != p.BilledCNY {
		t.Errorf("charged_cny = %v, want it equal to billed_cny %v", p.ChargedCNY, p.BilledCNY)
	}
}

// A target-amount request walks backward from the newest row until spend
// reaches the target, and reports the window it landed on rather than a
// caller-named date range.
func TestStatementByTargetLocatesWindow(t *testing.T) {
	dir := t.TempDir()
	masked := maskToken(testToken)
	loc := requestlog.BucketLocation()
	today := time.Now().In(loc)

	// Three $1 (¥7) rows on three consecutive days.
	for i := range 3 {
		day := today.AddDate(0, 0, -i)
		writeLog(t, dir, day, []requestlog.Record{{
			TS: day.Add(-2 * time.Hour), ClientToken: masked,
			Provider: "anthropic", Model: "claude-opus-4-7", AuthID: "a1", AuthKind: "oauth",
			CostUSD: 20, BilledUSD: 1, Multiplier: 0.05, CNYPerUSD: 7, Status: 200,
		}})
	}

	tokens := clienttoken.OpenInMemory()
	if err := tokens.Add(clienttoken.Token{Token: testToken, Name: "目标测试", Group: "default"}); err != nil {
		t.Fatalf("add token: %v", err)
	}
	sdb, err := saasdb.Open(filepath.Join(t.TempDir(), "saas.db"))
	if err != nil {
		t.Fatalf("open saas db: %v", err)
	}
	t.Cleanup(func() { _ = sdb.Close() })
	ctx := context.Background()
	if _, err := sdb.ExecContext(ctx,
		`INSERT INTO alipay_orders (out_trade_no, token, cny_amount, usd_credit, rate, status, trade_no, qr_code, created_at, paid_at)
		 VALUES ('t1', ?, 100, 10, 10, 'paid', '', '', ?, ?)`,
		testToken, today.Unix(), today.Unix()); err != nil {
		t.Fatalf("seed order: %v", err)
	}

	h := New(&config.Config{LogDir: dir, LogRetentionDays: 90},
		auth.NewPool(nil, nil, time.Minute, false, ""), nil, nil, tokens).
		WithSaaS(sdb, nil)
	r := gin.New()
	r.POST("/status/api/statement", h.handleStatementPreview)

	// ¥14 needs two of the three ¥7 rows.
	w := postJSON(t, r, "/status/api/statement", statementBody{Token: testToken, TargetCNY: 14})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var p statementPreview
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !p.ByTarget {
		t.Error("by_target must be true")
	}
	if p.TargetCNY != 14 {
		t.Errorf("target_cny = %v, want 14", p.TargetCNY)
	}
	if p.Requests != 2 {
		t.Errorf("requests = %d, want 2 — the walk must stop as soon as the target is reached", p.Requests)
	}
	if p.BilledCNY != 14 {
		t.Errorf("billed_cny = %v, want 14", p.BilledCNY)
	}
	if p.TotalPaidCNY != 100 {
		t.Errorf("total_paid_cny = %v, want 100", p.TotalPaidCNY)
	}
}

// A target the retained log can't reach, even summing every row, is refused
// rather than served as a statement that falls short of what it claims.
func TestStatementByTargetRejectsUnreachableSpend(t *testing.T) {
	dir := t.TempDir()
	masked := maskToken(testToken)
	loc := requestlog.BucketLocation()
	today := time.Now().In(loc)
	writeLog(t, dir, today, []requestlog.Record{{
		TS: today.Add(-time.Hour), ClientToken: masked,
		Provider: "anthropic", Model: "claude-opus-4-7", AuthID: "a1", AuthKind: "oauth",
		CostUSD: 20, BilledUSD: 1, Multiplier: 0.05, CNYPerUSD: 7, Status: 200,
	}})
	tokens := clienttoken.OpenInMemory()
	if err := tokens.Add(clienttoken.Token{Token: testToken, Name: "目标测试", Group: "default"}); err != nil {
		t.Fatalf("add token: %v", err)
	}
	sdb, err := saasdb.Open(filepath.Join(t.TempDir(), "saas.db"))
	if err != nil {
		t.Fatalf("open saas db: %v", err)
	}
	t.Cleanup(func() { _ = sdb.Close() })
	ctx := context.Background()
	// Plenty paid — the log, not the wallet, is the binding constraint here.
	if _, err := sdb.ExecContext(ctx,
		`INSERT INTO alipay_orders (out_trade_no, token, cny_amount, usd_credit, rate, status, trade_no, qr_code, created_at, paid_at)
		 VALUES ('t1', ?, 1000, 100, 10, 'paid', '', '', ?, ?)`,
		testToken, today.Unix(), today.Unix()); err != nil {
		t.Fatalf("seed order: %v", err)
	}

	h := New(&config.Config{LogDir: dir, LogRetentionDays: 90},
		auth.NewPool(nil, nil, time.Minute, false, ""), nil, nil, tokens).
		WithSaaS(sdb, nil)
	r := gin.New()
	r.POST("/status/api/statement", h.handleStatementPreview)

	w := postJSON(t, r, "/status/api/statement", statementBody{Token: testToken, TargetCNY: 500})
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (only ¥7 logged, target ¥500)", w.Code)
	}
}

// target_cny and from/to naming two different range-selection strategies at
// once is a contradiction, not a case to guess at.
func TestStatementTargetAndDateRangeAreMutuallyExclusive(t *testing.T) {
	r, _ := statementFixture(t)
	w := postJSON(t, r, "/status/api/statement", statementBody{
		Token: testToken, TargetCNY: 10, From: dayLabel(-1), To: dayLabel(0),
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 when target_cny and from/to are both set", w.Code)
	}
}

// A target amount is bounded by real spend, which the request log knows on its
// own, so the export does not need a billing database behind it. It used to be
// refused outright without SaaS because the cap was the Alipay-paid total.
func TestStatementByTargetWorksWithoutSaaS(t *testing.T) {
	r, _ := statementFixture(t) // no WithSaaS call — billing disabled
	// The fixture bills $1/day at a stored rate of 7, so ¥7 is one day's spend.
	w := postJSON(t, r, "/status/api/statement", statementBody{Token: testToken, TargetCNY: 7})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — spend alone bounds a target; body = %s", w.Code, w.Body.String())
	}
	var p statementPreview
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !p.ByTarget || p.BilledCNY < 7 {
		t.Errorf("by_target = %v, billed_cny = %v, want a target statement of at least ¥7", p.ByTarget, p.BilledCNY)
	}
}

func TestStatementRejectsAbsurdRange(t *testing.T) {
	r, _ := statementFixture(t)
	w := postJSON(t, r, "/status/api/statement", statementBody{
		Token: testToken, From: "2015-01-01", To: dayLabel(0),
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for an over-wide range", w.Code)
	}
}

// A row whose rate was never captured must be counted and disclosed rather
// than silently dropped: the request happened and the money moved, even if the
// yuan figure for it is a reconstruction.
func TestStatementDisclosesUnratedRows(t *testing.T) {
	r, dir := statementFixture(t)
	loc := requestlog.BucketLocation()
	today := time.Now().In(loc)
	writeLog(t, dir, today, []requestlog.Record{{
		TS: today.Add(-time.Hour), ClientToken: maskToken(testToken),
		Provider: "openai", Model: "gpt-5.6-sol", AuthID: "a2", AuthKind: "apikey",
		CostUSD: 3, BilledUSD: 3, Status: 200, // no CNYPerUSD
	}})

	w := postJSON(t, r, "/status/api/statement", statementBody{
		Token: testToken, From: dayLabel(0), To: dayLabel(0),
	})
	var p statementPreview
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if p.Requests != 2 {
		t.Errorf("requests = %d, want 2", p.Requests)
	}
	if p.UnratedRequests != 1 {
		t.Errorf("unrated_requests = %d, want 1", p.UnratedRequests)
	}
	// The rated row still contributes its full ¥7.
	if p.BilledCNY != 7 {
		t.Errorf("billed_cny = %v, want 7", p.BilledCNY)
	}
}

// openReadyStore opens the SQL index and waits for its first pass, so the test
// exercises the path production actually runs. Every other statement test here
// runs the JSONL fallback, which is why none of them could catch the bug below.
func openReadyStore(t *testing.T, dir string) {
	t.Helper()
	st, err := requestlog.OpenStore(dir)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(st.Close)
	deadline := time.Now().Add(15 * time.Second)
	for !st.Ready() {
		if time.Now().After(deadline) {
			t.Fatal("index never became ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A busy neighbour must not push a token's own history out of its statement.
//
// The scan used to be fleet-wide and capped, so the cap was spent on whoever
// was loudest most recently. In production that meant a token with 165,047
// requests had 33,699 of them itemised — the newest six days — and a statement
// that read as complete while reporting ¥80 of ¥1,648 actually spent. The cap
// is per-token now, so a neighbour's volume cannot displace anything.
//
// statementMaxRows is lowered rather than writing half a million rows: the
// defect only exists above the cap, so the cap is what the test has to cross.
func TestStatementIsNotCrowdedOutByABusierToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	masked := maskToken(testToken)
	loc := requestlog.BucketLocation()
	now := time.Now().In(loc)

	// Ours: 40 older requests, $0.50 each at a stored rate of 7 → ¥140 total.
	ours := make([]requestlog.Record, 0, 40)
	for i := range 40 {
		ours = append(ours, requestlog.Record{
			TS: now.AddDate(0, 0, -20).Add(time.Duration(i) * time.Minute), ClientToken: masked,
			Provider: "anthropic", Model: "claude-opus-4-7", AuthID: "a1", AuthKind: "oauth",
			CostUSD: 10, BilledUSD: 0.5, Multiplier: 0.05, CNYPerUSD: 7, Status: 200,
		})
	}
	writeLog(t, dir, now.AddDate(0, 0, -20), ours)

	// Theirs: 300 newer requests. Under a fleet-wide cap these are the rows
	// that survive, and ours are the rows that vanish.
	theirs := make([]requestlog.Record, 0, 300)
	for i := range 300 {
		theirs = append(theirs, requestlog.Record{
			TS: now.AddDate(0, 0, -1).Add(time.Duration(i) * time.Second), ClientToken: "sk-oth…9999",
			Provider: "anthropic", Model: "claude-opus-4-7", AuthID: "a1", AuthKind: "oauth",
			CostUSD: 10, BilledUSD: 50, Multiplier: 0.05, CNYPerUSD: 7, Status: 200,
		})
	}
	writeLog(t, dir, now.AddDate(0, 0, -1), theirs)

	openReadyStore(t, dir)

	// Well under the 340 rows in the archive, so a fleet-wide scan would be
	// truncated long before it reached ours — but comfortably above our 40.
	orig := statementMaxRows
	statementMaxRows = 100
	t.Cleanup(func() { statementMaxRows = orig })

	tokens := clienttoken.OpenInMemory()
	if err := tokens.Add(clienttoken.Token{Token: testToken, Name: "报销测试", Group: "default"}); err != nil {
		t.Fatalf("add token: %v", err)
	}
	h := New(&config.Config{LogDir: dir, LogRetentionDays: 90},
		auth.NewPool(nil, nil, time.Minute, false, ""), nil, nil, tokens)
	r := gin.New()
	r.POST("/status/api/statement", h.handleStatementPreview)

	w := postJSON(t, r, "/status/api/statement", statementBody{
		Token: testToken, From: dayLabel(-30), To: dayLabel(0),
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var p statementPreview
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if p.Requests != 40 {
		t.Errorf("requests = %d, want all 40 of ours — a neighbour's 300 newer rows must not displace them", p.Requests)
	}
	if p.BilledCNY < 139.99 || p.BilledCNY > 140.01 {
		t.Errorf("billed_cny = %v, want ¥140 (40 × $0.5 × 7)", p.BilledCNY)
	}
	// The neighbour bills $50 a row; any leakage would be impossible to miss.
	if p.LifetimeBilledCNY < 139.99 || p.LifetimeBilledCNY > 140.01 {
		t.Errorf("lifetime_billed_cny = %v, want ¥140 — no other token's spend may appear", p.LifetimeBilledCNY)
	}
}
