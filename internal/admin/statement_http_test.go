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
	"github.com/wjsoj/CPA-Claude/internal/statement"
	"github.com/wjsoj/cc-core/auth"
	"github.com/wjsoj/cc-core/clienttoken"
	"github.com/wjsoj/cc-core/requestlog"
)

const testToken = "sk-test-statement-0123456789abcdef"

// statementCfg pins the export rate so the fixtures' arithmetic is legible:
// every $1 row is worth exactly ¥7. Without SaaS there is no live rate handler,
// so this is the configured fallback the handler falls through to — and the
// yuan figures below are only stable because it is set.
func statementCfg(dir string) *config.Config {
	return &config.Config{
		LogDir: dir, LogRetentionDays: 90,
		SaaS: config.SaaSConfig{Exchange: config.ExchangeConfig{FallbackCNYPerUSD: 7}},
	}
}

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
				CostUSD: 20, BilledUSD: 1, Multiplier: 0.05, Status: 200,
			},
			{
				TS: day.Add(-2 * time.Hour), ClientToken: "sk-oth…9999",
				Provider: "anthropic", Model: "claude-opus-4-7", AuthID: "a1", AuthKind: "oauth",
				CostUSD: 2000, BilledUSD: 100, Status: 200,
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
		statementCfg(dir),
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
	// $1 a day at the fixture rate of ¥7 → ¥7 a day.
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
		CostUSD: 7, Status: 200, // pre-v0.8.61: the charge sits in CostUSD
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
	// $1 from the modern row and $7 from the legacy one, both at ¥7 per dollar.
	// Reading BilledUSD raw would price the legacy row at zero and report ¥7.
	if p.BilledCNY != 56 {
		t.Errorf("billed_cny = %v, want 56 ($8 at ¥7) — the legacy row's charge lives in cost_usd", p.BilledCNY)
	}
	if p.Requests != 2 {
		t.Errorf("requests = %d, want 2 — the legacy row must still be counted", p.Requests)
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

	// One surviving log row: $1 at the fixture rate of ¥7 → ¥7.
	writeLog(t, dir, today, []requestlog.Record{{
		TS: today.Add(-2 * time.Hour), ClientToken: masked,
		Provider: "anthropic", Model: "claude-opus-4-7", AuthID: "a1", AuthKind: "oauth",
		CostUSD: 20, BilledUSD: 1, Multiplier: 0.05, Status: 200,
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

	h := New(statementCfg(dir),
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
		CostUSD: 20, BilledUSD: 1, Multiplier: 0.05, Status: 200,
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

	h := New(statementCfg(dir),
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
			CostUSD: 20, BilledUSD: 1, Multiplier: 0.05, Status: 200,
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

	h := New(statementCfg(dir),
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
		CostUSD: 20, BilledUSD: 1, Multiplier: 0.05, Status: 200,
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

	h := New(statementCfg(dir),
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
	// The fixture bills $1/day at the fixture rate of ¥7, so ¥7 is one day's spend.
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

// With no billing handler and no configured exchange rate there is nothing left
// to look a rate up in, and the conversion is a plain multiplication — so a zero
// here does not degrade the document, it empties it. Every figure would print as
// ¥0.00 on a page whose whole purpose is to state what was spent, and nothing on
// it would look broken enough for a reader to distrust the total.
//
// The per-row rate the log used to carry was an accidental safety net for this;
// with it gone the fallback chain is the only thing standing between a
// misconfigured deployment and a statement of zero.
func TestStatementRateNeverCollapsesToZero(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	loc := requestlog.BucketLocation()
	today := time.Now().In(loc)
	writeLog(t, dir, today, []requestlog.Record{{
		TS: today.Add(-time.Hour), ClientToken: maskToken(testToken),
		Provider: "openai", Model: "gpt-5.6-sol", AuthID: "a2", AuthKind: "apikey",
		CostUSD: 40, BilledUSD: 2, Status: 200,
	}})
	tokens := clienttoken.OpenInMemory()
	if err := tokens.Add(clienttoken.Token{Token: testToken, Name: "汇率兜底", Group: "default"}); err != nil {
		t.Fatalf("add token: %v", err)
	}

	// Nothing configured at all: no SaaS handler, no exchange fallback. This is
	// the shape a plain non-billing deployment loads with when applyDefaults
	// never ran over the config.
	h := New(&config.Config{LogDir: dir, LogRetentionDays: 90},
		auth.NewPool(nil, nil, time.Minute, false, ""), nil, nil, tokens)
	if got := h.statementRate(); got != defaultStatementCNYPerUSD {
		t.Errorf("statementRate() = %v with no billing and no config, want the %v default",
			got, defaultStatementCNYPerUSD)
	}

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
	if want := 2 * defaultStatementCNYPerUSD; p.BilledCNY != want {
		t.Errorf("billed_cny = %v, want %v ($2 at the default rate) — a ¥0 statement is the failure mode here",
			p.BilledCNY, want)
	}
	if p.LifetimeBilledCNY != 2*defaultStatementCNYPerUSD {
		t.Errorf("lifetime_billed_cny = %v, want it converted at the same rate", p.LifetimeBilledCNY)
	}
}

// The configured fallback outranks the compiled-in default, and one rate covers
// every row: two rows of the same dollar amount can never print differently.
func TestStatementUsesConfiguredRateForEveryRow(t *testing.T) {
	r, dir := statementFixture(t) // fallback_cny_per_usd = 7
	loc := requestlog.BucketLocation()
	today := time.Now().In(loc)
	writeLog(t, dir, today, []requestlog.Record{{
		TS: today.Add(-time.Hour), ClientToken: maskToken(testToken),
		Provider: "openai", Model: "gpt-5.6-sol", AuthID: "a2", AuthKind: "apikey",
		CostUSD: 60, BilledUSD: 3, Status: 200,
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
	// $1 + $3 at the configured ¥7, not at the ¥7.2 default.
	if p.BilledCNY != 28 {
		t.Errorf("billed_cny = %v, want 28 — the configured rate must outrank the built-in default", p.BilledCNY)
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

	// Ours: 40 older requests, $0.50 each at the fixture rate of ¥7 → ¥140 total.
	ours := make([]requestlog.Record, 0, 40)
	for i := range 40 {
		ours = append(ours, requestlog.Record{
			TS: now.AddDate(0, 0, -20).Add(time.Duration(i) * time.Minute), ClientToken: masked,
			Provider: "anthropic", Model: "claude-opus-4-7", AuthID: "a1", AuthKind: "oauth",
			CostUSD: 10, BilledUSD: 0.5, Multiplier: 0.05, Status: 200,
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
			CostUSD: 10, BilledUSD: 50, Multiplier: 0.05, Status: 200,
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
	h := New(statementCfg(dir),
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

// The row cap is the one branch that can refuse an export outright, and it now
// belongs to the target-amount mode alone: only that mode has to materialise a
// window it hasn't located yet. A date range reads its totals off the cube and
// prints at most MaxDetailLines rows, so no volume of traffic can put one out
// of reach — which is what the 413 used to do to exactly the accounts that most
// needed a statement.
//
// The refusal's advice is "export by date range instead", so that has to work
// on the very archive the target mode just refused.
func TestStatementRowCapAppliesOnlyToTargetMode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	masked := maskToken(testToken)
	loc := requestlog.BucketLocation()
	now := time.Now().In(loc)

	// 30 requests a day for three days.
	for d := range 3 {
		day := now.AddDate(0, 0, -d)
		recs := make([]requestlog.Record, 0, 30)
		for i := range 30 {
			recs = append(recs, requestlog.Record{
				TS:          time.Date(day.Year(), day.Month(), day.Day(), 3, i, 0, 0, loc),
				ClientToken: masked, Provider: "anthropic", Model: "claude-opus-4-7",
				AuthID: "a1", AuthKind: "oauth",
				CostUSD: 2, BilledUSD: 0.1, Multiplier: 0.05, Status: 200,
			})
		}
		writeLog(t, dir, day, recs)
	}
	openReadyStore(t, dir)

	orig := statementMaxRows
	statementMaxRows = 50 // under three days (90), over one (30)
	t.Cleanup(func() { statementMaxRows = orig })

	tokens := clienttoken.OpenInMemory()
	if err := tokens.Add(clienttoken.Token{Token: testToken, Name: "上限测试", Group: "default"}); err != nil {
		t.Fatalf("add token: %v", err)
	}
	h := New(statementCfg(dir),
		auth.NewPool(nil, nil, time.Minute, false, ""), nil, nil, tokens)
	r := gin.New()
	r.POST("/status/api/statement", h.handleStatementPreview)

	// 90 rows against a cap of 50: the target walk cannot see far enough back
	// to place the window honestly, so it refuses.
	w := postJSON(t, r, "/status/api/statement", statementBody{
		Token: testToken, TargetCNY: 20,
	})
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("target status = %d, want 413; body = %s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("改用日期区间导出")) {
		t.Errorf("413 body = %s, want it to point at the date-range export", w.Body.String())
	}

	// Taking that advice has to actually work, over all three days — the same
	// 90 rows, well past the cap, which the aggregate path never materialises.
	w = postJSON(t, r, "/status/api/statement", statementBody{
		Token: testToken, From: dayLabel(-2), To: dayLabel(0),
	})
	if w.Code != http.StatusOK {
		t.Fatalf("date-range status = %d, want 200 — the advice must be actionable; body = %s",
			w.Code, w.Body.String())
	}
	var p statementPreview
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if p.Requests != 90 {
		t.Errorf("requests = %d, want all 90 rows of the three days asked for", p.Requests)
	}
	if p.BilledCNY != 63 {
		t.Errorf("billed_cny = %v, want 63 (90 × $0.1 × ¥7)", p.BilledCNY)
	}
}

// A date-range export reads its range total and its per-model table off the
// pre-summed cube instead of adding up the rows. That is only sound because
// cc-core's aggSelect folds Record.BilledOrCost per row before summing, and
// because both paths share the attempt_only = 0 baseline — neither of which
// this package controls. If either ever drifts, an export silently misstates
// what somebody is claiming back, with nothing on the page looking wrong.
//
// So the two are computed over one seeded range and compared: the handler's
// aggregate answer against a plain row-by-row sum of the very rows the
// itemised section would print.
func TestStatementAggregateMatchesRowByRowSum(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	masked := maskToken(testToken)
	loc := requestlog.BucketLocation()
	now := time.Now().In(loc)

	// Dyadic amounts, so "equal" can mean exactly equal rather than "close":
	// the two paths sum in different orders (SQLite vs Go) and only exactly
	// representable values are guaranteed to land on the same float.
	amounts := []float64{0.5, 0.25, 0.125, 2, 1}
	models := []string{"claude-opus-4-7", "gpt-5.6-sol", "claude-sonnet-5"}
	for d := range 4 {
		day := now.AddDate(0, 0, -d)
		recs := make([]requestlog.Record, 0, 8)
		for i := range 5 {
			rec := requestlog.Record{
				TS:          time.Date(day.Year(), day.Month(), day.Day(), 4, i, 0, 0, loc),
				ClientToken: masked, Provider: "anthropic", Model: models[i%len(models)],
				AuthID: "a1", AuthKind: "oauth",
				Input: 100, Output: 20, CacheRead: 5,
				BilledUSD: amounts[i], CostUSD: 40, Status: 200,
			}
			if i == 3 {
				// Legacy shape: the charge sits in cost_usd with billed_usd
				// unset. This is the row aggSelect's CASE exists for.
				rec.BilledUSD, rec.CostUSD = 0, amounts[i]
			}
			recs = append(recs, rec)
		}
		// Excluded by both paths, and it must be excluded by both identically.
		recs = append(recs, requestlog.Record{
			TS:          time.Date(day.Year(), day.Month(), day.Day(), 5, 0, 0, 0, loc),
			ClientToken: masked, Provider: "anthropic", Model: "claude-opus-4-7",
			AuthID: "a1", AuthKind: "oauth", BilledUSD: 99, Status: 503, AttemptOnly: true,
		})
		// A neighbour's row, on the same days and the same models.
		recs = append(recs, requestlog.Record{
			TS:          time.Date(day.Year(), day.Month(), day.Day(), 6, 0, 0, 0, loc),
			ClientToken: "sk-oth…9999", Provider: "anthropic", Model: "gpt-5.6-sol",
			AuthID: "a1", AuthKind: "oauth", BilledUSD: 64, Status: 200,
		})
		writeLog(t, dir, day, recs)
	}
	openReadyStore(t, dir) // the cube path production actually runs

	tokens := clienttoken.OpenInMemory()
	if err := tokens.Add(clienttoken.Token{Token: testToken, Name: "等价性", Group: "default"}); err != nil {
		t.Fatalf("add token: %v", err)
	}
	h := New(statementCfg(dir), auth.NewPool(nil, nil, time.Minute, false, ""), nil, nil, tokens)
	r := gin.New()
	r.POST("/status/api/statement", h.handleStatementPreview)

	from, to := dayLabel(-2), dayLabel(0) // three of the four seeded days
	w := postJSON(t, r, "/status/api/statement", statementBody{Token: testToken, From: from, To: to})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var p statementPreview
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// The reference: every row the export would itemise, summed by hand at the
	// same rate, exactly as buildStatement used to do it for every figure.
	const rate = 7 // statementCfg's configured fallback
	ref, err := requestlog.Query(requestlog.Filter{
		Dir: dir, ClientToken: masked, FromDay: from, ToDay: to, Limit: 100000,
	})
	if err != nil {
		t.Fatalf("reference query: %v", err)
	}
	var wantRequests int64
	var wantCNY float64
	wantByModel := map[string]statementModelRow{}
	for _, rec := range ref.Entries {
		cny := rec.BilledOrCost() * rate
		wantRequests++
		wantCNY += cny
		row := wantByModel[rec.Model]
		row.Model, row.Requests, row.BilledCNY = rec.Model, row.Requests+1, row.BilledCNY+cny
		wantByModel[rec.Model] = row
	}
	if wantRequests == 0 {
		t.Fatal("reference scan found nothing — the fixture never reached the query path")
	}

	if p.Requests != wantRequests {
		t.Errorf("requests = %d, row-by-row says %d", p.Requests, wantRequests)
	}
	if p.BilledCNY != wantCNY {
		t.Errorf("billed_cny = %v, row-by-row says %v", p.BilledCNY, wantCNY)
	}
	if len(p.ByModel) != len(wantByModel) {
		t.Fatalf("by_model has %d rows, row-by-row says %d: %+v", len(p.ByModel), len(wantByModel), p.ByModel)
	}
	var tableCNY float64
	for _, got := range p.ByModel {
		want, ok := wantByModel[got.Model]
		if !ok {
			t.Errorf("by_model carries %q, which no row produced", got.Model)
			continue
		}
		if got.Requests != want.Requests || got.BilledCNY != want.BilledCNY {
			t.Errorf("by_model[%s] = %d req / ¥%v, row-by-row says %d req / ¥%v",
				got.Model, got.Requests, got.BilledCNY, want.Requests, want.BilledCNY)
		}
		tableCNY += got.BilledCNY
	}
	// The table sits directly above the total on the page; a reader adds it up.
	if tableCNY != p.BilledCNY {
		t.Errorf("by_model sums to %v but the range total is %v", tableCNY, p.BilledCNY)
	}
	// The neighbour bills $64 a row, so any leakage would be unmissable.
	if p.BilledCNY > 200 {
		t.Errorf("billed_cny = %v — another token's spend leaked in", p.BilledCNY)
	}
}

// statementCtx drives buildStatement directly, which is the only way to see
// the itemised rows: the PDF embeds them through a subsetted font and the
// preview reports only how many there are.
func statementCtx(t *testing.T, h *Handler, body statementBody, withLines bool) (*statement.Statement, bool) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/status/api/statement.pdf", bytes.NewReader(raw))
	c.Request.Header.Set("Content-Type", "application/json")
	s, ok := h.buildStatement(c, withLines)
	if !ok {
		t.Fatalf("buildStatement refused: %d %s", w.Code, w.Body.String())
	}
	return s, ok
}

// When a range holds more requests than the document can print, the rows it
// keeps must be the newest ones.
//
// The loop used to walk oldest-to-newest and stop at MaxDetailLines, so a
// reimbursement claim spanning the 2026-08-09 cutover printed three thousand
// rows synthesised from wallet transactions — no model, no token counts —
// and none of the recent traffic the claim was actually about. Keeping the
// newest is the fix; printing them in reverse would be a different bug, so
// chronological order is asserted too.
func TestDetailLinesKeepTheNewestRows(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	masked := maskToken(testToken)
	loc := requestlog.BucketLocation()
	day := time.Now().In(loc)

	const total = statement.MaxDetailLines + 5
	base := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)
	recs := make([]requestlog.Record, 0, total)
	for i := range total {
		recs = append(recs, requestlog.Record{
			TS: base.Add(time.Duration(i) * time.Second), ClientToken: masked,
			Provider: "anthropic", Model: "claude-opus-4-7", AuthID: "a1", AuthKind: "oauth",
			Input: 10, Output: 2, BilledUSD: 0.5, Status: 200,
		})
	}
	writeLog(t, dir, day, recs)
	openReadyStore(t, dir)

	tokens := clienttoken.OpenInMemory()
	if err := tokens.Add(clienttoken.Token{Token: testToken, Name: "截断", Group: "default"}); err != nil {
		t.Fatalf("add token: %v", err)
	}
	h := New(statementCfg(dir), auth.NewPool(nil, nil, time.Minute, false, ""), nil, nil, tokens)

	oldestKept := base.Add(time.Duration(total-statement.MaxDetailLines) * time.Second)
	newest := base.Add(time.Duration(total-1) * time.Second)

	for _, tc := range []struct {
		name string
		body statementBody
	}{
		{"date range", statementBody{Token: testToken, From: dayLabel(0), To: dayLabel(0)}},
		// The same rule on the target path, whose walk is a separate loop.
		// Every row is needed to reach ¥10510.5 (3005 × $0.5 × 7).
		{"by target", statementBody{Token: testToken, TargetCNY: float64(total) * 0.5 * 7}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := statementCtx(t, h, tc.body, true)
			if s.Requests != total {
				t.Fatalf("requests = %d, want all %d in range", s.Requests, total)
			}
			if len(s.Lines) != statement.MaxDetailLines {
				t.Fatalf("lines = %d, want the %d cap", len(s.Lines), statement.MaxDetailLines)
			}
			if !s.LinesTruncated {
				t.Error("a truncated listing must say so on the document")
			}
			if got := s.Lines[len(s.Lines)-1].TS; !got.Equal(newest) {
				t.Errorf("last line at %s, want the newest request %s", got, newest)
			}
			if got := s.Lines[0].TS; !got.Equal(oldestKept) {
				t.Errorf("first line at %s, want %s — the %d newest rows are the ones worth printing",
					got, oldestKept, statement.MaxDetailLines)
			}
			for i := 1; i < len(s.Lines); i++ {
				if s.Lines[i].TS.Before(s.Lines[i-1].TS) {
					t.Fatalf("line %d (%s) predates line %d (%s) — the page reads forward",
						i, s.Lines[i].TS, i-1, s.Lines[i-1].TS)
				}
			}
		})
	}
}

// The two floors under the reconciliation gap earn their keep here.
//
// Without them the condition is `gap > 0`, and every existing test still
// passes — a discrepancy of a fraction of a cent is arithmetic noise, but it
// still renders, as a line reading "未能明细化的消费 ¥0.00" that asserts
// something is missing while stating that nothing is. Both floors are checked,
// because either alone would let one of these two cases through.
func TestSubCentLedgerGapIsNotReported(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name      string
		chargeUSD float64
	}{
		// Below ledgerGapEpsilonUSD: float noise, not a request.
		{"below the usd epsilon", 1 + 5e-5},
		// Over the USD epsilon but under half a fen once converted at ¥7:
		// $0.0005 is ¥0.0035, which prints as ¥0.00.
		{"rounds to zero yuan", 1 + 5e-4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			loc := requestlog.BucketLocation()
			today := time.Now().In(loc)
			writeLog(t, dir, today, []requestlog.Record{{
				TS: today.Add(-2 * time.Hour), ClientToken: maskToken(testToken),
				Provider: "anthropic", Model: "claude-opus-4-7", AuthID: "a1", AuthKind: "oauth",
				CostUSD: 20, BilledUSD: 1, Multiplier: 0.05, Status: 200,
			}})
			tokens := clienttoken.OpenInMemory()
			if err := tokens.Add(clienttoken.Token{Token: testToken, Name: "噪声", Group: "default"}); err != nil {
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
				testToken, -tc.chargeUSD, today.Add(-90*time.Minute).Unix()); err != nil {
				t.Fatalf("seed charge: %v", err)
			}

			h := New(statementCfg(dir),
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
			if p.UnitemisedCNY != 0 {
				t.Errorf("unitemised_cny = %v, want 0 — it would print as ¥%.2f, a line that contradicts itself",
					p.UnitemisedCNY, p.UnitemisedCNY)
			}
			if p.ChargedCNY != p.BilledCNY {
				t.Errorf("charged_cny = %v, want it presented as the itemised %v rather than a fen nobody can explain",
					p.ChargedCNY, p.BilledCNY)
			}
		})
	}
}
