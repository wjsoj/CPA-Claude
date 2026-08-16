package billing

import (
	"context"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/wjsoj/CPA-Claude/internal/saas/db"
)

// The personal download route lets a team invoice through on a second
// ownership rule — "your quota paid for part of it" — and that branch decides
// who may read another party's fapiao. These tests are its guardrail.

const (
	tokInitiator  = "sk-initr-0000000000000000000000000000aaaa"
	tokFunder     = "sk-fundr-0000000000000000000000000000bbbb"
	tokOutsider   = "sk-outsd-0000000000000000000000000000cccc"
	testPDFMarker = "%PDF-1.4"
)

// newInvoiceTest wires the user-facing invoice routes over a fresh DB holding
// one workspace: tokInitiator (admin) and tokFunder (member), each with ¥100 of
// paid orders behind them. tokOutsider is a stranger with a wallet of its own.
func newInvoiceTest(t *testing.T) (*gin.Engine, *db.DB, int64) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	d, err := db.Open(filepath.Join(t.TempDir(), "invoices.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	ctx := context.Background()
	ws, err := d.CreateWorkspace(ctx, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if err := d.AddMember(ctx, ws.ID, tokInitiator, db.WSRoleAdmin, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := d.AddMember(ctx, ws.ID, tokFunder, db.WSRoleMember, 0, 0); err != nil {
		t.Fatal(err)
	}
	for _, tok := range []string{tokInitiator, tokFunder, tokOutsider} {
		seedPaid(t, d, tok, 100)
	}
	h := &InvoiceHandler{DB: d, Auth: func(c *gin.Context) string { return c.GetHeader("X-Tok") }}
	e := gin.New()
	h.UserRoutes(e.Group("/api/wallet"))
	return e, d, ws.ID
}

// issuedTeamInvoice files a ¥150 team invoice — which spills past the admin's
// own ¥100 onto tokFunder — and issues it against a real PDF on disk, so a
// missing file can never stand in for a passing ownership check.
func issuedTeamInvoice(t *testing.T, d *db.DB, wsID int64) *db.Invoice {
	t.Helper()
	ctx := context.Background()
	inv, err := d.CreateWorkspaceInvoice(ctx, wsID, tokInitiator, 150,
		db.InvoiceTitle{Name: "某某科技有限公司", TaxNo: "91110108MA01ABCDEF"}, "fin@example.com")
	if err != nil {
		t.Fatalf("CreateWorkspaceInvoice: %v", err)
	}
	issued, err := d.MarkInvoiceIssued(ctx, inv.ID, writeTestPDF(t, "team fapiao"), "")
	if err != nil {
		t.Fatalf("MarkInvoiceIssued: %v", err)
	}
	return issued
}

// A member whose invoiceable quota funded the invoice can read it, even though
// the row is stored under the filing admin's token. Withholding it would mean a
// member can be charged for a fapiao they are never allowed to see.
func TestInvoiceDownloadAllowsFundingMember(t *testing.T) {
	e, d, wsID := newInvoiceTest(t)
	inv := issuedTeamInvoice(t, d, wsID)

	// Precondition: the funder really is a non-initiator contributor, so this
	// test exercises the allocation branch and not the inv.Token == tok one.
	allocs, err := d.InvoiceAllocations(context.Background(), inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	var funded bool
	for _, a := range allocs {
		funded = funded || a.Token == tokFunder
	}
	if !funded || inv.Token == tokFunder {
		t.Fatalf("fixture is wrong: funder %q not an allocation of an invoice owned by %q (%+v)", tokFunder, inv.Token, allocs)
	}

	w := do(e, "GET", "/api/wallet/invoices/"+strconv.FormatInt(inv.ID, 10)+"/download", tokFunder, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("funding member download = %d, want 200: %s", w.Code, w.Body)
	}
	if !strings.HasPrefix(w.Body.String(), testPDFMarker) {
		t.Errorf("body is not the PDF: %q", w.Body.String())
	}
}

// The same invoice is invisible to a token that funded none of it. This is the
// branch's whole risk: matching on "is a team invoice" alone would hand every
// user every team's fapiao.
func TestInvoiceDownloadRefusesUnrelatedToken(t *testing.T) {
	e, d, wsID := newInvoiceTest(t)
	inv := issuedTeamInvoice(t, d, wsID)

	w := do(e, "GET", "/api/wallet/invoices/"+strconv.FormatInt(inv.ID, 10)+"/download", tokOutsider, nil)
	assertInvoiceNotFound(t, w, "unrelated token download")
	if strings.Contains(w.Body.String(), testPDFMarker) {
		t.Fatalf("unrelated token was served the PDF: %s", w.Body.String())
	}
}

// A rejected team invoice has no document to hand out. Its allocation rows
// survive rejection by design (they are the audit record), so the ownership
// check still passes for the funder here and the status check is the only thing
// standing between them and a 500 on a non-existent path.
func TestInvoiceDownloadRefusesRejectedTeamInvoice(t *testing.T) {
	e, d, wsID := newInvoiceTest(t)
	ctx := context.Background()
	inv, err := d.CreateWorkspaceInvoice(ctx, wsID, tokInitiator, 150,
		db.InvoiceTitle{Name: "某某科技有限公司", TaxNo: "91110108MA01ABCDEF"}, "fin@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.MarkInvoiceRejected(ctx, inv.ID, "wrong title"); err != nil {
		t.Fatal(err)
	}
	// Give the rejected row a real PDF on disk. A rejected invoice normally has
	// no pdf_path — MarkInvoiceIssued is the only writer and it accepts pending
	// rows only — which would let the empty-path guard stand in for the status
	// guard and leave the status check itself untested. Pinning it needs a row
	// where status is the ONLY thing wrong, the shape an operator would produce
	// by rendering a draft and then rejecting it.
	if _, err := d.ExecContext(ctx, `UPDATE invoices SET pdf_path = ? WHERE id = ?`,
		writeTestPDF(t, "rejected draft"), inv.ID); err != nil {
		t.Fatal(err)
	}

	w := do(e, "GET", "/api/wallet/invoices/"+strconv.FormatInt(inv.ID, 10)+"/download", tokFunder, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("rejected invoice download = %d, want 404: %s", w.Code, w.Body)
	}
	if got, _ := decode(t, w.Body.Bytes())["error"].(string); got != "invoice not issued yet" {
		t.Errorf("error = %q, want %q", got, "invoice not issued yet")
	}
	if strings.Contains(w.Body.String(), testPDFMarker) {
		t.Fatalf("rejected invoice served a PDF: %s", w.Body.String())
	}
}

// The personal endpoint quantises the same way the team one does, and had the
// same defect: a positive amount under half a fen became zero on the way to the
// store, whose "must be positive" rejection the handler did not recognise as a
// user error and reported as a 500.
func TestPersonalInvoiceRejectsSubFenAmount(t *testing.T) {
	e, _, _ := newInvoiceTest(t)

	body := map[string]any{
		"cny_amount":    0.001,
		"contact_email": "a@b.c",
		"title":         map[string]any{"name": "x", "tax_no": "91110108MA01ABCDEF"},
	}
	if w := do(e, "POST", "/api/wallet/invoices", tokFunder, body); w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (%s)", w.Code, w.Body)
	}
}
