package resend

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// VerifyWebhook validates a Svix-style signed webhook request. Resend
// signs every webhook with the secret you got from the dashboard (which
// starts with "whsec_"). Returns nil iff the body's signature matches and
// the timestamp is within ±5 minutes of now (replay window).
//
// secret is the raw "whsec_..." string from the Resend webhook detail page.
// Headers needed: svix-id, svix-timestamp, svix-signature. Body is the
// exact bytes received (do NOT re-marshal — Svix signs the byte-for-byte
// payload).
func VerifyWebhook(secret string, headers http.Header, body []byte) error {
	if strings.TrimSpace(secret) == "" {
		return errors.New("resend webhook secret not configured")
	}
	id := headers.Get("Svix-Id")
	ts := headers.Get("Svix-Timestamp")
	sigHdr := headers.Get("Svix-Signature")
	if id == "" || ts == "" || sigHdr == "" {
		return errors.New("missing svix-* headers")
	}
	tsInt, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return fmt.Errorf("bad svix-timestamp: %w", err)
	}
	if delta := time.Now().Unix() - tsInt; delta > 5*60 || delta < -5*60 {
		return fmt.Errorf("svix-timestamp outside ±5min window (delta=%ds)", delta)
	}

	// Resend's secret has a "whsec_" prefix followed by a base64-encoded
	// key. Strip the prefix, decode, HMAC-SHA256 over "<id>.<ts>.<body>",
	// base64-encode the digest, compare against any v1,... entry in the
	// space-separated signature header.
	keyB64 := strings.TrimPrefix(secret, "whsec_")
	key, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil {
		return fmt.Errorf("decode secret: %w", err)
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(id))
	mac.Write([]byte("."))
	mac.Write([]byte(ts))
	mac.Write([]byte("."))
	mac.Write(body)
	want := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	for _, part := range strings.Split(sigHdr, " ") {
		v, sig, ok := strings.Cut(part, ",")
		if !ok || v != "v1" {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(sig), []byte(want)) == 1 {
			return nil
		}
	}
	return errors.New("no matching v1 signature")
}

// InboundAttachmentMeta is the shape Resend returns inside the webhook's
// data.attachments[] AND inside the Received Emails API responses. Mirrors
// the wire format directly so we can unmarshal both paths into it.
type InboundAttachmentMeta struct {
	ID                 string `json:"id"`
	Filename           string `json:"filename"`
	ContentType        string `json:"content_type"`
	ContentDisposition string `json:"content_disposition,omitempty"`
	ContentID          string `json:"content_id,omitempty"`
}

// InboundEvent is what arrives on the webhook for type="email.received".
type InboundEvent struct {
	Type      string `json:"type"`
	CreatedAt string `json:"created_at"`
	Data      struct {
		EmailID     string                  `json:"email_id"`
		CreatedAt   string                  `json:"created_at"`
		From        string                  `json:"from"`
		To          []string                `json:"to"`
		Cc          []string                `json:"cc"`
		Bcc         []string                `json:"bcc"`
		Subject     string                  `json:"subject"`
		MessageID   string                  `json:"message_id"`
		Attachments []InboundAttachmentMeta `json:"attachments"`
	} `json:"data"`
}

// ReceivedEmail is the shape Resend returns from
// GET /emails/received/:id. Has the same metadata as the webhook plus
// the actual body fields.
type ReceivedEmail struct {
	ID          string                  `json:"id"`
	From        string                  `json:"from"`
	To          []string                `json:"to"`
	Cc          []string                `json:"cc"`
	Subject     string                  `json:"subject"`
	HTML        string                  `json:"html"`
	Text        string                  `json:"text"`
	Attachments []InboundAttachmentMeta `json:"attachments"`
	CreatedAt   string                  `json:"created_at"`
}

// GetReceivedEmail fetches one inbound email's full body+attachment list
// by Resend's email_id. Used by the webhook handler in an async goroutine
// after the metadata row is persisted.
func (c *Client) GetReceivedEmail(ctx context.Context, emailID string) (*ReceivedEmail, error) {
	if c == nil || c.APIKey == "" {
		return nil, ErrDisabled
	}
	url := "https://api.resend.com/emails/receiving/" + emailID
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20)) // 8 MiB cap
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("resend: GET received/%s: status %d: %s", emailID, resp.StatusCode, string(body))
	}
	var r ReceivedEmail
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("resend: decode received/%s: %w", emailID, err)
	}
	return &r, nil
}

// GetReceivedAttachment fetches one attachment's raw bytes. Resend uses
// (email_id, attachment_id) and returns Content-Type set to the original
// MIME type.
func (c *Client) GetReceivedAttachment(ctx context.Context, emailID, attID string) (contentType string, data []byte, err error) {
	if c == nil || c.APIKey == "" {
		return "", nil, ErrDisabled
	}
	url := fmt.Sprintf("https://api.resend.com/emails/receiving/%s/attachments/%s", emailID, attID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	data, _ = io.ReadAll(io.LimitReader(resp.Body, 32<<20)) // 32 MiB cap
	if resp.StatusCode >= 300 {
		return "", nil, fmt.Errorf("resend: attachment %s/%s: status %d: %s", emailID, attID, resp.StatusCode, string(data))
	}
	return resp.Header.Get("Content-Type"), data, nil
}
