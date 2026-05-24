// Package resend is a tiny client around the Resend transactional email
// API — just the one POST /emails call we need for invoice notifications.
//
// Designed to fail soft: an empty API key turns Send into a no-op (logged
// at warn level) so the rest of the invoice flow keeps working even when
// the operator hasn't configured Resend yet. Same applies to network
// failures: we log and return the error but never panic the caller.
package resend

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

const apiURL = "https://api.resend.com/emails"

// Client is a thin wrapper around the Resend /emails endpoint. The zero
// value is unusable — use New.
type Client struct {
	APIKey string
	From   string

	HTTP *http.Client
}

// New returns a configured client. apiKey may be empty — in that case
// Send returns ErrDisabled without making any network call.
func New(apiKey, from string) *Client {
	return &Client{
		APIKey: strings.TrimSpace(apiKey),
		From:   strings.TrimSpace(from),
		HTTP: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// ErrDisabled is returned by Send when the client has no API key
// configured. Callers should treat it as a non-fatal "skipped" — the
// invoice itself was created/issued; only the notification side-effect
// degraded.
var ErrDisabled = errors.New("resend: api_key not configured")

// Attachment is the wire shape for the optional "attachments" field —
// Filename is what the recipient sees, Content is the raw bytes (we'll
// base64-encode for them).
type Attachment struct {
	Filename string
	Content  []byte
}

// Email captures the subset of Resend's request body we use.
type Email struct {
	To          []string
	Subject     string
	HTML        string
	Text        string
	ReplyTo     string
	Attachments []Attachment
}

type wireAttachment struct {
	Filename string `json:"filename"`
	Content  string `json:"content"` // base64
}

type wireBody struct {
	From        string           `json:"from"`
	To          []string         `json:"to"`
	Subject     string           `json:"subject"`
	HTML        string           `json:"html,omitempty"`
	Text        string           `json:"text,omitempty"`
	ReplyTo     string           `json:"reply_to,omitempty"`
	Attachments []wireAttachment `json:"attachments,omitempty"`
}

// Send delivers one email. Returns ErrDisabled when the client is
// not configured. Logs at info on success and warn on failure — callers
// are free to also log/handle the returned error.
func (c *Client) Send(ctx context.Context, e Email) error {
	if c == nil || c.APIKey == "" {
		log.Warnf("resend: skipped (api_key empty): to=%v subject=%q", e.To, e.Subject)
		return ErrDisabled
	}
	if len(e.To) == 0 {
		return errors.New("resend: to list empty")
	}
	body := wireBody{
		From:    c.From,
		To:      e.To,
		Subject: e.Subject,
		HTML:    e.HTML,
		Text:    e.Text,
		ReplyTo: e.ReplyTo,
	}
	for _, a := range e.Attachments {
		body.Attachments = append(body.Attachments, wireAttachment{
			Filename: a.Filename,
			Content:  base64.StdEncoding.EncodeToString(a.Content),
		})
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")

	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		log.Warnf("resend: send failed: %v (to=%v subject=%q)", err, e.To, e.Subject)
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 300 {
		log.Warnf("resend: upstream %d: %s (to=%v subject=%q)", resp.StatusCode, string(respBody), e.To, e.Subject)
		return fmt.Errorf("resend: status %d: %s", resp.StatusCode, string(respBody))
	}
	log.Infof("resend: sent to=%v subject=%q", e.To, e.Subject)
	return nil
}
