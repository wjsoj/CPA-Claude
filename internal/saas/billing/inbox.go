// inbox.go wires the Resend inbound-email webhook. One POST endpoint,
// public (no auth — protected by the Svix HMAC signature), and an
// async body-fetch goroutine that backfills body_html / body_text into
// the inbound_emails row after the webhook 200s. The admin-facing list
// + detail endpoints live in internal/admin/inbox.go.
package billing

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"github.com/wjsoj/CPA-Claude/internal/saas/db"
	"github.com/wjsoj/CPA-Claude/internal/saas/resend"
)

// InboxHandler exposes POST /api/webhooks/resend-inbound. Holds the DB
// and the Resend client (for the async body fetch).
type InboxHandler struct {
	DB            *db.DB
	Resend        *resend.Client
	WebhookSecret string
}

// NewInboxHandler returns the wired handler. WebhookSecret empty disables
// the route — webhook calls return 503 so a misconfigured deploy doesn't
// silently swallow inbound mail.
func NewInboxHandler(store *db.DB, rc *resend.Client, secret string) *InboxHandler {
	return &InboxHandler{DB: store, Resend: rc, WebhookSecret: secret}
}

// PublicRoutes mounts the webhook on the engine. Path is fixed to
// /api/webhooks/resend-inbound; the operator configures the matching URL
// on Resend's webhook page.
func (h *InboxHandler) PublicRoutes(g *gin.RouterGroup) {
	g.POST("/webhooks/resend-inbound", h.handleWebhook)
}

func (h *InboxHandler) handleWebhook(c *gin.Context) {
	if h.WebhookSecret == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "inbound webhook not configured"})
		return
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "read body: " + err.Error()})
		return
	}
	if err := resend.VerifyWebhook(h.WebhookSecret, c.Request.Header, body); err != nil {
		log.Warnf("resend-inbound: signature reject: %v", err)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "bad signature"})
		return
	}
	var ev resend.InboundEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad json: " + err.Error()})
		return
	}
	if ev.Type != "email.received" {
		// Ignore other event types — Resend may grow more later, but we
		// only care about inbound mail right now.
		c.JSON(http.StatusOK, gin.H{"ok": true, "ignored": ev.Type})
		return
	}

	atts := make([]db.InboundAttachment, 0, len(ev.Data.Attachments))
	for _, a := range ev.Data.Attachments {
		atts = append(atts, db.InboundAttachment{
			ID:                 a.ID,
			Filename:           a.Filename,
			ContentType:        a.ContentType,
			ContentDisposition: a.ContentDisposition,
			ContentID:          a.ContentID,
		})
	}
	recv := parseResendTime(ev.Data.CreatedAt)
	if recv.IsZero() {
		recv = time.Now()
	}
	row := &db.InboundEmail{
		ResendEmailID: ev.Data.EmailID,
		MessageID:     ev.Data.MessageID,
		From:          ev.Data.From,
		To:            ev.Data.To,
		Cc:            ev.Data.Cc,
		Subject:       ev.Data.Subject,
		ReceivedAt:    recv,
		Attachments:   atts,
		RawEvent:      string(body),
	}
	id, err := h.DB.InsertInbound(c.Request.Context(), row)
	if err != nil {
		log.Errorf("resend-inbound: insert failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	log.Infof("resend-inbound: stored id=%d resend_id=%s from=%s subject=%q",
		id, ev.Data.EmailID, ev.Data.From, ev.Data.Subject)

	// Fire-and-forget body fetch. The webhook ACK must be fast (Resend
	// retries on >5s response time); the body fetch is best-effort and
	// the row is already usable in the admin list.
	go h.fetchBody(id, ev.Data.EmailID)

	c.JSON(http.StatusOK, gin.H{"ok": true, "id": id})
}

func (h *InboxHandler) fetchBody(id int64, emailID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r, err := h.Resend.GetReceivedEmail(ctx, emailID)
	if err != nil {
		log.Warnf("resend-inbound: fetch body for %s failed: %v", emailID, err)
		return
	}
	atts := make([]db.InboundAttachment, 0, len(r.Attachments))
	for _, a := range r.Attachments {
		atts = append(atts, db.InboundAttachment{
			ID:                 a.ID,
			Filename:           a.Filename,
			ContentType:        a.ContentType,
			ContentDisposition: a.ContentDisposition,
			ContentID:          a.ContentID,
		})
	}
	if err := h.DB.PatchInboundBody(ctx, id, r.HTML, r.Text, atts); err != nil {
		log.Warnf("resend-inbound: patch body for id=%d failed: %v", id, err)
	}
}

// parseResendTime tolerates the two formats Resend mixes in event JSON:
// RFC3339 with nanos and "...+00:00" offset. Returns zero on failure.
func parseResendTime(s string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.999999-07:00"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
