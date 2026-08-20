package admin

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/wjsoj/CPA-Claude/internal/saas/billing"
	saasdb "github.com/wjsoj/CPA-Claude/internal/saas/db"
	"github.com/wjsoj/cc-core/requestlog"
)

// ---- Pricing groups ----

func (h *Handler) handleListGroups(c *gin.Context) {
	if h.wallets == nil {
		c.JSON(http.StatusOK, gin.H{"groups": []any{}, "enabled": false})
		return
	}
	gs, err := h.wallets.ListGroups(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]gin.H, 0, len(gs))
	for _, g := range gs {
		out = append(out, groupView(g))
	}
	c.JSON(http.StatusOK, gin.H{"groups": out, "enabled": true})
}

type groupBody struct {
	Name             string  `json:"name"`
	Description      string  `json:"description"`
	CodexMultiplier  float64 `json:"codex_multiplier"`
	ClaudeMultiplier float64 `json:"claude_multiplier"`
	CredentialGroup  string  `json:"credential_group"`
}

func (h *Handler) handleCreateGroup(c *gin.Context) {
	if h.wallets == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "saas billing disabled"})
		return
	}
	var body groupBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	g, err := h.wallets.CreateGroup(c.Request.Context(), saasdb.GroupParams{
		Name:             body.Name,
		Description:      body.Description,
		CodexMultiplier:  body.CodexMultiplier,
		ClaudeMultiplier: body.ClaudeMultiplier,
		CredentialGroup:  body.CredentialGroup,
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, groupView(g))
}

func (h *Handler) handlePatchGroup(c *gin.Context) {
	if h.wallets == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "saas billing disabled"})
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad id"})
		return
	}
	var body groupBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	g, err := h.wallets.UpdateGroup(c.Request.Context(), id, saasdb.GroupParams{
		Name:             body.Name,
		Description:      body.Description,
		CodexMultiplier:  body.CodexMultiplier,
		ClaudeMultiplier: body.ClaudeMultiplier,
		CredentialGroup:  body.CredentialGroup,
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, groupView(g))
}

func (h *Handler) handleDeleteGroup(c *gin.Context) {
	if h.wallets == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "saas billing disabled"})
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad id"})
		return
	}
	if err := h.wallets.DeleteGroup(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func groupView(g *saasdb.PricingGroup) gin.H {
	return gin.H{
		"id":                g.ID,
		"name":              g.Name,
		"description":       g.Description,
		"codex_multiplier":  g.CodexMultiplier,
		"claude_multiplier": g.ClaudeMultiplier,
		"credential_group":  g.CredentialGroup,
		"is_default":        g.IsDefault,
		"created_at":        g.CreatedAt.Unix(),
		"updated_at":        g.UpdatedAt.Unix(),
	}
}

// ---- Orders + admin wallet inspection ----

func (h *Handler) handleListAllOrders(c *gin.Context) {
	if h.wallets == nil {
		c.JSON(http.StatusOK, gin.H{"orders": []any{}})
		return
	}
	limit := 500
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 2000 {
			limit = n
		}
	}
	statusFilter := c.Query("status") // optional: "paid", "pending", "expired", "failed"
	os, err := h.wallets.ListAllOrders(c.Request.Context(), statusFilter, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// Headline totals come from their own aggregate over every paid order,
	// not from adding up the page. The page is capped, so summing it labelled
	// a window as a lifetime and dropped everything older than the cap.
	//
	// Deliberately independent of ?status=: these are always the lifetime paid
	// figures, so they stay comparable whatever the operator is filtering the
	// list down to. The panel only ever asks for paid orders anyway.
	sumCNY, sumUSD, paidCount, err := h.wallets.PaidOrderTotals(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// Label join: build a full-token → name map so the operator can see
	// who paid without having to cross-reference Tokens tab manually.
	// Tokens not currently registered (deleted or never-existed) fall
	// back to empty string.
	labels := make(map[string]string)
	for _, t := range h.tokens.List() {
		if t.Name != "" {
			labels[t.Token] = t.Name
		}
	}
	out := make([]gin.H, 0, len(os))
	for _, o := range os {
		out = append(out, gin.H{
			"out_trade_no": o.OutTradeNo,
			"token":        maskToken(o.Token),
			"label":        labels[o.Token],
			"cny_amount":   o.CNYAmount,
			"usd_credit":   o.USDCredit,
			"rate":         o.Rate,
			"status":       o.Status,
			"trade_no":     o.TradeNo,
			"created_at":   o.CreatedAt.Unix(),
			"paid_at": func() int64 {
				if o.PaidAt.IsZero() {
					return 0
				}
				return o.PaidAt.Unix()
			}(),
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"orders":    out,
		"total_cny": sumCNY,
		"total_usd": sumUSD,
		// count is this page; paid_count is every paid order there is. They
		// differ once the archive outgrows the page, which is exactly when
		// reporting the page as the total would start being wrong.
		"count":      len(out),
		"paid_count": paidCount,
	})
}

func (h *Handler) handleReconcileOrder(c *gin.Context) {
	if h.billing == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "saas billing disabled"})
		return
	}
	out := c.Param("id")
	result, err := h.billing.ReconcileOrder(c.Request.Context(), out)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"result": result})
}

// handleAdminWallet returns the wallet + recent ledger for a specific
// token — admin override for debugging users who report missing balance.
func (h *Handler) handleAdminWallet(c *gin.Context) {
	if h.wallets == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "saas billing disabled"})
		return
	}
	tok := c.Param("token")
	w, err := h.wallets.GetWallet(c.Request.Context(), tok)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	g, _ := h.wallets.GetGroup(c.Request.Context(), w.GroupID)
	txs, _ := h.wallets.ListWalletTx(c.Request.Context(), tok, 100)
	orders, _ := h.wallets.ListOrders(c.Request.Context(), tok, 50)
	txOut := make([]gin.H, 0, len(txs))
	for _, t := range txs {
		txOut = append(txOut, gin.H{
			"id":         t.ID,
			"kind":       t.Kind,
			"amount_usd": t.AmountUSD,
			"ref":        t.Ref,
			"note":       t.Note,
			"created_at": t.CreatedAt.Unix(),
		})
	}
	orderOut := make([]gin.H, 0, len(orders))
	for _, o := range orders {
		orderOut = append(orderOut, gin.H{
			"out_trade_no": o.OutTradeNo,
			"cny_amount":   o.CNYAmount,
			"usd_credit":   o.USDCredit,
			"rate":         o.Rate,
			"status":       o.Status,
			"trade_no":     o.TradeNo,
			"created_at":   o.CreatedAt.Unix(),
		})
	}
	resp := gin.H{
		"balance_usd":  w.BalanceUSD,
		"group_id":     w.GroupID,
		"transactions": txOut,
		"orders":       orderOut,
	}
	if g != nil {
		resp["group_name"] = g.Name
		resp["claude_multiplier"] = g.ClaudeMultiplier
		resp["codex_multiplier"] = g.CodexMultiplier
	}
	c.JSON(http.StatusOK, resp)
}

// ---- Workspaces (组共享额度 + 组管理员) ----

func workspaceView(w *saasdb.WorkspaceWithMeta) gin.H {
	return gin.H{
		"id":           w.ID,
		"name":         w.Name,
		"balance_usd":  w.BalanceUSD,
		"disabled":     w.Disabled,
		"member_count": w.MemberCount,
		"admin_count":  w.AdminCount,
		"created_at":   w.CreatedAt.Unix(),
		"updated_at":   w.UpdatedAt.Unix(),
	}
}

func (h *Handler) handleListWorkspaces(c *gin.Context) {
	if h.wallets == nil {
		c.JSON(http.StatusOK, gin.H{"workspaces": []any{}, "enabled": false})
		return
	}
	ws, err := h.wallets.ListWorkspaces(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]gin.H, 0, len(ws))
	for _, w := range ws {
		out = append(out, workspaceView(w))
	}
	c.JSON(http.StatusOK, gin.H{"workspaces": out, "enabled": true})
}

type createWorkspaceBody struct {
	Name       string  `json:"name"`
	AdminToken string  `json:"admin_token"`
	InitialUSD float64 `json:"initial_usd"`
}

func (h *Handler) handleCreateWorkspace(c *gin.Context) {
	if h.wallets == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "saas billing disabled"})
		return
	}
	var body createWorkspaceBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	adminTok := strings.TrimSpace(body.AdminToken)
	if adminTok == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "admin_token required"})
		return
	}
	if _, ok := h.tokens.Lookup(adminTok); !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "admin_token is not a registered client token"})
		return
	}
	ws, err := h.wallets.CreateWorkspace(c.Request.Context(), body.Name)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.wallets.AddMember(c.Request.Context(), ws.ID, adminTok, saasdb.WSRoleAdmin, 0, 0); err != nil {
		// Roll back the just-created (empty) workspace so a failed admin
		// assignment doesn't leave an orphan group.
		_ = h.wallets.UpdateWorkspace(c.Request.Context(), ws.ID, nil, ptrBool(true))
		if errors.Is(err, saasdb.ErrMemberExists) {
			c.JSON(http.StatusConflict, gin.H{"error": "that token already belongs to a workspace"})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": "assign admin: " + err.Error()})
		return
	}
	if body.InitialUSD > 0 {
		if _, err := h.wallets.AdjustWorkspaceBalance(c.Request.Context(), ws.ID, body.InitialUSD, saasdb.TxKindTopup, "admin-create", "initial pool credit"); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "seed balance: " + err.Error()})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"id": ws.ID, "name": ws.Name})
}

type patchWorkspaceBody struct {
	Name         *string  `json:"name"`
	Disabled     *bool    `json:"disabled"`
	BalanceDelta *float64 `json:"balance_delta"`
	BalanceNote  string   `json:"balance_note"`
}

func (h *Handler) handlePatchWorkspace(c *gin.Context) {
	if h.wallets == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "saas billing disabled"})
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad id"})
		return
	}
	var body patchWorkspaceBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if body.Name != nil || body.Disabled != nil {
		if err := h.wallets.UpdateWorkspace(c.Request.Context(), id, body.Name, body.Disabled); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}
	if body.BalanceDelta != nil && *body.BalanceDelta != 0 {
		note := body.BalanceNote
		if note == "" {
			note = "admin manual adjustment"
		}
		kind := saasdb.TxKindAdjust
		if *body.BalanceDelta > 0 {
			kind = saasdb.TxKindTopup
		}
		if _, err := h.wallets.AdjustWorkspaceBalance(c.Request.Context(), id, *body.BalanceDelta, kind, "admin-patch", note); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "balance: " + err.Error()})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *Handler) handleListWorkspaceMembers(c *gin.Context) {
	if h.wallets == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "saas billing disabled"})
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad id"})
		return
	}
	ms, err := h.wallets.ListMembers(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	labels := make(map[string]string)
	for _, t := range h.tokens.List() {
		if t.Name != "" {
			labels[t.Token] = t.Name
		}
	}
	gms := make([]billing.GroupMember, 0, len(ms))
	for _, m := range ms {
		gms = append(gms, billing.GroupMember{Token: m.Token, Label: labels[m.Token], Role: m.Role})
	}
	spend, spendOK := billing.MemberSpendToDate(h.cfg.LogDir, gms)
	out := make([]gin.H, 0, len(ms))
	for _, m := range ms {
		day, month := h.wallets.MemberPeriodPoolSpend(c.Request.Context(), m.Token)
		row := gin.H{
			"masked":          maskToken(m.Token),
			"label":           labels[m.Token],
			"role":            m.Role,
			"daily_usd_cap":   m.DailyUSDCap,
			"monthly_usd_cap": m.MonthlyUSDCap,
			// used_* is pool draw only — the figure the caps meter. Total spend
			// (pool + personal wallet fallback) is spend_*, from the request log.
			"used_day_usd":   day,
			"used_month_usd": month,
		}
		s := spend[maskToken(m.Token)]
		switch {
		case !spendOK:
			row["spend_source"] = "unavailable"
		case !s.Measurable:
			row["spend_source"] = "unmeasurable"
		default:
			// Same field set as the team console's memberView: one shape, so a
			// column added to either panel finds the data it expects on both.
			row["spend_source"] = "requestlog"
			row["spend_day_usd"] = s.Day.BilledUSD
			row["spend_month_usd"] = s.Month.BilledUSD
			row["spend_day_requests"] = s.Day.Count
			row["spend_month_requests"] = s.Month.Count
		}
		out = append(out, row)
	}
	c.JSON(http.StatusOK, gin.H{
		"members":       out,
		"timezone":      requestlog.BucketLocation().String(),
		"spend_partial": !spendOK,
	})
}

// handleWorkspaceUsage mirrors the team console's /api/team/usage for the
// operator, from the same producer — a support question about a team's bill
// must not get a different answer here than the customer sees.
func (h *Handler) handleWorkspaceUsage(c *gin.Context) {
	if h.wallets == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "saas billing disabled"})
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad id"})
		return
	}
	labels := make(map[string]string)
	for _, t := range h.tokens.List() {
		if t.Name != "" {
			labels[t.Token] = t.Name
		}
	}
	gu, err := billing.BuildGroupUsage(c.Request.Context(), billing.GroupUsageQuery{
		Wallets:     h.wallets,
		LogDir:      h.cfg.LogDir,
		LogIndexed:  h.logIndexed,
		WorkspaceID: id,
		FromDay:     c.Query("from"),
		ToDay:       c.Query("to"),
		Label:       func(tok string) string { return labels[tok] },
	})
	if err != nil {
		if errors.Is(err, billing.ErrBadWindow) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, billing.GroupUsageJSON(gu))
}

func ptrBool(b bool) *bool { return &b }
