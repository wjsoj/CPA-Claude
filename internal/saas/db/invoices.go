package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Invoice lifecycle states.
const (
	InvoicePending  = "pending"
	InvoiceIssued   = "issued"
	InvoiceRejected = "rejected"
)

// ErrInsufficientInvoiceable is returned by CreateInvoice when the request
// exceeds the wallet's available-to-invoice CNY pool. The pool is computed
// as `sum(paid orders CNY) - sum(pending|issued invoices CNY)` so retries
// or double-clicks can never over-invoice.
var ErrInsufficientInvoiceable = errors.New("requested CNY exceeds invoiceable balance")

// InvoiceTitle is one persisted header (company name + tax info) the user
// has either typed in or selected from the in-app search. Reused across
// future requests via the suggestion endpoint.
type InvoiceTitle struct {
	ID          int64
	Token       string
	Name        string
	TaxNo       string
	Address     string
	Phone       string
	Bank        string
	BankAccount string
	LastUsedAt  time.Time
	CreatedAt   time.Time
}

// Invoice is one fapiao request — created by the user, transitioned to
// issued (with pdf_path) or rejected (with note) by an admin.
type Invoice struct {
	ID            int64
	Token         string
	CNYAmount     float64
	TitleName     string
	TitleSnapshot string // JSON snapshot of InvoiceTitle at request time
	ContactEmail  string
	Status        string
	PDFPath       string
	Note          string
	CreatedAt     time.Time
	IssuedAt      time.Time
	RejectedAt    time.Time
}

// InvoiceSummary captures the per-token invoice quota dashboard. Numbers
// are in CNY for symmetry with how invoices are denominated (a fapiao is
// always in RMB even though the wallet itself is in USD).
type InvoiceSummary struct {
	PaidCNY      float64 // sum of cny_amount across paid orders
	LockedCNY    float64 // sum of pending invoices' cny_amount
	IssuedCNY    float64 // sum of issued invoices' cny_amount
	AvailableCNY float64 // PaidCNY - LockedCNY - IssuedCNY
}

// InvoiceableCNY returns the per-token invoice summary.
func (db *DB) InvoiceableCNY(ctx context.Context, token string) (*InvoiceSummary, error) {
	var s InvoiceSummary
	if err := db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(cny_amount),0) FROM alipay_orders WHERE token = ? AND status = ?`,
		token, OrderPaid).Scan(&s.PaidCNY); err != nil {
		return nil, err
	}
	if err := db.QueryRowContext(ctx,
		`SELECT
			COALESCE(SUM(CASE WHEN status = ? THEN cny_amount ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = ? THEN cny_amount ELSE 0 END), 0)
		 FROM invoices WHERE token = ?`,
		InvoicePending, InvoiceIssued, token).Scan(&s.LockedCNY, &s.IssuedCNY); err != nil {
		return nil, err
	}
	s.AvailableCNY = round2(s.PaidCNY - s.LockedCNY - s.IssuedCNY)
	if s.AvailableCNY < 0 {
		// Numerical drift only — never an over-invoice situation, since
		// CreateInvoice's gate is checked under the transaction below.
		s.AvailableCNY = 0
	}
	return &s, nil
}

// CreateInvoice atomically: 1) re-checks invoiceable >= cny under a
// transaction, 2) inserts the row in pending state. Returns the freshly
// created invoice (with auto-incremented ID).
func (db *DB) CreateInvoice(ctx context.Context, token string, cny float64, title InvoiceTitle, contactEmail string) (*Invoice, error) {
	if cny <= 0 {
		return nil, errors.New("cny amount must be positive")
	}
	title.Token = token
	snap, err := json.Marshal(invoiceTitlePayload(title))
	if err != nil {
		return nil, err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// Re-compute availability inside the tx — guarantees no two concurrent
	// requests can both pass the pre-check then collectively over-invoice.
	var paid, lockedIssued float64
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(cny_amount),0) FROM alipay_orders WHERE token = ? AND status = ?`,
		token, OrderPaid).Scan(&paid); err != nil {
		return nil, err
	}
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(cny_amount),0) FROM invoices WHERE token = ? AND status IN (?, ?)`,
		token, InvoicePending, InvoiceIssued).Scan(&lockedIssued); err != nil {
		return nil, err
	}
	avail := paid - lockedIssued
	// Half-fen tolerance so 100.00 == 100.00 doesn't lose to FP drift.
	if cny > avail+0.005 {
		return nil, fmt.Errorf("%w: requested %.2f available %.2f", ErrInsufficientInvoiceable, cny, avail)
	}

	now := time.Now().Unix()
	res, err := tx.ExecContext(ctx, `
		INSERT INTO invoices (token, cny_amount, title_name, title_snapshot, contact_email, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		token, cny, strings.TrimSpace(title.Name), string(snap),
		strings.TrimSpace(contactEmail), InvoicePending, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()

	// Persist / refresh the title row so the next-request suggestion
	// surface remembers it.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO invoice_titles (token, name, tax_no, address, phone, bank, bank_account, last_used_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(token, name) DO UPDATE SET
			tax_no = excluded.tax_no,
			address = excluded.address,
			phone = excluded.phone,
			bank = excluded.bank,
			bank_account = excluded.bank_account,
			last_used_at = excluded.last_used_at`,
		token, strings.TrimSpace(title.Name), strings.TrimSpace(title.TaxNo),
		strings.TrimSpace(title.Address), strings.TrimSpace(title.Phone),
		strings.TrimSpace(title.Bank), strings.TrimSpace(title.BankAccount),
		now, now); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	inv, err := db.GetInvoice(ctx, id)
	if err != nil {
		return nil, err
	}
	return inv, nil
}

// invoiceTitlePayload is the serializable view of an InvoiceTitle — used as
// the frozen snapshot inside invoices.title_snapshot and as the public
// suggestion-API shape.
func invoiceTitlePayload(t InvoiceTitle) map[string]any {
	return map[string]any{
		"name":         t.Name,
		"tax_no":       t.TaxNo,
		"address":      t.Address,
		"phone":        t.Phone,
		"bank":         t.Bank,
		"bank_account": t.BankAccount,
	}
}

func scanInvoice(row interface{ Scan(...any) error }) (*Invoice, error) {
	var v Invoice
	var c, i, r int64
	if err := row.Scan(&v.ID, &v.Token, &v.CNYAmount, &v.TitleName, &v.TitleSnapshot,
		&v.ContactEmail, &v.Status, &v.PDFPath, &v.Note, &c, &i, &r); err != nil {
		return nil, err
	}
	v.CreatedAt = time.Unix(c, 0)
	if i > 0 {
		v.IssuedAt = time.Unix(i, 0)
	}
	if r > 0 {
		v.RejectedAt = time.Unix(r, 0)
	}
	return &v, nil
}

const invoiceCols = `id, token, cny_amount, title_name, title_snapshot, contact_email, status, pdf_path, note, created_at, issued_at, rejected_at`

// GetInvoice returns one invoice by primary key.
func (db *DB) GetInvoice(ctx context.Context, id int64) (*Invoice, error) {
	row := db.QueryRowContext(ctx, `SELECT `+invoiceCols+` FROM invoices WHERE id = ?`, id)
	v, err := scanInvoice(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return v, err
}

// ListInvoicesByToken returns the per-user invoice history, newest first.
func (db *DB) ListInvoicesByToken(ctx context.Context, token string, limit int) ([]*Invoice, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := db.QueryContext(ctx,
		`SELECT `+invoiceCols+` FROM invoices WHERE token = ? ORDER BY id DESC LIMIT ?`,
		token, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Invoice
	for rows.Next() {
		v, err := scanInvoice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ListInvoices returns the admin view of all invoices, optionally filtered.
// status="" → all states; q matches against title_name (LIKE %q%) and
// contact_email.
func (db *DB) ListInvoices(ctx context.Context, status, q string, limit int) ([]*Invoice, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	args := []any{}
	where := "1=1"
	if status != "" {
		where += " AND status = ?"
		args = append(args, status)
	}
	if q = strings.TrimSpace(q); q != "" {
		where += " AND (title_name LIKE ? OR contact_email LIKE ? OR token LIKE ?)"
		like := "%" + q + "%"
		args = append(args, like, like, like)
	}
	args = append(args, limit)
	rows, err := db.QueryContext(ctx,
		`SELECT `+invoiceCols+` FROM invoices WHERE `+where+` ORDER BY id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Invoice
	for rows.Next() {
		v, err := scanInvoice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// MarkInvoiceIssued transitions a pending invoice → issued and records the
// PDF path + optional admin note. Only pending rows are accepted (idempotent
// retries return ErrNotFound and the caller should treat it as already done).
func (db *DB) MarkInvoiceIssued(ctx context.Context, id int64, pdfPath, note string) (*Invoice, error) {
	res, err := db.ExecContext(ctx,
		`UPDATE invoices SET status = ?, pdf_path = ?, note = ?, issued_at = ?
		 WHERE id = ? AND status = ?`,
		InvoiceIssued, pdfPath, note, time.Now().Unix(), id, InvoicePending)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	return db.GetInvoice(ctx, id)
}

// MarkInvoiceRejected transitions a pending invoice → rejected. The locked
// CNY pool is freed immediately because InvoiceableCNY only counts pending
// + issued.
func (db *DB) MarkInvoiceRejected(ctx context.Context, id int64, note string) (*Invoice, error) {
	res, err := db.ExecContext(ctx,
		`UPDATE invoices SET status = ?, note = ?, rejected_at = ?
		 WHERE id = ? AND status = ?`,
		InvoiceRejected, note, time.Now().Unix(), id, InvoicePending)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	return db.GetInvoice(ctx, id)
}

// ListInvoiceTitles returns the per-token saved headers, newest-used first.
// Empty query matches all.
func (db *DB) ListInvoiceTitles(ctx context.Context, token, q string, limit int) ([]*InvoiceTitle, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	args := []any{token}
	where := "token = ?"
	if q = strings.TrimSpace(q); q != "" {
		where += " AND (name LIKE ? OR tax_no LIKE ?)"
		like := "%" + q + "%"
		args = append(args, like, like)
	}
	args = append(args, limit)
	rows, err := db.QueryContext(ctx,
		`SELECT id, token, name, tax_no, address, phone, bank, bank_account, last_used_at, created_at
		 FROM invoice_titles WHERE `+where+` ORDER BY last_used_at DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*InvoiceTitle
	for rows.Next() {
		var t InvoiceTitle
		var lu, c int64
		if err := rows.Scan(&t.ID, &t.Token, &t.Name, &t.TaxNo, &t.Address, &t.Phone, &t.Bank, &t.BankAccount, &lu, &c); err != nil {
			return nil, err
		}
		t.LastUsedAt = time.Unix(lu, 0)
		t.CreatedAt = time.Unix(c, 0)
		out = append(out, &t)
	}
	return out, rows.Err()
}

// UpsertInvoiceTitle inserts or refreshes a saved title — used by the
// frontend's "save without immediately requesting an invoice" path.
func (db *DB) UpsertInvoiceTitle(ctx context.Context, t InvoiceTitle) error {
	if strings.TrimSpace(t.Name) == "" {
		return errors.New("name required")
	}
	now := time.Now().Unix()
	_, err := db.ExecContext(ctx, `
		INSERT INTO invoice_titles (token, name, tax_no, address, phone, bank, bank_account, last_used_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(token, name) DO UPDATE SET
			tax_no = excluded.tax_no,
			address = excluded.address,
			phone = excluded.phone,
			bank = excluded.bank,
			bank_account = excluded.bank_account,
			last_used_at = excluded.last_used_at`,
		t.Token, strings.TrimSpace(t.Name), strings.TrimSpace(t.TaxNo),
		strings.TrimSpace(t.Address), strings.TrimSpace(t.Phone),
		strings.TrimSpace(t.Bank), strings.TrimSpace(t.BankAccount),
		now, now)
	return err
}

// DeleteInvoiceTitle removes one saved header. No-op when absent.
func (db *DB) DeleteInvoiceTitle(ctx context.Context, token string, id int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM invoice_titles WHERE id = ? AND token = ?`, id, token)
	return err
}

// round2 is the same fen-rounding helper used by the orders side.
func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}
