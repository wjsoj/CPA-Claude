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
	// WorkspaceID is 0 for a personal invoice (Token is both the filer and
	// the sole quota source) and >0 for a team invoice, where Token is only
	// the admin who filed it and the quota came from invoice_allocations.
	WorkspaceID int64
}

// IsTeam reports whether the invoice drew on a workspace's members rather
// than on the filing token's own pool.
func (v *Invoice) IsTeam() bool { return v.WorkspaceID > 0 }

// InvoiceAllocation is one member's share of a team invoice. Rows are
// immutable: a rejected invoice releases the quota through the status join
// in InvoiceableCNY, not by deletion, so the table stays an audit record.
type InvoiceAllocation struct {
	InvoiceID int64
	Token     string
	CNYAmount float64
}

// InvoiceForToken is one row of a token's own invoice history — its personal
// invoices plus the team invoices that drew on its quota.
type InvoiceForToken struct {
	Invoice
	// AllocatedCNY is what THIS token contributed: the face value for a
	// personal invoice, the member's share for a team one.
	AllocatedCNY  float64
	WorkspaceName string
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

// rowQuerier is the read surface shared by *sql.DB, *sql.Tx and *sql.Conn, so
// the quota arithmetic below can run either standalone or inside the write
// lock CreateWorkspaceInvoice holds. There must be exactly one implementation
// of "how much can this token still invoice" — a second copy that drifts is
// how you end up issuing more fapiao than the customer ever paid for.
type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// invoiceSummaryFor computes one token's invoice quota against q.
//
// Two independent claims consume the pool and both must be counted:
//   - the token's own PERSONAL invoices (workspace_id = 0). The filter is
//     load-bearing: a team invoice is stored under the filing admin's token
//     too, so without it that admin would be debited the whole face value
//     here AND their allocation below — double-charged for one invoice.
//   - the token's share of TEAM invoices, via invoice_allocations. Only
//     pending|issued count, which is what releases a rejected team invoice's
//     quota without ever mutating the (immutable) allocation rows.
func invoiceSummaryFor(ctx context.Context, q rowQuerier, token string) (*InvoiceSummary, error) {
	var s InvoiceSummary
	if err := q.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(cny_amount),0) FROM alipay_orders WHERE token = ? AND status = ?`,
		token, OrderPaid).Scan(&s.PaidCNY); err != nil {
		return nil, err
	}
	if err := q.QueryRowContext(ctx,
		`SELECT
			COALESCE(SUM(CASE WHEN status = ? THEN cny_amount ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = ? THEN cny_amount ELSE 0 END), 0)
		 FROM invoices WHERE token = ? AND workspace_id = 0`,
		InvoicePending, InvoiceIssued, token).Scan(&s.LockedCNY, &s.IssuedCNY); err != nil {
		return nil, err
	}
	var teamLocked, teamIssued float64
	if err := q.QueryRowContext(ctx,
		`SELECT
			COALESCE(SUM(CASE WHEN i.status = ? THEN a.cny_amount ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN i.status = ? THEN a.cny_amount ELSE 0 END), 0)
		 FROM invoice_allocations a
		 JOIN invoices i ON i.id = a.invoice_id
		 WHERE a.token = ? AND i.status IN (?, ?)`,
		InvoicePending, InvoiceIssued, token, InvoicePending, InvoiceIssued).
		Scan(&teamLocked, &teamIssued); err != nil {
		return nil, err
	}
	s.LockedCNY += teamLocked
	s.IssuedCNY += teamIssued

	s.AvailableCNY = round2(s.PaidCNY - s.LockedCNY - s.IssuedCNY)
	if s.AvailableCNY < 0 {
		// Numerical drift only — never an over-invoice situation, since
		// CreateInvoice's gate is checked under the transaction below.
		s.AvailableCNY = 0
	}
	return &s, nil
}

// InvoiceableCNY returns the per-token invoice summary.
func (db *DB) InvoiceableCNY(ctx context.Context, token string) (*InvoiceSummary, error) {
	return invoiceSummaryFor(ctx, db.DB, token)
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
	// Shared with the read path (and with the team path) so a personal
	// request can't ignore quota a team invoice has already consumed.
	sum, err := invoiceSummaryFor(ctx, tx, token)
	if err != nil {
		return nil, err
	}
	avail := sum.AvailableCNY
	// Half-fen tolerance so 100.00 == 100.00 doesn't lose to FP drift.
	if cny > avail+invoiceEpsilon {
		return nil, fmt.Errorf("%w: requested %.2f available %.2f", ErrInsufficientInvoiceable, cny, avail)
	}

	now := time.Now().Unix()
	res, err := tx.ExecContext(ctx, `
		INSERT INTO invoices (token, cny_amount, title_name, title_snapshot, contact_email, status, created_at, workspace_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, 0)`,
		token, cny, strings.TrimSpace(title.Name), string(snap),
		strings.TrimSpace(contactEmail), InvoicePending, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()

	// Persist / refresh the title row so the next-request suggestion
	// surface remembers it.
	if _, err := tx.ExecContext(ctx, invoiceTitleUpsertSQL,
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

// invoiceTitleUpsertSQL is shared by the personal and team create paths and by
// the standalone save endpoint, so the header book behaves identically however
// the title got typed in.
const invoiceTitleUpsertSQL = `
	INSERT INTO invoice_titles (token, name, tax_no, address, phone, bank, bank_account, last_used_at, created_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(token, name) DO UPDATE SET
		tax_no = excluded.tax_no,
		address = excluded.address,
		phone = excluded.phone,
		bank = excluded.bank,
		bank_account = excluded.bank_account,
		last_used_at = excluded.last_used_at`

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
	if err := scanInvoiceInto(row, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// scanInvoiceInto reads the invoiceCols prefix into v, then any extra columns
// the caller selected after it (used by the joined listings).
func scanInvoiceInto(row interface{ Scan(...any) error }, v *Invoice, extra ...any) error {
	var c, i, r int64
	dest := append([]any{&v.ID, &v.Token, &v.CNYAmount, &v.TitleName, &v.TitleSnapshot,
		&v.ContactEmail, &v.Status, &v.PDFPath, &v.Note, &c, &i, &r, &v.WorkspaceID}, extra...)
	if err := row.Scan(dest...); err != nil {
		return err
	}
	v.CreatedAt = time.Unix(c, 0)
	if i > 0 {
		v.IssuedAt = time.Unix(i, 0)
	}
	if r > 0 {
		v.RejectedAt = time.Unix(r, 0)
	}
	return nil
}

const invoiceCols = `id, token, cny_amount, title_name, title_snapshot, contact_email, status, pdf_path, note, created_at, issued_at, rejected_at, workspace_id`

// invoiceColsQ is invoiceCols qualified with the `i` alias, for the joins the
// per-token and admin listings do against allocations / workspaces.
const invoiceColsQ = `i.id, i.token, i.cny_amount, i.title_name, i.title_snapshot, i.contact_email, i.status, i.pdf_path, i.note, i.created_at, i.issued_at, i.rejected_at, i.workspace_id`

// GetInvoice returns one invoice by primary key.
func (db *DB) GetInvoice(ctx context.Context, id int64) (*Invoice, error) {
	row := db.QueryRowContext(ctx, `SELECT `+invoiceCols+` FROM invoices WHERE id = ?`, id)
	v, err := scanInvoice(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return v, err
}

// ListInvoicesByToken returns the per-user invoice history, newest first: the
// token's own personal invoices plus every team invoice that drew on its
// quota. The team half is not optional — the member's invoiceable balance went
// down, so the invoice that spent it has to be visible to them.
//
// The LEFT JOIN keeps one row per invoice even when the filing admin has no
// allocation of their own (their whole share can be covered by other members).
func (db *DB) ListInvoicesByToken(ctx context.Context, token string, limit int) ([]*InvoiceForToken, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := db.QueryContext(ctx, `
		SELECT `+invoiceColsQ+`,
			CASE WHEN i.workspace_id = 0 THEN i.cny_amount ELSE COALESCE(a.cny_amount, 0) END,
			COALESCE(w.name, '')
		FROM invoices i
		LEFT JOIN invoice_allocations a ON a.invoice_id = i.id AND a.token = ?
		LEFT JOIN workspaces w ON w.id = i.workspace_id
		WHERE (i.workspace_id = 0 AND i.token = ?)
		   OR (i.workspace_id > 0 AND (a.token IS NOT NULL OR i.token = ?))
		ORDER BY i.id DESC LIMIT ?`,
		token, token, token, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*InvoiceForToken
	for rows.Next() {
		var r InvoiceForToken
		if err := scanInvoiceInto(rows, &r.Invoice, &r.AllocatedCNY, &r.WorkspaceName); err != nil {
			return nil, err
		}
		out = append(out, &r)
	}
	return out, rows.Err()
}

// InvoiceAllocations returns the per-member breakdown of a team invoice, in
// the order the quota was drawn (member join order). Empty for a personal
// invoice.
func (db *DB) InvoiceAllocations(ctx context.Context, invoiceID int64) ([]InvoiceAllocation, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT a.invoice_id, a.token, a.cny_amount
		FROM invoice_allocations a
		LEFT JOIN workspace_members m ON m.token = a.token
		WHERE a.invoice_id = ?
		ORDER BY COALESCE(m.created_at, 0) ASC, a.token ASC`, invoiceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []InvoiceAllocation
	for rows.Next() {
		var a InvoiceAllocation
		if err := rows.Scan(&a.InvoiceID, &a.Token, &a.CNYAmount); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// AllocationsByInvoice batch-loads allocations for a page of invoices, so the
// list views don't fan out into one query per row.
func (db *DB) AllocationsByInvoice(ctx context.Context, ids []int64) (map[int64][]InvoiceAllocation, error) {
	out := map[int64][]InvoiceAllocation{}
	if len(ids) == 0 {
		return out, nil
	}
	ph := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		ph[i] = "?"
		args[i] = id
	}
	rows, err := db.QueryContext(ctx, `
		SELECT a.invoice_id, a.token, a.cny_amount
		FROM invoice_allocations a
		LEFT JOIN workspace_members m ON m.token = a.token
		WHERE a.invoice_id IN (`+strings.Join(ph, ",")+`)
		ORDER BY a.invoice_id ASC, COALESCE(m.created_at, 0) ASC, a.token ASC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var a InvoiceAllocation
		if err := rows.Scan(&a.InvoiceID, &a.Token, &a.CNYAmount); err != nil {
			return nil, err
		}
		out[a.InvoiceID] = append(out[a.InvoiceID], a)
	}
	return out, rows.Err()
}

// ListWorkspaceInvoices returns a workspace's team invoices, newest first.
//
// The `workspace_id > 0` term is redundant against a caller who only ever
// passes a real workspace id, and it is there for the planner, not the rows:
// idx_invoices_ws is a PARTIAL index on that same predicate, and SQLite will
// only use a partial index when a WHERE term provably implies its condition.
// An equality on a bound parameter proves nothing at plan time, so without
// the literal term this degrades to a full table scan of every invoice ever
// filed. Same reason it appears in ListInvoices' workspace filter.
func (db *DB) ListWorkspaceInvoices(ctx context.Context, workspaceID int64, limit int) ([]*Invoice, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := db.QueryContext(ctx,
		`SELECT `+invoiceCols+` FROM invoices WHERE workspace_id = ? AND workspace_id > 0 ORDER BY id DESC LIMIT ?`,
		workspaceID, limit)
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
// contact_email; workspaceID > 0 narrows to one team's invoices.
// InvoiceAdminRow is the operator view of one invoice: the row plus the
// workspace name a team invoice belongs to (empty for personal).
type InvoiceAdminRow struct {
	Invoice
	WorkspaceName string
}

func (db *DB) ListInvoices(ctx context.Context, status, q string, workspaceID int64, limit int) ([]*InvoiceAdminRow, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	args := []any{}
	where := "1=1"
	if status != "" {
		where += " AND i.status = ?"
		args = append(args, status)
	}
	if workspaceID > 0 {
		// The `> 0` literal is what lets the planner use the partial
		// idx_invoices_ws — see ListWorkspaceInvoices for why the equality
		// alone is not enough.
		where += " AND i.workspace_id = ? AND i.workspace_id > 0"
		args = append(args, workspaceID)
	}
	if q = strings.TrimSpace(q); q != "" {
		where += " AND (i.title_name LIKE ? OR i.contact_email LIKE ? OR i.token LIKE ?)"
		like := "%" + q + "%"
		args = append(args, like, like, like)
	}
	args = append(args, limit)
	rows, err := db.QueryContext(ctx,
		`SELECT `+invoiceColsQ+`, COALESCE(w.name, '')
		 FROM invoices i
		 LEFT JOIN workspaces w ON w.id = i.workspace_id
		 WHERE `+where+` ORDER BY i.id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*InvoiceAdminRow
	for rows.Next() {
		var r InvoiceAdminRow
		if err := scanInvoiceInto(rows, &r.Invoice, &r.WorkspaceName); err != nil {
			return nil, err
		}
		out = append(out, &r)
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
	_, err := db.ExecContext(ctx, invoiceTitleUpsertSQL,
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
