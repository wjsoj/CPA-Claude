package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"github.com/wjsoj/CPA-Claude/internal/config"
	"github.com/wjsoj/CPA-Claude/internal/saas/billing"
	saasdb "github.com/wjsoj/CPA-Claude/internal/saas/db"
	"github.com/wjsoj/CPA-Claude/internal/saas/resend"
)

// buildPaymentGateway picks a Z-Pay or MockGateway based on whether the
// operator has filled in PID/Key. Missing creds = mock — useful for local
// development (the mock auto-confirms orders after 2s).
func buildPaymentGateway(cfg *config.Config) billing.Gateway {
	p := cfg.SaaS.Payment
	if strings.TrimSpace(p.PID) == "" || strings.TrimSpace(p.Key) == "" {
		log.Warn("saas: payment.pid or payment.key is empty — using MockGateway (no real payments)")
		return &billing.MockGateway{}
	}
	gw, err := billing.NewZPayGateway(billing.ZPayParams{
		BaseURL:   p.BaseURL,
		PID:       p.PID,
		Key:       p.Key,
		NotifyURL: p.NotifyURL,
		ReturnURL: p.ReturnURL,
	})
	if err != nil {
		log.Errorf("saas: zpay gateway init failed (%v) — falling back to MockGateway", err)
		return &billing.MockGateway{}
	}
	return gw
}

// mountBillingRoutes wires the SaaS wallet/payment endpoints onto a gin
// engine. No-op when SaaS billing is disabled. Routes:
//
//	GET  /api/wallet/balance        — authenticated; current balance + group
//	GET  /api/wallet/transactions   — authenticated; ledger
//	GET  /api/wallet/orders         — authenticated; recent orders
//	GET  /api/wallet/orders/:id     — authenticated; one order
//	POST /api/wallet/topup          — authenticated; create order
//	GET  /api/wallet/rate           — public; live CNY/USD
//	POST /api/wallet/notify         — public; gateway webhook
//	GET  /api/wallet/notify         — public; Z-Pay-style GET notify
//	GET  /api/wallet/groups         — public; list pricing groups (read-only)
//
// Bearer auth is enforced inside the handler via the TokenAuthFunc passed
// to billing.NewHandler — no middleware needed on the user routes.
func (s *Server) mountBillingRoutes(engine *gin.Engine) {
	if s.billing == nil {
		return
	}
	g := engine.Group("/api/wallet")
	s.billing.UserRoutes(g)
	s.billing.PublicRoutes(g)
	if s.invoice != nil {
		s.invoice.UserRoutes(g)
	}

	// Group-admin console — /api/team/*. Scoped to one workspace, gated by a
	// client token that administers it. Reuses the billing Handler's
	// order-creation machinery for pool top-ups.
	if s.saasDB != nil {
		team := &billing.TeamHandler{
			DB:      s.saasDB,
			Billing: s.billing,
			LogDir:  s.cfg.LogDir,
			Auth:    s.makeBearerAuth(),
			TokenExists: func(tok string) bool {
				_, ok := s.tokens.Lookup(tok)
				return ok
			},
			TokenLabel: func(tok string) string {
				if e, ok := s.tokens.Lookup(tok); ok {
					return e.Name
				}
				return ""
			},
		}
		team.Routes(engine.Group("/api/team"))
	}
	if s.inbox != nil {
		// Webhook lives under /api/webhooks/* (not /api/wallet/*) — it's
		// Resend-facing, not user-facing. Mounted on the same engine so
		// the operator has a single public URL to configure on Resend.
		s.inbox.PublicRoutes(engine.Group("/api"))
	}
	// Self-service token settings. The only user-mutable token field: the
	// per-token opt-in to fall back to (marked-up) upstream API keys when the
	// self-run OAuth pool is exhausted. Bearer-auth'd against the clienttoken
	// store (same resolver as makeBearerAuth) — lives on Server, not the
	// billing Handler, because the flag is in the clienttoken store, not the
	// wallet DB.
	g.GET("/settings", s.handleWalletSettingsGet)
	g.PATCH("/settings", s.handleWalletSettingsPatch)

	// Pricing-group catalog. Exposed for the status SPA so a user can see
	// which group their token belongs to without admin access.
	g.GET("/groups", func(c *gin.Context) {
		if s.saasDB == nil {
			c.JSON(200, gin.H{"groups": []any{}})
			return
		}
		gs, err := s.saasDB.ListGroups(c.Request.Context())
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		out := make([]gin.H, 0, len(gs))
		for _, gg := range gs {
			out = append(out, gin.H{
				"id":                gg.ID,
				"name":              gg.Name,
				"description":       gg.Description,
				"codex_multiplier":  gg.CodexMultiplier,
				"claude_multiplier": gg.ClaudeMultiplier,
				"is_default":        gg.IsDefault,
			})
		}
		c.JSON(200, gin.H{"groups": out})
	})
}

// handleWalletSettingsGet returns the authenticated token's self-service
// settings. Currently just the upstream-fallback opt-in.
func (s *Server) handleWalletSettingsGet(c *gin.Context) {
	tok := extractClientToken(c.Request)
	t, ok := s.tokens.Lookup(tok)
	if tok == "" || !ok {
		c.JSON(401, gin.H{"error": "missing or unknown bearer token"})
		return
	}
	// Effective value: default ON unless the user explicitly opted out.
	c.JSON(200, gin.H{"upstream_fallback": t.UpstreamFallbackEnabled()})
}

// handleWalletSettingsPatch toggles the authenticated token's upstream-fallback
// opt-in. Body: {"upstream_fallback": bool}. This is the only token field a
// user may change about themselves — everything else is operator-only.
func (s *Server) handleWalletSettingsPatch(c *gin.Context) {
	tok := extractClientToken(c.Request)
	if _, ok := s.tokens.Lookup(tok); tok == "" || !ok {
		c.JSON(401, gin.H{"error": "missing or unknown bearer token"})
		return
	}
	var body struct {
		UpstreamFallback *bool `json:"upstream_fallback"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.UpstreamFallback == nil {
		c.JSON(400, gin.H{"error": "upstream_fallback (bool) required"})
		return
	}
	if err := s.tokens.SetUpstreamFallback(tok, *body.UpstreamFallback); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"upstream_fallback": *body.UpstreamFallback})
}

// makeBearerAuth returns a TokenAuthFunc that resolves the bearer token
// against the clienttoken store. Used by the billing Handler to gate the
// /api/wallet/* endpoints to known tokens only.
func (s *Server) makeBearerAuth() billing.TokenAuthFunc {
	return func(c *gin.Context) string {
		tok := extractClientToken(c.Request)
		if tok == "" {
			return ""
		}
		if _, ok := s.tokens.Lookup(tok); !ok {
			return ""
		}
		return tok
	}
}

// saasBilling is the runtime glue between the proxy hot-path and the SaaS
// wallet store. Holds the open DB, the cached pricing-group multiplier
// resolver, and the payment handler. nil on every Server method when the
// operator has SaaS disabled in config.yaml.
type saasBilling struct {
	db *saasdb.DB
}

// buildInvoiceHandler returns the per-deploy invoice handler when SaaS is
// enabled, or nil when it isn't. Encapsulates the cfg.SaaS.Invoice →
// runtime-deps wiring so server.New stays linear.
func buildInvoiceHandler(s *Server, cfg *config.Config) *billing.InvoiceHandler {
	if s.saasDB == nil {
		return nil
	}
	var rc *resend.Client
	if cfg.SaaS.Invoice.ResendAPIKey != "" {
		rc = resend.New(cfg.SaaS.Invoice.ResendAPIKey, cfg.SaaS.Invoice.ResendFrom)
	} else {
		// Construct a no-key client so the rest of the surface can call
		// .Send unconditionally — it'll return ErrDisabled and the
		// caller treats that as a soft warning.
		rc = resend.New("", cfg.SaaS.Invoice.ResendFrom)
	}
	return billing.NewInvoiceHandler(
		s.saasDB,
		s.makeBearerAuth(),
		rc,
		cfg.SaaS.Invoice.PDFDir,
		cfg.SaaS.Invoice.OpsEmail,
		cfg.SaaS.Invoice.TitleSuggestURL,
	)
}

// buildInboxHandler returns the Resend-inbound webhook handler. Returns nil
// when SaaS is disabled. The Resend client is reused from invoice setup;
// if no API key is configured the body-fetch step degrades to a warn log.
func buildInboxHandler(s *Server, cfg *config.Config) *billing.InboxHandler {
	if s.saasDB == nil {
		return nil
	}
	rc := resend.New(cfg.SaaS.Invoice.ResendAPIKey, cfg.SaaS.Invoice.ResendFrom)
	return billing.NewInboxHandler(s.saasDB, rc, cfg.SaaS.Invoice.ResendWebhookSecret)
}

// PrecheckBalance returns the current wallet balance for token, creating
// an empty wallet row when this is the token's first request. Called on
// every authenticated request before forwarding upstream. Cheap — one
// SELECT on a PRIMARY KEY.
func (b *saasBilling) PrecheckBalance(ctx context.Context, token string) (float64, error) {
	if b == nil || b.db == nil {
		return 1, nil // disabled: pretend balance is fine
	}
	w, err := b.db.EnsureWallet(ctx, token)
	if err != nil {
		return 0, err
	}
	// A workspace member can also spend the shared pool. Surface
	// personal + available-pool so a member with a drained personal wallet
	// but a funded group pool isn't 402'd. MemberPoolAvail returns 0 for
	// non-members / disabled groups / empty pools.
	return w.BalanceUSD + b.db.MemberPoolAvail(ctx, token), nil
}

// SettleCharge applies the per-(group × provider) multiplier to the
// official upstream cost and debits the wallet. Called from doForward
// after a request settles successfully (status<400 and counts.Requests>0).
//
// "provider" is normalized — billing.PricingGroup.MultiplierFor accepts
// the cc-core canonical strings.
//
// Returns the (multiplier, billed) pair so the caller can attach them to
// the request log without re-querying the DB.
// overrideMult > 0 replaces the pricing-group multiplier entirely — used when
// the serving credential is an upstream API key carrying a per-key price
// multiplier (official × overrideMult), so marked-up relay capacity isn't sold
// at the cheap OAuth-subscription discount. 0 = no override (group multiplier).
func (b *saasBilling) SettleCharge(ctx context.Context, token, provider, model string, officialUSD, overrideMult float64, ref string) (multiplier float64, billed float64) {
	if b == nil || b.db == nil || token == "" || officialUSD <= 0 {
		return 1, 0
	}
	w, err := b.db.GetWallet(ctx, token)
	if err != nil {
		log.Warnf("saas: settle charge: wallet lookup failed for %s: %v", maskTokenShort(token), err)
		return 1, 0
	}
	g, err := b.db.GetGroup(ctx, w.GroupID)
	if err != nil {
		log.Warnf("saas: settle charge: group %d lookup failed: %v", w.GroupID, err)
		return 1, 0
	}
	mult := g.MultiplierFor(provider)
	note := fmt.Sprintf("%s/%s × %.4f", provider, model, mult)
	if overrideMult > 0 {
		mult = overrideMult
		note = fmt.Sprintf("%s/%s × %.4f (upstream key)", provider, model, mult)
	}
	cost := billing.ChargeFromOfficial(officialUSD, mult)
	if cost <= 0 {
		return mult, 0
	}
	// ChargeMemberFirst debits the workspace shared pool first (bounded by the
	// member's daily/monthly cap), then the personal wallet. For a token with
	// no workspace membership it degrades to a plain personal-wallet charge —
	// identical to the legacy AddBalance(charge) path. Either way the total
	// billed is `cost`, so the request-log row is unchanged.
	if _, _, err := b.db.ChargeMemberFirst(ctx, token, cost, ref, note); err != nil {
		log.Warnf("saas: settle charge: debit failed for %s: %v", maskTokenShort(token), err)
		return mult, 0
	}
	return mult, cost
}

// maskTokenShort renders a token for log lines without dragging in the
// admin package's masker. Six-char prefix + four-char suffix is enough to
// distinguish tokens in logs without making the value reconstructible.
func maskTokenShort(tok string) string {
	if len(tok) <= 10 {
		return "***"
	}
	return tok[:6] + "…" + tok[len(tok)-4:]
}
