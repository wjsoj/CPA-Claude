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
	// used in Anthropic usage proxy below

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"github.com/wjsoj/CPA-Claude/internal/auth"
	"github.com/wjsoj/CPA-Claude/internal/clienttoken"
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
	tokens  *clienttoken.Store
}

func New(cfg *config.Config, pool *auth.Pool, store *usage.Store, cat *pricing.Catalog, tokens *clienttoken.Store) *Handler {
	return &Handler{cfg: cfg, pool: pool, usage: store, pricing: cat, tokens: tokens}
}

// Register attaches the admin SPA and API routes.
// If cfg.AdminToken is empty the admin surface is disabled.
// The mount prefix is cfg.AdminPath (default /mgmt-console); changing it
// per deployment hides the panel from trivial /admin scans.
func (h *Handler) Register(r *gin.Engine) {
	if strings.TrimSpace(h.cfg.AdminToken) == "" {
		log.Info("admin: disabled (admin_token not set)")
		return
	}
	base := h.cfg.AdminPath
	log.Infof("admin: panel enabled at %s/", base)

	// Serve the SPA (no auth required for the HTML shell itself; the API
	// underneath is protected).
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Errorf("admin: failed to scope embed FS: %v", err)
		return
	}
	// API group must be registered BEFORE the static catch-all — gin's
	// radix tree won't accept fixed-path routes underneath a wildcard
	// sibling at the same prefix.
	api := r.Group(base + "/api")
	api.Use(h.adminAuth())
	{
		api.GET("/summary", h.handleSummary)
		api.POST("/auths/upload", h.handleUpload)
		api.PATCH("/auths/:id", h.handlePatchAuth)
		api.DELETE("/auths/:id", h.handleDeleteAuth)
		api.POST("/auths/:id/refresh", h.handleRefresh)
		api.POST("/auths/:id/clear-quota", h.handleClearQuota)
		api.POST("/auths/:id/clear-failure", h.handleClearFailure)
		api.POST("/oauth/start", h.handleOAuthStart)
		api.POST("/oauth/finish", h.handleOAuthFinish)
		api.POST("/apikeys", h.handleCreateAPIKey)
		api.POST("/auths/:id/anthropic-usage", h.handleAnthropicUsage)
		api.GET("/requests", h.handleRequestsQuery)
		api.GET("/requests/clients", h.handleRequestsClients)
		api.GET("/tokens", h.handleListTokens)
		api.POST("/tokens", h.handleCreateToken)
		api.PATCH("/tokens/:token", h.handlePatchToken)
		api.DELETE("/tokens/:token", h.handleDeleteToken)
	}

	// Static SPA under a dedicated sub-path so no wildcard conflicts with
	// <base>/api/*. Bare <base>/ and any <base>/app/* fall through to
	// index.html.
	r.GET(base, func(c *gin.Context) {
		c.Redirect(http.StatusFound, base+"/")
	})
	r.GET(base+"/", func(c *gin.Context) {
		serveAsset(c, sub, "index.html")
	})
	r.GET(base+"/app/*filepath", func(c *gin.Context) {
		p := strings.TrimPrefix(c.Param("filepath"), "/")
		if p == "" || strings.HasSuffix(p, "/") {
			p = "index.html"
		}
		serveAsset(c, sub, p)
	})
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
	ID            string        `json:"id"`
	Kind          string        `json:"kind"`
	Label         string        `json:"label"`
	Email         string        `json:"email,omitempty"`
	ProxyURL      string        `json:"proxy_url"`
	BaseURL       string        `json:"base_url,omitempty"`
	MaxConcurrent int           `json:"max_concurrent"`
	ActiveClients int           `json:"active_clients"`
	ClientTokens  []string      `json:"client_tokens"`
	Disabled      bool          `json:"disabled"`
	QuotaExceeded bool          `json:"quota_exceeded"`
	QuotaResetAt  *time.Time    `json:"quota_reset_at,omitempty"`
	ExpiresAt     *time.Time    `json:"expires_at,omitempty"`
	LastFailure   string        `json:"last_failure,omitempty"`
	FileBacked    bool          `json:"file_backed"`
	Healthy       bool          `json:"healthy"`
	HardFailure   bool          `json:"hard_failure"`
	FailureReason string        `json:"failure_reason,omitempty"`
	Usage         *usageSummary `json:"usage,omitempty"`
}

type usageSummary struct {
	Total    usage.Counts     `json:"total"`
	Sum24h   usage.Counts     `json:"sum_24h"`
	LastUsed *time.Time       `json:"last_used,omitempty"`
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
		live := h.pool.FindByID(st.Auth.ID)
		var healthy, hardFail bool
		var failReason string
		if live != nil {
			healthy, hardFail, failReason, _ = live.HealthSnapshot()
		}
		rows = append(rows, authRow{
			ID:            st.Auth.ID,
			Kind:          kind,
			Label:         st.Auth.Label,
			Email:         st.Auth.Email,
			ProxyURL:      st.Auth.ProxyURL,
			BaseURL:       st.Auth.BaseURL,
			MaxConcurrent: st.Auth.MaxConcurrent,
			ActiveClients: st.ActiveClients,
			ClientTokens:  st.ClientTokens,
			Disabled:      st.Auth.Disabled,
			QuotaExceeded: !st.Auth.QuotaExceededAt.IsZero(),
			QuotaResetAt:  quotaReset,
			ExpiresAt:     expAt,
			FileBacked:    strings.TrimSpace(st.Auth.FilePath) != "",
			Healthy:       healthy,
			HardFailure:   hardFail,
			FailureReason: failReason,
			Usage:         u,
		})
	}
	// Clients (per-access-token spending).
	clientSnap := h.usage.SnapshotClients()
	currentWeek := h.usage.CurrentWeekKey()
	clientRows := make([]clientRow, 0)
	seen := make(map[string]bool)
	addRow := func(token, label string, weeklyLimit float64, fromConfig, managed bool) {
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
		row := clientRow{
			Token:       maskToken(token),
			Label:       label,
			WeeklyUSD:   weekly,
			WeeklyLimit: weeklyLimit,
			Blocked:     weeklyLimit > 0 && weekly >= weeklyLimit,
			FromConfig:  fromConfig,
			Managed:     managed,
			Total:       total,
			Weekly:      weeks,
			LastUsed:    last,
		}
		if managed || fromConfig {
			row.FullToken = token
		}
		clientRows = append(clientRows, row)
	}
	// Rows for every configured or runtime-added access token.
	for _, t := range h.tokens.List() {
		addRow(t.Token, t.Name, t.WeeklyUSD, t.FromConfig, !t.FromConfig)
	}
	// Rows for every client we've actually seen that isn't already listed
	// (e.g. open-mode requests keyed by IP).
	for tok, pc := range clientSnap {
		if !seen[tok] {
			addRow(tok, pc.Label, 0, false, false)
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
	// Masked token (e.g. "sk-tes…aaaa") for display.
	Token string `json:"token"`
	// Full token; only set for rows that correspond to a registered client
	// token (not for the synthetic IP-keyed rows in open mode). The panel
	// needs this to build PATCH/DELETE URLs — admin auth covers exposure.
	FullToken   string            `json:"full_token,omitempty"`
	Label       string            `json:"label,omitempty"`
	WeeklyUSD   float64           `json:"weekly_usd"`
	WeeklyLimit float64           `json:"weekly_limit"`
	Blocked     bool              `json:"blocked"`
	FromConfig  bool              `json:"from_config,omitempty"`
	Managed     bool              `json:"managed,omitempty"` // true = panel can edit/delete
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
	BaseURL       *string `json:"base_url"`
	Label         *string `json:"label"`
}

func (h *Handler) handlePatchAuth(c *gin.Context) {
	id := c.Param("id")
	a := h.pool.FindByID(id)
	if a == nil {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "auth not found"})
		return
	}
	// API keys declared in config.yaml have no backing file — they can't be
	// edited at runtime. File-backed keys (in auth_dir) are mutable like OAuth.
	if a.Kind == auth.KindAPIKey && a.FilePath == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "config.yaml-defined API keys are read-only; edit the YAML and restart, or add the key via the panel instead"})
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
	if body.BaseURL != nil {
		a.SetBaseURL(strings.TrimRight(strings.TrimSpace(*body.BaseURL), "/"))
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
	a := h.pool.RemoveAuth(id)
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

func (h *Handler) handleClearFailure(c *gin.Context) {
	id := c.Param("id")
	a := h.pool.FindByID(id)
	if a == nil {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "auth not found"})
		return
	}
	a.ClearFailure()
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

// ---- API key CRUD ----

type createAPIKeyBody struct {
	APIKey   string `json:"api_key"`
	Label    string `json:"label"`
	ProxyURL string `json:"proxy_url"`
	BaseURL  string `json:"base_url"`
	Filename string `json:"filename"`
}

func (h *Handler) handleCreateAPIKey(c *gin.Context) {
	var body createAPIKeyBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	key := strings.TrimSpace(body.APIKey)
	if key == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "missing api_key"})
		return
	}
	label := strings.TrimSpace(body.Label)
	name := sanitizeFilename(body.Filename)
	if name == "" {
		if label != "" {
			name = sanitizeFilename("apikey-"+label) + ".json"
		} else {
			name = fmt.Sprintf("apikey-%d.json", time.Now().Unix())
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
	raw := map[string]any{
		"type":    "apikey",
		"api_key": key,
	}
	if label != "" {
		raw["label"] = label
	}
	if strings.TrimSpace(body.ProxyURL) != "" {
		raw["proxy_url"] = strings.TrimSpace(body.ProxyURL)
	}
	if strings.TrimSpace(body.BaseURL) != "" {
		raw["base_url"] = strings.TrimRight(strings.TrimSpace(body.BaseURL), "/")
	}
	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	a, err := auth.ParseFile(full, data)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := os.WriteFile(full, data, 0600); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.pool.AddAPIKey(a)
	c.JSON(http.StatusOK, gin.H{"status": "ok", "id": a.ID})
}

// ---- Anthropic upstream usage proxy ----

func (h *Handler) handleAnthropicUsage(c *gin.Context) {
	id := c.Param("id")
	a := h.pool.FindByID(id)
	if a == nil || a.Kind != auth.KindOAuth {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "oauth credential not found"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	// Ensure the access token is fresh before hitting the upstream endpoints.
	if err := a.EnsureFresh(ctx, 5*time.Minute, h.pool.UseUTLS()); err != nil {
		c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{"error": "token refresh: " + err.Error()})
		return
	}
	token, _ := a.Credentials()
	client := auth.ClientFor(a.Snapshot().ProxyURL, h.pool.UseUTLS())

	fetch := func(url string) (int, map[string]any, string) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return 0, nil, err.Error()
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Anthropic-Beta", "oauth-2025-04-20")
		req.Header.Set("Accept-Encoding", "identity")
		resp, err := client.Do(req)
		if err != nil {
			return 0, nil, err.Error()
		}
		defer resp.Body.Close()
		buf, _ := io.ReadAll(resp.Body)
		var obj map[string]any
		_ = json.Unmarshal(buf, &obj)
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			msg := strings.TrimSpace(string(buf))
			if len(msg) > 300 {
				msg = msg[:300] + "...(truncated)"
			}
			return resp.StatusCode, obj, msg
		}
		return resp.StatusCode, obj, ""
	}

	usageStatus, usageBody, usageErr := fetch("https://api.anthropic.com/api/oauth/usage")
	profileStatus, profileBody, profileErr := fetch("https://api.anthropic.com/api/oauth/profile")

	c.JSON(http.StatusOK, gin.H{
		"usage": gin.H{
			"status": usageStatus,
			"body":   usageBody,
			"error":  usageErr,
		},
		"profile": gin.H{
			"status": profileStatus,
			"body":   profileBody,
			"error":  profileErr,
		},
	})
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
	if v := c.Query("offset"); v != "" {
		fmt.Sscanf(v, "%d", &f.Offset)
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

// ---- Client access token CRUD ----

// tokenView is the shape returned to the panel. The token itself is masked
// unless the caller asks for the full value (`?full=1`), in which case
// every row is returned verbatim — used by the "copy to clipboard" button
// in the Add-token modal right after creation.
type tokenView struct {
	Token         string     `json:"token"`
	Masked        string     `json:"masked"`
	Name          string     `json:"name"`
	WeeklyUSD     float64    `json:"weekly_usd"`
	MaxConcurrent int        `json:"max_concurrent,omitempty"`
	CreatedAt     *time.Time `json:"created_at,omitempty"`
	FromConfig    bool       `json:"from_config"`
	// Live usage for the current ISO week, convenient for the panel row.
	WeeklyUsedUSD float64 `json:"weekly_used_usd"`
}

func (h *Handler) handleListTokens(c *gin.Context) {
	full := c.Query("full") == "1"
	rows := h.tokens.List()
	out := make([]tokenView, 0, len(rows))
	for _, t := range rows {
		v := tokenView{
			Masked:        maskToken(t.Token),
			Name:          t.Name,
			WeeklyUSD:     t.WeeklyUSD,
			MaxConcurrent: t.MaxConcurrent,
			FromConfig:    t.FromConfig,
			WeeklyUsedUSD: h.usage.WeeklyCostUSD(t.Token),
		}
		if !t.CreatedAt.IsZero() {
			ct := t.CreatedAt
			v.CreatedAt = &ct
		}
		if full {
			v.Token = t.Token
		}
		out = append(out, v)
	}
	c.JSON(http.StatusOK, gin.H{"tokens": out})
}

type createTokenBody struct {
	Token         string  `json:"token"`
	Name          string  `json:"name"`
	WeeklyUSD     float64 `json:"weekly_usd"`
	MaxConcurrent int     `json:"max_concurrent,omitempty"`
	Generate      bool    `json:"generate"` // if true and Token == "", mint a fresh sk-...
}

func (h *Handler) handleCreateToken(c *gin.Context) {
	var body createTokenBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	tok := strings.TrimSpace(body.Token)
	if tok == "" {
		if !body.Generate {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "token required (or set generate:true)"})
			return
		}
		v, err := clienttoken.Generate()
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "generate: " + err.Error()})
			return
		}
		tok = v
	}
	entry := clienttoken.Token{
		Token:         tok,
		Name:          body.Name,
		WeeklyUSD:     body.WeeklyUSD,
		MaxConcurrent: body.MaxConcurrent,
	}
	if err := h.tokens.Add(entry); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"status":     "ok",
		"token":      tok, // return the full value once so the panel can show it
		"name":       body.Name,
		"weekly_usd": body.WeeklyUSD,
	})
}

type patchTokenBody struct {
	Name          *string  `json:"name"`
	WeeklyUSD     *float64 `json:"weekly_usd"`
	MaxConcurrent *int     `json:"max_concurrent"`
}

func (h *Handler) handlePatchToken(c *gin.Context) {
	tok := c.Param("token")
	var body patchTokenBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.tokens.Update(tok, body.Name, body.WeeklyUSD, body.MaxConcurrent); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *Handler) handleDeleteToken(c *gin.Context) {
	tok := c.Param("token")
	if err := h.tokens.Delete(tok); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
