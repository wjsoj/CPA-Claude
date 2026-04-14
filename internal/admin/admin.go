// Package admin exposes a small REST API + an embedded SPA for managing
// OAuth credentials at runtime. Protected by config.AdminToken.
package admin

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"github.com/wjsoj/CPA-Claude/internal/auth"
	"github.com/wjsoj/CPA-Claude/internal/config"
	"github.com/wjsoj/CPA-Claude/internal/pricing"
	"github.com/wjsoj/CPA-Claude/internal/requestlog"
	"github.com/wjsoj/CPA-Claude/internal/usage"
)

//go:embed web
var webFS embed.FS

type Handler struct {
	cfg     *config.Config
	pool    *auth.Pool
	usage   *usage.Store
	pricing *pricing.Catalog
	budgets map[string]config.ClientBudget
}

func New(cfg *config.Config, pool *auth.Pool, store *usage.Store, cat *pricing.Catalog, budgets map[string]config.ClientBudget) *Handler {
	return &Handler{cfg: cfg, pool: pool, usage: store, pricing: cat, budgets: budgets}
}

// Register attaches the admin SPA and API routes.
// If cfg.AdminToken is empty the admin surface is disabled.
func (h *Handler) Register(r *gin.Engine) {
	if strings.TrimSpace(h.cfg.AdminToken) == "" {
		log.Info("admin: disabled (admin_token not set)")
		return
	}
	log.Info("admin: panel enabled at /admin/")

	// Serve the SPA (no auth required for the HTML shell itself; the API
	// underneath is protected).
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Errorf("admin: failed to scope embed FS: %v", err)
		return
	}
	r.GET("/admin", func(c *gin.Context) {
		c.Redirect(http.StatusFound, "/admin/")
	})
	r.GET("/admin/", func(c *gin.Context) {
		serveAsset(c, sub, "index.html")
	})
	r.GET("/admin/*filepath", func(c *gin.Context) {
		p := strings.TrimPrefix(c.Param("filepath"), "/")
		if p == "" || strings.HasSuffix(p, "/") {
			p = "index.html"
		}
		serveAsset(c, sub, p)
	})

	// API group.
	api := r.Group("/admin/api")
	api.Use(h.adminAuth())
	{
		api.GET("/summary", h.handleSummary)
		api.POST("/auths/upload", h.handleUpload)
		api.PATCH("/auths/:id", h.handlePatchAuth)
		api.DELETE("/auths/:id", h.handleDeleteAuth)
		api.POST("/auths/:id/refresh", h.handleRefresh)
		api.POST("/auths/:id/clear-quota", h.handleClearQuota)
		api.POST("/oauth/start", h.handleOAuthStart)
		api.POST("/oauth/finish", h.handleOAuthFinish)
		api.GET("/requests", h.handleRequestsQuery)
		api.GET("/requests/clients", h.handleRequestsClients)
	}
}

func serveAsset(c *gin.Context, root fs.FS, name string) {
	f, err := root.Open(name)
	if err != nil {
		// Fall back to index.html so client-side routing works.
		if name != "index.html" {
			serveAsset(c, root, "index.html")
			return
		}
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	c.Data(http.StatusOK, guessMime(name), data)
}

func guessMime(name string) string {
	switch filepath.Ext(name) {
	case ".html":
		return "text/html; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".js":
		return "application/javascript; charset=utf-8"
	case ".json":
		return "application/json"
	case ".svg":
		return "image/svg+xml"
	default:
		return "application/octet-stream"
	}
}

// adminAuth verifies the X-Admin-Token header (or Bearer token) against
// config.AdminToken using constant-time compare.
func (h *Handler) adminAuth() gin.HandlerFunc {
	want := []byte(strings.TrimSpace(h.cfg.AdminToken))
	return func(c *gin.Context) {
		got := strings.TrimSpace(c.GetHeader("X-Admin-Token"))
		if got == "" {
			v := strings.TrimSpace(c.GetHeader("Authorization"))
			if strings.HasPrefix(strings.ToLower(v), "bearer ") {
				got = strings.TrimSpace(v[len("bearer "):])
			}
		}
		if got == "" || subtle.ConstantTimeCompare([]byte(got), want) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid admin token"})
			return
		}
		c.Next()
	}
}

// ---- responses ----

type authRow struct {
	ID            string           `json:"id"`
	Kind          string           `json:"kind"`
	Label         string           `json:"label"`
	Email         string           `json:"email,omitempty"`
	ProxyURL      string           `json:"proxy_url"`
	MaxConcurrent int              `json:"max_concurrent"`
	ActiveClients int              `json:"active_clients"`
	ClientTokens  []string         `json:"client_tokens"`
	Disabled      bool             `json:"disabled"`
	QuotaExceeded bool             `json:"quota_exceeded"`
	QuotaResetAt  *time.Time       `json:"quota_reset_at,omitempty"`
	ExpiresAt     *time.Time       `json:"expires_at,omitempty"`
	LastFailure   string           `json:"last_failure,omitempty"`
	Usage         *usageSummary    `json:"usage,omitempty"`
}

type usageSummary struct {
	Total    usage.Counts    `json:"total"`
	Sum24h   usage.Counts    `json:"sum_24h"`
	LastUsed *time.Time      `json:"last_used,omitempty"`
	Daily    []usage.DayEntry `json:"daily"` // last 14 days, oldest first
}

func (h *Handler) handleSummary(c *gin.Context) {
	usageMap := h.usage.Snapshot()
	rows := make([]authRow, 0, 16)
	for _, st := range h.pool.Status() {
		kind := "oauth"
		if st.Auth.Kind == auth.KindAPIKey {
			kind = "apikey"
		}
		var quotaReset *time.Time
		if !st.Auth.QuotaResetAt.IsZero() {
			t := st.Auth.QuotaResetAt
			quotaReset = &t
		}
		var expAt *time.Time
		if !st.Auth.ExpiresAt.IsZero() {
			t := st.Auth.ExpiresAt
			expAt = &t
		}
		var u *usageSummary
		if v, ok := usageMap[st.Auth.ID]; ok {
			var lastPtr *time.Time
			if !v.LastUsed.IsZero() {
				lu := v.LastUsed
				lastPtr = &lu
			}
			u = &usageSummary{
				Total:    v.Total,
				Sum24h:   h.usage.Sum24h(st.Auth.ID),
				LastUsed: lastPtr,
				Daily:    v.DailyOrdered(14),
			}
		}
		rows = append(rows, authRow{
			ID:            st.Auth.ID,
			Kind:          kind,
			Label:         st.Auth.Label,
			Email:         st.Auth.Email,
			ProxyURL:      st.Auth.ProxyURL,
			MaxConcurrent: st.Auth.MaxConcurrent,
			ActiveClients: st.ActiveClients,
			ClientTokens:  st.ClientTokens,
			Disabled:      st.Auth.Disabled,
			QuotaExceeded: !st.Auth.QuotaExceededAt.IsZero(),
			QuotaResetAt:  quotaReset,
			ExpiresAt:     expAt,
			Usage:         u,
		})
	}
	// Clients (per-access-token spending).
	clientSnap := h.usage.SnapshotClients()
	currentWeek := h.usage.CurrentWeekKey()
	clientRows := make([]clientRow, 0)
	seen := make(map[string]bool)
	addRow := func(token, label string, weeklyLimit float64) {
		seen[token] = true
		pc, hasData := clientSnap[token]
		weekly := 0.0
		var weeks []usage.WeekEntry
		var total usage.ClientCost
		var last *time.Time
		if hasData {
			weeks = pc.WeeklyOrdered(8)
			if w, ok := pc.Weekly[currentWeek]; ok {
				weekly = w.CostUSD
			}
			total = pc.Total
			if !pc.LastUsed.IsZero() {
				lu := pc.LastUsed
				last = &lu
			}
		}
		clientRows = append(clientRows, clientRow{
			Token:       maskToken(token),
			Label:       label,
			WeeklyUSD:   weekly,
			WeeklyLimit: weeklyLimit,
			Blocked:     weeklyLimit > 0 && weekly >= weeklyLimit,
			Total:       total,
			Weekly:      weeks,
			LastUsed:    last,
		})
	}
	// Rows for every explicitly-budgeted token.
	for _, b := range h.cfg.ClientBudgets {
		addRow(b.Token, b.Label, b.WeeklyUSD)
	}
	// Rows for every client we've actually seen that isn't already listed.
	for tok, pc := range clientSnap {
		if !seen[tok] {
			addRow(tok, pc.Label, 0)
		}
	}

	// Pricing view (editable in config.yaml, read-only here).
	priceView := make(map[string]pricing.ModelPrice)
	for k, v := range h.pricing.Models() {
		priceView[k] = v
	}
	c.JSON(http.StatusOK, gin.H{
		"active_window_minutes": h.cfg.ActiveWindowMinutes,
		"auth_dir":              h.cfg.AuthDir,
		"default_proxy_url":     h.cfg.DefaultProxyURL,
		"auths":                 rows,
		"clients":               clientRows,
		"current_week":          currentWeek,
		"pricing": gin.H{
			"default": h.pricing.Default(),
			"models":  priceView,
		},
	})
}

type clientRow struct {
	Token       string            `json:"token"`
	Label       string            `json:"label,omitempty"`
	WeeklyUSD   float64           `json:"weekly_usd"`
	WeeklyLimit float64           `json:"weekly_limit"`
	Blocked     bool              `json:"blocked"`
	Total       usage.ClientCost  `json:"total"`
	Weekly      []usage.WeekEntry `json:"weekly,omitempty"`
	LastUsed    *time.Time        `json:"last_used,omitempty"`
}

func maskToken(t string) string {
	if len(t) <= 10 {
		return "***"
	}
	return t[:6] + "…" + t[len(t)-4:]
}

type patchAuthBody struct {
	Disabled      *bool   `json:"disabled"`
	MaxConcurrent *int    `json:"max_concurrent"`
	ProxyURL      *string `json:"proxy_url"`
	Label         *string `json:"label"`
}

func (h *Handler) handlePatchAuth(c *gin.Context) {
	id := c.Param("id")
	a := h.pool.FindByID(id)
	if a == nil {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "auth not found"})
		return
	}
	if a.Kind == auth.KindAPIKey {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "API keys are read-only in v1; edit config.yaml and restart"})
		return
	}
	var body patchAuthBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if body.Disabled != nil {
		a.SetDisabled(*body.Disabled)
	}
	if body.MaxConcurrent != nil {
		a.SetMaxConcurrent(*body.MaxConcurrent)
	}
	if body.ProxyURL != nil {
		a.SetProxyURL(strings.TrimSpace(*body.ProxyURL))
	}
	if body.Label != nil {
		label := strings.TrimSpace(*body.Label)
		if label != "" {
			a.Label = label
		}
	}
	if err := a.Persist(); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "persist failed: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *Handler) handleDeleteAuth(c *gin.Context) {
	id := c.Param("id")
	a := h.pool.RemoveOAuth(id)
	if a == nil {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "auth not found"})
		return
	}
	if a.FilePath != "" {
		if err := os.Remove(a.FilePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Warnf("admin: failed to remove %s: %v", a.FilePath, err)
		}
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *Handler) handleRefresh(c *gin.Context) {
	id := c.Param("id")
	a := h.pool.FindByID(id)
	if a == nil || a.Kind != auth.KindOAuth {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "oauth not found"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	// EnsureFresh with a huge leeway forces refresh regardless of current expiry.
	if err := a.EnsureFresh(ctx, 365*24*time.Hour, h.pool.UseUTLS()); err != nil {
		c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "expires_at": a.Snapshot().ExpiresAt})
}

func (h *Handler) handleClearQuota(c *gin.Context) {
	id := c.Param("id")
	a := h.pool.FindByID(id)
	if a == nil {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "auth not found"})
		return
	}
	a.ClearQuota()
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

type uploadBody struct {
	Filename      string          `json:"filename"`
	Content       json.RawMessage `json:"content"`
	Label         string          `json:"label"`
	MaxConcurrent int             `json:"max_concurrent"`
	ProxyURL      string          `json:"proxy_url"`
	Disabled      bool            `json:"disabled"`
}

func (h *Handler) handleUpload(c *gin.Context) {
	var body uploadBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if len(body.Content) == 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "missing content"})
		return
	}
	// Merge user-supplied metadata into the raw JSON so parseFile sees it.
	var merged map[string]any
	if err := json.Unmarshal(body.Content, &merged); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid JSON: " + err.Error()})
		return
	}
	if merged == nil {
		merged = make(map[string]any)
	}
	if body.Label != "" {
		merged["label"] = body.Label
	}
	if body.MaxConcurrent > 0 {
		merged["max_concurrent"] = body.MaxConcurrent
	}
	if strings.TrimSpace(body.ProxyURL) != "" {
		merged["proxy_url"] = strings.TrimSpace(body.ProxyURL)
	}
	if body.Disabled {
		merged["disabled"] = true
	}

	// Derive target filename.
	name := sanitizeFilename(body.Filename)
	if name == "" {
		email, _ := merged["email"].(string)
		if email != "" {
			name = sanitizeFilename(email) + ".json"
		} else {
			name = fmt.Sprintf("claude-%d.json", time.Now().Unix())
		}
	}
	if !strings.HasSuffix(strings.ToLower(name), ".json") {
		name += ".json"
	}
	if err := os.MkdirAll(h.cfg.AuthDir, 0700); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	full := filepath.Join(h.cfg.AuthDir, name)

	finalBytes, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	a, err := auth.ParseFile(full, finalBytes)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "parse: " + err.Error()})
		return
	}
	if err := os.WriteFile(full, finalBytes, 0600); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.pool.AddOAuth(a)
	c.JSON(http.StatusOK, gin.H{"status": "ok", "id": a.ID})
}

type oauthStartBody struct {
	ProxyURL string `json:"proxy_url"`
	Label    string `json:"label"`
}

func (h *Handler) handleOAuthStart(c *gin.Context) {
	var body oauthStartBody
	_ = c.ShouldBindJSON(&body)
	sess, authURL, err := auth.StartLogin(body.ProxyURL, body.Label)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"session_id":   sess.ID,
		"auth_url":     authURL,
		"redirect_uri": "http://localhost:54545/callback",
	})
}

type oauthFinishBody struct {
	SessionID     string `json:"session_id"`
	Callback      string `json:"callback"` // full URL, or "code#state", or raw code
	Code          string `json:"code"`
	State         string `json:"state"`
	MaxConcurrent int    `json:"max_concurrent"`
}

func (h *Handler) handleOAuthFinish(c *gin.Context) {
	var body oauthFinishBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if strings.TrimSpace(body.SessionID) == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "missing session_id"})
		return
	}
	code := strings.TrimSpace(body.Code)
	state := strings.TrimSpace(body.State)
	if code == "" && strings.TrimSpace(body.Callback) != "" {
		parsedCode, parsedState, err := auth.ParseCallback(body.Callback)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		code = parsedCode
		if state == "" {
			state = parsedState
		}
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	a, err := auth.FinishLogin(ctx, body.SessionID, code, state, h.cfg.AuthDir, body.MaxConcurrent, h.cfg.UseUTLS)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	h.pool.AddOAuth(a)
	c.JSON(http.StatusOK, gin.H{"status": "ok", "id": a.ID, "email": a.Email})
}

// ---- request log query ----

func (h *Handler) handleRequestsClients(c *gin.Context) {
	if h.cfg.LogDir == "" {
		c.JSON(http.StatusOK, gin.H{"clients": []string{}})
		return
	}
	cls, err := requestlog.Clients(h.cfg.LogDir)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"clients": cls})
}

func (h *Handler) handleRequestsQuery(c *gin.Context) {
	if h.cfg.LogDir == "" {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "log_dir not configured"})
		return
	}
	f := requestlog.Filter{
		Dir:    h.cfg.LogDir,
		Client: strings.TrimSpace(c.Query("client")),
		Model:  strings.TrimSpace(c.Query("model")),
	}
	if v := strings.TrimSpace(c.Query("from")); v != "" {
		if t, err := parseDateBound(v, false); err == nil {
			f.From = t
		}
	}
	if v := strings.TrimSpace(c.Query("to")); v != "" {
		if t, err := parseDateBound(v, true); err == nil {
			f.To = t
		}
	}
	if v := c.Query("limit"); v != "" {
		fmt.Sscanf(v, "%d", &f.Limit)
	}
	if v := c.Query("status"); v != "" {
		fmt.Sscanf(v, "%d", &f.Status)
	}
	res, err := requestlog.Query(f)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, res)
}

// parseDateBound accepts "YYYY-MM-DD" (start-of-day) or full RFC3339.
// endOfDay=true shifts bare dates to 23:59:59 so `to=2026-04-14` covers
// the whole day.
func parseDateBound(s string, endOfDay bool) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, err
	}
	if endOfDay {
		return t.Add(24*time.Hour - time.Second), nil
	}
	return t, nil
}

func sanitizeFilename(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "..", "")
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "\\", "_")
	return s
}
