package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"github.com/wjsoj/cc-core/advisor"
	"github.com/wjsoj/cc-core/auth"
	"github.com/wjsoj/cc-core/mimicry"
	"github.com/wjsoj/cc-core/requestlog"
	"github.com/wjsoj/cc-core/sidecar"
	ccstream "github.com/wjsoj/cc-core/stream"
	"github.com/wjsoj/cc-core/thinkingsig"
	"github.com/wjsoj/cc-core/usage"
)

// hopHeaders are stripped when forwarding to upstream.
var hopHeaders = map[string]bool{
	"Connection":          true,
	"Keep-Alive":          true,
	"Proxy-Authenticate":  true,
	"Proxy-Authorization": true,
	"Te":                  true,
	"Trailer":             true,
	"Transfer-Encoding":   true,
	"Upgrade":             true,
	// Anthropic auth is set by us — strip anything the client sent.
	"Authorization": true,
	"X-Api-Key":     true,
	"X-Client-Ip":   true,
}

func (s *Server) handleMessages(c *gin.Context) {
	s.forward(c, auth.ProviderAnthropic, "/v1/messages")
}

func (s *Server) handleCountTokens(c *gin.Context) {
	s.forward(c, auth.ProviderAnthropic, "/v1/messages/count_tokens")
}

// forward runs the per-provider retry loop and credential routing for a
// single client request. `provider` picks the credential pool subset; `path`
// is the provider-native upstream path. doForward still assumes Anthropic
// semantics for request shaping — Codex has its own doForward variant (see
// codex_proxy.go) which this dispatcher will call once provider != anthropic.
func (s *Server) forward(c *gin.Context, provider, path string) {
	clientTok, _ := c.Get("client_token")
	clientToken, _ := clientTok.(string)
	if clientToken == "" {
		clientToken = c.ClientIP()
	}
	clientNameV, _ := c.Get("client_name")
	clientName, _ := clientNameV.(string)
	start := time.Now()

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": "read body: " + err.Error()})
		return
	}

	// Per-window slot identity. Each Claude Code CLI window sends a distinct
	// X-Claude-Code-Session-Id, so the same user opening multiple windows is
	// scheduled as multiple independent slots (and can land on different
	// upstream credentials). Empty for raw API callers → one slot per token.
	slotID := clientSlotID(c)

	// Parse minimal request metadata for usage reporting + streaming detection.
	var peek struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	_ = json.Unmarshal(body, &peek)
	model := peek.Model
	if model == "" {
		model = "unknown"
	}

	// Ingress client filter (Claude endpoint only). Blocks non-interactive
	// SDK / scripting clients (raw SDKs, LiteLLM, python-requests, curl, …)
	// by User-Agent so they can't ride the OAuth mimicry layer. Blocklist-
	// based: the interactive client family (Claude Code, Claude Desktop,
	// Cursor) and any UA we don't recognize as abuse pass through. nil guard
	// = disabled. Codex endpoint is exempt (different client population).
	if s.guard != nil && auth.NormalizeProvider(provider) == auth.ProviderAnthropic {
		if d := s.guard.Inspect(c.Request.Header); d.Blocked {
			log.Warnf("client-guard: rejecting %s — %s", path, d.Reason)
			c.Header("X-Client-Blocked", "1")
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"type": "error",
				"error": gin.H{
					"type":    "forbidden",
					"message": "client not allowed: this endpoint only accepts interactive Claude clients (Claude Code, Claude Desktop, Cursor). Raw SDKs, LiteLLM, and scripting clients are blocked.",
					"reason":  d.Reason,
				},
			})
			s.emitLog(requestlog.Record{
				Client: clientName, ClientToken: maskClientToken(clientToken),
				Provider: provider, Model: model, Stream: peek.Stream, Path: path,
				Status: http.StatusForbidden, DurationMs: time.Since(start).Milliseconds(),
				Error: "client blocked: " + d.Reason,
			})
			return
		}
	}

	// Balance pre-check (SaaS billing). The pricing-group multiplier the
	// charge is computed from also lives on the wallet row, so this same
	// call also primes the group lookup we'll need at settle time. When
	// SaaS is disabled (server constructed without a billing handle), the
	// check is a no-op.
	clientEntry, _ := s.tokens.Lookup(clientToken)
	clientGroup := clientEntry.Group

	// Per-token provider gate. A token may be restricted to a single provider
	// (claude-only / openai-only); reject mismatched endpoints before doing any
	// routing work. Open mode / IP-fallback tokens get the zero-value Token
	// whose empty Providers list allows everything.
	if !clientEntry.AllowsProvider(provider) {
		c.Header("X-Provider-Restricted", provider)
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error": "this token is not permitted to use the " + provider + " endpoint",
		})
		s.emitLog(requestlog.Record{
			Client: clientName, ClientToken: maskClientToken(clientToken),
			Provider: provider, Model: model, Stream: peek.Stream, Path: path,
			Status: http.StatusForbidden, DurationMs: time.Since(start).Milliseconds(),
			Error: "provider not allowed for token",
		})
		return
	}

	if s.saas != nil && clientToken != "" {
		bal, err := s.saas.PrecheckBalance(c.Request.Context(), clientToken)
		if err != nil {
			c.AbortWithStatusJSON(500, gin.H{"error": "wallet lookup failed: " + err.Error()})
			s.emitLog(requestlog.Record{
				Client: clientName, ClientToken: maskClientToken(clientToken),
				Provider: provider, Model: model, Stream: peek.Stream, Path: path,
				Status: 500, DurationMs: time.Since(start).Milliseconds(),
				Error: "wallet lookup failed",
			})
			return
		}
		if bal <= 0 {
			c.Header("Retry-After", "60")
			c.AbortWithStatusJSON(402, gin.H{
				"error":       "insufficient balance",
				"balance_usd": bal,
				"hint":        "top up at /status/ then retry",
			})
			s.emitLog(requestlog.Record{
				Client: clientName, ClientToken: maskClientToken(clientToken),
				Provider: provider, Model: model, Stream: peek.Stream, Path: path,
				Status: 402, DurationMs: time.Since(start).Milliseconds(),
				Error: "insufficient balance",
			})
			return
		}
	}

	// Fail fast when the route can't be served by any available credential.
	// OAuth Codex credentials only speak /v1/responses — they can't serve
	// /v1/chat/completions, and without this check the forward loop would
	// cycle every OAuth cred (each returning retry=true), then surface a
	// misleading 503 "all upstream credentials exhausted". If no API-key
	// credential of this provider can serve the requested model, tell the
	// client directly what's wrong.
	if auth.NormalizeProvider(provider) == auth.ProviderOpenAI && path == "/v1/chat/completions" && !s.pool.HasAPIKeyFor(provider, clientGroup, model) {
		msg := fmt.Sprintf("model %q is only available via /v1/responses on this server (no OpenAI-compatible API-key credential is configured for it); retry with the /v1/responses endpoint", model)
		c.AbortWithStatusJSON(400, gin.H{"error": msg})
		s.emitLog(requestlog.Record{
			Client: clientName, ClientToken: maskClientToken(clientToken), Provider: provider, Model: model,
			Stream: peek.Stream, Path: path, Status: 400,
			DurationMs: time.Since(start).Milliseconds(), Error: "route unsupported for available credentials",
		})
		return
	}

	// Rate limit (RPM) per client token. Sliding 60s window; scoped
	// per-provider to match the inflight budget so Claude and Codex don't
	// share one cap. Checked before the concurrency gate so a burst of
	// 429s doesn't briefly occupy slots.
	rpmKey := auth.NormalizeProvider(provider) + "|" + clientToken
	if limit := s.clientRPM(clientToken); limit > 0 {
		if ok, retry := s.rpm.Allow(rpmKey, limit); !ok {
			c.Header("Retry-After", strconv.Itoa(retry))
			c.AbortWithStatusJSON(429, gin.H{
				"error":       "rate limit exceeded",
				"rpm_limit":   limit,
				"retry_after": retry,
			})
			s.emitLog(requestlog.Record{
				Client:      clientName,
				ClientToken: maskClientToken(clientToken),
				Provider:    provider,
				Model:       model,
				Stream:      peek.Stream,
				Path:        path,
				Status:      429,
				DurationMs:  time.Since(start).Milliseconds(),
				Error:       "rpm limit exceeded",
			})
			return
		}
	}

	// Concurrency limit per client token.
	maxConc := s.clientMaxConcurrent(clientToken)
	if maxConc > 0 {
		// Scope the counter per provider so Claude and Codex share a token
		// but not a concurrency bucket — matches the per-provider session
		// keying in Pool.Acquire.
		inflightKey := auth.NormalizeProvider(provider) + "|" + clientToken
		cur, releaseSlot := s.inflight.Begin(inflightKey)
		defer releaseSlot()
		if cur > int32(maxConc) {
			c.Header("Retry-After", "5")
			c.AbortWithStatusJSON(429, gin.H{
				"error":          "too many concurrent requests",
				"max_concurrent": maxConc,
				"in_flight":      int(cur),
			})
			s.emitLog(requestlog.Record{
				Client:      clientName,
				ClientToken: maskClientToken(clientToken),
				Provider:    provider,
				Model:       model,
				Stream:      peek.Stream,
				Path:        path,
				Status:      429,
				DurationMs:  time.Since(start).Milliseconds(),
				Error:       "concurrent limit exceeded",
			})
			return
		}
	}

	// Try upstream with retries across auths. On saturation / quota / auth
	// errors, we pick a different auth and retry (bounded).
	const maxAttempts = 4
	tried := make(map[string]bool)
	attempts := 0
	for attempt := 0; attempt < maxAttempts; attempt++ {
		excludeIDs := make([]string, 0, len(tried))
		for id := range tried {
			excludeIDs = append(excludeIDs, id)
		}
		a := s.pool.Acquire(c.Request.Context(), provider, clientToken, clientGroup, model, slotID, excludeIDs...)
		if a == nil {
			msg := "no upstream credentials available"
			if len(tried) > 0 {
				msg = "all upstream credentials exhausted"
			}
			c.AbortWithStatusJSON(503, gin.H{"error": msg})
			s.emitLog(requestlog.Record{
				Client: clientName, ClientToken: maskClientToken(clientToken), Provider: provider, Model: model,
				Stream: peek.Stream, Path: path, Status: 503, Attempts: attempts,
				DurationMs: time.Since(start).Milliseconds(), Error: msg,
			})
			return
		}
		tried[a.ID] = true
		attempts++

		var retry, done bool
		switch auth.NormalizeProvider(a.Provider) {
		case auth.ProviderOpenAI:
			retry, done = s.doForwardCodex(c, a, path, body, peek.Stream, model, clientToken, clientName, start, attempts)
		default:
			retry, done = s.doForward(c, a, path, body, peek.Stream, model, clientToken, slotID, clientName, start, attempts)
		}
		if done {
			s.pool.Release(provider, clientToken, slotID)
			return
		}
		if !retry {
			s.pool.Release(provider, clientToken, slotID)
			return
		}
		log.Warnf("proxy: retrying with a different credential (last auth=%s)", a.ID)
	}
	c.AbortWithStatusJSON(503, gin.H{"error": "upstream retries exhausted"})
	s.emitLog(requestlog.Record{
		Client: clientName, ClientToken: maskClientToken(clientToken), Provider: provider, Model: model,
		Stream: peek.Stream, Path: path, Status: 503, Attempts: attempts,
		DurationMs: time.Since(start).Milliseconds(), Error: "upstream retries exhausted",
	})
}

func (s *Server) emitLog(r requestlog.Record) {
	if s.reqLog == nil {
		return
	}
	s.reqLog.Log(r)
}

// clientSlotID derives a per-window slot identifier from the incoming request.
// Claude Code sends a stable per-window X-Claude-Code-Session-Id header (also
// mirrored in metadata.user_id.session_id); the Codex CLI sends a session_id
// header. Treating each distinct value as its own pool slot lets one user's
// multiple CLI windows occupy independent slots and be load-balanced across
// different credentials. Returns "" when the client supplies neither (raw API
// callers) — the pool then keeps one slot per client token.
func clientSlotID(c *gin.Context) string {
	if v := strings.TrimSpace(c.GetHeader("X-Claude-Code-Session-Id")); v != "" {
		return v
	}
	if v := strings.TrimSpace(c.GetHeader("Session_id")); v != "" {
		return v
	}
	return ""
}

func maskClientToken(t string) string {
	if len(t) <= 10 {
		return "***"
	}
	return t[:6] + "…" + t[len(t)-4:]
}

// flagStripThinking persists the strip-thinking decision on a credential after
// a thinking-signature recovery succeeds, so future requests on it sanitize
// prior thinking signatures proactively (ahead of the forward) instead of
// failing once per request and replaying. Idempotent + best-effort.
func flagStripThinking(a *auth.Auth) {
	if a.StripThinkingEnabled() {
		return
	}
	if err := a.MarkStripThinking(); err != nil {
		log.Warnf("proxy: %s strip-thinking persist failed: %v", a.ID, err)
		return
	}
	log.Infof("proxy: %s flagged strip-thinking (persisted) — prior thinking signatures will be sanitized proactively on future requests", a.ID)
}

// doForward sends the request with one credential. Returns (retry, done):
//
//	retry=true  → caller should try another credential
//	done=true   → response was delivered successfully (status < 400 or
//	              non-retryable error already written to client)
func (s *Server) doForward(c *gin.Context, a *auth.Auth, path string, body []byte, stream bool, model, clientToken, slotID, clientName string, start time.Time, attempts int) (retry bool, done bool) {
	// Mid-conversation account switch: drop prior `thinking` block
	// signatures before forwarding. Both OAuth and API-key paths bind
	// thinking signatures to the issuing account, so this runs ahead
	// of the API-key branch. Scoped to /v1/messages — no other path
	// carries multi-turn assistant history. The natural sidecar.Notify
	// below handles the "treat as new session" telemetry: if the new
	// account has no live sidecar session, it fires the standard 9-step
	// bootstrap; if it does, the existing heartbeat covers continuity.
	if path == "/v1/messages" {
		switched := s.switchTracker.Check(clientToken, body, a.ID)
		// StripThinkingEnabled credentials (relays that rotate backend accounts
		// per request, e.g. aws2) reject every echoed thinking signature, so we
		// sanitize ahead of the forward instead of failing once and replaying.
		// The flag is set + persisted automatically on first signature recovery.
		if switched || a.StripThinkingEnabled() {
			if switched {
				log.Infof("auth switch detected: clientToken=%s now on auth=%s — sanitizing prior thinking signatures",
					maskClientToken(clientToken), a.ID)
			}
			body = thinkingsig.SanitizeForSwitch(body)
		}
	}

	if a.Kind == auth.KindAPIKey {
		return s.doForwardAnthropicAPIKey(c, a, path, body, stream, model, clientToken, clientName, start, attempts)
	}
	baseURL := s.cfg.AnthropicBaseURL
	// Per-credential base URL override (used for relay/midstream vendors on
	// API-key credentials).
	if ab := strings.TrimRight(a.Snapshot().BaseURL, "/"); ab != "" {
		baseURL = ab
	}
	url := baseURL + path + "?beta=true"
	isAnthropicBase := strings.HasPrefix(strings.ToLower(baseURL), "https://api.anthropic.com")

	// Per-credential model rewrite (API-key only, e.g. third-party relays
	// that require a vendor-prefixed model name like "[0.1]a/claude-sonnet-4-6").
	// Routing already filtered to credentials that accept this model; here we
	// just substitute the body's "model" field if the map says so. The
	// client-facing model string used for usage/pricing stays unchanged.
	upstreamBody := body
	if upstreamModel, ok := a.ResolveUpstreamModel(model); ok && upstreamModel != model && upstreamModel != "" {
		if rewritten, err := rewriteModelField(body, upstreamModel); err == nil {
			upstreamBody = rewritten
		} else {
			log.Warnf("proxy: model rewrite (%s -> %s) failed via %s: %v", model, upstreamModel, a.ID, err)
		}
	}

	// Body-layer Claude Code mimicry: rebuild system to match the real CC
	// 4-block layout (billing block + "You are Claude Code..." + original
	// system prompt with cache_control), inject metadata.user_id with a
	// per-account device_id and a per-(account, clientToken, conversation)
	// session_id, sign the cch billing hash. The client's prompt is
	// preserved verbatim — only the surrounding wrapper is normalized.
	// Only runs on /v1/messages (count_tokens isn't billed and shouldn't
	// be modified). Haiku requests skip mimicry inside the function.
	id := mimicry.SimIdentity{
		AccountKey:  a.AccountKey(),
		AccountUUID: a.AccountUUIDValue(),
		ClientToken: clientToken,
	}

	// Sidecar: dispatch the per-session bootstrap+quota_probe the first
	// time we see this (account, clientToken) pair. Real CC fires the
	// 9-step bootstrap (GrowthBook → settings → grove → bootstrap →
	// penguin → quota probe → mcp_servers → mcp_registry → releases)
	// BEFORE its first business /v1/messages — an OAuth bearer whose
	// very first observed traffic is /v1/messages with full system+tools
	// is a single-shot fingerprint of a non-CC client. Notify returns a
	// channel closed when bootstrap reaches the quota_probe step; we
	// gate the first business request on it, capped at sidecar.BootstrapWaitCap
	// so a stuck sidecar can't hang user traffic.
	bootstrapReady := s.sidecar.Notify(a, clientToken)
	if path == "/v1/messages" {
		upstreamBody = mimicry.ApplyClaudeCodeBodyMimicry(upstreamBody, model, id)
	}

	ctx := c.Request.Context()
	if bootstrapReady != nil {
		select {
		case <-bootstrapReady:
		case <-ctx.Done():
			// client cancelled — let downstream layer handle it normally
		case <-time.After(sidecar.BootstrapWaitCap):
			log.Warnf("sidecar: bootstrap-wait timeout for %s — proceeding without preceding bootstrap traffic", a.ID)
		}
	}
	upReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(upstreamBody))
	if err != nil {
		c.AbortWithStatusJSON(500, gin.H{"error": err.Error()})
		return false, true
	}

	// Forward selected client headers.
	copyForwardableHeaders(c.Request.Header, upReq.Header)
	stripIngressHeaders(upReq.Header)

	// Anthropic auth + Claude Code fingerprint headers. Pass the same
	// SimIdentity so X-Claude-Code-Session-Id matches metadata.user_id.session_id.
	applyAnthropicHeaders(upReq, a, stream, isAnthropicBase, id, upstreamBody)

	client := auth.ClientFor(a.ProxyURL, s.cfg.UseUTLS)
	resp, err := client.Do(upReq)
	if err != nil {
		// Client went away (ctrl-C, closed connection, etc.) — not a
		// credential fault. Record a non-fatal hint for the admin panel,
		// skip retrying onto other credentials (they would all hit the
		// same dead context and get falsely blamed), and don't bother
		// writing a response body to the vanished client.
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			a.MarkClientCancel(err.Error())
			log.Infof("proxy: client canceled via %s: %v", a.ID, err)
			authKind := "oauth"
			if a.Kind == auth.KindAPIKey {
				authKind = "apikey"
			}
			s.emitLog(requestlog.Record{
				Client:      clientName,
				ClientToken: maskClientToken(clientToken),
				Provider:    auth.NormalizeProvider(a.Provider),
				AuthID:      a.ID,
				AuthLabel:   a.Label,
				AuthKind:    authKind,
				Model:       model,
				Stream:      stream,
				Path:        path,
				Status:      499, // nginx convention: client closed request
				DurationMs:  time.Since(start).Milliseconds(),
				Attempts:    attempts,
				Error:       "client canceled",
			})
			return false, true
		}
		a.MarkFailure(err.Error())
		log.Warnf("proxy: upstream error via %s: %v", a.ID, err)
		return true, false
	}

	// Decompress upstream gzip/br before reading anything — we asked for
	// gzip,br to match the real CC fingerprint, but every internal path
	// (usage parsing, SSE streamer, model rewrite, body forwarding) wants
	// plain bytes. The Content-Encoding header is also stripped so the
	// client receives identity even though upstream sent compressed.
	ccstream.Decompress(resp)

	// Upstream error — log, do lightweight credential bookkeeping, and
	// faithfully forward the original response to the client as-is.
	if resp.StatusCode >= 400 {
		errBody, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		// Reactive thinking-block recovery, two tiers. A thinking-block
		// rejection means the assistant turns echoed in messages[] carry
		// thinking signatures bound to a different account than the one now
		// validating them. Causes: switch-detector miss (first-touch on a
		// continuing conversation, server restart, 2h GC eviction), relays
		// rotating backend accounts per request, or signatures minted
		// outside this proxy. Two flavors with OPPOSITE remedies:
		//
		//   - "Invalid signature in thinking block" → strip the signed
		//     thinking from PAST turns (tier 1, SanitizeForSwitch) and
		//     replay, continuing as a fresh signature-free session.
		//   - "thinking blocks in the latest assistant message cannot be
		//     modified" → stripping the latest turn is itself rejected, so
		//     tier 1 can't help; tier 2 replays with thinking disabled
		//     entirely (DisableThinking) so there's nothing left to validate.
		//
		// Gated on the body matcher, NOT the status code: Anthropic returns
		// these as 400, but relays re-wrap them as 500/529. IsThinkingError
		// requires the literal thinking-block wording, so an unrelated 5xx
		// won't trip it. If both tiers fail, fall through to normal handling.
		// replay re-sends a thinking-sanitized body on the SAME credential,
		// reapplying the per-credential model rewrite and CC body mimicry.
		// Returns true (and swaps in the new resp) when the upstream accepts
		// it. Shared by the tier-1 (strip stale thinking) and tier-2 (disable
		// thinking entirely) recovery steps below.
		replay := func(candidate []byte, label string) bool {
			if bytes.Equal(candidate, body) {
				return false
			}
			retryUpstream := candidate
			if upstreamModel, ok := a.ResolveUpstreamModel(model); ok && upstreamModel != model && upstreamModel != "" {
				if rewritten, err := rewriteModelField(retryUpstream, upstreamModel); err == nil {
					retryUpstream = rewritten
				}
			}
			retryUpstream = mimicry.ApplyClaudeCodeBodyMimicry(retryUpstream, model, id)
			log.Warnf("proxy: %s returned %d thinking-block error — %s and retrying once on same credential", a.ID, resp.StatusCode, label)
			retryReq, rerr := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(retryUpstream))
			if rerr != nil {
				return false
			}
			copyForwardableHeaders(c.Request.Header, retryReq.Header)
			stripIngressHeaders(retryReq.Header)
			applyAnthropicHeaders(retryReq, a, stream, isAnthropicBase, id, retryUpstream)
			retryResp, rderr := client.Do(retryReq)
			if rderr != nil {
				log.Warnf("proxy: %s %s retry transport error: %v", a.ID, label, rderr)
				return false
			}
			ccstream.Decompress(retryResp)
			if retryResp.StatusCode < 400 {
				log.Infof("proxy: %s %s retry succeeded", a.ID, label)
				resp = retryResp
				return true
			}
			_ = retryResp.Body.Close()
			log.Warnf("proxy: %s %s retry still %d", a.ID, label, retryResp.StatusCode)
			return false
		}

		recovered := false
		if path == "/v1/messages" && thinkingsig.IsThinkingError(errBody) {
			// Tier 1 — signature flavor: strip stale thinking from PAST turns
			// (SanitizeForSwitch keeps the conversation in thinking mode).
			if thinkingsig.IsSignatureError(errBody) {
				recovered = replay(thinkingsig.SanitizeForSwitch(body), "sanitizing")
				if recovered {
					flagStripThinking(a)
				}
			}
			// Tier 2 — when stripping can't help ("latest assistant message
			// cannot be modified") or tier 1 still failed: replay with thinking
			// disabled entirely so there's nothing left to validate.
			if !recovered {
				recovered = replay(thinkingsig.DisableThinking(body), "disabling-thinking")
			}
		}
		if recovered {
			goto recoveredFromSignature
		}

		// Account-ban detection: Anthropic returns "organization has been
		// disabled" / "account has been disabled" on terminal bans, usually
		// with 401/403 but occasionally 400. These should hard-disable the
		// credential (manual clear required), not just cooldown.
		banned := isAccountBanBody(errBody)

		// Credential bookkeeping: mark the auth so the pool can make
		// smarter scheduling decisions, but never hide the error from
		// the client. Generic 4xx (400/404/413/422/...) are client-request
		// faults — credential is fine, so no MarkFailure.
		switch {
		case banned:
			a.MarkHardFailure(fmt.Sprintf("account banned (upstream %d)", resp.StatusCode))
			log.Warnf("auth: %s hard-disabled — account ban detected (status %d)", a.ID, resp.StatusCode)
		case resp.StatusCode == 429 && bytes.Contains(errBody, []byte("Extra usage is required")):
			// Request-level rejection (long context), not a credential
			// problem — no cooldown.
		case resp.StatusCode == 429:
			// Four flavors of 429 from Anthropic, treated differently. Check
			// in this order — earlier checks are more specific signals:
			//
			//  1. Authoritative ratelimit headers — `anthropic-ratelimit-
			//     unified-status` (or `unified-5h-status` / `unified-7d-status`)
			//     == "rejected" together with `anthropic-ratelimit-unified-reset`
			//     (or per-bucket reset). This is the single most reliable quota
			//     signal Anthropic ships, present on every modern API call,
			//     regardless of body wording. Cool down until the stamped reset
			//     time so IsHealthy stays false until the credential genuinely
			//     recovers.
			//  2. Subscription usage limit ("Claude AI usage limit
			//     reached|<unix-ts>") — older / human-readable variant of (1).
			//     Honour the body timestamp.
			//  3. Stealth ban (no Retry-After, no anthropic-ratelimit-*
			//     headers, body is the generic rate_limit_error blurb):
			//     Anthropic occasionally serves bans this way. Hard-
			//     fail immediately so the credential stops cycling
			//     back into rotation every 30 seconds.
			//  4. Ordinary RPM/TPM rate limit: short cooldown +
			//     MarkRateLimited counter (15-strike escalation still
			//     applies as a backstop).
			//
			// Only (1) and (2) advance MarkUsageLimitReached (which deliberately
			// does NOT touch the consecutive-429 counter — those are real quota
			// signals, not stealth-ban candidates).
			if resetAt, banned, ok := parseUnifiedRatelimitRejected(resp.Header); ok && !banned {
				a.MarkUsageLimitReached(resetAt)
				log.Warnf("auth: %s usage limit (unified-ratelimit rejected) — cooldown until %s", a.ID, resetAt.Format(time.RFC3339))
			} else if resetAt, ok := parseClaudeUsageLimitBody(errBody); ok {
				a.MarkUsageLimitReached(resetAt)
				log.Warnf("auth: %s subscription usage limit — cooldown until %s", a.ID, resetAt.Format(time.RFC3339))
			} else {
				// "No reset signal" 429s — either unified-ratelimit
				// rejected with every reset stamp past/missing, or no
				// ratelimit headers at all. We don't know if the account
				// is banned or just genuinely rate-limited with a buggy
				// upstream payload, so defer the hard-fail decision to
				// the 15-strike accumulator inside MarkRateLimited
				// (rateLimit429HardFailureThreshold). One bad reply
				// shouldn't be enough to take a credential offline.
				resetAt := parseRetryAfter(resp.Header)
				s.pool.ReportUpstreamError(a, resp.StatusCode, resetAt)
			}
		case resp.StatusCode == 401 || resp.StatusCode == 403:
			resetAt := parseRetryAfter(resp.Header)
			s.pool.ReportUpstreamError(a, resp.StatusCode, resetAt)
		case resp.StatusCode == 529, resp.StatusCode >= 500:
			a.MarkFailure(fmt.Sprintf("upstream %d", resp.StatusCode))
		}

		log.Warnf("proxy: %s returned %d — forwarding to client. body=%s", a.ID, resp.StatusCode, truncate(errBody, 500))
		authKind := "oauth"
		if a.Kind == auth.KindAPIKey {
			authKind = "apikey"
		}
		s.emitLog(requestlog.Record{
			Client:      clientName,
			ClientToken: maskClientToken(clientToken),
			AuthID:      a.ID,
			AuthLabel:   a.Label,
			AuthKind:    authKind,
			Model:       model,
			Status:      resp.StatusCode,
			DurationMs:  time.Since(start).Milliseconds(),
			Stream:      stream,
			Path:        path,
			Attempts:    attempts,
			Error:       fmt.Sprintf("upstream %d", resp.StatusCode),
		})
		// Break sticky session so the next request from this client can
		// be assigned to a different (hopefully healthy) credential.
		s.pool.Unstick(auth.NormalizeProvider(a.Provider), clientToken, slotID)

		writeResponseHeaders(c, resp)
		c.Writer.Write(errBody)
		return false, true
	}

recoveredFromSignature:
	// Success or non-retryable error — stream response body to client.
	writeResponseHeaders(c, resp)

	var counts usage.Counts
	var sub advisor.SubUsage
	counts.Requests = 1
	a.MarkSuccess()

	// When this credential rewrote the request's model name (relay vendors
	// with vendor-prefixed names), rewrite it back in the response so the
	// client keeps seeing the model it asked for. Claude Code uses the
	// model field on message_start to correlate conversation turns; a
	// vendor-prefixed name breaks multi-turn continuation.
	var rewriteClientModel string
	if upstreamModel, ok := a.ResolveUpstreamModel(model); ok && upstreamModel != model && upstreamModel != "" {
		rewriteClientModel = model
	}

	if stream && strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		streamSSE(c, resp, &counts, &sub, rewriteClientModel)
	} else {
		respBody, _ := io.ReadAll(resp.Body)
		if rewriteClientModel != "" {
			respBody = rewriteResponseModel(respBody, rewriteClientModel)
		}
		c.Writer.Write(respBody)
		counts.Add(extractUsageFromJSON(respBody, &sub))
	}
	_ = resp.Body.Close()
	authKind := "oauth"
	if a.Kind == auth.KindAPIKey {
		authKind = "apikey"
	}
	s.usage.Record(a.ID, a.Label, counts)
	// Charge the client for the tokens they actually consumed.
	var costUSD float64
	if resp.StatusCode < 400 && counts.Requests > 0 && clientToken != "" {
		costUSD = s.pricing.Cost(auth.NormalizeProvider(a.Provider), model, counts)
	}
	// Advisor (server-side opus sub-call) is billed alongside the main
	// request: same auth absorbs the load, same client is charged, but the
	// requestlog gets a separate row per advisor model so by-model views
	// don't conflate sonnet-orchestrator cost with opus-advisor cost.
	advisorCost := s.recordSubUsage(a, authKind, clientToken, clientName, model, path, resp.StatusCode, sub)
	var multiplier, billedMain float64 = 1, 0
	if resp.StatusCode < 400 && counts.Requests > 0 && clientToken != "" {
		// Single RecordClient call: weekly cost ledger should reflect the
		// total dollar cost of this /v1/messages call, advisor included.
		// Counts.Requests stays at 1 — advisor is a sub-call, not a request.
		var clientCounts usage.Counts
		clientCounts.Add(counts)
		for _, sc := range sub.Snapshot() {
			clientCounts.Add(sc)
		}
		s.usage.RecordClient(clientToken, clientName, clientCounts, costUSD+advisorCost)
		// SaaS settle — debit the per-request charge from the wallet.
		// Advisor sub-charges are debited separately inside
		// recordSubUsage so each row in the request log carries its own
		// billed amount.
		multiplier, billedMain = s.saas.SettleCharge(c.Request.Context(),
			clientToken, auth.NormalizeProvider(a.Provider), model, costUSD,
			"request:"+a.ID)
	}
	s.emitLog(requestlog.Record{
		Client:      clientName,
		ClientToken: maskClientToken(clientToken),
		Provider:    auth.NormalizeProvider(a.Provider),
		AuthID:      a.ID,
		AuthLabel:   a.Label,
		AuthKind:    authKind,
		Model:       model,
		Input:       counts.InputTokens,
		Output:      counts.OutputTokens,
		CacheRead:   counts.CacheReadTokens,
		CacheCreate: counts.CacheCreateTokens,
		CostUSD:     costUSD,
		BilledUSD:   billedMain,
		Multiplier:  multiplier,
		Status:      resp.StatusCode,
		DurationMs:  time.Since(start).Milliseconds(),
		Stream:      stream,
		Path:        path,
		Attempts:    attempts,
	})
	return false, true
}

// doForwardAnthropicAPIKey is the API-key passthrough for Anthropic-shaped
// upstreams (api.anthropic.com or third-party relays). Unlike the OAuth path,
// we do not inject any Claude Code mimicry headers, do not use uTLS, and do
// not retry across credentials. Whatever the upstream returns is forwarded
// to the client verbatim — credential cooldowns and cross-credential retries
// are intentionally skipped. The only request-side change allowed is the
// per-credential model rewrite (and the matching response-side rewrite) so
// model_map'd relay vendors keep working.
//
// Health tracking: success → MarkSuccess; every error (401/402/403,
// 4xx/5xx/429, transport) → MarkFailure, which records the failure for
// admin visibility but — for API-key credentials — never auto-promotes to
// a sticky hard-failure. API keys are operator-managed BYOK / relay
// channels: a flaky relay backend, a missing model, or a stretch of 500s
// must not pull the whole channel out of rotation until someone clears it
// by hand. Auto-retirement on repeated failure is reserved for OAuth
// subscription accounts (enforced in cc-core auth.MarkFailure /
// MarkHardFailure, which exempt KindAPIKey). Operators still disable a key
// manually from the admin panel (the Disabled flag) when they truly want it
// offline.
func (s *Server) doForwardAnthropicAPIKey(c *gin.Context, a *auth.Auth, path string, body []byte, stream bool, model, clientToken, clientName string, start time.Time, attempts int) (retry bool, done bool) {
	baseURL := s.cfg.AnthropicBaseURL
	if ab := strings.TrimRight(a.Snapshot().BaseURL, "/"); ab != "" {
		baseURL = ab
	}
	upURL := baseURL + path

	upstreamBody := body
	rewriteClientModel := ""
	if upstreamModel, ok := a.ResolveUpstreamModel(model); ok && upstreamModel != model && upstreamModel != "" {
		if rewritten, err := rewriteModelField(body, upstreamModel); err == nil {
			upstreamBody = rewritten
			rewriteClientModel = model
		} else {
			log.Warnf("proxy(apikey): model rewrite (%s -> %s) failed via %s: %v", model, upstreamModel, a.ID, err)
		}
	}

	ctx := c.Request.Context()
	upReq, err := http.NewRequestWithContext(ctx, http.MethodPost, upURL, bytes.NewReader(upstreamBody))
	if err != nil {
		c.AbortWithStatusJSON(500, gin.H{"error": err.Error()})
		return false, true
	}
	copyForwardableHeaders(c.Request.Header, upReq.Header)
	stripIngressHeaders(upReq.Header)
	token, _ := a.Credentials()
	upReq.Header.Set("x-api-key", token)

	client := auth.ClientFor(a.ProxyURL, false)
	resp, err := client.Do(upReq)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			log.Infof("proxy(apikey): client canceled via %s: %v", a.ID, err)
			s.emitLog(requestlog.Record{
				Client: clientName, ClientToken: maskClientToken(clientToken),
				Provider: auth.NormalizeProvider(a.Provider), AuthID: a.ID, AuthLabel: a.Label, AuthKind: "apikey",
				Model: model, Stream: stream, Path: path, Status: 499,
				DurationMs: time.Since(start).Milliseconds(), Attempts: attempts, Error: "client canceled",
			})
			return false, true
		}
		log.Warnf("proxy(apikey): upstream transport error via %s: %v", a.ID, err)
		a.MarkFailure(fmt.Sprintf("transport: %v", err))
		c.AbortWithStatusJSON(502, gin.H{"error": err.Error()})
		s.emitLog(requestlog.Record{
			Client: clientName, ClientToken: maskClientToken(clientToken),
			Provider: auth.NormalizeProvider(a.Provider), AuthID: a.ID, AuthLabel: a.Label, AuthKind: "apikey",
			Model: model, Stream: stream, Path: path, Status: 502,
			DurationMs: time.Since(start).Milliseconds(), Attempts: attempts, Error: err.Error(),
		})
		return false, true
	}

	// Decompress upstream gzip/br before reading. Some relays emit gzipped
	// 4xx error pages even when the request didn't advertise an
	// Accept-Encoding; without this the captured snippet is binary.
	ccstream.Decompress(resp)

	// Reactive signature-error recovery — the API-key twin of the OAuth
	// path above. Relay API keys fan out across their own backend account
	// pool and rotate per request, so a `thinking` signature minted on one
	// backend turn lands on a different backend the next turn → 400
	// "Invalid `signature` in `thinking` block". switchTracker only ever
	// sees OUR credential (always the same relay key), so the proactive
	// sanitize in doForward never fires for relay-internal rotation; this
	// reactive replay is the only rescue on this path. Done before
	// writeResponseHeaders so the client never sees the transient error.
	// Gated on >=400 (not just 400) because relays re-wrap Anthropic's
	// signature 400 as 500/529; the IsSignatureError body match below is
	// the real guard.
	if resp.StatusCode >= 400 && path == "/v1/messages" {
		errBody, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		recovered := false
		if thinkingsig.IsThinkingError(errBody) {
			// replay re-sends a thinking-sanitized body on the same relay key.
			replay := func(candidate []byte, label string) bool {
				if bytes.Equal(candidate, body) {
					return false
				}
				retryUpstream := candidate
				if upstreamModel, ok := a.ResolveUpstreamModel(model); ok && upstreamModel != model && upstreamModel != "" {
					if rewritten, rerr := rewriteModelField(retryUpstream, upstreamModel); rerr == nil {
						retryUpstream = rewritten
					}
				}
				log.Warnf("proxy(apikey): %s returned %d thinking-block error — %s and retrying once on same credential", a.ID, resp.StatusCode, label)
				retryReq, rerr := http.NewRequestWithContext(ctx, http.MethodPost, upURL, bytes.NewReader(retryUpstream))
				if rerr != nil {
					return false
				}
				copyForwardableHeaders(c.Request.Header, retryReq.Header)
				stripIngressHeaders(retryReq.Header)
				retryReq.Header.Set("x-api-key", token)
				retryResp, derr := client.Do(retryReq)
				if derr != nil {
					log.Warnf("proxy(apikey): %s %s retry transport error: %v", a.ID, label, derr)
					return false
				}
				ccstream.Decompress(retryResp)
				if retryResp.StatusCode < 400 {
					log.Infof("proxy(apikey): %s %s retry succeeded", a.ID, label)
					resp = retryResp
					return true
				}
				_ = retryResp.Body.Close()
				log.Warnf("proxy(apikey): %s %s retry still %d", a.ID, label, retryResp.StatusCode)
				return false
			}
			// Tier 1 — strip stale thinking from past turns (signature flavor).
			if thinkingsig.IsSignatureError(errBody) {
				recovered = replay(thinkingsig.SanitizeForSwitch(body), "sanitizing")
				if recovered {
					flagStripThinking(a)
				}
			}
			// Tier 2 — disable thinking entirely ("latest assistant message
			// cannot be modified", or tier 1 still failed).
			if !recovered {
				recovered = replay(thinkingsig.DisableThinking(body), "disabling-thinking")
			}
		}
		if !recovered {
			// Hand the original (already-consumed) error body back to the
			// unchanged code below as if nothing happened.
			resp.Body = io.NopCloser(bytes.NewReader(errBody))
		}
	}

	writeResponseHeaders(c, resp)
	var counts usage.Counts
	var sub advisor.SubUsage
	var errSnippet string
	if resp.StatusCode >= 400 {
		// Capture upstream body for the request log + warning. Without
		// this, API-key 4xx is silent — only the gin access line shows
		// up — and operators have no signal whether the relay rejected
		// the model, exhausted the key's quota, IP-banned us, etc.
		errBody, _ := io.ReadAll(resp.Body)
		c.Writer.Write(errBody)
		errSnippet = truncate(errBody, 500)
		log.Warnf("proxy(apikey): %s returned %d — body=%s", a.ID, resp.StatusCode, errSnippet)
	} else {
		counts.Requests = 1
		if stream && strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
			streamSSE(c, resp, &counts, &sub, rewriteClientModel)
		} else {
			respBody, _ := io.ReadAll(resp.Body)
			if rewriteClientModel != "" {
				respBody = rewriteResponseModel(respBody, rewriteClientModel)
			}
			c.Writer.Write(respBody)
			counts.Add(extractUsageFromJSON(respBody, &sub))
		}
	}
	_ = resp.Body.Close()

	switch {
	case resp.StatusCode < 400:
		a.MarkSuccess()
	case resp.StatusCode == http.StatusUnauthorized ||
		resp.StatusCode == http.StatusPaymentRequired ||
		resp.StatusCode == http.StatusForbidden:
		// Record for visibility. cc-core's MarkFailure exempts KindAPIKey
		// from the consecutive-failure auto-disable, so this never retires
		// the channel — only a manual admin disable does.
		a.MarkFailure(fmt.Sprintf("upstream %d", resp.StatusCode))
	case resp.StatusCode == http.StatusTooManyRequests,
		resp.StatusCode == http.StatusServiceUnavailable,
		resp.StatusCode == http.StatusNotFound:
		// API-key relays routinely emit:
		//  - 429: per-key throttle
		//  - 503: vendor-side overload / brief maintenance
		//  - 404: route not implemented (e.g. /v1/messages/count_tokens
		//    on relays that only proxy /v1/messages)
		// None of these reflect on the credential itself. Skip Mark*
		// so transient upstream weather and route-coverage gaps don't
		// pin a working key into cooldown or trip the consecutive-429
		// stealth-ban accumulator.
	default:
		a.MarkFailure(fmt.Sprintf("upstream %d", resp.StatusCode))
	}

	var costUSD float64
	var multiplier, billedMain float64 = 1, 0
	if resp.StatusCode < 400 {
		s.usage.Record(a.ID, a.Label, counts)
		if counts.Requests > 0 && clientToken != "" {
			costUSD = s.pricing.Cost(auth.NormalizeProvider(a.Provider), model, counts)
		}
		advisorCost := s.recordSubUsage(a, "apikey", clientToken, clientName, model, path, resp.StatusCode, sub)
		if counts.Requests > 0 && clientToken != "" {
			var clientCounts usage.Counts
			clientCounts.Add(counts)
			for _, sc := range sub.Snapshot() {
				clientCounts.Add(sc)
			}
			s.usage.RecordClient(clientToken, clientName, clientCounts, costUSD+advisorCost)
			multiplier, billedMain = s.saas.SettleCharge(c.Request.Context(),
				clientToken, auth.NormalizeProvider(a.Provider), model, costUSD,
				"request:"+a.ID)
		}
	}
	errField := ""
	if resp.StatusCode >= 400 {
		errField = fmt.Sprintf("upstream %d: %s", resp.StatusCode, truncate([]byte(errSnippet), 200))
	}
	s.emitLog(requestlog.Record{
		Client:      clientName,
		ClientToken: maskClientToken(clientToken),
		Provider:    auth.NormalizeProvider(a.Provider),
		AuthID:      a.ID,
		AuthLabel:   a.Label,
		AuthKind:    "apikey",
		Model:       model,
		Input:       counts.InputTokens,
		Output:      counts.OutputTokens,
		CacheRead:   counts.CacheReadTokens,
		CacheCreate: counts.CacheCreateTokens,
		CostUSD:     costUSD,
		BilledUSD:   billedMain,
		Multiplier:  multiplier,
		Status:      resp.StatusCode,
		DurationMs:  time.Since(start).Milliseconds(),
		Stream:      stream,
		Path:        path,
		Attempts:    attempts,
		Error:       errField,
	})
	return false, true
}

// stripIngressHeaders removes headers that describe the *ingress path* into
// our server before forwarding upstream. Critical when the server sits
// behind Cloudflare Tunnel: cloudflared injects Cdn-Loop: cloudflare plus a
// pile of Cf-* headers, and api.anthropic.com / chatgpt.com are themselves
// behind CF — seeing those headers triggers CF's loop-prevention WAF and
// returns 403 HTML. Prefix match so future CF additions are covered.
func stripIngressHeaders(h http.Header) {
	for k := range h {
		lower := strings.ToLower(k)
		if strings.HasPrefix(lower, "cf-") || strings.HasPrefix(lower, "cdn-") ||
			strings.HasPrefix(lower, "x-forwarded-") || strings.HasPrefix(lower, "x-real-") {
			h.Del(k)
		}
	}
	for _, k := range []string{"Forwarded", "Via", "Cookie", "Referer", "Origin", "True-Client-Ip"} {
		h.Del(k)
	}
}

func copyForwardableHeaders(src, dst http.Header) {
	for k, vs := range src {
		if hopHeaders[http.CanonicalHeaderKey(k)] {
			continue
		}
		// Don't forward Host.
		if strings.EqualFold(k, "Host") {
			continue
		}
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

func writeResponseHeaders(c *gin.Context, resp *http.Response) {
	for k, vs := range resp.Header {
		if hopHeaders[http.CanonicalHeaderKey(k)] {
			continue
		}
		for _, v := range vs {
			c.Writer.Header().Add(k, v)
		}
	}
	c.Writer.WriteHeader(resp.StatusCode)
}

// applyAnthropicHeaders is a thin adapter from CPA-Claude's *auth.Auth
// to cc-core/mimicry.ApplyClaudeCodeHeaders. The actual header policy
// (pinned UA / X-Stainless-* / Anthropic-Beta / session-id / accept-
// encoding) lives in cc-core/mimicry so multiple forks stay in lockstep
// with the CC version target.
func applyAnthropicHeaders(req *http.Request, a *auth.Auth, stream, isAnthropicBase bool, id mimicry.SimIdentity, body []byte) {
	token, kind := a.Credentials()
	mimicry.ApplyClaudeCodeHeaders(req, token, kindToMimicry(kind), stream, isAnthropicBase, id, body)
}

func kindToMimicry(k auth.Kind) string {
	if k == auth.KindAPIKey {
		return mimicry.KindAPIKey
	}
	return mimicry.KindOAuth
}

// streamSSE copies SSE events to the client as they arrive and parses
// message_delta events to accumulate usage. When rewriteClientModel is
// non-empty, each data: JSON has its top-level "model" and nested
// "message.model" fields rewritten to that value before being forwarded.
//
// Framing uses cc-core/stream.SSEScanner so the event/data parsing logic
// is shared with other forks; this function is just the proxy-specific
// glue (model rewrite + usage accumulation + flusher dispatch).
func streamSSE(c *gin.Context, resp *http.Response, counts *usage.Counts, sub *advisor.SubUsage, rewriteClientModel string) {
	flusher, _ := c.Writer.(http.Flusher)
	sc := ccstream.NewSSEScanner(resp.Body, 64*1024)
	for sc.Scan() {
		line := sc.Line()
		outLine := line
		if payload := sc.Data(); payload != nil {
			if rewriteClientModel != "" && len(payload) > 0 && payload[0] == '{' {
				if rewritten := rewriteResponseModel(payload, rewriteClientModel); rewritten != nil {
					trim := bytes.TrimRight(line, "\r\n")
					tail := line[len(trim):]
					rebuilt := make([]byte, 0, len("data: ")+len(rewritten)+len(tail))
					rebuilt = append(rebuilt, []byte("data: ")...)
					rebuilt = append(rebuilt, rewritten...)
					rebuilt = append(rebuilt, tail...)
					outLine = rebuilt
				}
			}
			if ev := sc.Event(); ev == "message_start" || ev == "message_delta" {
				mergeSSEUsage(counts, sub, payload)
			}
		}
		c.Writer.Write(outLine)
		if flusher != nil {
			flusher.Flush()
		}
	}
}

// usageJSON is the wire shape of `usage` (and `message.usage`) on /v1/messages.
type usageJSON struct {
	InputTokens              int64                    `json:"input_tokens"`
	OutputTokens             int64                    `json:"output_tokens"`
	CacheCreationInputTokens int64                    `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64                    `json:"cache_read_input_tokens"`
	Iterations               []advisor.IterationUsage `json:"iterations,omitempty"`
}

func (u usageJSON) toCounts() usage.Counts {
	return usage.Counts{
		InputTokens:       u.InputTokens,
		OutputTokens:      u.OutputTokens,
		CacheCreateTokens: u.CacheCreationInputTokens,
		CacheReadTokens:   u.CacheReadInputTokens,
	}
}

// recordSubUsage charges advisor (and any future server-side sub-model)
// counts to the same auth that handled the parent request, and emits one
// extra requestlog row per distinct sub-model so by-model aggregation in
// the admin panel separates orchestrator cost from advisor cost.
//
// Returns the total advisor USD cost so the caller can fold it into the
// per-client weekly ledger as a single sum (one /v1/messages call = one
// weekly Requests bump regardless of how many sub-models ran).
//
// No-op when the response is an error (status >= 400) or there are no
// advisor iterations. Auth-side load tracking only applies to successful
// sub-calls — a failed parent rarely has billable advisor activity, and
// double-counting would distort WeightedTotal-driven load balancing.
func (s *Server) recordSubUsage(a *auth.Auth, authKind, clientToken, clientName, parentModel, path string, status int, sub advisor.SubUsage) float64 {
	if status >= 400 || sub.IsEmpty() {
		return 0
	}
	provider := auth.NormalizeProvider(a.Provider)
	var total float64
	for subModel, sc := range sub.Snapshot() {
		// Sub-calls bump the auth's daily/hourly bucket and WeightedTotal so
		// the credential bears the full opus load. Requests stays 0: the
		// parent already counted +1.
		s.usage.Record(a.ID, a.Label, sc)
		cost := s.pricing.Cost(provider, subModel, sc)
		total += cost
		// SaaS settle: advisor sub-call is debited under the sub-model's
		// own provider+model so the multiplier picked is correct (advisor
		// is currently always Claude-side, but plumb provider through so
		// future server-side OpenAI advisors still work).
		var mult, billed float64 = 1, 0
		if clientToken != "" {
			mult, billed = s.saas.SettleCharge(context.Background(),
				clientToken, provider, subModel, cost,
				"advisor:"+a.ID)
		}
		s.emitLog(requestlog.Record{
			Client:      clientName,
			ClientToken: maskClientToken(clientToken),
			Provider:    provider,
			AuthID:      a.ID,
			AuthLabel:   a.Label,
			AuthKind:    authKind,
			Model:       subModel,
			Input:       sc.InputTokens,
			Output:      sc.OutputTokens,
			CacheRead:   sc.CacheReadTokens,
			CacheCreate: sc.CacheCreateTokens,
			CostUSD:     cost,
			BilledUSD:   billed,
			Multiplier:  mult,
			Status:      status,
			// DurationMs/Stream/Attempts intentionally zero: this row is a
			// sub-call summary, not an independent request — adding wall
			// time would double-count it in admin's "total time" stats.
			Path: path + "#advisor:" + subModel,
		})
	}
	return total
}

// extractUsageFromJSON pulls the top-level "usage" from a non-streaming
// /v1/messages response. Advisor sub-billing iterations are folded into
// `sub` if non-nil.
func extractUsageFromJSON(body []byte, sub *advisor.SubUsage) usage.Counts {
	var wrap struct {
		Usage usageJSON `json:"usage"`
	}
	_ = json.Unmarshal(body, &wrap)
	if sub != nil {
		sub.ReplaceFrom(wrap.Usage.Iterations)
	}
	return wrap.Usage.toCounts()
}

// mergeSSEUsage overlays usage fields from a single Anthropic SSE data
// payload onto dst, using overwrite-if-positive semantics. This is NOT
// additive: Anthropic's stream sends the input/cache token baseline in
// message_start and the cumulative final usage (often repeating the same
// input/cache values plus the real output count) in message_delta, so
// summing the two events would double-count input and cache tokens.
//
// Shapes handled:
//
//	message_start:  {type: "message_start", message: {usage: {...}}}
//	message_delta:  {type: "message_delta", usage: {...}}
//
// Zero values from a later event don't clobber a prior non-zero value —
// matches the protocol where message_delta sometimes omits the input
// fields (e.g. emits input_tokens=0).
func mergeSSEUsage(dst *usage.Counts, sub *advisor.SubUsage, payload []byte) {
	if dst == nil {
		return
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(payload, &probe); err != nil {
		return
	}
	var u usageJSON
	if raw, ok := probe["usage"]; ok {
		_ = json.Unmarshal(raw, &u)
	} else if raw, ok := probe["message"]; ok {
		var nested struct {
			Usage usageJSON `json:"usage"`
		}
		if err := json.Unmarshal(raw, &nested); err == nil {
			u = nested.Usage
		} else {
			return
		}
	} else {
		return
	}
	if u.InputTokens > 0 {
		dst.InputTokens = u.InputTokens
	}
	if u.OutputTokens > 0 {
		dst.OutputTokens = u.OutputTokens
	}
	if u.CacheCreationInputTokens > 0 {
		dst.CacheCreateTokens = u.CacheCreationInputTokens
	}
	if u.CacheReadInputTokens > 0 {
		dst.CacheReadTokens = u.CacheReadInputTokens
	}
	if sub != nil && len(u.Iterations) > 0 {
		// message_delta.usage.iterations is cumulative — last non-empty
		// observation wins, never append.
		sub.ReplaceFrom(u.Iterations)
	}
}

func parseRetryAfter(h http.Header) time.Time {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return time.Time{}
	}
	if n, err := strconv.Atoi(v); err == nil {
		return time.Now().Add(time.Duration(n) * time.Second)
	}
	if t, err := http.ParseTime(v); err == nil {
		return t
	}
	return time.Time{}
}

// parseUnifiedRatelimitRejected reports whether Anthropic's
// `anthropic-ratelimit-unified-*` headers signal that this credential is out
// of quota right now. When yes, the returned time is when to try again
// (parsed from `*-reset`, expressed as Unix seconds; falls back 1h ahead if
// missing/unparseable).
//
// Real responses carry a snapshot like:
//
//	anthropic-ratelimit-unified-status: rejected           ← top-level decision
//	anthropic-ratelimit-unified-5h-status: rejected        ← per-bucket states
//	anthropic-ratelimit-unified-7d-status: allowed
//	anthropic-ratelimit-unified-5h-reset: 1777824000       ← per-bucket reset
//	anthropic-ratelimit-unified-7d-reset: 1778018400
//	anthropic-ratelimit-unified-reset: 1777824000          ← top-level reset
//	anthropic-ratelimit-unified-representative-claim: five_hour
//
// We treat ANY of `unified-status / unified-5h-status / unified-7d-status`
// being "rejected" (or a prefix thereof, e.g. "rejected_*") as a quota signal.
// For the reset time we prefer top-level `unified-reset`, else the latest of
// the rejected per-bucket resets — that way a 7d rejection isn't released by
// the (sooner) 5h reset.
//
// Returns:
//   - ok=false: not rejected.
//   - ok=true, banned=true: rejected but no usable FUTURE reset stamp
//     (every parseable stamp is in the past, or there is no stamp at all).
//     This shape — "you're rejected, recovery time = unknown / already past"
//     — is the stealth-ban signature: a banned account stays "rejected"
//     forever, so admin-panel users see "Quota exceeded → resets in now"
//     looping. Caller must hard-fail the credential.
//   - ok=true, banned=false: rejected with a real future reset stamp →
//     normal quota cooldown until that time.
func parseUnifiedRatelimitRejected(h http.Header) (resetAt time.Time, banned bool, ok bool) {
	const statusPrefix = "rejected"
	isRejected := func(headerName string) bool {
		v := strings.ToLower(strings.TrimSpace(h.Get(headerName)))
		return v != "" && strings.HasPrefix(v, statusPrefix)
	}

	bucketStatuses := []struct{ statusHdr, resetHdr string }{
		{"Anthropic-Ratelimit-Unified-5h-Status", "Anthropic-Ratelimit-Unified-5h-Reset"},
		{"Anthropic-Ratelimit-Unified-7d-Status", "Anthropic-Ratelimit-Unified-7d-Reset"},
	}
	rejected := isRejected("Anthropic-Ratelimit-Unified-Status")
	var bucketReset time.Time
	for _, b := range bucketStatuses {
		if !isRejected(b.statusHdr) {
			continue
		}
		rejected = true
		if t, parsed := parseUnixSecondsHeader(h.Get(b.resetHdr)); parsed && t.After(bucketReset) {
			bucketReset = t
		}
	}
	if !rejected {
		return time.Time{}, false, false
	}
	now := time.Now()
	if t, parsed := parseUnixSecondsHeader(h.Get("Anthropic-Ratelimit-Unified-Reset")); parsed && t.After(now) {
		return clampReset(t), false, true
	}
	if !bucketReset.IsZero() && bucketReset.After(now) {
		return clampReset(bucketReset), false, true
	}
	// Rejected with no future reset (every stamp is past, or missing entirely).
	// This is what a banned subscription account looks like — Anthropic flags
	// it "rejected" forever with no recovery time. Tell caller to hard-fail.
	return time.Time{}, true, true
}

// parseUnixSecondsHeader parses an `epoch-seconds` integer header value into
// a time.Time. Tolerates whitespace; returns ok=false on empty / non-integer.
func parseUnixSecondsHeader(v string) (time.Time, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return time.Time{}, false
	}
	secs, err := strconv.ParseInt(v, 10, 64)
	if err != nil || secs <= 0 {
		return time.Time{}, false
	}
	return time.Unix(secs, 0), true
}

// clampReset caps a parsed future reset stamp at 30 days as a defense
// against malformed payloads. Caller is responsible for ensuring t is
// already in the future (past stamps are a separate signal — see
// parseUnifiedRatelimitRejected).
func clampReset(t time.Time) time.Time {
	max := time.Now().Add(30 * 24 * time.Hour)
	if t.After(max) {
		return max
	}
	return t
}

// parseClaudeUsageLimitBody extracts the reset timestamp from a Claude
// subscription usage-limit 429. Anthropic encodes it as
// "Claude AI usage limit reached|<unix-seconds>" in the message field, e.g.
//
//	{"type":"error","error":{"type":"rate_limit_error",
//	  "message":"Claude AI usage limit reached|1714761600"}}
//
// ok=true means we found the marker AND parsed a sane future timestamp;
// caller should treat this as a regular quota cooldown (NOT a stealth ban),
// because the body is explicit about both the cause and the recovery time.
func parseClaudeUsageLimitBody(b []byte) (time.Time, bool) {
	if len(b) == 0 {
		return time.Time{}, false
	}
	const marker = "Claude AI usage limit reached"
	lower := bytes.ToLower(b)
	idx := bytes.Index(lower, []byte(strings.ToLower(marker)))
	if idx < 0 {
		return time.Time{}, false
	}
	// Walk past the marker in the original (non-lowercased) body; we want
	// the literal "|<digits>" tail. Tolerate optional whitespace.
	tail := b[idx+len(marker):]
	for len(tail) > 0 && (tail[0] == ' ' || tail[0] == '\t') {
		tail = tail[1:]
	}
	if len(tail) == 0 || tail[0] != '|' {
		// Marker present but no timestamp — still a usage-limit signal,
		// but we have nothing to set the cooldown to. Fall back to a
		// best-effort 1h cooldown so the credential doesn't loop.
		return time.Now().Add(1 * time.Hour), true
	}
	tail = tail[1:]
	end := 0
	for end < len(tail) && tail[end] >= '0' && tail[end] <= '9' {
		end++
	}
	if end == 0 {
		return time.Now().Add(1 * time.Hour), true
	}
	secs, err := strconv.ParseInt(string(tail[:end]), 10, 64)
	if err != nil {
		return time.Now().Add(1 * time.Hour), true
	}
	t := time.Unix(secs, 0)
	// Reject obviously bogus timestamps (already passed or > 30 days out)
	// — degrade to the 1h fallback so we don't park a credential forever
	// on a malformed payload.
	if t.Before(time.Now()) || t.After(time.Now().Add(30*24*time.Hour)) {
		return time.Now().Add(1 * time.Hour), true
	}
	return t, true
}

// isAccountBanBody reports whether the upstream error body looks like a
// terminal account/organization ban from Anthropic. Match is case-insensitive
// and deliberately narrow to avoid firing on routine rate-limit / usage-limit
// copy (e.g. "your organization's usage limit").
func isAccountBanBody(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	lower := bytes.ToLower(b)
	markers := [][]byte{
		[]byte("organization has been disabled"),
		[]byte("account has been disabled"),
		[]byte("account is disabled"),
		[]byte("organization is disabled"),
		// Org-level OAuth revocation. Anthropic returns a 403
		// permission_error with this exact wording when the
		// subscription account has been blocked from using OAuth
		// (typically a stealth/soft ban). Recovery requires manual
		// intervention, not a cooldown — treat as terminal.
		[]byte("oauth authentication is currently not allowed"),
	}
	for _, m := range markers {
		if bytes.Contains(lower, m) {
			return true
		}
	}
	return false
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "...(truncated)"
}

// rewriteModelField returns a copy of the JSON request body with its top-level
// "model" string set to upstream. Used when an API-key credential has a
// model_map entry that rewrites the client's model name to a vendor-specific
// one (e.g. "claude-opus-4-6" -> "[0.16]稳定喵/claude-opus-4-6"). Falls back
// to leaving the body alone if the JSON can't be parsed as an object.
func rewriteModelField(body []byte, upstream string) ([]byte, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, err
	}
	if obj == nil {
		return body, nil
	}
	mb, err := json.Marshal(upstream)
	if err != nil {
		return nil, err
	}
	obj["model"] = mb
	return json.Marshal(obj)
}

// rewriteResponseModel substitutes the client-facing model name into the
// response JSON so the client never sees the relay vendor's prefixed name
// (e.g. "[0.16]稳定喵/claude-opus-4-6"). Handles both the non-streaming
// /v1/messages response (top-level "model") and SSE event payloads
// (message_start nests "message.model"). Returns the original bytes if
// parsing fails or no known model path is present.
func rewriteResponseModel(data []byte, clientModel string) []byte {
	if len(data) == 0 || clientModel == "" {
		return data
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return data
	}
	changed := false
	newModel, err := json.Marshal(clientModel)
	if err != nil {
		return data
	}
	if _, ok := obj["model"]; ok {
		obj["model"] = newModel
		changed = true
	}
	if raw, ok := obj["message"]; ok && len(raw) > 0 && raw[0] == '{' {
		var inner map[string]json.RawMessage
		if err := json.Unmarshal(raw, &inner); err == nil {
			if _, ok := inner["model"]; ok {
				inner["model"] = newModel
				if merged, err := json.Marshal(inner); err == nil {
					obj["message"] = merged
					changed = true
				}
			}
		}
	}
	if !changed {
		return data
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return data
	}
	return out
}

// unused — kept to avoid import churn if future error types are added.
var _ = fmt.Sprintf
var _ = context.Background
