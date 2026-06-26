package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Workspace roles. Role gates the /api/team console only — it does NOT affect
// billing. Admins and members charge through ChargeMemberFirst identically.
const (
	WSRoleAdmin  = "admin"
	WSRoleMember = "member"
)

// ErrMemberExists is returned by AddMember when the token already belongs to a
// workspace (the UNIQUE(token) constraint). Callers translate to a 409.
var ErrMemberExists = errors.New("token already belongs to a workspace")

// cstZone is the China-standard (Asia/Shanghai, UTC+8) zone used for the
// per-member daily/monthly cap boundaries. A FixedZone avoids a tzdata
// dependency on minimal container images — UTC+8 has no DST so it's exact.
var cstZone = time.FixedZone("CST", 8*3600)

// dayStartUnix / monthStartUnix return the unix second of the start of the
// current Beijing-time day / month. Member cap rollups sum workspace_tx
// charges created at-or-after these boundaries.
func dayStartUnix(now time.Time) int64 {
	t := now.In(cstZone)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, cstZone).Unix()
}

func monthStartUnix(now time.Time) int64 {
	t := now.In(cstZone)
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, cstZone).Unix()
}

type Workspace struct {
	ID         int64
	Name       string
	BalanceUSD float64
	Disabled   bool
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// WorkspaceWithMeta augments a workspace row with the member count for the
// admin list view.
type WorkspaceWithMeta struct {
	Workspace
	MemberCount int
	AdminCount  int
}

type WorkspaceMember struct {
	WorkspaceID   int64
	Token         string
	Role          string
	DailyUSDCap   float64
	MonthlyUSDCap float64
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type WorkspaceTx struct {
	ID          int64
	WorkspaceID int64
	Token       string
	Kind        string
	AmountUSD   float64
	Ref         string
	Note        string
	CreatedAt   time.Time
}

// ---- Workspace CRUD ----

func (db *DB) CreateWorkspace(ctx context.Context, name string) (*Workspace, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("workspace name required")
	}
	now := time.Now().Unix()
	res, err := db.ExecContext(ctx,
		`INSERT INTO workspaces (name, balance_usd, disabled, created_at, updated_at) VALUES (?, 0, 0, ?, ?)`,
		name, now, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return db.GetWorkspace(ctx, id)
}

func (db *DB) GetWorkspace(ctx context.Context, id int64) (*Workspace, error) {
	row := db.QueryRowContext(ctx,
		`SELECT id, name, balance_usd, disabled, created_at, updated_at FROM workspaces WHERE id = ?`, id)
	return scanWorkspace(row)
}

func scanWorkspace(row interface{ Scan(...any) error }) (*Workspace, error) {
	var w Workspace
	var disabled int
	var c, u int64
	if err := row.Scan(&w.ID, &w.Name, &w.BalanceUSD, &disabled, &c, &u); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	w.Disabled = disabled != 0
	w.CreatedAt = time.Unix(c, 0)
	w.UpdatedAt = time.Unix(u, 0)
	return &w, nil
}

// ListWorkspaces returns every workspace with member/admin counts for the
// operator panel, newest first.
func (db *DB) ListWorkspaces(ctx context.Context) ([]*WorkspaceWithMeta, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT w.id, w.name, w.balance_usd, w.disabled, w.created_at, w.updated_at,
		       COALESCE(m.cnt, 0), COALESCE(m.admins, 0)
		FROM workspaces w
		LEFT JOIN (
			SELECT workspace_id, COUNT(*) cnt,
			       SUM(CASE WHEN role = 'admin' THEN 1 ELSE 0 END) admins
			FROM workspace_members GROUP BY workspace_id
		) m ON m.workspace_id = w.id
		ORDER BY w.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*WorkspaceWithMeta
	for rows.Next() {
		var wm WorkspaceWithMeta
		var disabled int
		var c, u int64
		if err := rows.Scan(&wm.ID, &wm.Name, &wm.BalanceUSD, &disabled, &c, &u, &wm.MemberCount, &wm.AdminCount); err != nil {
			return nil, err
		}
		wm.Disabled = disabled != 0
		wm.CreatedAt = time.Unix(c, 0)
		wm.UpdatedAt = time.Unix(u, 0)
		out = append(out, &wm)
	}
	return out, rows.Err()
}

// UpdateWorkspace applies partial name / disabled changes (nil = no change).
func (db *DB) UpdateWorkspace(ctx context.Context, id int64, name *string, disabled *bool) error {
	if _, err := db.GetWorkspace(ctx, id); err != nil {
		return err
	}
	if name != nil {
		n := strings.TrimSpace(*name)
		if n == "" {
			return errors.New("workspace name required")
		}
		if _, err := db.ExecContext(ctx, `UPDATE workspaces SET name = ?, updated_at = ? WHERE id = ?`, n, time.Now().Unix(), id); err != nil {
			return err
		}
	}
	if disabled != nil {
		d := 0
		if *disabled {
			d = 1
		}
		if _, err := db.ExecContext(ctx, `UPDATE workspaces SET disabled = ?, updated_at = ? WHERE id = ?`, d, time.Now().Unix(), id); err != nil {
			return err
		}
	}
	return nil
}

// AdjustWorkspaceBalance applies a signed delta to the pool and records a
// workspace_tx row. Used by the operator panel (kind=adjust) and is the
// super-admin counterpart to per-token AddBalance. allowNegative=true mirrors
// the wallet adjust semantics so an operator can correct over-credits.
func (db *DB) AdjustWorkspaceBalance(ctx context.Context, id int64, deltaUSD float64, kind, ref, note string) (float64, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var bal float64
	if err := tx.QueryRowContext(ctx, `SELECT balance_usd FROM workspaces WHERE id = ?`, id).Scan(&bal); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	bal += deltaUSD
	now := time.Now().Unix()
	if _, err := tx.ExecContext(ctx, `UPDATE workspaces SET balance_usd = ?, updated_at = ? WHERE id = ?`, bal, now, id); err != nil {
		return 0, err
	}
	if kind == "" {
		kind = TxKindAdjust
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO workspace_tx (workspace_id, token, kind, amount_usd, ref, note, created_at) VALUES (?, '', ?, ?, ?, ?, ?)`,
		id, kind, deltaUSD, ref, note, now); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return bal, nil
}

// ---- Member CRUD ----

// AddMember binds a token to a workspace. Returns ErrMemberExists if the token
// already belongs to any workspace (UNIQUE(token)).
func (db *DB) AddMember(ctx context.Context, workspaceID int64, token, role string, dailyCap, monthlyCap float64) error {
	if _, err := db.GetWorkspace(ctx, workspaceID); err != nil {
		return err
	}
	if role != WSRoleAdmin {
		role = WSRoleMember
	}
	if dailyCap < 0 {
		dailyCap = 0
	}
	if monthlyCap < 0 {
		monthlyCap = 0
	}
	now := time.Now().Unix()
	_, err := db.ExecContext(ctx,
		`INSERT INTO workspace_members (workspace_id, token, role, daily_usd_cap, monthly_usd_cap, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		workspaceID, token, role, dailyCap, monthlyCap, now, now)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return ErrMemberExists
		}
		return err
	}
	return nil
}

// UpdateMember applies partial role/cap changes to an existing member.
func (db *DB) UpdateMember(ctx context.Context, token string, role *string, dailyCap, monthlyCap *float64) error {
	if _, err := db.MemberFor(ctx, token); err != nil {
		return err
	}
	now := time.Now().Unix()
	if role != nil {
		r := WSRoleMember
		if *role == WSRoleAdmin {
			r = WSRoleAdmin
		}
		if _, err := db.ExecContext(ctx, `UPDATE workspace_members SET role = ?, updated_at = ? WHERE token = ?`, r, now, token); err != nil {
			return err
		}
	}
	if dailyCap != nil {
		v := *dailyCap
		if v < 0 {
			v = 0
		}
		if _, err := db.ExecContext(ctx, `UPDATE workspace_members SET daily_usd_cap = ?, updated_at = ? WHERE token = ?`, v, now, token); err != nil {
			return err
		}
	}
	if monthlyCap != nil {
		v := *monthlyCap
		if v < 0 {
			v = 0
		}
		if _, err := db.ExecContext(ctx, `UPDATE workspace_members SET monthly_usd_cap = ?, updated_at = ? WHERE token = ?`, v, now, token); err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) RemoveMember(ctx context.Context, token string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM workspace_members WHERE token = ?`, token)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (db *DB) MemberFor(ctx context.Context, token string) (*WorkspaceMember, error) {
	row := db.QueryRowContext(ctx,
		`SELECT workspace_id, token, role, daily_usd_cap, monthly_usd_cap, created_at, updated_at FROM workspace_members WHERE token = ?`, token)
	return scanMember(row)
}

func scanMember(row interface{ Scan(...any) error }) (*WorkspaceMember, error) {
	var m WorkspaceMember
	var c, u int64
	if err := row.Scan(&m.WorkspaceID, &m.Token, &m.Role, &m.DailyUSDCap, &m.MonthlyUSDCap, &c, &u); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	m.CreatedAt = time.Unix(c, 0)
	m.UpdatedAt = time.Unix(u, 0)
	return &m, nil
}

func (db *DB) ListMembers(ctx context.Context, workspaceID int64) ([]*WorkspaceMember, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT workspace_id, token, role, daily_usd_cap, monthly_usd_cap, created_at, updated_at
		 FROM workspace_members WHERE workspace_id = ? ORDER BY role DESC, created_at ASC`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*WorkspaceMember
	for rows.Next() {
		m, err := scanMember(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// WorkspaceAdminFor returns the (enabled) workspace a token administers, or
// ErrNotFound if the token is not an admin of any workspace. Backs the
// /api/team teamAuth middleware.
func (db *DB) WorkspaceAdminFor(ctx context.Context, token string) (*Workspace, error) {
	var wsID int64
	err := db.QueryRowContext(ctx,
		`SELECT workspace_id FROM workspace_members WHERE token = ? AND role = ?`, token, WSRoleAdmin).Scan(&wsID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return db.GetWorkspace(ctx, wsID)
}

func (db *DB) CountWorkspaceAdmins(ctx context.Context, workspaceID int64) (int, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM workspace_members WHERE workspace_id = ? AND role = ?`, workspaceID, WSRoleAdmin).Scan(&n)
	return n, err
}

// SoleAdminWorkspace reports whether token is the ONLY admin of its workspace
// (used to refuse deleting a token that would orphan a group). ok=false when
// the token is not an admin or shares admin duty with someone else.
func (db *DB) SoleAdminWorkspace(ctx context.Context, token string) (int64, bool, error) {
	m, err := db.MemberFor(ctx, token)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return 0, false, nil
		}
		return 0, false, err
	}
	if m.Role != WSRoleAdmin {
		return 0, false, nil
	}
	n, err := db.CountWorkspaceAdmins(ctx, m.WorkspaceID)
	if err != nil {
		return 0, false, err
	}
	return m.WorkspaceID, n <= 1, nil
}

// MemberPeriodPoolSpend returns how much the member has drawn from the pool in
// the current Beijing-time day and month (positive USD). Feeds the member list
// "used / cap" display.
func (db *DB) MemberPeriodPoolSpend(ctx context.Context, token string) (day, month float64) {
	now := time.Now()
	_ = db.QueryRowContext(ctx,
		`SELECT COALESCE(-SUM(amount_usd), 0) FROM workspace_tx WHERE token = ? AND kind = 'charge' AND created_at >= ?`,
		token, dayStartUnix(now)).Scan(&day)
	_ = db.QueryRowContext(ctx,
		`SELECT COALESCE(-SUM(amount_usd), 0) FROM workspace_tx WHERE token = ? AND kind = 'charge' AND created_at >= ?`,
		token, monthStartUnix(now)).Scan(&month)
	return day, month
}

func (db *DB) ListWorkspaceTx(ctx context.Context, workspaceID int64, limit int) ([]*WorkspaceTx, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := db.QueryContext(ctx,
		`SELECT id, workspace_id, token, kind, amount_usd, ref, note, created_at
		 FROM workspace_tx WHERE workspace_id = ? ORDER BY id DESC LIMIT ?`, workspaceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*WorkspaceTx
	for rows.Next() {
		var t WorkspaceTx
		var c int64
		if err := rows.Scan(&t.ID, &t.WorkspaceID, &t.Token, &t.Kind, &t.AmountUSD, &t.Ref, &t.Note, &c); err != nil {
			return nil, err
		}
		t.CreatedAt = time.Unix(c, 0)
		out = append(out, &t)
	}
	return out, rows.Err()
}

// ---- Hot-path billing ----

// MemberPoolAvail returns how much of the shared pool the token may still draw
// right now: min(pool balance, remaining daily cap, remaining monthly cap), or
// 0 if the token has no membership / the workspace is disabled / the pool is
// empty. Used by PrecheckBalance (added to the personal balance) — cheap, two
// indexed SUMs.
func (db *DB) MemberPoolAvail(ctx context.Context, token string) float64 {
	var wsID int64
	var dailyCap, monthlyCap float64
	err := db.QueryRowContext(ctx,
		`SELECT workspace_id, daily_usd_cap, monthly_usd_cap FROM workspace_members WHERE token = ?`, token).
		Scan(&wsID, &dailyCap, &monthlyCap)
	if err != nil {
		return 0
	}
	var wsBal float64
	var disabled int
	if err := db.QueryRowContext(ctx, `SELECT balance_usd, disabled FROM workspaces WHERE id = ?`, wsID).Scan(&wsBal, &disabled); err != nil {
		return 0
	}
	if disabled != 0 || wsBal <= 0 {
		return 0
	}
	now := time.Now()
	var usedDay, usedMonth float64
	_ = db.QueryRowContext(ctx, `SELECT COALESCE(-SUM(amount_usd), 0) FROM workspace_tx WHERE token = ? AND kind = 'charge' AND created_at >= ?`, token, dayStartUnix(now)).Scan(&usedDay)
	_ = db.QueryRowContext(ctx, `SELECT COALESCE(-SUM(amount_usd), 0) FROM workspace_tx WHERE token = ? AND kind = 'charge' AND created_at >= ?`, token, monthStartUnix(now)).Scan(&usedMonth)
	avail := wsBal
	if dailyCap > 0 {
		if left := dailyCap - usedDay; left < avail {
			avail = left
		}
	}
	if monthlyCap > 0 {
		if left := monthlyCap - usedMonth; left < avail {
			avail = left
		}
	}
	if avail < 0 {
		avail = 0
	}
	return avail
}

// ChargeMemberFirst debits `cost` from the token's workspace pool first
// (bounded by the pool balance and the member's remaining daily/monthly cap),
// then the remainder from the token's personal wallet. Both debits commit in
// ONE transaction.
//
// Concurrency: the transaction is opened with BEGIN IMMEDIATE (write lock from
// the start) so the cap rollup's read-modify-write can't race — two concurrent
// charges for the same member can never both observe the same "already used"
// and overshoot the cap. modernc.org/sqlite has no per-Tx txlock option, so we
// take a dedicated *sql.Conn and drive BEGIN/COMMIT manually.
//
// When the token has no membership (or the workspace is disabled) the whole
// cost is debited from the personal wallet — identical to the legacy
// AddBalance(charge) path. The personal wallet is allowed to go negative: the
// upstream request already happened, so it must be billed.
//
// EnsureWallet (its own transaction) is called BEFORE the immediate lock to
// avoid a nested-write deadlock.
func (db *DB) ChargeMemberFirst(ctx context.Context, token string, cost float64, ref, note string) (poolPaid, personalPaid float64, err error) {
	if token == "" || cost <= 0 {
		return 0, 0, nil
	}
	if _, err = db.EnsureWallet(ctx, token); err != nil {
		return 0, 0, err
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return 0, 0, err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	now := time.Now()
	nowUnix := now.Unix()

	// Resolve membership inside the locked transaction.
	var wsID int64
	var dailyCap, monthlyCap float64
	mErr := conn.QueryRowContext(ctx,
		`SELECT workspace_id, daily_usd_cap, monthly_usd_cap FROM workspace_members WHERE token = ?`, token).
		Scan(&wsID, &dailyCap, &monthlyCap)
	if mErr != nil && !errors.Is(mErr, sql.ErrNoRows) {
		return 0, 0, mErr
	}

	if mErr == nil {
		var wsBal float64
		var disabled int
		wErr := conn.QueryRowContext(ctx, `SELECT balance_usd, disabled FROM workspaces WHERE id = ?`, wsID).Scan(&wsBal, &disabled)
		if wErr != nil && !errors.Is(wErr, sql.ErrNoRows) {
			return 0, 0, wErr
		}
		if wErr == nil && disabled == 0 && wsBal > 0 {
			var usedDay, usedMonth float64
			_ = conn.QueryRowContext(ctx, `SELECT COALESCE(-SUM(amount_usd), 0) FROM workspace_tx WHERE token = ? AND kind = 'charge' AND created_at >= ?`, token, dayStartUnix(now)).Scan(&usedDay)
			_ = conn.QueryRowContext(ctx, `SELECT COALESCE(-SUM(amount_usd), 0) FROM workspace_tx WHERE token = ? AND kind = 'charge' AND created_at >= ?`, token, monthStartUnix(now)).Scan(&usedMonth)
			avail := wsBal
			if dailyCap > 0 {
				if left := dailyCap - usedDay; left < avail {
					avail = left
				}
			}
			if monthlyCap > 0 {
				if left := monthlyCap - usedMonth; left < avail {
					avail = left
				}
			}
			if avail < 0 {
				avail = 0
			}
			poolPaid = cost
			if poolPaid > avail {
				poolPaid = avail
			}
		}
	}

	if poolPaid > 0 {
		if _, err = conn.ExecContext(ctx, `UPDATE workspaces SET balance_usd = balance_usd - ?, updated_at = ? WHERE id = ?`, poolPaid, nowUnix, wsID); err != nil {
			return 0, 0, err
		}
		if _, err = conn.ExecContext(ctx,
			`INSERT INTO workspace_tx (workspace_id, token, kind, amount_usd, ref, note, created_at) VALUES (?, ?, 'charge', ?, ?, ?, ?)`,
			wsID, token, -poolPaid, ref, note, nowUnix); err != nil {
			return 0, 0, err
		}
	}

	personalPaid = cost - poolPaid
	if personalPaid > 0 {
		var bal float64
		if err = conn.QueryRowContext(ctx, `SELECT balance_usd FROM wallets WHERE token = ?`, token).Scan(&bal); err != nil {
			return 0, 0, err
		}
		bal -= personalPaid
		if _, err = conn.ExecContext(ctx, `UPDATE wallets SET balance_usd = ?, updated_at = ? WHERE token = ?`, bal, nowUnix, token); err != nil {
			return 0, 0, err
		}
		pnote := note
		if poolPaid > 0 {
			pnote = note + " (personal fallback)"
		}
		if _, err = conn.ExecContext(ctx,
			`INSERT INTO wallet_tx (token, kind, amount_usd, ref, note, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			token, TxKindCharge, -personalPaid, ref, pnote, nowUnix); err != nil {
			return 0, 0, err
		}
	}

	if _, err = conn.ExecContext(ctx, "COMMIT"); err != nil {
		return 0, 0, err
	}
	committed = true
	return poolPaid, personalPaid, nil
}

// CreditPaidOrderToWorkspace is the workspace-pool counterpart of
// CreditPaidOrder: atomically mark the order paid and credit the shared pool.
// Idempotent (a second concurrent call sees 0 rows affected on the order
// UPDATE and returns ErrOrderNotPending). Deliberately a separate method so the
// battle-tested personal CreditPaidOrder path is not touched.
func (db *DB) CreditPaidOrderToWorkspace(ctx context.Context, outTradeNo, tradeNo string, workspaceID int64, usdCredit float64, ref, note string) (float64, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	now := time.Now().Unix()

	res, err := tx.ExecContext(ctx,
		`UPDATE alipay_orders SET status = ?, trade_no = ?, paid_at = ? WHERE out_trade_no = ? AND status = ?`,
		OrderPaid, tradeNo, now, outTradeNo, OrderPending)
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, ErrOrderNotPending
	}

	var bal float64
	if err := tx.QueryRowContext(ctx, `SELECT balance_usd FROM workspaces WHERE id = ?`, workspaceID).Scan(&bal); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	bal += usdCredit
	if _, err := tx.ExecContext(ctx, `UPDATE workspaces SET balance_usd = ?, updated_at = ? WHERE id = ?`, bal, now, workspaceID); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO workspace_tx (workspace_id, token, kind, amount_usd, ref, note, created_at) VALUES (?, '', ?, ?, ?, ?, ?)`,
		workspaceID, TxKindTopup, usdCredit, ref, note, now); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return bal, nil
}
