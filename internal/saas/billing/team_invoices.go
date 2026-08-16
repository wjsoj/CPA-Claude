package billing

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"github.com/wjsoj/CPA-Claude/internal/saas/db"
)

// Team invoicing: the workspace admin files ONE fapiao for the whole group and
// the amount is drawn from each member's own invoiceable pool. Routes live on
// the /api/team group, so authMW has already proven the caller administers
// exactly the workspace these handlers act on — no handler below re-derives
// the scope from user input.

func (t *TeamHandler) invoiceRoutes(g *gin.RouterGroup) {
	g.GET("/invoice/summary", t.invoiceSummary)
	g.GET("/invoices", t.listInvoices)
	g.POST("/invoices", t.createInvoice)
	g.GET("/invoices/:id/download", t.downloadInvoice)
}

// memberInvoiceView renders one member's quota line. Only the masked token
// leaves the server — the console never needs the full member token.
func (t *TeamHandler) memberInvoiceView(m db.MemberInvoiceSummary) gin.H {
	label := ""
	if t.TokenLabel != nil {
		label = t.TokenLabel(m.Token)
	}
	return gin.H{
		"masked":        maskToken(m.Token),
		"label":         label,
		"role":          m.Role,
		"paid_cny":      round2(m.PaidCNY),
		"locked_cny":    round2(m.LockedCNY),
		"issued_cny":    round2(m.IssuedCNY),
		"available_cny": round2(m.AvailableCNY),
	}
}

func (t *TeamHandler) invoiceSummary(c *gin.Context) {
	ws := t.ws(c)
	s, err := t.DB.WorkspaceInvoiceableCNY(c.Request.Context(), ws.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	members := make([]gin.H, 0, len(s.Members))
	for _, m := range s.Members {
		members = append(members, t.memberInvoiceView(m))
	}
	c.JSON(http.StatusOK, gin.H{
		"workspace": gin.H{"id": ws.ID, "name": ws.Name},
		"total": gin.H{
			"paid_cny":      round2(s.Total.PaidCNY),
			"locked_cny":    round2(s.Total.LockedCNY),
			"issued_cny":    round2(s.Total.IssuedCNY),
			"available_cny": round2(s.Total.AvailableCNY),
		},
		"members": members,
	})
}

// allocationViews masks + labels a team invoice's per-member breakdown.
func (t *TeamHandler) allocationViews(allocs []db.InvoiceAllocation) []gin.H {
	out := make([]gin.H, 0, len(allocs))
	for _, a := range allocs {
		label := ""
		if t.TokenLabel != nil {
			label = t.TokenLabel(a.Token)
		}
		out = append(out, gin.H{
			"masked":     maskToken(a.Token),
			"label":      label,
			"cny_amount": round2(a.CNYAmount),
		})
	}
	return out
}

func (t *TeamHandler) listInvoices(c *gin.Context) {
	ws := t.ws(c)
	ctx := c.Request.Context()
	invs, err := t.DB.ListWorkspaceInvoices(ctx, ws.ID, 200)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ids := make([]int64, 0, len(invs))
	for _, v := range invs {
		ids = append(ids, v.ID)
	}
	// One batched query rather than one per invoice — a long-lived team's
	// history is the common case here, not the exception.
	byInvoice, err := t.DB.AllocationsByInvoice(ctx, ids)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]gin.H, 0, len(invs))
	for _, v := range invs {
		row := invoiceUserView(v)
		row["allocations"] = t.allocationViews(byInvoice[v.ID])
		out = append(out, row)
	}
	c.JSON(http.StatusOK, gin.H{"invoices": out})
}

func (t *TeamHandler) createInvoice(c *gin.Context) {
	ws := t.ws(c)
	var b createInvoiceBody
	if err := c.BindJSON(&b); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	// Same validation as the personal flow — a team fapiao is not a different
	// document, only a different funding source.
	//
	// Validate what will actually be billed, not what was typed. The amount is
	// quantised to fen below, and anything under half a fen quantises to zero —
	// which the store rejects as non-positive, surfacing a user's typo as a 500.
	amountCNY := round2(b.CNYAmount)
	if amountCNY < minInvoiceCNY {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cny_amount must be at least ¥0.01"})
		return
	}
	if !isLikelyEmail(b.ContactEmail) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "contact_email invalid"})
		return
	}
	if strings.TrimSpace(b.Title.Name) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "title.name required"})
		return
	}
	b.Title.TaxNo = strings.ToUpper(strings.TrimSpace(b.Title.TaxNo))
	if !isLikelyTaxNo(b.Title.TaxNo) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "title.tax_no required (统一社会信用代码, 15-20 位字母数字)"})
		return
	}

	inv, err := t.DB.CreateWorkspaceInvoice(c.Request.Context(), ws.ID, t.adminToken(c), amountCNY,
		db.InvoiceTitle{
			Name: b.Title.Name, TaxNo: b.Title.TaxNo, Address: b.Title.Address,
			Phone: b.Title.Phone, Bank: b.Title.Bank, BankAccount: b.Title.BankAccount,
		}, strings.TrimSpace(b.ContactEmail))
	if err != nil {
		if errors.Is(err, db.ErrInsufficientInvoiceable) {
			// The error carries the real group-wide available total, which is
			// what the admin needs to retry with.
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if t.Invoices != nil {
		go t.Invoices.notifyOps(inv, ws.Name)
	}

	allocs, _ := t.DB.InvoiceAllocations(c.Request.Context(), inv.ID)
	row := invoiceUserView(inv)
	row["allocations"] = t.allocationViews(allocs)
	c.JSON(http.StatusOK, row)
}

func (t *TeamHandler) downloadInvoice(c *gin.Context) {
	ws := t.ws(c)
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad id"})
		return
	}
	inv, err := t.DB.GetInvoice(c.Request.Context(), id)
	// Scope check against the middleware's workspace, never against a
	// caller-supplied id: an admin of team A must not be able to pull team B's
	// fapiao by guessing an invoice number.
	if err != nil || inv.WorkspaceID != ws.ID {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if inv.Status != db.InvoiceIssued || inv.PDFPath == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "invoice not issued yet"})
		return
	}
	if _, err := os.Stat(inv.PDFPath); err != nil {
		log.Warnf("team invoice download: pdf missing on disk id=%d path=%s: %v", id, inv.PDFPath, err)
		c.JSON(http.StatusNotFound, gin.H{"error": "pdf file missing"})
		return
	}
	c.Header("Content-Type", "application/pdf")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="invoice-%d.pdf"`, inv.ID))
	c.File(inv.PDFPath)
}
