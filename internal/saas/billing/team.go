package billing

import (
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/wjsoj/CPA-Claude/internal/saas/db"
	"github.com/wjsoj/cc-core/requestlog"
)

// TeamHandler serves the group-admin console at /api/team/*. The caller logs
// in with a client token that is the `admin` of a workspace (the same token it
// uses to call the API — see the product decision to reuse a client token as
// the login credential). Every endpoint is scoped to that one workspace and
// deliberately exposes NOTHING about upstream credentials, other workspaces, or
// members' personal wallets.
type TeamHandler struct {
	DB      *db.DB
	Billing *Handler // shared order-creation machinery (gateway, rate, limits)
	LogDir  string   // request-log directory for the per-member request view

	// Auth resolves the bearer token to a registered client-token string, or
	// "" when unauthenticated. Same resolver the wallet routes use.
	Auth TokenAuthFunc
	// TokenExists reports whether a token is a registered client token —
	// guards member-add against phantom tokens.
	TokenExists func(token string) bool
	// TokenLabel returns a display name for a token (may be empty).
	TokenLabel func(token string) string
}

const (
	teamCtxToken = "team_token"
	teamCtxWS    = "team_ws"
)

// Routes mounts the team console under g (expected: engine.Group("/api/team")).
func (t *TeamHandler) Routes(g *gin.RouterGroup) {
	g.Use(t.authMW())
	g.GET("/me", t.me)
	g.GET("/members", t.listMembers)
	g.POST("/members", t.addMember)
	g.PATCH("/members/:masked", t.patchMember)
	g.DELETE("/members/:masked", t.removeMember)
	g.GET("/ledger", t.ledger)
	g.GET("/requests", t.requests)
	g.POST("/topup", t.topup)
}

// authMW resolves the bearer token, verifies it administers a workspace, and
// stashes the token + workspace on the gin context. Anything else → 403.
func (t *TeamHandler) authMW() gin.HandlerFunc {
	return func(c *gin.Context) {
		tok := ""
		if t.Auth != nil {
			tok = t.Auth(c)
		}
		if tok == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing bearer token"})
			return
		}
		ws, err := t.DB.WorkspaceAdminFor(c.Request.Context(), tok)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "not a workspace admin"})
			return
		}
		c.Set(teamCtxToken, tok)
		c.Set(teamCtxWS, ws)
		c.Next()
	}
}

func (t *TeamHandler) ws(c *gin.Context) *db.Workspace {
	if v, ok := c.Get(teamCtxWS); ok {
		if ws, ok := v.(*db.Workspace); ok {
			return ws
		}
	}
	return nil
}

func (t *TeamHandler) adminToken(c *gin.Context) string {
	if v, ok := c.Get(teamCtxToken); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func (t *TeamHandler) me(c *gin.Context) {
	ws := t.ws(c)
	// Re-read for a fresh balance (the cached middleware copy can be stale
	// after a charge/top-up).
	if fresh, err := t.DB.GetWorkspace(c.Request.Context(), ws.ID); err == nil {
		ws = fresh
	}
	c.JSON(http.StatusOK, gin.H{
		"workspace": gin.H{
			"id":          ws.ID,
			"name":        ws.Name,
			"balance_usd": ws.BalanceUSD,
			"disabled":    ws.Disabled,
		},
		"role": db.WSRoleAdmin,
	})
}

func (t *TeamHandler) memberView(c *gin.Context, m *db.WorkspaceMember) gin.H {
	day, month := t.DB.MemberPeriodPoolSpend(c.Request.Context(), m.Token)
	label := ""
	if t.TokenLabel != nil {
		label = t.TokenLabel(m.Token)
	}
	return gin.H{
		"masked":          maskToken(m.Token),
		"label":           label,
		"role":            m.Role,
		"daily_usd_cap":   m.DailyUSDCap,
		"monthly_usd_cap": m.MonthlyUSDCap,
		"used_day_usd":    day,
		"used_month_usd":  month,
		"created_at":      m.CreatedAt.Unix(),
	}
}

func (t *TeamHandler) listMembers(c *gin.Context) {
	ws := t.ws(c)
	ms, err := t.DB.ListMembers(c.Request.Context(), ws.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]gin.H, 0, len(ms))
	for _, m := range ms {
		out = append(out, t.memberView(c, m))
	}
	c.JSON(http.StatusOK, gin.H{"members": out})
}

type addMemberBody struct {
	Token         string  `json:"token"`
	Role          string  `json:"role"`
	DailyUSDCap   float64 `json:"daily_usd_cap"`
	MonthlyUSDCap float64 `json:"monthly_usd_cap"`
}

func (t *TeamHandler) addMember(c *gin.Context) {
	ws := t.ws(c)
	var body addMemberBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	tok := strings.TrimSpace(body.Token)
	if tok == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "token required"})
		return
	}
	if t.TokenExists != nil && !t.TokenExists(tok) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown client token — create it in the admin panel first"})
		return
	}
	if err := t.DB.AddMember(c.Request.Context(), ws.ID, tok, body.Role, body.DailyUSDCap, body.MonthlyUSDCap); err != nil {
		if errors.Is(err, db.ErrMemberExists) {
			c.JSON(http.StatusConflict, gin.H{"error": "that token already belongs to a workspace"})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	m, _ := t.DB.MemberFor(c.Request.Context(), tok)
	c.JSON(http.StatusOK, t.memberView(c, m))
}

// resolveMember maps a masked token (the URL identifier) back to the full
// member token within this workspace. Masks are 6+4 chars so collisions are
// astronomically unlikely; an actual collision returns 409.
func (t *TeamHandler) resolveMember(c *gin.Context, masked string) (*db.WorkspaceMember, int, error) {
	ws := t.ws(c)
	ms, err := t.DB.ListMembers(c.Request.Context(), ws.ID)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	var found *db.WorkspaceMember
	for _, m := range ms {
		if maskToken(m.Token) == masked {
			if found != nil {
				return nil, http.StatusConflict, errors.New("ambiguous masked token")
			}
			found = m
		}
	}
	if found == nil {
		return nil, http.StatusNotFound, errors.New("member not found")
	}
	return found, http.StatusOK, nil
}

type patchMemberBody struct {
	Role          *string  `json:"role"`
	DailyUSDCap   *float64 `json:"daily_usd_cap"`
	MonthlyUSDCap *float64 `json:"monthly_usd_cap"`
}

func (t *TeamHandler) patchMember(c *gin.Context) {
	m, status, err := t.resolveMember(c, c.Param("masked"))
	if err != nil {
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	var body patchMemberBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// Guard: don't let the last admin demote themselves out of management.
	if body.Role != nil && *body.Role != db.WSRoleAdmin && m.Role == db.WSRoleAdmin {
		n, _ := t.DB.CountWorkspaceAdmins(c.Request.Context(), m.WorkspaceID)
		if n <= 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "cannot demote the only admin; promote another member first"})
			return
		}
	}
	if err := t.DB.UpdateMember(c.Request.Context(), m.Token, body.Role, body.DailyUSDCap, body.MonthlyUSDCap); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	updated, _ := t.DB.MemberFor(c.Request.Context(), m.Token)
	c.JSON(http.StatusOK, t.memberView(c, updated))
}

func (t *TeamHandler) removeMember(c *gin.Context) {
	m, status, err := t.resolveMember(c, c.Param("masked"))
	if err != nil {
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	// Removing the only admin would orphan the group's management.
	if m.Role == db.WSRoleAdmin {
		n, _ := t.DB.CountWorkspaceAdmins(c.Request.Context(), m.WorkspaceID)
		if n <= 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "cannot remove the only admin; promote another member first"})
			return
		}
	}
	if err := t.DB.RemoveMember(c.Request.Context(), m.Token); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (t *TeamHandler) ledger(c *gin.Context) {
	ws := t.ws(c)
	txs, err := t.DB.ListWorkspaceTx(c.Request.Context(), ws.ID, 200)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]gin.H, 0, len(txs))
	for _, tx := range txs {
		row := gin.H{
			"kind":       tx.Kind,
			"amount_usd": tx.AmountUSD,
			"note":       tx.Note,
			"created_at": tx.CreatedAt.Unix(),
		}
		if tx.Token != "" {
			row["member"] = maskToken(tx.Token)
		}
		out = append(out, row)
	}
	c.JSON(http.StatusOK, gin.H{"ledger": out})
}

// requests returns recent request-log entries for this workspace's members,
// merged newest-first. Tokens in the log are already masked; we match members
// by their masked form. Bounded: at most 50 members scanned, 200 rows out.
func (t *TeamHandler) requests(c *gin.Context) {
	if t.LogDir == "" {
		c.JSON(http.StatusOK, gin.H{"requests": []any{}})
		return
	}
	ws := t.ws(c)
	ms, err := t.DB.ListMembers(c.Request.Context(), ws.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if len(ms) > 50 {
		ms = ms[:50]
	}
	labelByMask := make(map[string]string, len(ms))
	type entry struct {
		ts  time.Time
		row gin.H
	}
	var all []entry
	for _, m := range ms {
		masked := maskToken(m.Token)
		if t.TokenLabel != nil {
			labelByMask[masked] = t.TokenLabel(m.Token)
		}
		res, qerr := requestlog.Query(requestlog.Filter{
			Dir:         t.LogDir,
			ClientToken: masked,
			Limit:       100,
			// Only res.Entries is read below — skip the per-member aggregates
			// and stop scanning at the newest 100 hits so a 50-member team
			// dashboard doesn't trigger 50 full-archive scans.
			PageOnly: true,
		})
		if qerr != nil {
			continue
		}
		for _, r := range res.Entries {
			all = append(all, entry{ts: r.TS, row: gin.H{
				"member":     masked,
				"label":      labelByMask[masked],
				"ts":         r.TS.Unix(),
				"provider":   r.Provider,
				"model":      r.Model,
				"status":     r.Status,
				"input":      r.Input,
				"output":     r.Output,
				"cost_usd":   r.CostUSD,
				"billed_usd": r.BilledUSD,
			}})
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ts.After(all[j].ts) })
	if len(all) > 200 {
		all = all[:200]
	}
	out := make([]gin.H, 0, len(all))
	for _, e := range all {
		out = append(out, e.row)
	}
	c.JSON(http.StatusOK, gin.H{"requests": out})
}

type teamTopupBody struct {
	USD float64 `json:"usd"`
}

func (t *TeamHandler) topup(c *gin.Context) {
	ws := t.ws(c)
	var body teamTopupBody
	if err := c.BindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	resp, status, err := t.Billing.CreateTopup(c, t.adminToken(c), ws.ID, body.USD)
	if err != nil {
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	c.JSON(status, resp)
}
