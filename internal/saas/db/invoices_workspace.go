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

// invoiceEpsilon is the half-fen tolerance every invoiceable comparison uses,
// so a request for exactly the available balance is never lost to FP drift.
const invoiceEpsilon = 0.005

// MemberInvoiceSummary is one member's slice of a workspace's invoiceable pool.
type MemberInvoiceSummary struct {
	Token string
	Role  string
	InvoiceSummary
}

// WorkspaceInvoiceSummary is what a team admin sees before filing: the group
// total plus who it would actually be drawn from. Members are ordered by join
// time, then by token — the same order CreateWorkspaceInvoice consumes them in,
// so the preview matches the eventual allocation. The token tiebreak is not a
// rare edge: created_at is whole Unix seconds, so a whole team pulled in during
// one batch invite joins "at the same time" and is drawn from in token order.
type WorkspaceInvoiceSummary struct {
	WorkspaceID int64
	Total       InvoiceSummary
	Members     []MemberInvoiceSummary
}

// WorkspaceInvoiceableCNY rolls every member's personal invoice quota up into
// the workspace total. The pool is per-member and stays per-member: a team
// invoice is a convenience for the group's paperwork, not a shared balance,
// because the invoiceable amount derives from who actually paid money in.
func (db *DB) WorkspaceInvoiceableCNY(ctx context.Context, workspaceID int64) (*WorkspaceInvoiceSummary, error) {
	members, err := listMembersForInvoice(ctx, db.DB, workspaceID)
	if err != nil {
		return nil, err
	}
	out := &WorkspaceInvoiceSummary{WorkspaceID: workspaceID}
	for _, m := range members {
		s, err := invoiceSummaryFor(ctx, db.DB, m.Token)
		if err != nil {
			return nil, err
		}
		out.Members = append(out.Members, MemberInvoiceSummary{Token: m.Token, Role: m.Role, InvoiceSummary: *s})
		out.Total.PaidCNY += s.PaidCNY
		out.Total.LockedCNY += s.LockedCNY
		out.Total.IssuedCNY += s.IssuedCNY
		out.Total.AvailableCNY += s.AvailableCNY
	}
	out.Total.PaidCNY = round2(out.Total.PaidCNY)
	out.Total.LockedCNY = round2(out.Total.LockedCNY)
	out.Total.IssuedCNY = round2(out.Total.IssuedCNY)
	// Summed from the already-floored per-member figures rather than
	// recomputed from the totals: a member who somehow sits at a negative
	// balance must not silently fund the rest of the team.
	out.Total.AvailableCNY = round2(out.Total.AvailableCNY)
	return out, nil
}

type invoiceMember struct {
	Token string
	Role  string
}

// listMembersForInvoice returns a workspace's members in stable order: join
// time first, token second for anyone who joined within the same second. The
// order is part of the contract — allocation fills members up in this sequence,
// so an admin can predict (and a test can assert) who pays.
func listMembersForInvoice(ctx context.Context, q interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}, workspaceID int64) ([]invoiceMember, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT token, role FROM workspace_members WHERE workspace_id = ? ORDER BY created_at ASC, token ASC`,
		workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []invoiceMember
	for rows.Next() {
		var m invoiceMember
		if err := rows.Scan(&m.Token, &m.Role); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// CreateWorkspaceInvoice files ONE fapiao for a whole workspace, drawing the
// amount from its members' individual invoiceable pools.
//
// Concurrency: BEGIN IMMEDIATE on a dedicated *sql.Conn, the same shape as
// ChargeMemberFirst — modernc.org/sqlite has no per-Tx txlock option. This is
// a read-modify-write over every member's quota, so under a DEFERRED
// transaction two concurrent requests would both read the same "already
// invoiced" and jointly issue more fapiao than the group ever paid for.
//
// Allocation is greedy in the order listMembersForInvoice returns — join time,
// then token for members who joined in the same second: fill each member to
// their available balance until the amount is covered. Greedy rather than pro-rata
// because the numbers have to be explainable to an accountant — "the first
// members' payments were invoiced, the later ones' were not" reads cleanly,
// whereas pro-rata scatters sub-fen residue across everyone.
func (db *DB) CreateWorkspaceInvoice(ctx context.Context, workspaceID int64, initiatorToken string, cny float64, title InvoiceTitle, contactEmail string) (*Invoice, error) {
	if cny <= 0 {
		return nil, errors.New("cny amount must be positive")
	}
	if workspaceID <= 0 {
		return nil, errors.New("workspace id required")
	}
	title.Token = initiatorToken
	snap, err := json.Marshal(invoiceTitlePayload(title))
	if err != nil {
		return nil, err
	}

	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	members, err := listMembersForInvoice(ctx, conn, workspaceID)
	if err != nil {
		return nil, err
	}

	// Re-measure every member inside the lock; the pre-check the caller may
	// have run is only advisory.
	avail := make([]float64, len(members))
	var total float64
	for i, m := range members {
		s, err := invoiceSummaryFor(ctx, conn, m.Token)
		if err != nil {
			return nil, err
		}
		avail[i] = s.AvailableCNY
		total += s.AvailableCNY
	}
	total = round2(total)
	if cny > total+invoiceEpsilon {
		return nil, fmt.Errorf("%w: requested %.2f available %.2f", ErrInsufficientInvoiceable, cny, total)
	}

	allocs := allocateInvoice(members, avail, cny)

	now := time.Now().Unix()
	res, err := conn.ExecContext(ctx, `
		INSERT INTO invoices (token, cny_amount, title_name, title_snapshot, contact_email, status, created_at, workspace_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		initiatorToken, cny, strings.TrimSpace(title.Name), string(snap),
		strings.TrimSpace(contactEmail), InvoicePending, now, workspaceID)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	for _, a := range allocs {
		if _, err := conn.ExecContext(ctx,
			`INSERT INTO invoice_allocations (invoice_id, token, cny_amount) VALUES (?, ?, ?)`,
			id, a.Token, a.CNYAmount); err != nil {
			return nil, err
		}
	}

	// The header book stays the filing admin's own — they are the one typing
	// it in, and a team-level title dimension would be a second place to keep
	// the same company details in sync.
	if _, err := conn.ExecContext(ctx, invoiceTitleUpsertSQL,
		initiatorToken, strings.TrimSpace(title.Name), strings.TrimSpace(title.TaxNo),
		strings.TrimSpace(title.Address), strings.TrimSpace(title.Phone),
		strings.TrimSpace(title.Bank), strings.TrimSpace(title.BankAccount),
		now, now); err != nil {
		return nil, err
	}

	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return nil, err
	}
	committed = true
	return db.GetInvoice(ctx, id)
}

// allocateInvoice splits cny across members in order, capped by each one's
// available balance. The last non-zero share absorbs any rounding residue so
// the itemised allocations always add up to exactly the invoice's face value —
// otherwise the per-member breakdown and the total printed above it disagree,
// which is the first thing a finance team notices.
func allocateInvoice(members []invoiceMember, avail []float64, cny float64) []InvoiceAllocation {
	var out []InvoiceAllocation
	remaining := cny
	for i, m := range members {
		if remaining <= invoiceEpsilon {
			break
		}
		take := avail[i]
		if take > remaining {
			take = remaining
		}
		take = round2(take)
		if take <= 0 {
			continue
		}
		if take > remaining {
			take = remaining
		}
		out = append(out, InvoiceAllocation{Token: m.Token, CNYAmount: take})
		remaining -= take
	}
	// Residue absorption, deliberately kept as a guard rather than relied on:
	// with today's inputs it is unreachable. Every avail[i] has already been
	// round2'd by invoiceSummaryFor, so each take is either an exact two-decimal
	// avail or `remaining` itself, and the shares close on cny by construction —
	// a 3M-iteration fuzz never moved d off zero. It stays because what it
	// guards is the invariant (itemised lines sum to the face value), and the
	// only thing currently upholding that invariant is the shape of the loop
	// above. Feed it an avail carrying more than two decimals — a paid amount
	// from a gateway that settles in sub-fen units, or a pro-rata split
	// replacing the greedy fill — and round2 starts shaving each take, the
	// shares come up short, and this is the line that keeps the document
	// internally consistent.
	if len(out) > 0 {
		var sum float64
		for _, a := range out {
			sum += a.CNYAmount
		}
		if d := cny - sum; d > 1e-9 || d < -1e-9 {
			last := &out[len(out)-1]
			last.CNYAmount = signedRound2(last.CNYAmount + d)
		}
	}
	return out
}

// signedRound2 rounds half-away-from-zero. round2 is positive-only (it adds
// 0.5 unconditionally), and the residue correction above can be negative.
func signedRound2(v float64) float64 {
	if v < 0 {
		return -round2(-v)
	}
	return round2(v)
}
