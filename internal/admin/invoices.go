package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	saasdb "github.com/wjsoj/CPA-Claude/internal/saas/db"
	"github.com/wjsoj/CPA-Claude/internal/saas/resend"
)

func parseJSONMap(s string) map[string]any {
	if strings.TrimSpace(s) == "" {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return map[string]any{}
	}
	return m
}

// InvoiceAdmin captures the dependencies admin endpoints need on top of
// the wallet DB — directory for PDF uploads, the Resend client to ping
// users when an invoice is issued. nil-resilient: an empty PDFDir falls
// back to a tmp dir; a nil Resend logs only.
type InvoiceAdmin struct {
	PDFDir string
	Resend *resend.Client
}

// handleListInvoices is the admin overview — all tokens, all states. q
// filters across title_name / contact_email / token (LIKE %q%).
func (h *Handler) handleListInvoices(c *gin.Context) {
	if h.wallets == nil {
		c.JSON(http.StatusOK, gin.H{"invoices": []any{}})
		return
	}
	status := strings.TrimSpace(c.Query("status")) // "", pending, issued, rejected
	q := c.Query("q")
	invs, err := h.wallets.ListInvoices(c.Request.Context(), status, q, 500)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	labels := map[string]string{}
	if h.tokens != nil {
		for _, t := range h.tokens.List() {
			if t.Name != "" {
				labels[t.Token] = t.Name
			}
		}
	}
	out := make([]gin.H, 0, len(invs))
	for _, v := range invs {
		out = append(out, gin.H{
			"id":            v.ID,
			"token":         maskToken(v.Token),
			"label":         labels[v.Token],
			"cny_amount":    v.CNYAmount,
			"title_name":    v.TitleName,
			"title":         parseJSONMap(v.TitleSnapshot),
			"contact_email": v.ContactEmail,
			"status":        v.Status,
			"pdf_uploaded":  v.PDFPath != "",
			"note":          v.Note,
			"created_at":    v.CreatedAt.Unix(),
			"issued_at":     unixOrZero(v.IssuedAt),
			"rejected_at":   unixOrZero(v.RejectedAt),
		})
	}
	c.JSON(http.StatusOK, gin.H{"invoices": out})
}

func unixOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

// handleIssueInvoice accepts a multipart PDF upload, persists it under
// invoice.PDFDir, marks the invoice issued, and emails the user the PDF
// as an attachment via Resend (best-effort).
func (h *Handler) handleIssueInvoice(c *gin.Context) {
	if h.wallets == nil || h.invoice == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "saas billing disabled"})
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad id"})
		return
	}
	// Limit upload to 8 MiB — fapiao PDFs are well under 1 MiB in
	// practice; cap is here only as a sanity guard.
	if err := c.Request.ParseMultipartForm(8 << 20); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "multipart parse: " + err.Error()})
		return
	}
	note := strings.TrimSpace(c.PostForm("note"))

	fh, _, err := c.Request.FormFile("pdf")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "pdf file required"})
		return
	}
	defer fh.Close()
	data, err := io.ReadAll(io.LimitReader(fh, 16<<20))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "read pdf: " + err.Error()})
		return
	}
	if len(data) < 5 || string(data[:4]) != "%PDF" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "not a PDF file"})
		return
	}

	inv, err := h.wallets.GetInvoice(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "invoice not found"})
		return
	}
	if inv.Status != saasdb.InvoicePending {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invoice is " + inv.Status + ", expected pending"})
		return
	}

	dir := h.invoice.PDFDir
	if dir == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "pdf_dir not configured"})
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "mkdir: " + err.Error()})
		return
	}
	pdfPath := fmt.Sprintf("%s/invoice-%d.pdf", strings.TrimRight(dir, "/"), id)
	if err := os.WriteFile(pdfPath, data, 0o600); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "write pdf: " + err.Error()})
		return
	}

	updated, err := h.wallets.MarkInvoiceIssued(c.Request.Context(), id, pdfPath, note)
	if err != nil {
		_ = os.Remove(pdfPath)
		if errors.Is(err, saasdb.ErrNotFound) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invoice not pending (concurrent change?)"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Best-effort email — kicked off in background so a slow Resend
	// doesn't slow the admin click.
	go h.deliverInvoiceEmail(updated, data)

	c.JSON(http.StatusOK, gin.H{
		"id":         updated.ID,
		"status":     updated.Status,
		"pdf_path":   pdfPath,
		"issued_at":  unixOrZero(updated.IssuedAt),
		"email_sent": h.invoice.Resend != nil && h.invoice.Resend.APIKey != "",
	})
}

func (h *Handler) deliverInvoiceEmail(inv *saasdb.Invoice, pdf []byte) {
	if h.invoice == nil || h.invoice.Resend == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	subj := fmt.Sprintf("您的发票 — ¥%.2f (%s)", inv.CNYAmount, inv.TitleName)
	html := fmt.Sprintf(
		"<p>您好,</p>"+
			"<p>感谢您使用 CPA-Claude。您申请的发票已开具,详见附件 PDF。</p>"+
			"<ul>"+
			"<li>发票编号: #%d</li>"+
			"<li>开票金额: ¥%.2f</li>"+
			"<li>抬头: %s</li>"+
			"</ul>"+
			"<p>如有任何问题,请回复本邮件。</p>",
		inv.ID, inv.CNYAmount, inv.TitleName)
	err := h.invoice.Resend.Send(ctx, resend.Email{
		To:      []string{inv.ContactEmail},
		Subject: subj,
		HTML:    html,
		Attachments: []resend.Attachment{
			{Filename: fmt.Sprintf("invoice-%d.pdf", inv.ID), Content: pdf},
		},
	})
	if err != nil {
		log.Warnf("invoice: delivery email failed for #%d → %s: %v", inv.ID, inv.ContactEmail, err)
	}
}

type rejectInvoiceBody struct {
	Note string `json:"note"`
}

func (h *Handler) handleRejectInvoice(c *gin.Context) {
	if h.wallets == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "saas billing disabled"})
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad id"})
		return
	}
	var b rejectInvoiceBody
	_ = c.BindJSON(&b)
	note := strings.TrimSpace(b.Note)
	if note == "" {
		note = "rejected by admin"
	}
	updated, err := h.wallets.MarkInvoiceRejected(c.Request.Context(), id, note)
	if err != nil {
		if errors.Is(err, saasdb.ErrNotFound) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invoice not pending"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": updated.ID, "status": updated.Status, "note": updated.Note})
}

// handleAdminInvoiceDownload lets the admin pull a previously-issued PDF —
// useful for re-checks. Doesn't apply any token filter (admin auth has
// already covered authorization).
func (h *Handler) handleAdminInvoiceDownload(c *gin.Context) {
	if h.wallets == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "saas billing disabled"})
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad id"})
		return
	}
	inv, err := h.wallets.GetInvoice(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if inv.PDFPath == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "no pdf attached"})
		return
	}
	if _, err := os.Stat(inv.PDFPath); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "pdf missing on disk"})
		return
	}
	c.Header("Content-Type", "application/pdf")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="invoice-%d.pdf"`, inv.ID))
	c.File(inv.PDFPath)
}
