package admin

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	saasdb "github.com/wjsoj/CPA-Claude/internal/saas/db"
	"github.com/wjsoj/CPA-Claude/internal/saas/resend"
)

// handleInboxList — GET /mgmt-console/api/inbox?status=&q=
// status: "" (all) or "unread"
// q: substring against subject / from_addr
func (h *Handler) handleInboxList(c *gin.Context) {
	if h.wallets == nil {
		c.JSON(http.StatusOK, gin.H{"emails": []any{}, "unread": 0})
		return
	}
	status := strings.TrimSpace(c.Query("status"))
	q := c.Query("q")
	rows, err := h.wallets.ListInbound(c.Request.Context(), status, q, 200)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	unread, _ := h.wallets.CountInboundUnread(c.Request.Context())
	out := make([]gin.H, 0, len(rows))
	for _, v := range rows {
		out = append(out, gin.H{
			"id":              v.ID,
			"resend_email_id": v.ResendEmailID,
			"from":            v.From,
			"to":              v.To,
			"subject":         v.Subject,
			"received_at":     v.ReceivedAt.Unix(),
			"fetched":         !v.FetchedAt.IsZero(),
			"unread":          v.ReadAt.IsZero(),
			"has_attachments": len(v.Attachments) > 0,
		})
	}
	c.JSON(http.StatusOK, gin.H{"emails": out, "unread": unread})
}

// handleInboxGet — GET /mgmt-console/api/inbox/:id. Marks the row read as
// a side effect (idempotent — only the first read sets read_at).
func (h *Handler) handleInboxGet(c *gin.Context) {
	if h.wallets == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "saas disabled"})
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad id"})
		return
	}
	v, err := h.wallets.GetInbound(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	_ = h.wallets.MarkInboundRead(c.Request.Context(), id)

	atts := make([]gin.H, 0, len(v.Attachments))
	for _, a := range v.Attachments {
		atts = append(atts, gin.H{
			"id":           a.ID,
			"filename":     a.Filename,
			"content_type": a.ContentType,
			"disposition":  a.ContentDisposition,
			"content_id":   a.ContentID,
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"id":              v.ID,
		"resend_email_id": v.ResendEmailID,
		"message_id":      v.MessageID,
		"from":            v.From,
		"to":              v.To,
		"cc":              v.Cc,
		"subject":         v.Subject,
		"received_at":     v.ReceivedAt.Unix(),
		"body_html":       v.BodyHTML,
		"body_text":       v.BodyText,
		"attachments":     atts,
		"fetched":         !v.FetchedAt.IsZero(),
	})
}

// handleInboxDelete — DELETE /mgmt-console/api/inbox/:id
func (h *Handler) handleInboxDelete(c *gin.Context) {
	if h.wallets == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "saas disabled"})
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad id"})
		return
	}
	if err := h.wallets.DeleteInbound(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// handleInboxAttachment — GET /mgmt-console/api/inbox/:id/attachments/:aid
// Proxies the attachment from Resend's API. We don't cache attachment
// bytes in our DB because Resend already stores them and most attachments
// will be downloaded zero or one times.
func (h *Handler) handleInboxAttachment(c *gin.Context) {
	if h.wallets == nil || h.invoice == nil || h.invoice.Resend == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "resend client not configured"})
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad id"})
		return
	}
	attID := strings.TrimSpace(c.Param("aid"))
	if attID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad aid"})
		return
	}
	v, err := h.wallets.GetInbound(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	var filename, ctype string
	for _, a := range v.Attachments {
		if a.ID == attID {
			filename = a.Filename
			ctype = a.ContentType
			break
		}
	}
	if filename == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "attachment not found"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*1e9)
	defer cancel()
	ct, data, err := h.invoice.Resend.GetReceivedAttachment(ctx, v.ResendEmailID, attID)
	if err != nil {
		log.Warnf("admin inbox attachment fetch: %v", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	if ct == "" {
		ct = ctype
	}
	if ct == "" {
		ct = "application/octet-stream"
	}
	c.Header("Content-Disposition", "attachment; filename=\""+saasdbSanitizeFilename(filename)+"\"")
	c.Data(http.StatusOK, ct, data)
}

// saasdbSanitizeFilename strips characters that would break a Content-
// Disposition header. We're conservative — anything outside a safe set is
// replaced with underscore. Doesn't need to be reversible.
func saasdbSanitizeFilename(name string) string {
	const safe = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._- "
	var b strings.Builder
	for _, r := range name {
		if strings.ContainsRune(safe, r) {
			b.WriteRune(r)
			continue
		}
		b.WriteRune('_')
	}
	if b.Len() == 0 {
		return "attachment"
	}
	return b.String()
}

// Silence unused-import vet when saasdb / resend are only touched by other
// files in this package. We do use them at compile time via h.wallets and
// h.invoice.Resend so the references below are merely lint-pleasers.
var _ = saasdb.ErrNotFound
var _ = resend.ErrDisabled
