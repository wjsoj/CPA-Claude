package server

import (
	"context"
	"errors"
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

// endpoint is one listening http.Server paired with its provider label. The
// admin panel lives on exactly one endpoint (the primary — see pickPrimary).
type endpoint struct {
	name     string // "claude" | "codex"
	provider string // canonical auth.Provider*
	http     *http.Server
	primary  bool // hosts the admin panel + status SPA
}

type Server struct {
	cfg       *config.Config
	pool      *auth.Pool
	usage     *usage.Store
	pricing   *pricing.Catalog
	tokens    *clienttoken.Store
	reqLog    *requestlog.Writer
	endpoints []*endpoint
	// inflight is keyed by (provider | clientToken): Claude and Codex are
	// treated as independent budgets for the same user so a client running
	// Claude at cap doesn't block its Codex calls (and vice-versa). Matches
	// the per-provider stickiness already used by Pool.Acquire.
	inflight sync.Map
	// rpm enforces a sliding-window requests-per-minute cap. Keyed by
	// (provider | clientToken) — same scoping as inflight so Claude and
	// Codex traffic don't share one budget.
	rpm rpmLimiter
}

// New constructs the multi-endpoint server. At least one endpoint must be
// enabled — otherwise the returned Server has no listeners and Start will
// refuse to run. Admin panel + public /status page are mounted on the
// "primary" endpoint: Claude when enabled, otherwise Codex.
func New(cfg *config.Config, pool *auth.Pool, store *usage.Store, reqLog *requestlog.Writer, tokens *clienttoken.Store) *Server {
	gin.SetMode(gin.ReleaseMode)
	cat := pricing.NewCatalog(cfg.Pricing)
	s := &Server{cfg: cfg, pool: pool, usage: store, pricing: cat, tokens: tokens, reqLog: reqLog}

	primary := pickPrimary(cfg)
	adminH := admin.New(cfg, pool, store, cat, tokens)

	if cfg.Endpoints.Claude.IsEnabled() {
		eng := s.buildClaudeEngine(adminH, primary == "claude")
		s.endpoints = append(s.endpoints, &endpoint{
			name:     "claude",
			provider: auth.ProviderAnthropic,
			primary:  primary == "claude",
			http: &http.Server{
				Addr:    fmt.Sprintf("%s:%d", cfg.Endpoints.Claude.Host, cfg.Endpoints.Claude.Port),
				Handler: eng,
			},
		})
	}
	if cfg.Endpoints.Codex.IsEnabled() {
		eng := s.buildCodexEngine(adminH, primary == "codex")
		s.endpoints = append(s.endpoints, &endpoint{
			name:     "codex",
			provider: auth.ProviderOpenAI,
			primary:  primary == "codex",
			http: &http.Server{
				Addr:    fmt.Sprintf("%s:%d", cfg.Endpoints.Codex.Host, cfg.Endpoints.Codex.Port),
				Handler: eng,
			},
		})
	}
	return s
}

// pickPrimary returns the name of the endpoint that will host the admin
// panel + public status page. Claude wins when both are up — matches the
// user's existing workflow and URLs.
func pickPrimary(cfg *config.Config) string {
	switch {
	case cfg.Endpoints.Claude.IsEnabled():
		return "claude"
	case cfg.Endpoints.Codex.IsEnabled():
		return "codex"
	default:
		return ""
	}
}

// Endpoints returns a read-only snapshot of the configured live endpoints,
// for diagnostics / admin display. Safe after Start().
func (s *Server) Endpoints() []EndpointInfo {
	out := make([]EndpointInfo, 0, len(s.endpoints))
	for _, ep := range s.endpoints {
		out = append(out, EndpointInfo{
			Name: ep.name, Provider: ep.provider, Addr: ep.http.Addr, Primary: ep.primary,
		})
	}
	return out
}

// EndpointInfo is the diagnostic shape exported by Server.Endpoints.
type EndpointInfo struct {
	Name     string `json:"name"`
	Provider string `json:"provider"`
	Addr     string `json:"addr"`
	Primary  bool   `json:"primary"`
}

// Start launches every configured endpoint in its own goroutine and blocks
// until all of them have stopped. Returns the first non-http.ErrServerClosed
// error (or nil on clean shutdown).
func (s *Server) Start() error {
	if len(s.endpoints) == 0 {
		return errors.New("no endpoints enabled — set endpoints.claude.port or endpoints.codex.port in config.yaml")
	}
	errCh := make(chan error, len(s.endpoints))
	for _, ep := range s.endpoints {
		ep := ep
		go func() {
			tag := ep.name
			if ep.primary {
				tag += "*"
			}
			log.Infof("cpa-claude[%s] listening on %s", tag, ep.http.Addr)
			err := ep.http.ListenAndServe()
			if errors.Is(err, http.ErrServerClosed) {
				err = nil
			}
			errCh <- err
		}()
	}
	var firstErr error
	for i := 0; i < len(s.endpoints); i++ {
		if err := <-errCh; err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Shutdown gracefully stops every endpoint in parallel.
func (s *Server) Shutdown(ctx context.Context) error {
	var wg sync.WaitGroup
	errs := make([]error, len(s.endpoints))
	for i, ep := range s.endpoints {
		wg.Add(1)
		go func(i int, ep *endpoint) {
			defer wg.Done()
			errs[i] = ep.http.Shutdown(ctx)
		}(i, ep)
	}
	wg.Wait()
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

// buildClaudeEngine returns the gin.Engine serving Anthropic-native routes
// (/v1/messages, /v1/messages/count_tokens). When primary is true it also
// mounts the admin SPA + public status page.
func (s *Server) buildClaudeEngine(adminH *admin.Handler, primary bool) *gin.Engine {
	engine := gin.New()
	engine.Use(gin.Recovery(), loggingMiddleware("claude"), corsMiddleware())
	engine.GET("/healthz", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok", "endpoint": "claude"}) })

	v1 := engine.Group("/v1")
	v1.Use(s.clientAuth())
	{
		v1.POST("/messages", s.handleMessages)
		v1.POST("/messages/count_tokens", s.handleCountTokens)
	}

	// Legacy JSON status (behind client auth). The bare /status path is the
	// public status SPA, served only on the primary endpoint below.
	engine.GET("/status.json", s.clientAuth(), s.handleStatus)

	if primary {
		adminH.RegisterStatus(engine)
		adminH.Register(engine)
	}
	return engine
}

// buildCodexEngine is the sibling engine for the OpenAI/Codex endpoint.
// The route handlers are stubs in this phase — they land in Phase 3. For
// now the engine exists so the port binds and /healthz responds, which is
// enough to verify the multi-endpoint machinery end-to-end.
func (s *Server) buildCodexEngine(adminH *admin.Handler, primary bool) *gin.Engine {
	engine := gin.New()
	engine.Use(gin.Recovery(), loggingMiddleware("codex"), corsMiddleware())
	engine.GET("/healthz", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok", "endpoint": "codex"}) })

	v1 := engine.Group("/v1")
	v1.Use(s.clientAuth())
	{
		// Stubs — implemented in Phase 3 (see codex_proxy.go).
		v1.POST("/chat/completions", s.handleCodexChatCompletions)
		v1.POST("/responses", s.handleCodexResponses)
		v1.POST("/responses/compact", s.handleCodexResponsesCompact)
		v1.GET("/models", s.handleCodexModels)
	}

	if primary {
		adminH.RegisterStatus(engine)
		adminH.Register(engine)
	}
	return engine
}

func loggingMiddleware(tag string) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		log.Infof("[%s] %s %s %d %s", tag, c.Request.Method, c.Request.URL.Path, c.Writer.Status(), time.Since(start))
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
		name, _, _, _, ok := s.tokens.Lookup(tok)
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
		Provider      string    `json:"provider,omitempty"`
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
			Provider:      auth.NormalizeProvider(st.Auth.Provider),
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
		"endpoints":             s.Endpoints(),
	})
}

// clientMaxConcurrent returns the effective max concurrent requests for this
// client. Per-token override wins; otherwise the global default applies.
func (s *Server) clientMaxConcurrent(clientToken string) int {
	if _, _, maxConc, _, ok := s.tokens.Lookup(clientToken); ok && maxConc > 0 {
		return maxConc
	}
	return s.cfg.ClientMaxConcurrent
}

// clientRPM returns the effective requests-per-minute cap for this client.
// Per-token override wins; otherwise the global default applies. Returns 0
// when both are unset (no cap).
func (s *Server) clientRPM(clientToken string) int {
	if rpm, ok := s.tokens.RPM(clientToken); ok && rpm > 0 {
		return rpm
	}
	return s.cfg.ClientRPM
}
