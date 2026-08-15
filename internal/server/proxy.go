package server

import (
	"bufio"
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
	"github.com/wjsoj/cc-core/downstream"
	"github.com/wjsoj/cc-core/mimicry"
	"github.com/wjsoj/cc-core/relay"
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
			// Fail open. This read is an optimisation — the authoritative debit
			// happens at SettleCharge, which runs on its own connection after the
			// response — so a transient SQLite contention on the way in must not
			// cost a paying customer their request. It used to answer 500, which
			// in the last 24h took three requests down for a lookup that would
			// have succeeded on the retry the customer had to make by hand.
			// Logged at error level because a *persistent* failure here means
			// depleted wallets are being served, and that has to be visible.
			log.Errorf("saas: wallet pre-check failed for %s, serving anyway: %v", maskClientToken(clientToken), err)
			s.emitLog(requestlog.Record{
				Client: clientName, ClientToken: maskClientToken(clientToken),
				Provider: provider, Model: model, Stream: peek.Stream, Path: path,
				DurationMs: time.Since(start).Milliseconds(), AttemptOnly: true,
				Error: "wallet pre-check failed, served anyway: " + err.Error(),
			})
		} else if bal <= 0 {
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
	//
	// The token's own fallback opt-out lands here too: with it off, no API key
	// is reachable however healthy the fleet is, so the request is just as
	// unservable — and saying so up front beats cycling every OAuth credential
	// to arrive at a 503 that never mentions the switch.
	if auth.NormalizeProvider(provider) == auth.ProviderOpenAI && path == "/v1/chat/completions" &&
		(!s.pool.HasAPIKeyFor(provider, clientGroup, model) || !s.allowAPIKeyFallback(c.Request.Context(), provider, clientToken)) {
		msg := fmt.Sprintf("model %q is only available via /v1/responses on this server (no OpenAI-compatible API-key credential is configured for it); retry with the /v1/responses endpoint", model)
		if !s.allowAPIKeyFallback(c.Request.Context(), provider, clientToken) && s.pool.HasAPIKeyFor(provider, clientGroup, model) {
			msg = fmt.Sprintf("model %q is only reachable over /v1/chat/completions through upstream API keys that charge above your current rate, and this API key has opted out of those; either enable the upstream fallback in your wallet settings or use the /v1/responses endpoint", model)
		}
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
		// Codex gets the same looser budget as the concurrency gate — the
		// Codex CLI fans out many short, bursty requests that would otherwise
		// trip the shared RPM cap. Mirrors config.CodexConcurrencyMultiplier
		// usage below. Claude is unaffected.
		if auth.NormalizeProvider(provider) == auth.ProviderOpenAI {
			if m := s.cfg.CodexConcurrencyMultiplier; m > 0 {
				limit *= m
			}
		}
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
	if maxConc > 0 && auth.NormalizeProvider(provider) == auth.ProviderOpenAI {
		// Codex gets a looser budget — see config.CodexConcurrencyMultiplier
		// (same multiplier is applied to the RPM gate above).
		// Guard against a misconfigured 0/negative, which would otherwise
		// zero out maxConc and disable the gate entirely.
		if m := s.cfg.CodexConcurrencyMultiplier; m > 0 {
			maxConc *= m
		}
	}
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

	// Hand off to the credential-failover retry loop.
	s.forwardWithFailover(c, provider, path, model, clientToken, clientGroup, clientName, slotID, body, peek.Stream, start)
}

// forwardWithFailover runs the per-request retry loop: it acquires a
// credential, forwards via the provider-appropriate doForward, and on a
// credential-level error (429 quota/rate-limit, 401/403, account ban — which
// doForward withholds rather than writing through) transparently switches to
// another healthy credential. The user only ever sees an error when the pool
// has no slot left: excludeIDs narrows the candidate set each round so the
// loop terminates naturally once Acquire returns nil (every healthy credential
// tried). maxAttempts is only a backstop against a pathologically large
// all-failing fleet. When every credential is exhausted, the most recent
// withheld upstream error is replayed verbatim (e.g. a 429 + Retry-After)
// instead of a synthetic 503, so clients back off correctly.
func (s *Server) forwardWithFailover(c *gin.Context, provider, path, model, clientToken, clientGroup, clientName, slotID string, body []byte, stream bool, start time.Time) {
	const maxAttempts = 12
	tried := make(map[string]bool)
	attempts := 0
	var lastDeferred *deferredResponse
	// Per-token opt-in: whether the OAuth-exhausted path may fall back to
	// upstream API keys. Stable for the whole request, computed once.
	allowFallback := s.allowAPIKeyFallback(c.Request.Context(), provider, clientToken)
	// Set when OAuth preparation failed: a local identity-binding judgement,
	// not a credential fault, so we stop trying OAuth credentials (they would
	// all fail the same way) and go straight to an API key with the untouched
	// body. Deliberately does NOT mark the credential unhealthy.
	apiKeyOnly := false
	preparationFallbackPending := false

	// surfaceDeferred replays a withheld upstream error to the client once no
	// healthy credential remains — preferable to a synthetic 503 because the
	// client (e.g. Claude Code) backs off correctly on the genuine 429 +
	// Retry-After it would otherwise have received directly.
	surfaceDeferred := func(d *deferredResponse) {
		replayDeferred(c, d)
		s.emitLog(requestlog.Record{
			Client: clientName, ClientToken: maskClientToken(clientToken), Provider: provider,
			AuthID: d.authID, Model: model, Status: d.status, Attempts: attempts,
			Stream: stream, Path: path, DurationMs: time.Since(start).Milliseconds(),
			Error: fmt.Sprintf("upstream %d (all credentials exhausted)", d.status),
		})
	}

	for attempt := 0; attempt < maxAttempts; attempt++ {
		excludeIDs := make([]string, 0, len(tried))
		for id := range tried {
			excludeIDs = append(excludeIDs, id)
		}
		a := s.pool.AcquireWithOptions(c.Request.Context(), provider, clientToken, clientGroup, model, slotID, auth.AcquireOptions{
			AllowAPIKeyFallback: allowFallback,
			APIKeyOnly:          apiKeyOnly,
			ExcludeIDs:          excludeIDs,
		})
		if a == nil {
			// No healthy/untried credential left. If we withheld an upstream
			// error on the way here, surface that genuine status; otherwise
			// there was nothing in the pool to serve the request at all.
			if lastDeferred != nil {
				surfaceDeferred(lastDeferred)
				return
			}
			msg, retryAfter := s.poolUnavailable(provider, allowFallback)
			// The most actionable failure of all: every remaining channel is one
			// this token declined on price. Say so — and say what it would cost —
			// or the user reads "all N credentials are out of quota" and waits for
			// an operator who has nothing to fix. (A channel at or below their own
			// rate is never withheld; see allowAPIKeyFallback.)
			if !allowFallback && s.pool.HasAPIKeyFor(provider, clientGroup, model) {
				msg += "; the only channels still available charge above your current rate, and this API key has opted out of those — enable the upstream fallback in your wallet settings to use them"
			}
			if preparationFallbackPending {
				msg = "claude request preparation failed and no API-key fallback was available"
			} else if len(tried) > 0 {
				// We did get credentials, tried them all, and every one bounced.
				msg = fmt.Sprintf("tried all %d available %s credentials for this request and none could serve it; %s",
					len(tried), auth.NormalizeProvider(provider), msg)
			}
			if retryAfter > 0 {
				c.Header("Retry-After", strconv.Itoa(retryAfter))
			}
			c.AbortWithStatusJSON(503, gin.H{"error": msg})
			s.emitLog(requestlog.Record{
				Client: clientName, ClientToken: maskClientToken(clientToken), Provider: provider, Model: model,
				Stream: stream, Path: path, Status: 503, Attempts: attempts,
				DurationMs: time.Since(start).Milliseconds(), Error: msg,
			})
			return
		}
		tried[a.ID] = true
		attempts++

		// Validate OAuth preparation before anything reaches Anthropic — the
		// sidecar bootstrap included. It is entirely local and deterministic,
		// so a failure says nothing about the credential: don't mark it, switch
		// to an API key and send the untouched original body instead.
		var preflightPrepared mimicry.BodyTransformResult
		if auth.NormalizeProvider(a.Provider) == auth.ProviderAnthropic && a.Kind == auth.KindOAuth && path == "/v1/messages" {
			prepErr := s.preflightClaudeOAuth(c, a, body, model, clientToken, stream, &preflightPrepared)
			if prepErr != nil {
				reason := claudePreparationFailureReason(prepErr)
				log.Errorf("proxy: Claude preparation failed before upstream (auth=%s model=%s reason=%s fallback=apikey): %v",
					a.ID, model, reason, prepErr)
				s.emitLog(requestlog.Record{
					Client: clientName, ClientToken: maskClientToken(clientToken), Provider: provider,
					AuthID: a.ID, AuthLabel: a.Label, AuthKind: "oauth", Model: model,
					Stream: stream, Path: path, Attempts: attempts, DurationMs: time.Since(start).Milliseconds(),
					Error: "claude request preparation failed (" + reason + "); fallback=apikey", AttemptOnly: true,
				})
				apiKeyOnly = true
				preparationFallbackPending = true
				lastDeferred = nil
				continue
			}
		}
		if preparationFallbackPending && a.Kind == auth.KindAPIKey {
			log.Warnf("proxy: Claude preparation fallback selected API-key credential %s", a.ID)
			preparationFallbackPending = false
		}

		var retry, done bool
		var deferred *deferredResponse
		switch auth.NormalizeProvider(a.Provider) {
		case auth.ProviderOpenAI:
			retry, done = s.doForwardCodex(c, a, path, body, stream, model, clientToken, clientName, start, attempts)
		default:
			// attempt > 0 ⇒ transparent retry; doForward skips the blocking
			// bootstrap-wait so the credential switch stays fast.
			retry, done, deferred = s.doForward(c, a, path, body, stream, model, clientToken, slotID, clientName, start, attempts, attempt > 0, preflightPrepared)
		}
		if done {
			s.pool.Release(provider, clientToken, slotID)
			return
		}
		if !retry {
			s.pool.Release(provider, clientToken, slotID)
			return
		}
		// Credential-level error withheld from the client — remember the most
		// recent one so it can be surfaced if every credential is exhausted,
		// then loop on to the next credential.
		if deferred != nil {
			lastDeferred = deferred
		}
		log.Warnf("proxy: retrying with a different credential (last auth=%s)", a.ID)
	}
	// Backstop reached (maxAttempts) — surface the last withheld error if any.
	if lastDeferred != nil {
		surfaceDeferred(lastDeferred)
		return
	}
	c.AbortWithStatusJSON(503, gin.H{"error": "upstream retries exhausted"})
	s.emitLog(requestlog.Record{
		Client: clientName, ClientToken: maskClientToken(clientToken), Provider: provider, Model: model,
		Stream: stream, Path: path, Status: 503, Attempts: attempts,
		DurationMs: time.Since(start).Milliseconds(), Error: "upstream retries exhausted",
	})
}

func (s *Server) emitLog(r requestlog.Record) {
	if s.reqLog == nil {
		return
	}
	s.reqLog.Log(r)
}

// poolRetryAfterCapSeconds bounds the Retry-After we hand a client when the
// pool is empty. See the cooling branch in poolUnavailable for why an honest
// quota-reset deadline is the wrong thing to put on the wire.
const poolRetryAfterCapSeconds = 300

// poolUnavailable explains why Acquire came back empty for this provider, so a
// client sees what is actually wrong (and whether waiting will help) instead of
// an opaque "no upstream credentials available".
//
// The distinction that matters to a caller is transient-vs-not: credentials in
// a cooldown (quota/rate-limit) or a degraded re-probe window come back on
// their own, so the right move is to back off and retry. Hard-failed or
// disabled credentials do not — they need an operator. Saturation is neither:
// the fleet is healthy and simply busy.
//
// Returns the message and a Retry-After hint in seconds (0 = retrying will not
// help by itself).
func (s *Server) poolUnavailable(provider string, allowFallback bool) (string, int) {
	want := auth.NormalizeProvider(provider)
	var total, disabled, hardFailed, cooling, degraded, saturated int
	var soonest time.Time
	now := time.Now()

	for _, st := range s.pool.Status() {
		if auth.NormalizeProvider(st.Auth.Provider) != want {
			continue
		}
		if st.Auth.Kind == auth.KindAPIKey && !allowFallback {
			continue // not eligible for this token; don't describe it
		}
		total++
		if st.Auth.Disabled {
			disabled++
			continue
		}
		a := s.pool.FindByID(st.Auth.ID)
		if a == nil {
			continue
		}
		healthy, hardFailure, _, _ := a.HealthSnapshot()
		switch {
		case hardFailure:
			hardFailed++
		case !st.Auth.QuotaResetAt.IsZero() && st.Auth.QuotaResetAt.After(now):
			cooling++
			if soonest.IsZero() || st.Auth.QuotaResetAt.Before(soonest) {
				soonest = st.Auth.QuotaResetAt
			}
		case !healthy:
			degraded++
		case st.Auth.MaxConcurrent > 0 && st.ActiveClients >= st.Auth.MaxConcurrent:
			saturated++
		}
	}

	switch {
	case total == 0:
		return fmt.Sprintf("no %s credentials are configured on this proxy", want), 0
	case hardFailed+disabled == total:
		return fmt.Sprintf("all %d %s credentials are hard-failed or disabled and need an operator to clear them; retrying will not help",
			total, want), 0
	case cooling > 0 && !soonest.IsZero():
		wait := int(time.Until(soonest).Seconds()) + 1
		if wait < 1 {
			wait = 1
		}
		// The quota reset is the LAST way the pool recovers, not the first: a
		// degraded credential re-probes itself, a saturated one frees a slot, and
		// an operator can add or re-enable an account at any moment. A Codex
		// weekly limit is ~5 days out, and handing that to a client as
		// `Retry-After: 495000` tells it to stop trying for the rest of the week
		// over a pool that is usually back within minutes. Cap the hint; the
		// message still carries the honest reset time for a human to read.
		if wait > poolRetryAfterCapSeconds {
			wait = poolRetryAfterCapSeconds
		}
		return fmt.Sprintf("all %d %s credentials are rate-limited or out of quota; the earliest resets in %s",
			total, want, time.Until(soonest).Round(time.Second)), wait
	case degraded > 0:
		// The credentials are quarantined after upstream errors and will be
		// re-probed automatically (cc-core degradedProbeAfter). Transient.
		return fmt.Sprintf("%d of %d %s credentials are temporarily degraded after upstream errors and are being re-probed; retry shortly",
			degraded, total, want), 30
	case saturated > 0:
		return fmt.Sprintf("all %d %s credentials are at their concurrency limit; too many requests are in flight, retry shortly",
			total, want), 5
	}
	return fmt.Sprintf("no %s credential is currently available to serve this request", want), 15
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
	// "session-id", with a hyphen, is what a real Codex client sends — see
	// mimicry.CodexSessionIDHeader and crack/codexapp0.147.0/SPEC.md §2.1.
	// It must be checked BEFORE the underscore spelling below, which no genuine
	// client emits: Go canonicalizes "Session_id" to itself (an underscore is
	// not a header-name separator), so that branch could never match an inbound
	// "session-id" and every Codex session was landing on the empty slot. One
	// slot for all of a token's concurrent sessions means they share an upstream
	// credential AND, since the slot feeds the session anchor, a single
	// prompt_cache_key — upstream then sees one session carrying several
	// unrelated threads at once, which is a shape no real client produces.
	if v := strings.TrimSpace(c.GetHeader("session-id")); v != "" {
		return v
	}
	if v := strings.TrimSpace(c.GetHeader("Session_id")); v != "" {
		return v
	}
	return ""
}

// RelayPeerName identifies this proxy in the relay headers it stamps. main sets
// it to include the build version; the receiver treats it as informational
// only — trust comes from authenticating the token, never from this string.
var RelayPeerName = "cpa-claude"

// applyRelayIdentity tells a cooperating peer who is actually calling.
//
// Without it, everything this proxy forwards over one API key looks to the peer
// like a single client, so its scheduler pins every one of our users onto one
// of its credentials while the rest of its pool sits idle. With it, each of our
// users presents the slot they would have presented had they connected to the
// peer directly.
//
// Only credentials explicitly marked relay_peer get this. To a vendor or a
// third-party relay our users' identity is not useful, and shipping it would
// leak the shape of our client base into someone else's logs.
func applyRelayIdentity(h http.Header, a *auth.Auth, c *gin.Context, clientToken string) {
	if a == nil || a.Kind != auth.KindAPIKey || !a.RelayPeer {
		return // stripIngressHeaders already cleared anything inbound
	}
	relay.Apply(h, RelayPeerName, clientToken, clientSlotID(c))
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

// deferredResponse is an upstream error response withheld from the client so
// the request can be transparently retried on another credential. The forward
// loop keeps the most recent one; if every healthy credential is exhausted it
// replays this verbatim instead of synthesizing a 503, so the client still
// receives the genuine upstream status (e.g. a 429 with its Retry-After) and
// backs off correctly. nil when nothing was withheld.
type deferredResponse struct {
	status int
	header http.Header
	body   []byte
	authID string
}

// replayDeferred writes a withheld upstream error response to the client,
// honouring the same hop-by-hop header filter as writeResponseHeaders.
func replayDeferred(c *gin.Context, d *deferredResponse) {
	// Same allowlist as writeResponseHeaders. This is the path that replays a
	// withheld upstream 429, so it is the one where dropping the unified
	// rate-limit headers matters most — Retry-After survives (synthesized from
	// their reset timestamps if upstream sent none), which is the part the
	// client actually backs off on.
	downstream.CopyResponseHeaders(c.Writer.Header(), d.header, time.Now())
	c.Writer.WriteHeader(d.status)
	body, _ := downstream.ScrubErrorPayload(d.body)
	_, _ = c.Writer.Write(body)
}

// doForward sends the request with one credential. Returns (retry, done, deferred):
//
//	retry=true   → caller should try another credential. When the retry was
//	               prompted by a credential-level upstream error (429 quota /
//	               rate-limit, 401/403, account ban) the response is withheld
//	               from the client and returned in deferred so the loop can
//	               surface it if no healthy credential remains. A nil deferred
//	               on retry=true means a transport error (nothing received).
//	done=true    → response was delivered to the client (status < 400 or a
//	               non-retryable error already written through).
//
// isRetry is true on the 2nd+ attempt of a request; it suppresses the
// blocking bootstrap-wait gate so a transparent credential switch doesn't
// re-stack the sidecar wait on every alternate credential.
// preflightPrepared is the result forwardWithFailover already validated for
// this credential, or a zero value when it had none (a direct call, or a body
// this function is about to change). It is reused only when the bytes still
// match, so the request that goes out is exactly the one that was checked.
func (s *Server) doForward(c *gin.Context, a *auth.Auth, path string, body []byte, stream bool, model, clientToken, slotID, clientName string, start time.Time, attempts int, isRetry bool, preflightPrepared mimicry.BodyTransformResult) (retry bool, done bool, deferred *deferredResponse) {
	originalBody := body

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

	id := mimicry.SimIdentity{
		AccountKey:  a.AccountKey(),
		AccountUUID: a.AccountUUIDValue(),
		ClientToken: clientToken,
	}

	// The prepared pipeline owns /v1/messages on OAuth: it rebinds the
	// downstream identity to this account, pins the version, repairs the beta
	// vector and cache breakpoints, and refuses to emit a partially rewritten
	// body. The model rewrite and dateline scrub happen inside it, before the
	// body digest is taken — see claude_prepared.go.
	//
	// Other paths (notably /v1/messages/count_tokens) keep the plain header
	// layer: real CC treats count_tokens as its own request class, and the
	// prepared pipeline has no capture backing it there.
	upstreamBody := body
	preparedPath := path == "/v1/messages"
	var prepared mimicry.BodyTransformResult
	if preparedPath {
		if preflightPrepared.IsValid() && bytes.Equal(body, originalBody) {
			// Reuse the preflight result: identical bytes, same credential, and
			// re-preparing would only risk diverging from what was validated.
			prepared = preflightPrepared
		} else {
			var err error
			prepared, err = prepareClaudeOAuthBody(body, model, a, id)
			if err != nil {
				// forwardWithFailover preflights this so it can fall back to an
				// API key. Reaching here means a direct call or a body that
				// changed under us; fail closed rather than forwarding an
				// unbound identity.
				log.Errorf("proxy: prepare Claude request via %s: %v (reason=%s)", a.ID, err, claudePreparationFailureReason(err))
				c.AbortWithStatusJSON(500, gin.H{"error": "the request could not be prepared"})
				return false, true, nil
			}
		}
		upstreamBody = prepared.Body()
	} else if upstreamModel, ok := a.ResolveUpstreamModel(model); ok && upstreamModel != model && upstreamModel != "" {
		// Non-prepared paths (count_tokens) still need the per-credential model
		// map — notably DefaultClaudeOAuthModelMap, which folds retired Opus and
		// Sonnet generations onto the current ones. Skipping it here would send
		// upstream a model name it no longer serves.
		if rewritten, err := rewriteModelField(upstreamBody, upstreamModel); err == nil {
			upstreamBody = rewritten
		} else {
			log.Warnf("proxy: model rewrite (%s -> %s) failed via %s: %v", model, upstreamModel, a.ID, err)
		}
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

	ctx := c.Request.Context()
	// Skip the blocking bootstrap-wait on retries: a transparent credential
	// switch (after a 429/auth error on the previous credential) must not
	// re-stack the sidecar wait for every alternate credential. The bootstrap
	// still fires in the background via Notify above; we just don't gate user
	// traffic on it the second time around.
	if bootstrapReady != nil && !isRetry {
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
		return false, true, nil
	}

	// Forward selected client headers.
	copyForwardableHeaders(c.Request.Header, upReq.Header)
	stripIngressHeaders(upReq.Header)

	// Anthropic auth + Claude Code fingerprint headers. On the prepared path
	// body and headers must come from the SAME opaque result — the apply step
	// re-validates the credential binding and the body digest, so a header set
	// can never be installed over a body it wasn't prepared for.
	if preparedPath {
		if err := applyAnthropicPreparedHeaders(upReq, a, stream, isAnthropicBase, prepared); err != nil {
			log.Errorf("proxy: prepare Claude headers via %s: %v (reason=%s)", a.ID, err, claudePreparationFailureReason(err))
			c.AbortWithStatusJSON(500, gin.H{"error": "the request could not be prepared"})
			return false, true, nil
		}
	} else {
		// Pass the same SimIdentity so X-Claude-Code-Session-Id matches
		// metadata.user_id.session_id.
		applyAnthropicHeaders(upReq, a, stream, isAnthropicBase, id, upstreamBody)
	}

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
			return false, true, nil
		}
		a.MarkFailure(err.Error())
		log.Warnf("proxy: upstream error via %s: %v", a.ID, err)
		return true, false, nil
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
			if normalized, changed := mimicry.NormalizeDateline(retryUpstream); changed {
				retryUpstream = normalized
			}
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
		//
		// retryable marks credential-level failures (this credential is bad
		// right now, but another might serve the request): 429 quota/rate-
		// limit, 401/403 auth rejection, account ban. For these we withhold
		// the response and let forward() transparently switch credentials so
		// the user never sees the error while the pool still has a slot.
		// Request-level faults (generic 4xx, "Extra usage is required") and
		// upstream-wide weather (5xx/529) stay non-retryable.
		retryable := false
		switch {
		case banned:
			a.MarkHardFailure(fmt.Sprintf("account banned (upstream %d)", resp.StatusCode))
			log.Warnf("auth: %s hard-disabled — account ban detected (status %d)", a.ID, resp.StatusCode)
			retryable = true
		case resp.StatusCode == 429 && isLongContextRejection(errBody):
			// Request-level rejection (long context), not a credential
			// problem — no cooldown, no retry (every credential rejects it).
		case resp.StatusCode == 429:
			retryable = true
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
			if resetAt, scope, banned, ok := parseUnifiedRatelimitRejected(resp.Header, model); ok && !banned {
				if scope != "" {
					// Model-scoped rejection (fable's weekly allotment): cool down
					// only this model family so the credential keeps serving every
					// other model instead of being flagged account-wide.
					a.MarkModelRateLimited(scope, resetAt)
					log.Warnf("auth: %s model-scoped limit (%s) — cooldown until %s", a.ID, scope, resetAt.Format(time.RFC3339))
				} else {
					a.MarkUsageLimitReached(resetAt)
					log.Warnf("auth: %s usage limit (unified-ratelimit rejected) — cooldown until %s", a.ID, resetAt.Format(time.RFC3339))
				}
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
			retryable = true
			resetAt := parseRetryAfter(resp.Header)
			s.pool.ReportUpstreamError(a, resp.StatusCode, resetAt)
		case resp.StatusCode == 529, resp.StatusCode >= 500:
			a.MarkFailure(fmt.Sprintf("upstream %d", resp.StatusCode))
		}

		authKind := "oauth"
		if a.Kind == auth.KindAPIKey {
			authKind = "apikey"
		}
		// Break sticky session so the retry (and any future request from this
		// client) can be assigned to a different, hopefully healthy credential.
		s.pool.Unstick(auth.NormalizeProvider(a.Provider), clientToken, slotID)

		if retryable {
			// Withhold the response and signal the caller to switch
			// credentials. We deliberately do NOT emitLog here: the request
			// hasn't reached the client yet, and logging a 429 row for an
			// attempt the user never saw would inflate the error dashboard.
			// The final outcome (success on another credential, or the
			// surfaced error once the pool is exhausted) logs the
			// authoritative row. The journald warn line keeps ops visibility.
			log.Warnf("proxy: %s returned %d — retrying on another credential. body=%s", a.ID, resp.StatusCode, truncate(errBody, 500))
			return true, false, &deferredResponse{
				status: resp.StatusCode,
				header: resp.Header.Clone(),
				body:   errBody,
				authID: a.ID,
			}
		}

		log.Warnf("proxy: %s returned %d — forwarding to client. body=%s", a.ID, resp.StatusCode, truncate(errBody, 500))
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

		writeResponseHeaders(c, resp)
		// A forwarded upstream error still carries the request_id of OUR call.
		scrubbedErr, _ := downstream.ScrubErrorPayload(errBody)
		c.Writer.Write(scrubbedErr)
		return false, true, nil
	}

recoveredFromSignature:
	// Success or non-retryable error — stream response body to client.
	authKind := "oauth"
	if a.Kind == auth.KindAPIKey {
		authKind = "apikey"
	}

	// counts.Requests is left at ZERO here and set only where usage is actually
	// observed (usageJSON.toCounts / mergeSSEUsage). It used to be hard-set to 1
	// on this line, which quietly turned the downstream `counts.Requests > 0`
	// billing gate into a tautology: a 200 that carried no usage at all still
	// passed it, priced out to $0, and was served for free. A 2026-08 audit of
	// the sibling deployment's request log found 3,808 such rows, concentrated
	// in the most expensive models (opus-4-8, fable-5, opus-4-7).
	//
	// The auth-side ledger still counts every served request; see the `ledger`
	// copy below, which restores Requests=1 for that purpose only.
	var counts usage.Counts
	var sub advisor.SubUsage

	// When this credential rewrote the request's model name (relay vendors
	// with vendor-prefixed names), rewrite it back in the response so the
	// client keeps seeing the model it asked for. Claude Code uses the
	// model field on message_start to correlate conversation turns; a
	// vendor-prefixed name breaks multi-turn continuation.
	var rewriteClientModel string
	if upstreamModel, ok := a.ResolveUpstreamModel(model); ok && upstreamModel != model && upstreamModel != "" {
		rewriteClientModel = model
	}

	// The health verdict is computed here and applied only once the body has
	// been fully relayed (markResponseHealth, below). MarkSuccess used to fire
	// on this line — before a single byte of the response had been read — so a
	// 200 whose body was an error envelope, or an SSE stream carrying nothing
	// but `event: error`, or a stream cut off mid-flight, all counted as
	// successes and wiped the credential's failure counters. See
	// responseOutcome in upstream_health.go for why a 200 is not evidence.
	var outcome responseOutcome

	if stream && strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		// Headers commit lazily on the first byte, so a stream that breaks
		// before any output reaches the client can transparently fail over to
		// another credential (the common "RST right after 200" case).
		res := streamSSE(c, resp, &counts, &sub, rewriteClientModel, func() { writeResponseHeaders(c, resp) })
		if !res.sawTerminal && !res.wroteAny {
			_ = resp.Body.Close()
			if isClientDisconnect(c.Request.Context(), res.err) {
				a.MarkClientCancel(errString(res.err))
				s.emitLog(requestlog.Record{
					Client: clientName, ClientToken: maskClientToken(clientToken),
					Provider: auth.NormalizeProvider(a.Provider), AuthID: a.ID, AuthLabel: a.Label,
					AuthKind: authKind, Model: model, Status: 499, Stream: stream, Path: path, Attempts: attempts,
					DurationMs: time.Since(start).Milliseconds(), Error: "client canceled before first event",
				})
				return false, true, nil
			}
			log.Warnf("proxy: stream broke before any output via %s (attempt %d, %s): %v — retrying on another credential",
				a.ID, attempts, time.Since(start).Round(time.Millisecond), res.err)
			return true, false, nil
		}
		outcome.errorPayload = res.sawError
		outcome.detail = res.errDetail
		if !res.sawTerminal {
			outcome.truncated = true
			outcome.clientGone = isClientDisconnect(c.Request.Context(), res.err)
			if outcome.detail == "" {
				outcome.detail = errString(res.err)
			}
			log.Warnf("proxy: SSE truncated mid-stream via %s (attempt %d, events=%d, bytes=%d, %s): %v",
				a.ID, attempts, res.events, res.bytes, time.Since(start).Round(time.Millisecond), res.err)
		}
		if res.sawError {
			log.Warnf("proxy: %s streamed a 200 whose only terminal event was `error` — counting it as a credential failure. frame=%s",
				a.ID, res.errDetail)
		}
	} else {
		writeResponseHeaders(c, resp)
		respBody, _ := io.ReadAll(resp.Body)
		if rewriteClientModel != "" {
			respBody = rewriteResponseModel(respBody, rewriteClientModel)
		}
		c.Writer.Write(respBody)
		counts.Add(extractUsageFromJSON(respBody, &sub))
		if isErr, detail := bodyLooksLikeAPIError(respBody); isErr {
			outcome.errorPayload = true
			outcome.detail = detail
			log.Warnf("proxy: %s returned %d with an error envelope — counting it as a credential failure. body=%s",
				a.ID, resp.StatusCode, detail)
		}
	}
	_ = resp.Body.Close()
	markResponseHealth(a, resp.StatusCode, outcome)

	// Auth-side ledger: every request this credential served counts toward its
	// load, whether or not the upstream reported usage. Requests is restored on
	// a copy so the billing gate below keeps reading the honest signal.
	ledger := counts
	ledger.Requests = 1
	s.usage.Record(a.ID, a.Label, ledger)

	// A 200 that reported no usage at all is an upstream accounting failure, not
	// a free lunch. It cannot be billed — there is no observed quantity to price
	// — but it must be visible rather than looking like an ordinary $0 row.
	if resp.StatusCode < 400 && usage.MissingUsage(counts) {
		log.Warnf("proxy: %s returned %d without usage accounting (model=%s stream=%v) — billing $0",
			a.ID, resp.StatusCode, model, stream)
	}

	// Charge the client for the tokens they actually consumed, priced on the
	// model we actually bought upstream (see billingModelFor) rather than the
	// name the client typed. Display stays on the client-facing name.
	var costUSD float64
	if resp.StatusCode < 400 && counts.Requests > 0 && clientToken != "" {
		costUSD = s.pricing.Cost(auth.NormalizeProvider(a.Provider), billingModelFor(a, model), counts)
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
		multiplier, billedMain = s.saas.SettleCharge(context.WithoutCancel(c.Request.Context()),
			clientToken, auth.NormalizeProvider(a.Provider), model, costUSD,
			apiKeyPriceOverride(a), "request:"+a.ID)
	}
	s.emitLog(requestlog.Record{
		Client:        clientName,
		ClientToken:   maskClientToken(clientToken),
		Provider:      auth.NormalizeProvider(a.Provider),
		AuthID:        a.ID,
		AuthLabel:     a.Label,
		AuthKind:      authKind,
		Model:         model,
		Input:         counts.InputTokens,
		Output:        counts.OutputTokens,
		CacheRead:     counts.CacheReadTokens,
		CacheCreate:   counts.CacheCreateTokens,
		CacheCreate1h: counts.CacheCreate1hTokens,
		CostUSD:       costUSD,
		BilledUSD:     billedMain,
		Multiplier:    multiplier,
		CNYPerUSD:     s.settleCNYRate(),
		Status:        resp.StatusCode,
		DurationMs:    time.Since(start).Milliseconds(),
		Stream:        stream,
		Path:          path,
		Attempts:      attempts,
	})
	return false, true, nil
}

// billingModelFor returns the model name a request should be PRICED on, which
// is not always the name the client asked for.
//
// On an Anthropic OAuth credential, cc-core's DefaultClaudeOAuthModelMap folds
// retired generations onto the current model (claude-opus-4-7 → claude-opus-5,
// claude-sonnet-4-6 → claude-sonnet-5). What we actually buy from Anthropic is
// the resolved model, so that is what we cost. The client keeps seeing the name
// it asked for everywhere else — the response model is rewritten back
// (rewriteClientModel), the request log records the client-facing name, and the
// wallet ledger note quotes it — so this changes the amount, never the label.
// For Opus the two are identical anyway (same price card); for Sonnet the
// resolved name is cheaper until the sonnet-5 introductory rate lapses on
// 2026-08-31, and the customer gets that difference.
//
// API-key credentials are deliberately excluded. Their model_map is a relay
// vendor's naming convention, not a model substitution: it rewrites to names
// like "[0.1]a/claude-sonnet-4-6" that match no price card, so pricing them on
// the upstream name would silently drop every such request onto the provider
// default. Those credentials price on the client name and adjust with
// apiKeyPriceOverride.
func billingModelFor(a *auth.Auth, clientModel string) string {
	if a == nil || a.Kind != auth.KindOAuth {
		return clientModel
	}
	if upstream, ok := a.ResolveUpstreamModel(clientModel); ok && upstream != "" {
		return upstream
	}
	return clientModel
}

// doForwardAnthropicAPIKey is the API-key passthrough for Anthropic-shaped
// upstreams (api.anthropic.com or third-party relays). Unlike the OAuth path,
// we inject no Claude Code mimicry headers and do not use uTLS: the request
// is forwarded essentially verbatim. The only request-side change allowed is
// the per-credential model rewrite (and the matching response-side rewrite)
// so model_map'd relay vendors keep working.
//
// Failure handling is driven by classifyUpstreamStatus, which separates
// faults the client caused from faults the upstream or the credential caused
// (see upstream_health.go):
//
//	faultNone       → MarkSuccess, response forwarded
//	faultCredential → MarkHardFailure, withheld + retried on another credential
//	faultUpstream   → MarkFailure,     withheld + retried on another credential
//	faultClient     → no health change, forwarded verbatim, never retried
//
// Only the upstream-side classes touch health, so one client sending
// malformed requests can no longer degrade a channel that is serving
// everyone else correctly.
//
// Both upstream-side classes feed cc-core's API-key circuit breaker: enough
// consecutive faults pause the channel for a self-expiring, exponentially
// growing interval, so traffic rotates onto another key instead of re-paying
// a doomed round-trip per request, and the channel probes itself back into
// rotation with no operator involvement. A definitive credential rejection
// pauses on the first strike. Neither ever *retires* the channel — only the
// explicit Disabled flag takes an API key offline for good.
//
// A <400 response is additionally checked against the Messages API wire
// format before it is committed or billed (validateAnthropicResponse) — a
// relay answering 200 with an HTML block page is a faultUpstream, not a
// zero-token success.
//
// The (retry, done, deferred) contract matches doForward: a retryable fault
// is withheld and returned in deferred so forwardWithFailover can switch
// credentials transparently, replaying the withheld response only if every
// credential is exhausted. There is no bootstrap-wait gate on this path, so
// it takes no isRetry flag.
func (s *Server) doForwardAnthropicAPIKey(c *gin.Context, a *auth.Auth, path string, body []byte, stream bool, model, clientToken, clientName string, start time.Time, attempts int) (retry bool, done bool, deferred *deferredResponse) {
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
		return false, true, nil
	}
	copyForwardableHeaders(c.Request.Header, upReq.Header)
	stripIngressHeaders(upReq.Header)
	applyRelayIdentity(upReq.Header, a, c, clientToken)
	token, _ := a.Credentials()
	upReq.Header.Set("x-api-key", token)

	client := auth.ClientFor(a.ProxyURL, false)
	resp, err := client.Do(upReq)
	if err != nil {
		// The client hanging up is not a channel fault: every other credential
		// would hit the same dead context and get falsely blamed. Record the
		// cancel hint and stop — no failure, no retry.
		if isClientDisconnect(ctx, err) {
			a.MarkClientCancel(errString(err))
			log.Infof("proxy(apikey): client canceled via %s: %v", a.ID, err)
			s.emitLog(requestlog.Record{
				Client: clientName, ClientToken: maskClientToken(clientToken),
				Provider: auth.NormalizeProvider(a.Provider), AuthID: a.ID, AuthLabel: a.Label, AuthKind: "apikey",
				Model: model, Stream: stream, Path: path, Status: 499,
				DurationMs: time.Since(start).Milliseconds(), Attempts: attempts, Error: "client canceled",
			})
			return false, true, nil
		}
		// Transport/gateway failure. This used to hand the client a bare 502
		// and stop, so a fleet of API keys behaved like a fleet of one: the
		// FIRST key's dead socket ended the request and the remaining keys
		// were never tried. It is exactly the class a retryable 5xx is — the
		// upstream is unreachable, another channel may well be — so roll back
		// to the failover loop, which excludes this credential and tries the
		// next. Nothing has been written downstream yet, so the retry is
		// transparent. Matches the OAuth path and the Codex API-key path.
		log.Warnf("proxy(apikey): upstream transport error via %s: %v — rotating to next credential", a.ID, err)
		a.MarkFailure(fmt.Sprintf("transport: %v", err))
		s.emitLog(requestlog.Record{
			Client: clientName, ClientToken: maskClientToken(clientToken),
			Provider: auth.NormalizeProvider(a.Provider), AuthID: a.ID, AuthLabel: a.Label, AuthKind: "apikey",
			Model: model, Stream: stream, Path: path, Status: 502,
			DurationMs: time.Since(start).Milliseconds(), Attempts: attempts, Error: err.Error(),
			AttemptOnly: true,
		})
		return true, false, nil
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

	// Response-contract check. A <400 status is not on its own evidence that
	// the exchange worked: a dead relay in front of the real API answers 200
	// with an HTML block page, which would otherwise be marked healthy,
	// streamed to the client as an empty response, and billed as zero tokens.
	// Validate the wire format first and demote a violation to faultUpstream
	// so it is withheld and retried like any other upstream failure.
	//
	// Buffer the body so the peek is non-consuming; Close still reaches the
	// original body, so the connection is released exactly as before.
	statusForFault := resp.StatusCode
	var bodyBuf *bufio.Reader
	if resp.StatusCode < 400 {
		bodyBuf = bufio.NewReaderSize(resp.Body, 64*1024)
		resp.Body = struct {
			io.Reader
			io.Closer
		}{bodyBuf, resp.Body}
		if v := validateAnthropicResponse(resp.Header, bodyBuf); v.Detail != "" {
			log.Warnf("proxy(apikey): %s returned %d but the body is not an Anthropic response (%s) — treating as an upstream failure",
				a.ID, resp.StatusCode, truncate([]byte(v.Detail), 300))
			statusForFault = http.StatusBadGateway
		}
	}

	// Credential health bookkeeping + retryability, computed before writing
	// anything so a retryable fault can be withheld and retried on another
	// credential while the pool still has a slot.
	fault := classifyUpstreamStatus(statusForFault)
	switch fault {
	case faultNone:
		// Deliberately NO MarkSuccess here. A <400 status line is written
		// before the model has produced anything, so it is not evidence the
		// exchange worked — the body still has to be inspected. The verdict is
		// applied by markResponseHealth once the body has been relayed.
	case faultCredential, faultUpstream:
		// faultCredential (revoked / forbidden / out of funds) is definitive,
		// so the channel pauses on this single strike rather than
		// re-presenting a dead key on every subsequent request.
		//
		// faultUpstream (throttling, gateway errors, a contract violation) is
		// not a verdict on the key itself, so it takes several in a row — with
		// one exception: a 429 goes through the pool's throttling path so the
		// upstream's own Retry-After is honoured instead of being discarded in
		// favour of a guess. See reportAnthropicAPIKeyFault.
		//
		// Neither is ever sticky for an API key: every pause expires by itself.
		s.reportAnthropicAPIKeyFault(a, statusForFault, parseRetryAfter(resp.Header), "")
	case faultClient:
		// The caller's own request is at fault (400 malformed, 404 route not
		// implemented by this relay, 413 too large, …). Another credential
		// would return the identical error, so forward it through untouched
		// and leave credential health alone.
	}

	if fault.retryable() {
		errBody, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		log.Warnf("proxy(apikey): %s returned %d — retrying on another credential. body=%s", a.ID, resp.StatusCode, truncate(errBody, 500))
		// statusForFault, not resp.StatusCode: a contract violation arrives as
		// 200 but must be replayed to the client as the 502 it really is if
		// every credential ends up exhausted.
		return true, false, &deferredResponse{
			status: statusForFault,
			header: resp.Header.Clone(),
			body:   errBody,
			authID: a.ID,
		}
	}

	writeResponseHeaders(c, resp)
	var counts usage.Counts
	var sub advisor.SubUsage
	var errSnippet string
	// Deferred health verdict for a <400 exchange — see responseOutcome.
	var outcome responseOutcome
	if resp.StatusCode >= 400 {
		// Capture upstream body for the request log + warning. Without
		// this, API-key 4xx is silent — only the gin access line shows
		// up — and operators have no signal whether the relay rejected
		// the model, exhausted the key's quota, IP-banned us, etc.
		errBody, _ := io.ReadAll(resp.Body)
		// The relay's own correlators are no more the caller's than Anthropic's
		// are. errSnippet keeps the unscrubbed text — it goes to our log, which
		// is exactly where an operator needs the upstream id to trace a failure.
		scrubbedErr, _ := downstream.ScrubErrorPayload(errBody)
		c.Writer.Write(scrubbedErr)
		errSnippet = truncate(errBody, 500)
		log.Warnf("proxy(apikey): %s returned %d — body=%s", a.ID, resp.StatusCode, errSnippet)
	} else {
		// counts.Requests stays zero until usage is actually observed — see the
		// OAuth path above for why hard-setting it here made the billing gate a
		// tautology.
		// Dispatch on the client's stream flag + the actual bytes, not the
		// upstream Content-Type alone: relays are known to stream the SSE
		// back as text/plain, which under a header-only check fell through to
		// the whole-body JSON parse and silently lost all usage (billing =
		// $0). Same fix the Codex path already carries. Peek is non-consuming.
		if stream && responseIsSSE(resp.Header, bodyBuf) {
			// Headers are already committed above (the 4xx branch needs them),
			// so commit=nil — no cross-credential retry on this verbatim
			// passthrough path. The relay still adds keepalive + truncation
			// detection so a broken stream is logged, not silently swallowed.
			res := streamSSE(c, resp, &counts, &sub, rewriteClientModel, nil)
			outcome.errorPayload = res.sawError
			outcome.detail = res.errDetail
			if !res.sawTerminal {
				outcome.truncated = true
				outcome.clientGone = isClientDisconnect(c.Request.Context(), res.err)
				if outcome.detail == "" {
					outcome.detail = errString(res.err)
				}
				if !outcome.clientGone {
					log.Warnf("proxy(apikey): SSE truncated mid-stream via %s (events=%d, bytes=%d, %s): %v",
						a.ID, res.events, res.bytes, time.Since(start).Round(time.Millisecond), res.err)
				}
			}
			if res.sawError {
				log.Warnf("proxy(apikey): %s streamed a 200 whose only terminal event was `error` — counting it as a credential failure. frame=%s",
					a.ID, res.errDetail)
			}
		} else {
			respBody, _ := io.ReadAll(resp.Body)
			if rewriteClientModel != "" {
				respBody = rewriteResponseModel(respBody, rewriteClientModel)
			}
			c.Writer.Write(respBody)
			counts.Add(extractUsageFromJSON(respBody, &sub))
			if isErr, detail := bodyLooksLikeAPIError(respBody); isErr {
				outcome.errorPayload = true
				outcome.detail = detail
				log.Warnf("proxy(apikey): %s returned %d with an error envelope — counting it as a credential failure. body=%s",
					a.ID, resp.StatusCode, detail)
			}
		}
	}
	_ = resp.Body.Close()
	markResponseHealth(a, resp.StatusCode, outcome)

	var costUSD float64
	var multiplier, billedMain float64 = 1, 0
	if resp.StatusCode < 400 {
		ledger := counts
		ledger.Requests = 1
		s.usage.Record(a.ID, a.Label, ledger)
		if usage.MissingUsage(counts) {
			log.Warnf("proxy(apikey): %s returned %d without usage accounting (model=%s stream=%v) — billing $0",
				a.ID, resp.StatusCode, model, stream)
		}
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
			multiplier, billedMain = s.saas.SettleCharge(context.WithoutCancel(c.Request.Context()),
				clientToken, auth.NormalizeProvider(a.Provider), model, costUSD,
				apiKeyPriceOverride(a), "request:"+a.ID)
		}
	}
	errField := ""
	if resp.StatusCode >= 400 {
		errField = fmt.Sprintf("upstream %d: %s", resp.StatusCode, truncate([]byte(errSnippet), 200))
	}
	s.emitLog(requestlog.Record{
		Client:        clientName,
		ClientToken:   maskClientToken(clientToken),
		Provider:      auth.NormalizeProvider(a.Provider),
		AuthID:        a.ID,
		AuthLabel:     a.Label,
		AuthKind:      "apikey",
		Model:         model,
		Input:         counts.InputTokens,
		Output:        counts.OutputTokens,
		CacheRead:     counts.CacheReadTokens,
		CacheCreate:   counts.CacheCreateTokens,
		CacheCreate1h: counts.CacheCreate1hTokens,
		CostUSD:       costUSD,
		BilledUSD:     billedMain,
		Multiplier:    multiplier,
		CNYPerUSD:     s.settleCNYRate(),
		Status:        resp.StatusCode,
		DurationMs:    time.Since(start).Milliseconds(),
		Stream:        stream,
		Path:          path,
		Attempts:      attempts,
		Error:         errField,
	})
	return false, true, nil
}

// stripIngressHeaders removes headers that describe the *ingress path* into
// our server before forwarding upstream. Critical when the server sits
// behind Cloudflare Tunnel: cloudflared injects Cdn-Loop: cloudflare plus a
// pile of Cf-* headers, and api.anthropic.com / chatgpt.com are themselves
// behind CF — seeing those headers triggers CF's loop-prevention WAF and
// returns 403 HTML. Prefix match so future CF additions are covered.
func stripIngressHeaders(h http.Header) {
	// A downstream caller must never be able to stamp its own relay identity:
	// these headers are only meaningful when WE assert them, and forwarding a
	// client-supplied one would both forge an identity at the peer and put an
	// unexplained header on requests to Anthropic. Stamping (for credentials
	// that are our own peers) happens after this, in applyRelayIdentity.
	relay.Strip(h)
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

// writeResponseHeaders forwards the upstream response headers the client is
// allowed to see.
//
// The allowlist lives in cc-core/downstream. Copying verbatim used to hand the
// caller our pool's operational state: the twelve anthropic-ratelimit-unified-*
// headers (the serving account's tier, its 5h/7d utilisation, its overage
// status and exact reset timestamps), anthropic-organization-id,
// anthropic-workspace-id, the upstream request-id and cf-ray.
//
// Safe by capture, not by hope: a real third-party gateway returns none of them
// and Claude Code works against it unchanged (cc-core crack/thirdparty/SPEC.md
// §4). Retry-After is preserved, and synthesized from the reset timestamps when
// upstream sent none, so client backoff is unaffected.
//
// Called after the retry loop has already read those headers for scheduling.
func writeResponseHeaders(c *gin.Context, resp *http.Response) {
	downstream.CopyResponseHeaders(c.Writer.Header(), resp.Header, time.Now())
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
// is shared with other forks; this function is the proxy-specific glue
// (model rewrite + usage accumulation + flusher dispatch).
//
// Resilience (mirrors the Codex relay): headers commit lazily via commit() on
// the first downstream byte, so a stream that breaks before any output can be
// retried by the caller on another credential; a synthetic Anthropic `ping`
// event is emitted after >=10s of downstream silence so intermediaries
// (Cloudflare Tunnel / the client idle timeout) don't cut a long stream while
// the model is mid-think or running a server-side advisor sub-call; and the
// terminal event (message_stop) is tracked so a truncated upstream is reported
// instead of looking like a clean end-of-stream.
func streamSSE(c *gin.Context, resp *http.Response, counts *usage.Counts, sub *advisor.SubUsage, rewriteClientModel string, commit func()) sseRelayResult {
	flusher, _ := c.Writer.(http.Flusher)
	sc := ccstream.NewSSEScanner(resp.Body, 64*1024)
	events := 0
	// sawError is tracked separately from sawTerminal because the two answer
	// different questions. `event: error` IS terminal — the stream is over, so
	// the relay must stop — but it is the opposite of a healthy exchange. They
	// were conflated, which meant an SSE body consisting of nothing but an
	// error frame set sawTerminal=true and walked straight into MarkSuccess.
	// A 200 is all the upstream can send once an SSE stream has begun, so the
	// error frame is the ONLY place the failure is visible.
	sawError := false
	errDetail := ""

	// next supplies framing + model-rewrite + usage to the shared relay; the
	// relay (cc-core/stream.Relay) owns keepalive + lazy commit + write locking.
	next := func() (out []byte, terminal bool, err error) {
		if !sc.Scan() {
			if e := sc.Err(); e != nil {
				return nil, false, e
			}
			return nil, false, io.EOF
		}
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
			switch sc.Event() {
			case "message_start", "message_delta":
				mergeSSEUsage(counts, sub, payload)
				events++
			case "message_stop":
				terminal = true
			case "error":
				terminal = true
				sawError = true
				if errDetail == "" {
					errDetail = truncate(payload, 200)
				}
			}
			// Strip the upstream request id out of error frames. Gated on the
			// event name inside cc-core, so the thousands of delta frames in a
			// response cost one string compare and are never parsed.
			if scrubbed, changed := downstream.ScrubSSELine(sc.Event(), outLine); changed {
				outLine = scrubbed
			}
		}
		return outLine, terminal, nil
	}

	// A synthetic `ping` event is exactly what the real Anthropic API sends
	// during gaps, so Claude Code handles it natively.
	r := ccstream.Relay(c.Writer, func() {
		if flusher != nil {
			flusher.Flush()
		}
	}, ccstream.RelayOptions{
		Commit:           commit,
		KeepaliveIdle:    10 * time.Second,
		KeepalivePayload: []byte("event: ping\ndata: {\"type\": \"ping\"}\n\n"),
		Next:             next,
	})
	return sseRelayResult{
		sawTerminal: r.SawTerminal, wroteAny: r.WroteAny, events: events,
		bytes: r.Bytes, err: r.Err, sawError: sawError, errDetail: errDetail,
	}
}

// sseRelayResult reports the outcome of an Anthropic SSE relay so the caller can
// choose between a transparent retry (nothing reached the client yet) and a
// logged give-up (bytes already committed downstream — uninterruptible).
type sseRelayResult struct {
	sawTerminal bool   // message_stop / error event relayed — the stream ended
	sawError    bool   // the terminal event was `error`, i.e. a failed exchange
	errDetail   string // first error frame payload (health note / diagnostics)
	wroteAny    bool   // at least one byte was committed to the client
	events      int    // message_start/_delta events relayed (diagnostics)
	bytes       int64  // bytes written downstream (diagnostics)
	err         error  // underlying read error when the stream broke early
}

// usageJSON is the wire shape of `usage` (and `message.usage`) on /v1/messages.
type usageJSON struct {
	InputTokens              int64                    `json:"input_tokens"`
	OutputTokens             int64                    `json:"output_tokens"`
	CacheCreationInputTokens int64                    `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64                    `json:"cache_read_input_tokens"`
	Iterations               []advisor.IterationUsage `json:"iterations,omitempty"`

	// CacheCreation is Anthropic's per-TTL breakdown of the cache writes that
	// CacheCreationInputTokens totals. Present since the extended-TTL beta; a
	// response that omits it leaves the sub-counts at zero, which the pricing
	// layer reads as "don't distinguish" and bills exactly as before.
	//
	// It matters because mimicry rewrites every cache breakpoint to ttl:"1h"
	// (mimicry/body.go, ClaudeDefaultCacheTTL) and Anthropic prices a 1h write
	// at 2x input against a 5m write's 1.25x. Without this field there is no
	// way to tell after the fact which rate a request should have paid.
	CacheCreation *struct {
		Ephemeral5m int64 `json:"ephemeral_5m_input_tokens"`
		Ephemeral1h int64 `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation,omitempty"`
}

// observed reports whether this payload carried any billable quantity. Used to
// derive Counts.Requests so "we saw usage" stays distinguishable from "the
// request completed" - conflating them served requests free (see the callers).
func (u usageJSON) observed() bool {
	return u.InputTokens > 0 || u.OutputTokens > 0 ||
		u.CacheCreationInputTokens > 0 || u.CacheReadInputTokens > 0
}

func (u usageJSON) toCounts() usage.Counts {
	c := usage.Counts{
		InputTokens:       u.InputTokens,
		OutputTokens:      u.OutputTokens,
		CacheCreateTokens: u.CacheCreationInputTokens,
		CacheReadTokens:   u.CacheReadInputTokens,
	}
	if u.CacheCreation != nil {
		c.CacheCreate1hTokens = u.CacheCreation.Ephemeral1h
	}
	if u.observed() {
		c.Requests = 1
	}
	return c
}

// allowAPIKeyFallback decides whether a request may fall back to upstream API
// keys once the self-run OAuth pool is exhausted. In non-SaaS operator mode
// (billing disabled) the legacy always-fall-back behaviour is kept. In SaaS mode
// it defaults ON for every token and a user may opt out per-token via the
// self-service Wallet setting (clienttoken.Token.UpstreamFallbackEnabled, nil =
// default on).
//
// What the opt-out means is narrower than it looks, and deliberately so: it says
// "don't serve me from a channel that charges me MORE than I pay now", not
// "leave me with nothing". Falling back to a channel billed at the user's own
// group rate costs them exactly what the OAuth pool costs them, so refusing it
// buys them nothing and hands them a 503 instead — which is precisely what
// happened on 2026-08-12, when two users who had opted out (to avoid the 6×
// anthropic relays) also lost the Codex relay that bills at parity, and took
// 375 of the day's 494 Codex 503s between them.
//
// So an opted-out token still falls back whenever no reachable API key for this
// provider would cost it more. With the switch ON nothing changes: every
// channel, marked-up ones included, stays available.
func (s *Server) allowAPIKeyFallback(ctx context.Context, provider, clientToken string) bool {
	if s.billing == nil {
		return true
	}
	tok, ok := s.tokens.Lookup(clientToken)
	if !ok {
		// Unknown token never reaches here in practice (auth gate runs first);
		// be conservative and allow, matching the default-on policy.
		return true
	}
	if tok.UpstreamFallbackEnabled() {
		return true
	}
	return !s.apiKeyFallbackWouldCostMore(ctx, provider, clientToken)
}

// apiKeyFallbackWouldCostMore reports whether any API-key credential the pool
// could pick for this provider would bill the user above their own group rate.
//
// It asks about the whole reachable set rather than one credential because the
// gate the answer feeds (auth.AcquireOptions.AllowAPIKeyFallback) is a single
// bool: the pool picks the key itself, so "cheap enough" has to hold for every
// key it might land on. Disabled keys are skipped — they can never be picked —
// and an unreadable wallet answers true, because the safe direction when the
// price is unknown is to honour the user's opt-out rather than spend their money.
func (s *Server) apiKeyFallbackWouldCostMore(ctx context.Context, provider, clientToken string) bool {
	if s.saas == nil {
		return false // no billing at all: nothing can cost more
	}
	groupMult, promoActive, ok := s.saas.GroupRate(ctx, clientToken, provider)
	if !ok {
		return true
	}
	if promoActive {
		// A promotion outranks per-key overrides, so every channel bills the
		// same and none of them can be the expensive one.
		return false
	}
	want := auth.NormalizeProvider(provider)
	for _, st := range s.pool.Status() {
		if auth.NormalizeProvider(st.Auth.Provider) != want || st.Auth.Kind != auth.KindAPIKey || st.Auth.Disabled {
			continue
		}
		live := s.pool.FindByID(st.Auth.ID)
		if live == nil {
			continue
		}
		// PriceMultiplierValue() == 0 means "no override" — the key bills at the
		// user's own group rate, i.e. parity with the OAuth pool.
		if m := live.PriceMultiplierValue(); m > 0 && m > groupMult {
			return true
		}
	}
	return false
}

// apiKeyPriceOverride returns the per-credential billing multiplier to pass to
// SettleCharge: the API key's PriceMultiplier when it's an API-key credential
// with one set, else 0 (meaning "use the client's pricing-group multiplier").
// OAuth credentials never override — their cheap subscription rate is exactly
// what the group discount models.
func apiKeyPriceOverride(a *auth.Auth) float64 {
	if a != nil && a.Kind == auth.KindAPIKey {
		return a.PriceMultiplierValue()
	}
	return 0
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
				apiKeyPriceOverride(a), "advisor:"+a.ID)
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
			CNYPerUSD:   s.settleCNYRate(),
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
	if u.CacheCreation != nil && u.CacheCreation.Ephemeral1h > 0 {
		dst.CacheCreate1hTokens = u.CacheCreation.Ephemeral1h
	}
	// Requests marks "usage was observed at least once in this stream", so it
	// latches on and is never cleared by a later event that omits the fields.
	if u.observed() {
		dst.Requests = 1
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

// parseUnifiedRatelimitRejected inspects Anthropic's `anthropic-ratelimit-
// unified-*` headers and classifies a quota rejection.
//
// Real responses carry a snapshot like:
//
//	anthropic-ratelimit-unified-status: rejected           ← top-level decision
//	anthropic-ratelimit-unified-5h-status: allowed         ← shared 5h window
//	anthropic-ratelimit-unified-7d-status: allowed         ← shared 7d window
//	anthropic-ratelimit-unified-5h-reset / -7d-reset / -reset
//	anthropic-ratelimit-unified-representative-claim: five_hour
//
// The shared 5h/7d buckets are account-wide — every model draws on them. There
// is NO per-model bucket header: a model-scoped window (e.g. fable's weekly
// allotment, ~50% of weekly per unified-fallback-percentage) surfaces only in
// the oauth/usage `limits[]` body, never in headers. So a scoped rejection is
// inferred from the request MODEL — when the top level is rejected but neither
// shared bucket is, and the failing request is for a model with its own scope
// (fable), the cooldown is scoped to that family instead of the whole account.
//
// reqModel is the client-requested model driving this request.
//
// Returns:
//   - ok=false: not rejected.
//   - scope != "": model-family-scoped cooldown (never banned) — caller must
//     use MarkModelRateLimited so the credential keeps serving other models.
//   - scope == "", banned=false: account-wide cooldown until resetAt.
//   - scope == "", banned=true: rejected with no usable FUTURE reset stamp —
//     the stealth-ban signature (a banned account stays "rejected" forever with
//     no recovery time). Caller escalates.
func parseUnifiedRatelimitRejected(h http.Header, reqModel string) (resetAt time.Time, scope string, banned bool, ok bool) {
	const statusPrefix = "rejected"
	isRejected := func(headerName string) bool {
		v := strings.ToLower(strings.TrimSpace(h.Get(headerName)))
		return v != "" && strings.HasPrefix(v, statusPrefix)
	}

	// Shared 5h/7d buckets — a rejection here is account-wide.
	sharedBuckets := []struct{ statusHdr, resetHdr string }{
		{"Anthropic-Ratelimit-Unified-5h-Status", "Anthropic-Ratelimit-Unified-5h-Reset"},
		{"Anthropic-Ratelimit-Unified-7d-Status", "Anthropic-Ratelimit-Unified-7d-Reset"},
	}
	sharedRejected := false
	var sharedReset time.Time
	for _, b := range sharedBuckets {
		if !isRejected(b.statusHdr) {
			continue
		}
		sharedRejected = true
		if t, parsed := parseUnixSecondsHeader(h.Get(b.resetHdr)); parsed && t.After(sharedReset) {
			sharedReset = t
		}
	}

	topRejected := isRejected("Anthropic-Ratelimit-Unified-Status")
	if !sharedRejected && !topRejected {
		return time.Time{}, "", false, false
	}

	now := time.Now()
	topReset, topOK := parseUnixSecondsHeader(h.Get("Anthropic-Ratelimit-Unified-Reset"))

	// Model-scoped rejection: top-level rejected, shared buckets fine, and the
	// request is for a model with its own quota scope. Cool down only that model
	// family — never ban the whole credential. Fall back to a modest re-probe
	// window if no reset stamp is present.
	if !sharedRejected {
		if s := auth.AnthropicModelScope(reqModel); s != "" {
			if topOK && topReset.After(now) {
				resetAt = clampReset(topReset)
			} else {
				resetAt = now.Add(time.Hour)
			}
			return resetAt, s, false, true
		}
	}

	// Account-wide. Prefer the top-level reset, else the latest shared bucket
	// reset — a 7d rejection isn't released by the (sooner) 5h reset.
	if topOK && topReset.After(now) {
		return clampReset(topReset), "", false, true
	}
	if !sharedReset.IsZero() && sharedReset.After(now) {
		return clampReset(sharedReset), "", false, true
	}
	// Rejected with no future reset — the stealth-ban signature.
	return time.Time{}, "", true, true
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

// isLongContextRejection reports whether a 429 body is the per-request
// "this prompt is too long for your subscription's context window" rejection
// rather than a credential-level quota/rate problem. These fire when a request
// exceeds the standard 200K context and would need usage-based billing
// ("extra usage"/credits) that subscription accounts don't have — so EVERY
// credential rejects the identical request. It must not cool down the
// credential or trigger a cross-pool retry (which would flag the whole pool
// unavailable for one oversized request). Anthropic has shipped the message
// under at least two wordings, hence the multi-marker match. Case-insensitive.
func isLongContextRejection(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	lower := bytes.ToLower(b)
	markers := [][]byte{
		[]byte("extra usage is required"),                     // older wording
		[]byte("usage credits are required for long context"), // current wording
		[]byte("long context request"),                        // defensive: copy tweaks
	}
	for _, m := range markers {
		if bytes.Contains(lower, m) {
			return true
		}
	}
	return false
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
