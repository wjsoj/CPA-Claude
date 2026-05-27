package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// InboundAttachment is one entry inside the `attachments` JSON column.
// Mirrors the shape Resend gives us in the webhook payload — we keep the
// IDs so the admin can later GET /mgmt-console/api/inbox/:id/attachments/:aid
// which proxies to Resend's attachment download endpoint.
type InboundAttachment struct {
	ID                 string `json:"id"`
	Filename           string `json:"filename"`
	ContentType        string `json:"content_type"`
	ContentDisposition string `json:"content_disposition,omitempty"`
	ContentID          string `json:"content_id,omitempty"`
}

// InboundEmail is one row of the inbox. body_* / attachments are only
// filled after the async fetch step; pre-fetch rows show subject + from
// without body, which is fine for the list view.
type InboundEmail struct {
	ID            int64
	ResendEmailID string
	MessageID     string
	From          string
	To            []string
	Cc            []string
	Subject       string
	ReceivedAt    time.Time
	BodyHTML      string
	BodyText      string
	Attachments   []InboundAttachment
	RawEvent      string
	FetchedAt     time.Time
	ReadAt        time.Time
	CreatedAt     time.Time
}

// InsertInbound stores a freshly-arrived webhook event. Idempotent on
// (resend_email_id) — Resend retries can land twice and we don't want
// duplicate rows. Returns ErrDuplicate-equivalent by silently returning
// the existing row's ID without error.
func (db *DB) InsertInbound(ctx context.Context, e *InboundEmail) (int64, error) {
	toJSON, _ := json.Marshal(e.To)
	ccJSON, _ := json.Marshal(e.Cc)
	atJSON, _ := json.Marshal(e.Attachments)
	if len(atJSON) == 0 {
		atJSON = []byte("[]")
	}
	now := time.Now().Unix()
	res, err := db.ExecContext(ctx, `
		INSERT INTO inbound_emails (
			resend_email_id, message_id, from_addr, to_addrs, cc_addrs,
			subject, received_at, attachments, raw_event, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(resend_email_id) DO NOTHING`,
		e.ResendEmailID, e.MessageID, e.From, string(toJSON), string(ccJSON),
		e.Subject, e.ReceivedAt.Unix(), string(atJSON), e.RawEvent, now)
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Duplicate — fetch existing ID so caller can still log it.
		var id int64
		_ = db.QueryRowContext(ctx,
			`SELECT id FROM inbound_emails WHERE resend_email_id = ?`,
			e.ResendEmailID).Scan(&id)
		return id, nil
	}
	id, _ := res.LastInsertId()
	return id, nil
}

// PatchInboundBody fills in body_html, body_text, and refreshed attachments
// after the async fetch. fetched_at gets stamped to "now". Called once per
// row after the body has been retrieved from Resend's Received Emails API.
func (db *DB) PatchInboundBody(ctx context.Context, id int64, html, text string, atts []InboundAttachment) error {
	atJSON, _ := json.Marshal(atts)
	if len(atJSON) == 0 {
		atJSON = []byte("[]")
	}
	_, err := db.ExecContext(ctx, `
		UPDATE inbound_emails SET body_html = ?, body_text = ?, attachments = ?, fetched_at = ?
		WHERE id = ?`,
		html, text, string(atJSON), time.Now().Unix(), id)
	return err
}

// MarkInboundRead flips read_at to current time on first read; idempotent
// on already-read rows (read_at stays at the original time).
func (db *DB) MarkInboundRead(ctx context.Context, id int64) error {
	_, err := db.ExecContext(ctx, `
		UPDATE inbound_emails SET read_at = ? WHERE id = ? AND read_at = 0`,
		time.Now().Unix(), id)
	return err
}

// DeleteInbound removes a row (admin-only).
func (db *DB) DeleteInbound(ctx context.Context, id int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM inbound_emails WHERE id = ?`, id)
	return err
}

const inboundCols = `id, resend_email_id, message_id, from_addr, to_addrs, cc_addrs, subject, received_at, body_html, body_text, attachments, raw_event, fetched_at, read_at, created_at`

func scanInbound(row interface{ Scan(...any) error }) (*InboundEmail, error) {
	var v InboundEmail
	var to, cc, atts string
	var recv, fetched, read, created int64
	if err := row.Scan(&v.ID, &v.ResendEmailID, &v.MessageID, &v.From, &to, &cc,
		&v.Subject, &recv, &v.BodyHTML, &v.BodyText, &atts, &v.RawEvent,
		&fetched, &read, &created); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(to), &v.To)
	_ = json.Unmarshal([]byte(cc), &v.Cc)
	_ = json.Unmarshal([]byte(atts), &v.Attachments)
	v.ReceivedAt = time.Unix(recv, 0)
	if fetched > 0 {
		v.FetchedAt = time.Unix(fetched, 0)
	}
	if read > 0 {
		v.ReadAt = time.Unix(read, 0)
	}
	v.CreatedAt = time.Unix(created, 0)
	return &v, nil
}

// GetInbound returns one inbox row by PK.
func (db *DB) GetInbound(ctx context.Context, id int64) (*InboundEmail, error) {
	row := db.QueryRowContext(ctx, `SELECT `+inboundCols+` FROM inbound_emails WHERE id = ?`, id)
	v, err := scanInbound(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return v, err
}

// ListInbound returns the admin inbox listing. status="" → all; "unread" →
// only read_at=0. q matches subject / from_addr (LIKE %q%).
func (db *DB) ListInbound(ctx context.Context, status, q string, limit int) ([]*InboundEmail, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	args := []any{}
	where := "1=1"
	if status == "unread" {
		where += " AND read_at = 0"
	}
	if q = strings.TrimSpace(q); q != "" {
		where += " AND (subject LIKE ? OR from_addr LIKE ?)"
		like := "%" + q + "%"
		args = append(args, like, like)
	}
	args = append(args, limit)
	rows, err := db.QueryContext(ctx,
		`SELECT `+inboundCols+` FROM inbound_emails WHERE `+where+` ORDER BY received_at DESC LIMIT ?`,
		args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*InboundEmail
	for rows.Next() {
		v, err := scanInbound(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// CountInboundUnread returns the unread email count — used by the admin
// panel for the bell badge.
func (db *DB) CountInboundUnread(ctx context.Context) (int, error) {
	var n int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM inbound_emails WHERE read_at = 0`).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}
