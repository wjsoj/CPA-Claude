package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"github.com/wjsoj/CPA-Claude/internal/admin"
	"github.com/wjsoj/CPA-Claude/internal/auth"
	"github.com/wjsoj/CPA-Claude/internal/config"
	"github.com/wjsoj/CPA-Claude/internal/pricing"
	"github.com/wjsoj/CPA-Claude/internal/requestlog"
	"github.com/wjsoj/CPA-Claude/internal/usage"
)

type Server struct {
	cfg         *config.Config
	pool        *auth.Pool
	usage       *usage.Store
	pricing     *pricing.Catalog
	budgets     map[string]config.ClientBudget // client-token → budget
	clientNames map[string]string              // client-token → human name
	reqLog      *requestlog.Writer
	http        *http.Server
}

func New(cfg *config.Config, pool *auth.Pool, store *usage.Store, reqLog *requestlog.Writer) *Server {
	gin.SetMode(gin.ReleaseMode)
	cat := pricing.NewCatalog(cfg.Pricing)
	budgets := make(map[string]config.ClientBudget, len(cfg.ClientBudgets))
	for _, b := range cfg.ClientBudgets {
		t := strings.TrimSpace(b.Token)
		if t != "" && b.WeeklyUSD > 0 {
			budgets[t] = b
		}
	}
	// Build token → name map. Prefer explicit AccessToken.Name; fall back to
	// the matching ClientBudget.Label so a single source of truth is enough
	// for simple setups.
	names := make(map[string]string, len(cfg.AccessTokens))
	for _, at := range cfg.AccessTokens {
		t := strings.TrimSpace(at.Token)
		if t == "" {
			continue
		}
		if n := strings.TrimSpace(at.Name); n != "" {
			names[t] = n
		}
	}
	for tok, b := range budgets {
		if _, has := names[tok]; !has && b.Label != "" {
			names[tok] = b.Label
		}
	}
	s := &Server{cfg: cfg, pool: pool, usage: store, pricing: cat, budgets: budgets, clientNames: names, reqLog: reqLog}

	engine := gin.New()
	engine.Use(gin.Recovery(), loggingMiddleware(), corsMiddleware())

	engine.GET("/healthz", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })

	v1 := engine.Group("/v1")
	v1.Use(s.clientAuth())
	{
		v1.POST("/messages", s.handleMessages)
		v1.POST("/messages/count_tokens", s.handleCountTokens)
	}

	// Status endpoint (also behind client auth if tokens configured).
	engine.GET("/status", s.clientAuth(), s.handleStatus)

	// Admin SPA + API.
	admin.New(cfg, pool, store, cat, budgets).Register(engine)

	s.http = &http.Server{
		Addr:    fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Handler: engine,
	}
	return s
}

func (s *Server) Start() error {
	log.Infof("cpa-claude listening on %s", s.http.Addr)
	return s.http.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.http.Shutdown(ctx)
}

func loggingMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		log.Infof("%s %s %d %s", c.Request.Method, c.Request.URL.Path, c.Writer.Status(), time.Since(start))
	}
}

func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "*")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// clientAuth matches the incoming Authorization: Bearer or x-api-key against
// config.AccessTokens. If no tokens are configured the proxy is open.
// The matched token's human name (if configured) is stored in the gin
// context under "client_name".
func (s *Server) clientAuth() gin.HandlerFunc {
	allowed := make(map[string]struct{}, len(s.cfg.AccessTokens))
	for _, at := range s.cfg.AccessTokens {
		t := strings.TrimSpace(at.Token)
		if t != "" {
			allowed[t] = struct{}{}
		}
	}
	open := len(allowed) == 0
	return func(c *gin.Context) {
		tok := extractClientToken(c.Request)
		if open {
			if tok == "" {
				tok = c.ClientIP()
			}
			c.Set("client_token", tok)
			c.Set("client_name", s.clientNames[tok])
			c.Next()
			return
		}
		if tok == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing bearer token"})
			return
		}
		if _, ok := allowed[tok]; !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}
		c.Set("client_token", tok)
		c.Set("client_name", s.clientNames[tok])
		c.Next()
	}
}

func extractClientToken(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get("Authorization")); v != "" {
		parts := strings.SplitN(v, " ", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "bearer") {
			return strings.TrimSpace(parts[1])
		}
		return v
	}
	if v := strings.TrimSpace(r.Header.Get("x-api-key")); v != "" {
		return v
	}
	return ""
}

func (s *Server) handleStatus(c *gin.Context) {
	type row struct {
		ID              string    `json:"id"`
		Kind            string    `json:"kind"`
		Label           string    `json:"label"`
		Email           string    `json:"email,omitempty"`
		ProxyURL        string    `json:"proxy_url,omitempty"`
		MaxConcurrent   int       `json:"max_concurrent"`
		ActiveClients   int       `json:"active_clients"`
		ClientTokens    []string  `json:"client_tokens,omitempty"`
		Disabled        bool      `json:"disabled,omitempty"`
		QuotaExceeded   bool      `json:"quota_exceeded,omitempty"`
		QuotaResetAt    time.Time `json:"quota_reset_at,omitempty"`
		ExpiresAt       time.Time `json:"expires_at,omitempty"`
	}
	var rows []row
	for _, st := range s.pool.Status() {
		kind := "oauth"
		if st.Auth.Kind == auth.KindAPIKey {
			kind = "apikey"
		}
		rows = append(rows, row{
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
			QuotaResetAt:  st.Auth.QuotaResetAt,
			ExpiresAt:     st.Auth.ExpiresAt,
		})
	}
	c.JSON(200, gin.H{
		"active_window_minutes": int(s.pool.ActiveWindow() / time.Minute),
		"auths":                 rows,
		"usage":                 s.usage.Snapshot(),
	})
}
