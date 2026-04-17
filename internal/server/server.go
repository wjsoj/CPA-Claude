package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"github.com/wjsoj/CPA-Claude/internal/admin"
	"github.com/wjsoj/CPA-Claude/internal/auth"
	"github.com/wjsoj/CPA-Claude/internal/clienttoken"
	"github.com/wjsoj/CPA-Claude/internal/config"
	"github.com/wjsoj/CPA-Claude/internal/pricing"
	"github.com/wjsoj/CPA-Claude/internal/requestlog"
	"github.com/wjsoj/CPA-Claude/internal/usage"
)

type Server struct {
	cfg       *config.Config
	pool      *auth.Pool
	usage     *usage.Store
	pricing   *pricing.Catalog
	tokens    *clienttoken.Store
	reqLog    *requestlog.Writer
	http      *http.Server
	inflight  sync.Map // client token → *int32 (atomic in-flight count)
}

func New(cfg *config.Config, pool *auth.Pool, store *usage.Store, reqLog *requestlog.Writer, tokens *clienttoken.Store) *Server {
	gin.SetMode(gin.ReleaseMode)
	cat := pricing.NewCatalog(cfg.Pricing)
	s := &Server{cfg: cfg, pool: pool, usage: store, pricing: cat, tokens: tokens, reqLog: reqLog}

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
	admin.New(cfg, pool, store, cat, tokens).Register(engine)

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
// the live client token store (config.yaml tokens + runtime-added tokens).
// If the store is empty the proxy runs in open mode.
func (s *Server) clientAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		tok := extractClientToken(c.Request)
		if s.tokens.Empty() {
			if tok == "" {
				tok = c.ClientIP()
			}
			c.Set("client_token", tok)
			c.Set("client_name", "")
			c.Next()
			return
		}
		if tok == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing bearer token"})
			return
		}
		name, _, _, ok := s.tokens.Lookup(tok)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}
		c.Set("client_token", tok)
		c.Set("client_name", name)
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
		ID            string    `json:"id"`
		Kind          string    `json:"kind"`
		Label         string    `json:"label"`
		Email         string    `json:"email,omitempty"`
		ProxyURL      string    `json:"proxy_url,omitempty"`
		MaxConcurrent int       `json:"max_concurrent"`
		ActiveClients int       `json:"active_clients"`
		ClientTokens  []string  `json:"client_tokens,omitempty"`
		Disabled      bool      `json:"disabled,omitempty"`
		QuotaExceeded bool      `json:"quota_exceeded,omitempty"`
		QuotaResetAt  time.Time `json:"quota_reset_at,omitempty"`
		ExpiresAt     time.Time `json:"expires_at,omitempty"`
	}
	var rows []row
	for _, st := range s.pool.Status() {
		kind := "oauth"
		if st.Auth.Kind == auth.KindAPIKey {
			kind = "apikey"
		}
		masked := make([]string, 0, len(st.ClientTokens))
		for _, t := range st.ClientTokens {
			masked = append(masked, auth.MaskToken(t))
		}
		rows = append(rows, row{
			ID:            st.Auth.ID,
			Kind:          kind,
			Label:         st.Auth.Label,
			Email:         st.Auth.Email,
			ProxyURL:      st.Auth.ProxyURL,
			MaxConcurrent: st.Auth.MaxConcurrent,
			ActiveClients: st.ActiveClients,
			ClientTokens:  masked,
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

// clientMaxConcurrent returns the effective max concurrent requests for this
// client. Per-token override wins; otherwise the global default applies.
func (s *Server) clientMaxConcurrent(clientToken string) int {
	if _, _, maxConc, ok := s.tokens.Lookup(clientToken); ok && maxConc > 0 {
		return maxConc
	}
	return s.cfg.ClientMaxConcurrent
}
