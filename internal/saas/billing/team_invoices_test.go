package billing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wjsoj/CPA-Claude/internal/saas/db"
)

func seedPaid(t *testing.T, d *db.DB, token string, cny float64) {
	t.Helper()
	if _, err := d.ExecContext(context.Background(),
		`INSERT INTO alipay_orders (out_trade_no, token, cny_amount, usd_credit, rate, status, trade_no, qr_code, created_at, paid_at)
		 VALUES (?, ?, ?, 1, 7.15, 'paid', '', '', ?, ?)`,
		token+"-ord", token, cny, time.Now().Unix(), time.Now().Unix()); err != nil {
		t.Fatalf("seed paid order: %v", err)
	}
}

func decode(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return m
}

func teamInvoiceBody(cny float64) map[string]any {
	return map[string]any{
		"cny_amount":    cny,
		"contact_email": "fin@example.com",
		"title":         map[string]any{"name": "某某科技有限公司", "tax_no": "91110108MA01ABCDEF"},
	}
}

// The summary is the group's roll-up and its per-member breakdown, and it is
// admin-only — the console is the workspace admin's view of who can be
// invoiced for what.
func TestTeamInvoiceSummary(t *testing.T) {
	e, d := newTeamTest(t)
	seedPaid(t, d, tokAdmin, 100)
	seedPaid(t, d, tokMember, 40)

	if w := do(e, "GET", "/api/team/invoice/summary", tokMember, nil); w.Code != http.StatusForbidden {
		t.Fatalf("member access = %d, want 403", w.Code)
	}
	w := do(e, "GET", "/api/team/invoice/summary", tokAdmin, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	m := decode(t, w.Body.Bytes())
	total := m["total"].(map[string]any)
	if total["paid_cny"].(float64) != 140 || total["available_cny"].(float64) != 140 {
		t.Errorf("total = %+v, want paid/available 140", total)
	}
	members := m["members"].([]any)
	if len(members) != 2 {
		t.Fatalf("members = %d, want 2", len(members))
	}
	// Full tokens must never leave the server.
	for _, mm := range members {
		row := mm.(map[string]any)
		if row["masked"] == tokAdmin || row["masked"] == tokMember {
			t.Errorf("member row leaks the raw token: %+v", row)
		}
	}
}

// Creating a team invoice: the response itemises who paid for it, and the
// members' quota really moves.
func TestTeamInvoiceCreateAndList(t *testing.T) {
	e, d := newTeamTest(t)
	seedPaid(t, d, tokAdmin, 100)
	seedPaid(t, d, tokMember, 100)

	w := do(e, "POST", "/api/team/invoices", tokAdmin, teamInvoiceBody(150))
	if w.Code != http.StatusOK {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	created := decode(t, w.Body.Bytes())
	if created["status"] != "pending" || created["cny_amount"].(float64) != 150 {
		t.Fatalf("created = %+v", created)
	}
	allocs := created["allocations"].([]any)
	if len(allocs) != 2 {
		t.Fatalf("allocations = %d, want 2", len(allocs))
	}
	var sum float64
	for _, a := range allocs {
		sum += a.(map[string]any)["cny_amount"].(float64)
	}
	if sum != 150 {
		t.Errorf("allocations sum to %.2f, want the 150 face value", sum)
	}

	w = do(e, "GET", "/api/team/invoices", tokAdmin, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	list := decode(t, w.Body.Bytes())["invoices"].([]any)
	if len(list) != 1 || len(list[0].(map[string]any)["allocations"].([]any)) != 2 {
		t.Fatalf("list = %+v, want 1 invoice with 2 allocations", list)
	}

	s, err := d.InvoiceableCNY(context.Background(), tokMember)
	if err != nil {
		t.Fatal(err)
	}
	if s.AvailableCNY != 50 {
		t.Errorf("member available = %.2f, want 50", s.AvailableCNY)
	}
}

// A request over the group's pool is refused with the real available total, so
// the admin can retry with a number that works.
func TestTeamInvoiceOverPoolIsRefused(t *testing.T) {
	e, d := newTeamTest(t)
	seedPaid(t, d, tokAdmin, 30)
	seedPaid(t, d, tokMember, 30)

	w := do(e, "POST", "/api/team/invoices", tokAdmin, teamInvoiceBody(100))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body)
	}
	if msg, _ := decode(t, w.Body.Bytes())["error"].(string); msg == "" ||
		!strings.Contains(msg, "available 60.00") {
		t.Errorf("error %q does not state the real available total", msg)
	}
}

// Validation is the personal flow's, not a looser copy of it.
func TestTeamInvoiceValidation(t *testing.T) {
	e, d := newTeamTest(t)
	seedPaid(t, d, tokAdmin, 100)

	for name, body := range map[string]map[string]any{
		"zero amount":  {"cny_amount": 0, "contact_email": "a@b.c", "title": map[string]any{"name": "x", "tax_no": "91110108MA01ABCDEF"}},
		"bad email":    {"cny_amount": 10, "contact_email": "nope", "title": map[string]any{"name": "x", "tax_no": "91110108MA01ABCDEF"}},
		"empty title":  {"cny_amount": 10, "contact_email": "a@b.c", "title": map[string]any{"name": " ", "tax_no": "91110108MA01ABCDEF"}},
		"short tax_no": {"cny_amount": 10, "contact_email": "a@b.c", "title": map[string]any{"name": "x", "tax_no": "123"}},
	} {
		if w := do(e, "POST", "/api/team/invoices", tokAdmin, body); w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400 (%s)", name, w.Code, w.Body)
		}
	}
}

// writeTestPDF drops a real, servable PDF on disk and returns its path.
//
// It has to be real: a download test whose fixture points at a missing file
// cannot tell the ownership check from the os.Stat check — both answer 404, so
// deleting the scope check leaves the test green. With the file present, an
// unguarded handler answers 200 with the bytes.
func writeTestPDF(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "invoice.pdf")
	if err := os.WriteFile(p, []byte("%PDF-1.4\n"+body+"\n%%EOF\n"), 0o600); err != nil {
		t.Fatalf("write test pdf: %v", err)
	}
	return p
}

// assertInvoiceNotFound pins the failure on the ownership check specifically:
// the "not found" body is the scope check's, while a leak that merely trips
// over a missing file says "pdf file missing" and a wrong-status invoice says
// "invoice not issued yet".
func assertInvoiceNotFound(t *testing.T, w *httptest.ResponseRecorder, what string) {
	t.Helper()
	if w.Code != http.StatusNotFound {
		t.Fatalf("%s = %d, want 404 (body %s)", what, w.Code, w.Body)
	}
	if got, _ := decode(t, w.Body.Bytes())["error"].(string); got != "not found" {
		t.Fatalf("%s: error = %q, want %q — the request got past the ownership check", what, got, "not found")
	}
}

// A team admin must not be able to pull another workspace's fapiao by guessing
// an invoice number — the scope check is against the middleware's workspace.
func TestTeamInvoiceDownloadIsWorkspaceScoped(t *testing.T) {
	e, d := newTeamTest(t)
	ctx := context.Background()

	// An unrelated personal invoice belonging to nobody in this workspace,
	// issued with a PDF that really exists so the only thing standing between
	// the caller and the bytes is the workspace check.
	other := "sk-other-0000000000000000000000000000dddd"
	seedPaid(t, d, other, 100)
	inv, err := d.CreateInvoice(ctx, other, 50, db.InvoiceTitle{Name: "别家", TaxNo: "91110108MA01ABCDEF"}, "o@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.MarkInvoiceIssued(ctx, inv.ID, writeTestPDF(t, "another company's fapiao"), ""); err != nil {
		t.Fatal(err)
	}
	w := do(e, "GET", "/api/team/invoices/"+strconv.FormatInt(inv.ID, 10)+"/download", tokAdmin, nil)
	assertInvoiceNotFound(t, w, "cross-workspace download")
	if strings.Contains(w.Body.String(), "%PDF") {
		t.Fatalf("cross-workspace download served the PDF: %s", w.Body.String())
	}
}
