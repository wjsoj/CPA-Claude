package admin

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	saasdb "github.com/wjsoj/CPA-Claude/internal/saas/db"
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
	os, err := h.wallets.ListAllOrders(c.Request.Context(), 200)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]gin.H, 0, len(os))
	for _, o := range os {
		out = append(out, gin.H{
			"out_trade_no": o.OutTradeNo,
			"token":        maskToken(o.Token),
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
	c.JSON(http.StatusOK, gin.H{"orders": out})
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
