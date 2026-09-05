package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	gorillaws "github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"

	"github.com/wjsoj/cc-core/auth"
	"github.com/wjsoj/cc-core/codexerr"
	"github.com/wjsoj/cc-core/codexws"
	"github.com/wjsoj/cc-core/downstream"
	"github.com/wjsoj/cc-core/mimicry"
	"github.com/wjsoj/cc-core/pricing"
	"github.com/wjsoj/cc-core/relay"
	"github.com/wjsoj/cc-core/requestlog"
	"github.com/wjsoj/cc-core/servicetier"
	"github.com/wjsoj/cc-core/usage"
)

// Codex WebSocket ingress. Real codex-tui 0.135.0 streams a turn over a
// WebSocket; a long-lived WS carries protocol-level ping/pong, so it survives
// the silent gaps (reasoning -> answer, tool thinking) that truncate the HTTP
// SSE path and surface to clients as "stream disconnected before completion".
//
// This is a passthrough relay: the client already speaks the Codex WS protocol,
// so frames are forwarded verbatim between client and upstream. We only parse
// out, best-effort: the model (for credential acquisition + billing),
// previous_response_id (for the cross-group safety boundary, see codex_session.go),
// the response id (to bind a conversation to its account), and usage (for
// billing, carried inside the terminal event). The whole path is opt-in
// (config.codex_ws.enabled) and unverified against a real ChatGPT token — see
// CLAUDE.md's Codex-OAuth caveat.

const (
	codexWSFirstFrameTimeout = 30 * time.Second
	codexWSUpstreamPingEvery = 20 * time.Second
	codexWSReadDeadline      = 15 * time.Minute
	codexWSWriteDeadline     = 2 * time.Minute
	// codexWSMaxAcquire bounds dial-time credential switches. Once the first
	// upstream frame is relayed to the client the credential is locked (no
	// silent switch is possible after bytes are committed to the client).
	codexWSMaxAcquire = 4
	// codexWSBillQueue is the per-session buffer of completed turns awaiting
	// asynchronous settlement. Deep enough that a slow SaaS write never
	// back-pressures the relay in practice; a full queue falls back to inline
	// billing rather than dropping a charge.
	codexWSBillQueue = 64
)

// codexWSTurnBill is one completed WS turn queued for asynchronous settlement.
type codexWSTurnBill struct {
	meta servicetier.Turn
	turn usage.Counts
	dur  time.Duration
}

var codexWSUpgrader = gorillaws.Upgrader{
	ReadBufferSize:    4096,
	WriteBufferSize:   4096,
	EnableCompression: true,
	// The bearer token already authenticated the request (clientAuth middleware
	// ran before this handler); the WS Origin header is not a security boundary
	// for a token-authenticated API, so accept any origin.
	CheckOrigin: func(*http.Request) bool { return true },
}

func isCodexWSUpgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket") &&
		strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade")
}

// handleCodexResponsesWS upgrades a /v1/responses GET into a WebSocket and
// bridges it to the ChatGPT Codex backend over an upstream WebSocket dialed with
// the cc-core uTLS fingerprint.
func (s *Server) handleCodexResponsesWS(c *gin.Context) {
	if !isCodexWSUpgrade(c.Request) {
		c.AbortWithStatusJSON(http.StatusUpgradeRequired, gin.H{"error": "WebSocket upgrade required (Upgrade: websocket)"})
		return
	}
	const provider = auth.ProviderOpenAI
	start := time.Now()

	clientTokV, _ := c.Get("client_token")
	clientToken, _ := clientTokV.(string)
	if clientToken == "" {
		clientToken = c.ClientIP()
	}
	clientNameV, _ := c.Get("client_name")
	clientName, _ := clientNameV.(string)

	clientEntry, _ := s.tokens.Lookup(clientToken)
	clientGroup := clientEntry.Group
	if !clientEntry.AllowsProvider(provider) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "this token is not permitted to use the " + provider + " endpoint"})
		return
	}

	// Pre-flight gates — same single funnel as forward(): SaaS balance, then
	// per-(provider|token) RPM and concurrency. These can still answer with an
	// HTTP status because the WS handshake has not happened yet.
	if s.saas != nil && clientToken != "" {
		bal, err := s.saas.PrecheckBalance(c.Request.Context(), clientToken)
		if err != nil {
			// Fail open — see the same branch in forward().
			log.Errorf("saas: wallet pre-check failed for %s on the WS path, serving anyway: %v", maskClientToken(clientToken), err)
		} else if bal <= 0 {
			c.Header("Retry-After", "60")
			c.AbortWithStatusJSON(402, gin.H{"error": "insufficient balance", "balance_usd": bal})
			return
		}
	}

	rpmKey := auth.NormalizeProvider(provider) + "|" + clientToken
	if limit := s.clientRPM(clientToken); limit > 0 {
		if m := s.cfg.CodexConcurrencyMultiplier; m > 0 {
			limit *= m
		}
		if ok, retry := s.rpm.Allow(rpmKey, limit); !ok {
			c.Header("Retry-After", strconv.Itoa(retry))
			c.AbortWithStatusJSON(429, gin.H{"error": "rate limit exceeded", "retry_after": retry})
			return
		}
	}
	maxConc := s.clientMaxConcurrent(clientToken)
	if maxConc > 0 {
		if m := s.cfg.CodexConcurrencyMultiplier; m > 0 {
			maxConc *= m
		}
		inflightKey := auth.NormalizeProvider(provider) + "|" + clientToken
		cur, releaseSlot := s.inflight.Begin(inflightKey)
		defer releaseSlot()
		//nolint:gosec // G115: maxConc is an operator-set concurrency limit (small positive int), not attacker-controlled.
		if cur > int32(maxConc) {
			c.Header("Retry-After", "5")
			c.AbortWithStatusJSON(429, gin.H{"error": "too many concurrent requests", "max_concurrent": maxConc})
			return
		}
	}

	slotID := clientSlotID(c)

	// Fair-share gate on pool slots. A WS session holds its slot for the whole
	// life of the socket (chatgpt.com keeps these open up to an hour), unlike an
	// HTTP request which holds one for seconds — so without this a couple of
	// heavy WS users sit on most of the provider's slot capacity and everyone
	// else gets 503s from a healthy fleet. Refuse only slots this token does not
	// already hold, so an established session is never torn down. Checked before
	// the upgrade, while we can still answer with an HTTP status the client
	// surfaces to its user.
	if maxSess := s.cfg.ClientMaxSessions; maxSess > 0 && clientToken != "" {
		if held, already := s.pool.SessionsHeld(provider, clientToken, slotID); !already && held >= maxSess {
			log.Warnf("codex ws: token %s at its session cap (%d held, max %d) — refusing a new session",
				maskClientToken(clientToken), held, maxSess)
			c.Header("Retry-After", "30")
			c.AbortWithStatusJSON(429, gin.H{
				"error": fmt.Sprintf("you already have %d concurrent %s sessions open (the limit is %d); "+
					"close an idle Codex window and retry — long-lived sessions hold an upstream slot for up to an hour",
					held, auth.NormalizeProvider(provider), maxSess),
				"held":         held,
				"max_sessions": maxSess,
			})
			return
		}
	}

	// Upgrade the client connection. Past this point no HTTP status can be sent;
	// failures close the WS with a control frame.
	clientConn, err := codexWSUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Warnf("codex ws: client upgrade failed: %v", err)
		return
	}
	defer clientConn.Close()
	clientConn.SetReadLimit(s.cfg.CodexWS.ReadLimitBytes)

	// First client frame (response.create) — learn model + previous_response_id
	// before acquiring a credential.
	_ = clientConn.SetReadDeadline(time.Now().Add(codexWSFirstFrameTimeout))
	mt, firstFrame, err := clientConn.ReadMessage()
	if err != nil || mt != gorillaws.TextMessage {
		closeCodexWS(clientConn, gorillaws.CloseProtocolError, "expected initial JSON frame")
		return
	}
	_ = clientConn.SetReadDeadline(time.Time{})

	// The model is read off the first frame for routing, billing and log rows.
	//
	// The current CLI also puts this model and the first frame's tier in
	// its handshake routing hint. Billing still tracks EVERY turn separately.
	model := codexWSExtractModel(firstFrame)
	if model == "" {
		model = "unknown"
	}

	// Cross-group previous_response_id safety: if the chain belongs to this
	// group's sticky account, keep it; otherwise strip it so the upstream
	// rebuilds from full input (prevents replaying tenant A's chain on B).
	if prevID := codexPreviousResponseID(firstFrame); prevID != "" {
		if _, ok := s.codexRespAccount.Get(clientGroup, prevID); !ok {
			firstFrame = removeCodexPreviousResponseID(firstFrame)
			log.Infof("codex ws: stripped cross-group previous_response_id (group=%q)", clientGroup)
		}
	}

	betaValue := codexws.CodexOpenAIBetaWS
	if s.cfg.CodexWS.BetaVersion == "v1" {
		betaValue = codexws.CodexOpenAIBetaWSV1
	}
	// Acquire a credential, retrying dial-time failures on another one. OAuth
	// dials chatgpt.com directly; a relay-peer API key dials the peer's own WS
	// ingress (see codexWSDialTarget), so an exhausted OAuth fleet degrades to
	// the relay instead of dropping the session.
	allowFallback := s.allowAPIKeyFallback(c.Request.Context(), provider, clientToken)
	tried := map[string]bool{}
	var up codexws.Conn
	var a *auth.Auth
	var ident mimicry.CodexFrameIdentity
	for i := 0; i < codexWSMaxAcquire; i++ {
		exclude := make([]string, 0, len(tried))
		for id := range tried {
			exclude = append(exclude, id)
		}
		cand := s.pool.AcquireWithOptions(c.Request.Context(), provider, clientToken, clientGroup, model, slotID, auth.AcquireOptions{
			AllowAPIKeyFallback: allowFallback,
			ExcludeIDs:          exclude,
		})
		if cand == nil {
			break
		}
		tried[cand.ID] = true
		// Resolve the upstream identity for THIS credential. The anchor is
		// server-side: the account key pins it to the credential (so a
		// credential switch correctly starts a new upstream session), and the
		// client token plus slot separate concurrent downstream conversations.
		// slotID is deliberately not used as the session id itself — it comes
		// from a downstream header, and the session id becomes our upstream
		// prompt_cache_key, so a caller able to choose it could aim at another
		// tenant's cached prefix.
		candIdent := s.codexSessions.Identity(cand.AccountKey(), cand.AccountKey()+"|"+clientToken+"|"+slotID)
		target, ok := s.codexWSDialTarget(cand, clientToken, slotID, betaValue, &candIdent)
		if !ok {
			// A plain vendor API key speaks HTTP only — nothing to dial.
			s.pool.Release(provider, clientToken, slotID)
			continue
		}
		if hint := mimicry.CodexRoutingHint(model, servicetier.Request(firstFrame)); hint != "" {
			target.Header[mimicry.CodexRoutingHintHeader] = []string{hint}
		}
		conn, resp, derr := codexws.Dial(c.Request.Context(), target)
		// On a non-101 the body carries the upstream error; on success gorilla
		// hands back a NopCloser over leftover bytes (the live conn lives on
		// `conn`, not resp.Body), so closing here is safe either way. Headers
		// stay readable after the body is closed.
		var status int
		var retryAfter time.Time
		if resp != nil {
			status = resp.StatusCode
			retryAfter = parseRetryAfter(resp.Header)
			if resp.Body != nil {
				_ = resp.Body.Close()
			}
		}
		if derr != nil {
			// derr may embed an unparsed upstream response (e.g. an HTTP/2 SETTINGS
			// frame when ALPN mis-negotiates), which gorilla renders as a long
			// \x-escaped string. Cap it so a binary reply can't dump a screenful.
			log.Warnf("codex ws: upstream dial via %s failed (status=%d): %s", cand.ID, status, truncate([]byte(derr.Error()), 200))
			if status == http.StatusUnauthorized && cand.Kind == auth.KindOAuth {
				// Same treatment as the HTTP path (codex_auth_reject.go): the
				// handshake body carries the backend's rejection of the bearer
				// this dial presented.
				seen := strings.TrimPrefix(target.Header.Get("Authorization"), "Bearer ")
				s.rejectCodexBearer(cand, seen, []byte(derr.Error()))
				s.pool.Unstick(provider, clientToken, slotID)
			} else if s.reportCodexWSDialFault(c.Request.Context(), cand, status, retryAfter, derr) {
				s.pool.Unstick(provider, clientToken, slotID)
			}
			s.pool.Release(provider, clientToken, slotID)
			continue
		}
		a = cand
		up = conn
		ident = candIdent
		break
	}
	if up == nil || a == nil {
		// A close frame is all we have left — the upgrade is long done — so the
		// reason has to carry the actionable part itself. Keep it under
		// gorilla's 123-byte control-frame limit.
		reason, logErr := "no upstream credential available", "ws: no upstream credential"
		if !allowFallback && s.pool.HasAPIKeyFor(provider, clientGroup, model) {
			reason = "no upstream credential at your rate; enable the upstream fallback in wallet settings to use pricier ones"
			logErr = "ws: no upstream credential (token opted out of the pricier API-key channels still available)"
		}
		closeCodexWS(clientConn, gorillaws.CloseTryAgainLater, reason)
		s.emitLog(requestlog.Record{
			Client: clientName, ClientToken: maskClientToken(clientToken), Provider: provider, Model: model,
			Stream: true, Path: "/v1/responses", Status: 503, DurationMs: time.Since(start).Milliseconds(),
			Error: logErr,
		})
		return
	}
	defer up.Close()
	defer s.pool.Release(provider, clientToken, slotID)

	// Relay the first frame upstream, then run the bidirectional pump.
	firstFrame, _, err = servicetier.NormalizeRequest(firstFrame)
	if err != nil {
		closeCodexWS(clientConn, gorillaws.ClosePolicyViolation, "invalid service tier")
		return
	}
	firstFrame = rebindCodexFrame(firstFrame, ident, a)
	tierTracker := &servicetier.TurnTracker{}
	if !tierTracker.Sent(firstFrame) {
		closeCodexWS(clientConn, gorillaws.ClosePolicyViolation, "invalid turn metadata")
		return
	}
	_ = up.SetWriteDeadline(time.Now().Add(codexWSWriteDeadline))
	if err := up.WriteMessage(codexws.TextMessage, firstFrame); err != nil {
		log.Warnf("codex ws: first upstream write via %s failed: %v", a.ID, err)
		closeCodexWS(clientConn, gorillaws.CloseInternalServerErr, "upstream write failed")
		return
	}

	var counts usage.Counts
	// Bill each turn as it completes, not once at the end: a WS session can run
	// for an hour and hundreds of turns, and deferring settlement to the close
	// makes the credential's cost lag its real upstream usage (the quota % ticks
	// up live while total cost sits still) and loses the whole session's billing
	// outright if the process restarts mid-stream.
	//
	// Settlement is asynchronous (matches sub2api): a single per-session goroutine
	// drains a buffered channel and runs the billing funnel (pricing + SaaS DB
	// write + request log) off the relay's hot path, so a slow SettleCharge never
	// stalls the next turn's forwarding. The channel is drained (not abandoned) on
	// close, so a normal disconnect loses nothing; only an outright process crash
	// can drop turns still queued — the same trade sub2api's worker pool makes.
	billCh := make(chan codexWSTurnBill, codexWSBillQueue)
	var billWG sync.WaitGroup
	billWG.Add(1)
	go func() {
		defer billWG.Done()
		for tb := range billCh {
			s.billCodexWSTurn(c, a, codexBillingTurnModel(model, tb.meta), clientToken, clientName, tb.turn, tb.dur, pricing.CostOptions{ServiceTier: tb.meta.Requested, ResponseServiceTier: tb.meta.Observed})
		}
	}()
	billTurn := func(turn usage.Counts, dur time.Duration) {
		tb := codexWSTurnBill{meta: tierTracker.LastCompleted(), turn: turn, dur: dur}
		select {
		case billCh <- tb:
		default:
			// Queue full (billing lagging behind a very bursty session): settle
			// inline rather than drop the charge. Rare; bounds memory too.
			s.billCodexWSTurn(c, a, codexBillingTurnModel(model, tb.meta), clientToken, clientName, tb.turn, tb.dur, pricing.CostOptions{ServiceTier: tb.meta.Requested, ResponseServiceTier: tb.meta.Observed})
		}
	}
	// An account-scoped shed (quota / rate limit — NOT capacity, see
	// codexerr.ClientFrame) says the credential we are pinned to is a bad bet for
	// the next dial too. The socket can't be re-homed mid-session, so the move
	// available is to break the sticky assignment: whatever the CLI retries onto
	// lands elsewhere. Health is deliberately untouched — the pool's own quota
	// bookkeeping already handles the credential; cooling it here would take its
	// other models offline too. Fires once per session.
	var unstuck sync.Once
	onShed := func() {
		unstuck.Do(func() { s.pool.Unstick(provider, clientToken, slotID) })
	}
	s.pumpCodexWS(c.Request.Context(), clientConn, up, a, clientGroup, ident, &counts, billTurn, onShed, tierTracker)
	close(billCh)
	billWG.Wait() // drain every queued turn before the request returns

	// The per-turn path already settled cost + client billing + request log for
	// every completed turn. Here we only fold the session's full token totals
	// into the auth ledger (drives load-balancing weight + the credential's
	// token display); cost is intentionally zero to avoid double-charging.
	if counts.InputTokens > 0 || counts.OutputTokens > 0 || counts.CacheReadTokens > 0 || counts.CacheCreateTokens > 0 {
		s.usage.Record(a.ID, a.Label, counts)
	}
	if counts.Requests > 0 {
		a.MarkSuccess()
	}
}

// pumpCodexWS relays frames between the client and upstream WebSockets until
// either side closes. Usage and response-id binding are extracted from the
// upstream->client direction; the cross-group previous_response_id rewrite is
// applied on the client->upstream direction for follow-up turns. Both relay
// goroutines are joined before returning so counts is safe for billing.
// pumpCodexWS runs the bidirectional relay. counts accumulates the whole
// session's tokens (used for the auth's token ledger once the socket closes).
// onTurn is invoked once per completed turn (each terminal event) with just
// that turn's usage, so cost/client-billing/request-log settle in real time
// instead of being deferred to the end of a session that may last an hour or
// die mid-flight — see the caller.
// onShed is invoked when upstream sheds a turn for capacity/quota inside the
// otherwise-healthy socket (see the capacity handling in the relay below).
func (s *Server) pumpCodexWS(ctx context.Context, client *gorillaws.Conn, up codexws.Conn, a *auth.Auth, group string, ident mimicry.CodexFrameIdentity, counts *usage.Counts, onTurn func(turn usage.Counts, dur time.Duration), onShed func(), tierTrackers ...*servicetier.TurnTracker) {
	var tierTracker *servicetier.TurnTracker
	if len(tierTrackers) > 0 {
		tierTracker = tierTrackers[0]
	}
	done := make(chan struct{})
	var once sync.Once
	// stop tears the session down. closeCode/closeReason describe why, so the
	// client gets a real WebSocket Close frame instead of a bare TCP close.
	//
	// A bare Close() is what codex-tui reports as "websocket closed by server
	// before response completed": mid-turn the socket simply dies, with no frame
	// to say whether the server crashed, the upstream hiccuped, or the turn
	// finished. Sending a close frame first lets the CLI distinguish a normal end
	// (NormalClosure) from a retryable upstream drop (TryAgainLater).
	stop := func(code int, reason string) {
		once.Do(func() {
			close(done)
			_ = up.Close()
			closeCodexWS(client, code, reason)
			_ = client.Close()
		})
	}

	var wg sync.WaitGroup
	wg.Add(2)

	// upstream -> client
	go func() {
		defer wg.Done()
		// billed tracks the token totals already settled via onTurn, so each
		// terminal event bills only its own turn's delta. turnStart bounds the
		// per-turn duration reported to the request log.
		var billed usage.Counts
		turnStart := time.Now()
		// midTurn is true once upstream has sent anything for a turn that hasn't
		// reached its terminal event — i.e. exactly the window in which a drop
		// leaves the client with a half-written response.
		midTurn := false
		for {
			_ = up.SetReadDeadline(time.Now().Add(codexWSReadDeadline))
			mt, data, err := up.ReadMessage()
			if err != nil {
				// Upstream ended the session. A clean close mid-turn is still a
				// truncated response from the client's point of view, so both are
				// logged; only an unexpected drop is surfaced as retryable.
				if codexWSNormalClose(err) && !midTurn {
					stop(gorillaws.CloseNormalClosure, "upstream closed")
					return
				}
				// This used to `return` silently, which is why a user-visible
				// "closed before response completed" left no trace in the logs at
				// all. Always record it, with the credential that was serving.
				log.Warnf("codex ws: upstream read via %s ended after %s (midTurn=%t): %v",
					a.ID, time.Since(turnStart).Round(time.Millisecond), midTurn, err)
				stop(gorillaws.CloseTryAgainLater, "upstream connection lost")
				return
			}
			// out is what the client sees; data stays the upstream original so
			// classification, usage extraction and terminal detection all read
			// what upstream actually said.
			out := data
			if mt == codexws.TextMessage && len(data) > 0 {
				tierTracker.Observe(data)
				if rid := codexResponseID(data); rid != "" {
					s.codexRespAccount.Bind(group, rid, a.ID)
				}
				counts.Add(extractCodexBackendUsageFromJSON(data))

				var shed, capacity bool
				if out, shed, capacity = codexerr.ClientFrame(data); shed {
					log.Warnf("codex ws: %s shed a turn (capacity=%t, midTurn=%t): %s",
						a.ID, capacity, midTurn, truncate(data, 200))
					if !capacity && onShed != nil {
						onShed()
					}
				}

				if codexTerminalEvent(data) {
					tierTracker.Complete()
					counts.Requests++
					midTurn = false
					if onTurn != nil {
						onTurn(codexTurnDelta(*counts, billed), time.Since(turnStart))
						billed = *counts
						billed.Requests = 0 // Requests isn't part of the token delta
						turnStart = time.Now()
					}
				} else {
					midTurn = true
				}

				// Withhold the pool's operational state, LAST — after usage
				// extraction, response-id binding and per-turn billing, all of
				// which read `data` (what upstream actually said) rather than
				// `out` (what the client gets). Placing it earlier would make a
				// dropped frame skip settlement; today none of the dropped types
				// is terminal, but that is not a property worth depending on.
				var keep bool
				if out, keep = downstream.ScrubCodexEvent(out); !keep {
					continue
				}
			}
			_ = client.SetWriteDeadline(time.Now().Add(codexWSWriteDeadline))
			// Echo the frame's own type. Only text frames go through the
			// scrub above, so relabelling a binary frame as text would forward
			// bytes the client cannot parse AND that were never inspected.
			if err := client.WriteMessage(mt, out); err != nil {
				// The client is gone; there is nobody left to send a frame to.
				log.Infof("codex ws: client write via %s failed (midTurn=%t): %v", a.ID, midTurn, err)
				stop(gorillaws.CloseNormalClosure, "")
				return
			}
		}
	}()

	// client -> upstream
	go func() {
		defer wg.Done()
		for {
			mt, data, err := client.ReadMessage()
			if err != nil {
				// Client hung up — the ordinary way a session ends. Nothing to
				// report to it; just tear the upstream leg down.
				stop(gorillaws.CloseNormalClosure, "")
				return
			}
			if mt == gorillaws.TextMessage {
				if prevID := codexPreviousResponseID(data); prevID != "" {
					if _, ok := s.codexRespAccount.Get(group, prevID); !ok {
						data = removeCodexPreviousResponseID(data)
					}
				}
				// Every turn after the first goes through here too. Skipping it
				// would leak the downstream client's identity from turn two
				// onward, which is the same disclosure as leaking it on turn one.
				if codexWSFrameIsResponseCreate(data) {
					var tierErr error
					data, _, tierErr = servicetier.NormalizeRequest(data)
					if tierErr != nil {
						stop(gorillaws.ClosePolicyViolation, "invalid service tier")
						return
					}
				}
				data = rebindCodexFrame(data, ident, a)
				if !tierTracker.Sent(data) {
					stop(gorillaws.ClosePolicyViolation, "too many pending turns")
					return
				}
			}
			_ = up.SetWriteDeadline(time.Now().Add(codexWSWriteDeadline))
			// Echo the frame's own type, for the mirror-image reason: only text
			// frames are rebound, so a binary frame relabelled as text would
			// reach upstream still carrying the downstream client's identity.
			if err := up.WriteMessage(mt, data); err != nil {
				log.Warnf("codex ws: upstream write via %s failed: %v", a.ID, err)
				stop(gorillaws.CloseTryAgainLater, "upstream write failed")
				return
			}
		}
	}()

	// Upstream keepalive ping during quiet turns.
	go func() {
		t := time.NewTicker(codexWSUpstreamPingEvery)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				// Server shutting down or the request context was canceled.
				stop(gorillaws.CloseServiceRestart, "server shutting down")
				return
			case <-t.C:
				_ = up.Ping(time.Now().Add(5 * time.Second))
			}
		}
	}()

	wg.Wait()
}

// codexTurnDelta returns the tokens consumed since the last settled turn —
// cur (running session total) minus billed (total already settled) — tagged as
// one request. Keeping this a pure function makes the "each turn bills only its
// own delta, never the running total" invariant directly testable.
func codexTurnDelta(cur, billed usage.Counts) usage.Counts {
	return usage.Counts{
		InputTokens:       cur.InputTokens - billed.InputTokens,
		OutputTokens:      cur.OutputTokens - billed.OutputTokens,
		CacheCreateTokens: cur.CacheCreateTokens - billed.CacheCreateTokens,
		CacheReadTokens:   cur.CacheReadTokens - billed.CacheReadTokens,
		Requests:          1,
	}
}

// billCodexWSTurn settles a single completed WS turn through the same funnel as
// the HTTP Codex path: official cost -> SaaS SettleCharge (group×provider
// multiplier) -> client ledger -> request log. turn carries just this turn's
// tokens with Requests==1. The auth's own token ledger is NOT touched here — it
// is folded in once for the whole session when the socket closes, so per-turn
// settlement never double-counts it. One request-log row is emitted per turn,
// so the admin panel shows each turn's real cost as it happens rather than a
// single hour-long row at the end.
func (s *Server) billCodexWSTurn(c *gin.Context, a *auth.Auth, model, clientToken, clientName string, turn usage.Counts, dur time.Duration, options ...pricing.CostOptions) {
	billingOptions := pricing.CostOptions{}
	if len(options) > 0 {
		billingOptions = options[0]
	}
	billingOptions.CodexOAuth = a.Kind == auth.KindOAuth
	var priced pricing.CostResult
	// A WS session can also be served by a relay peer once the OAuth fleet is
	// exhausted, and that credential carries its own markup — so the kind has to
	// be read off the auth rather than assumed, both for the charge's source tag
	// and for the row the admin panel groups by.
	kind := "oauth"
	source := "codex-oauth-ws:"
	if a.Kind == auth.KindAPIKey {
		kind = "apikey"
		source = "codex-ws:"
	}
	var costUSD float64
	var multiplier, billed float64 = 1, 0
	if turn.Requests > 0 && clientToken != "" {
		priced = s.pricing.CostWithOptions(auth.ProviderOpenAI, billingModelFor(a, model), turn, billingOptions)
		costUSD = priced.CostUSD
		s.usage.RecordClient(clientToken, clientName, turn, costUSD)
		if s.saas != nil {
			multiplier, billed = s.saas.SettleCharge(context.WithoutCancel(c.Request.Context()),
				clientToken, auth.ProviderOpenAI, model, costUSD,
				apiKeyPriceOverride(a), source+a.ID)
		}
	}
	s.emitLog(requestlog.Record{
		RequestedServiceTier: billingOptions.ServiceTier,
		UpstreamServiceTier:  billingOptions.ResponseServiceTier,
		ServiceTier:          priced.Tier.Billing,
		Client:               clientName,
		ClientToken:          maskClientToken(clientToken),
		Provider:             auth.ProviderOpenAI,
		AuthID:               a.ID,
		AuthLabel:            a.Label,
		AuthKind:             kind,
		Model:                model,
		Input:                turn.InputTokens,
		Output:               turn.OutputTokens,
		CacheRead:            turn.CacheReadTokens,
		CostUSD:              costUSD,
		BilledUSD:            billed,
		Multiplier:           multiplier,
		Status:               200,
		DurationMs:           dur.Milliseconds(),
		Stream:               true,
		Path:                 "/v1/responses",
	})
}

// codexWSNormalClose reports whether a read error is the upstream closing the
// socket in an orderly way, as opposed to the connection dropping underneath
// us. Only the orderly case may be relayed to the client as a normal closure;
// everything else (h2 PROTOCOL_ERROR, a dead pooled conn, a proxy hiccup, a
// read deadline) means the response was cut off and the client should be told
// it can retry.
func codexWSNormalClose(err error) bool {
	return gorillaws.IsCloseError(err,
		gorillaws.CloseNormalClosure,
		gorillaws.CloseGoingAway,
	)
}

func closeCodexWS(conn *gorillaws.Conn, code int, reason string) {
	_ = conn.WriteControl(gorillaws.CloseMessage,
		gorillaws.FormatCloseMessage(code, reason),
		time.Now().Add(2*time.Second))
}

// codexWSDialTarget builds the upstream WebSocket handshake for one credential,
// or reports !ok when the credential cannot serve a WS session at all.
//
// Two kinds of upstream speak this protocol:
//
//   - OAuth (ChatGPT Plus/Pro/Team) — dials chatgpt.com's Codex WS backend with
//     the codex-tui fingerprint, under the Chrome uTLS ClientHello.
//   - A relay peer (an API key flagged relay_peer, i.e. a cooperating proxy
//     running this same cc-core stack) — dials THAT proxy's own /v1/responses WS
//     ingress with the API key as the client bearer token. This is what keeps WS
//     sessions alive once the OAuth fleet is quota-exhausted: without it the
//     handshake is already complete when the pool comes back empty, so there is
//     no HTTP status left to send and the CLI just sees the socket close.
//     Identity is stamped with cc-core/relay exactly as the HTTP path does
//     (applyRelayIdentity), so the peer schedules each of our users onto its own
//     slot instead of collapsing them onto one credential.
//
// A plain vendor API key gets !ok: third-party OpenAI-compatible relays serve
// HTTP POST only, and dialing them would spend the handshake budget to collect a
// 404 that also has to be kept off the credential's health record.
func (s *Server) codexWSDialTarget(a *auth.Auth, clientToken, slotID, betaValue string, ident *mimicry.CodexFrameIdentity) (codexws.DialConfig, bool) {
	snap := a.Snapshot()
	accessToken, _ := a.Credentials()

	if a.Kind == auth.KindOAuth {
		accountID, _ := a.CodexIdentity()
		// Per-credential base URL override is allowed for vendor-relay setups,
		// matching doForwardCodexOAuth.
		base := s.cfg.ChatGPTBackendBaseURL
		if snap.BaseURL != "" {
			base = strings.TrimSuffix(strings.TrimRight(snap.BaseURL, "/"), "/codex")
		}
		return codexws.DialConfig{
			URL: codexWSUpstreamURL(base),
			// Identity carries the session/thread/installation ids, and the same
			// value goes to mimicry.RewriteCodexClientFrame for every frame on
			// this socket. Handing the handshake and the frames one object is
			// what stops them disagreeing — a genuine client always has them
			// identical, so a mismatch is a one-comparison tell.
			Header: codexws.BuildUpstreamHeadersWithOptions(codexws.UpstreamHeaderOptions{
				AccessToken: accessToken,
				AccountID:   accountID,
				Identity:    ident,
				BetaValue:   betaValue,
			}),
			ProxyURL:  snap.ProxyURL,
			UseUTLS:   s.cfg.UseUTLS,
			ReadLimit: s.cfg.CodexWS.ReadLimitBytes,
		}, true
	}

	if !a.RelayPeer {
		return codexws.DialConfig{}, false
	}
	baseURL := strings.TrimRight(snap.BaseURL, "/")
	if baseURL == "" {
		baseURL = strings.TrimRight(s.cfg.OpenAIBaseURL, "/")
	}
	if baseURL == "" {
		return codexws.DialConfig{}, false
	}
	// accountID is empty: Chatgpt-Account-Id names an upstream ChatGPT account,
	// which is the peer's business to choose, not ours to assert.
	header := codexws.BuildUpstreamHeadersWithOptions(codexws.UpstreamHeaderOptions{
		AccessToken: accessToken,
		AccountID:   "",
		Identity:    ident,
		BetaValue:   betaValue,
	})
	relay.Apply(header, RelayPeerName, clientToken, slotID)
	return codexws.DialConfig{
		URL:      codexWSRelayURL(baseURL),
		Header:   header,
		ProxyURL: snap.ProxyURL,
		// The peer is a cooperating proxy, not a Cloudflare-fronted vendor: the
		// HTTP relay path dials it without uTLS too.
		UseUTLS:   false,
		ReadLimit: s.cfg.CodexWS.ReadLimitBytes,
	}, true
}

// reportCodexWSDialFault records a failed WS handshake on the credential that
// served it and reports whether the sticky assignment should be broken.
//
// The classification mirrors the HTTP paths rather than inventing a third one:
// an API-key relay goes through reportCodexAPIKeyFault (breaker ladder,
// Retry-After aware on 429), an OAuth credential through the pool's
// ReportUpstreamError for the credential-scoped statuses. A status the shared
// classifier calls the client's own fault — most usefully a 404/426 from a peer
// whose WS ingress is disabled — touches no health state at all: the credential
// is fine, it simply has no socket to offer, and quarantining it would take its
// HTTP traffic down with it.
func (s *Server) reportCodexWSDialFault(ctx context.Context, a *auth.Auth, status int, retryAfter time.Time, derr error) (unstick bool) {
	if status != 0 && !classifyUpstreamStatus(status).retryable() {
		log.Warnf("codex ws: %s answered the handshake with %d — not a credential fault, leaving health untouched", a.ID, status)
		return false
	}
	// status == 0 means the handshake never produced an HTTP response at all:
	// the failure is below the protocol, so it says nothing about the
	// credential until we have ruled out the two things that routinely land
	// here and are nobody's fault.
	if status == 0 {
		if isClientDisconnect(ctx, derr) {
			// The user closed the tab while we were dialling.
			return false
		}
		if isCodexWSDialFlap(derr) {
			log.Infof("codex ws: handshake to %s flapped (%v) — network-level, leaving health untouched", a.ID, derr)
			return false
		}
	}
	if a.Kind == auth.KindAPIKey {
		if status == 0 {
			status = http.StatusBadGateway // transport failure: same class as a gateway error
		}
		s.reportCodexAPIKeyFault(a, status, retryAfter)
		return true
	}
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests:
		s.pool.ReportUpstreamError(a, status, retryAfter)
		return true
	default:
		a.MarkFailure(derr.Error())
		return false
	}
}

// isCodexWSDialFlap reports whether a failed WebSocket handshake was a
// network-level flap rather than anything the credential did.
//
// It is deliberately wider than auth.IsTransientNetErr, which does NOT count a
// timeout: on the HTTP paths a timeout is meaningful, because the request
// reached an upstream that then failed to answer in time, and because those
// paths sit behind the transport's own backoff-retry (auth.retryRoundTripper) —
// by the time an error surfaces there the flap has already been shown to
// persist. A WS handshake has neither property. codexws.Dial goes straight out
// with a 10s budget and no internal retry, so a single slow TLS handshake or a
// dropped SYN arrives here as `read tcp …: i/o timeout` on the first try, and
// nothing about it distinguishes a bad credential from a bad moment.
//
// Counting those toward health is how a network hiccup takes subscription
// accounts offline: five in a row hard-fail an OAuth credential
// (hardFailureThreshold), and two are enough to degrade it. The dial loop
// already handles the failure the right way by moving to the next credential —
// this only stops it from also leaving a mark.
//
// It applies to relay peers as well as OAuth. A timeout proves no more about a
// peer than about a subscription account, and the peer is the last channel left
// when the OAuth fleet is spent — quarantining it on a dropped SYN is how the
// fallback goes dark exactly when it is needed. Failures that do carry
// information (a refused connection, an unexplained transport error, any 5xx)
// still count for both.
func isCodexWSDialFlap(err error) bool {
	if err == nil {
		return false
	}
	if auth.IsTransientNetErr(err) {
		return true
	}
	// Covers `i/o timeout` (net.OpError), the dialer's own handshake deadline,
	// and context.DeadlineExceeded. A client cancellation is context.Canceled,
	// whose Timeout() is false, and is classified by isClientDisconnect instead.
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// codexWSRelayURL turns a relay peer's HTTP base URL into its Codex responses
// WebSocket URL. The path join is the shared API-key rule
// (mimicry.JoinCodexAPIKeyUpstreamURL), so a bare-origin peer keeps /v1 and one
// configured with a path stays authoritative — exactly as on the HTTP path, so
// a peer that works for POST /v1/responses works for the WS upgrade too.
func codexWSRelayURL(base string) string {
	return httpToWSURL(mimicry.JoinCodexAPIKeyUpstreamURL(base, "/v1/responses"))
}

// codexWSUpstreamURL turns the configured ChatGPT backend base (https://...
// /backend-api) into the Codex responses WebSocket URL (wss://.../codex/responses).
func codexWSUpstreamURL(base string) string {
	return httpToWSURL(strings.TrimRight(base, "/") + "/codex/responses")
}

// httpToWSURL swaps an http(s) scheme for its WebSocket equivalent, leaving a
// URL that already names one (or names none) alone.
func httpToWSURL(u string) string {
	switch {
	case strings.HasPrefix(u, "https://"):
		return "wss://" + strings.TrimPrefix(u, "https://")
	case strings.HasPrefix(u, "http://"):
		return "ws://" + strings.TrimPrefix(u, "http://")
	default:
		return u
	}
}

// codexWSExtractModel best-effort reads the model from the first client frame,
// checking the top level and a nested "response" envelope.
func codexWSExtractModel(frame []byte) string {
	var probe struct {
		Model    string `json:"model"`
		Response struct {
			Model string `json:"model"`
		} `json:"response"`
	}
	if json.Unmarshal(frame, &probe) != nil {
		return ""
	}
	if probe.Model != "" {
		return probe.Model
	}
	return probe.Response.Model
}

// codexResponseID extracts response.id from a Codex backend event payload.
func codexResponseID(payload []byte) string {
	var ev struct {
		Response struct {
			ID string `json:"id"`
		} `json:"response"`
	}
	if json.Unmarshal(payload, &ev) != nil {
		return ""
	}
	return ev.Response.ID
}

// rebindCodexFrame maps a downstream client's response.create frame into the
// identity we advertised on this socket's handshake.
//
// Without it the frame keeps the DOWNSTREAM client's own client_metadata —
// installation id, session/thread/turn/window ids — so N users of one pooled
// credential present to OpenAI as N installations on a single ChatGPT account,
// and the frame contradicts the handshake headers, which a genuine client
// always matches.
//
// A rewrite failure is a LOCAL judgement, never a credential fault: it must not
// MarkFailure, must not trigger failover, and must not drop the turn. Forwarding
// the original frame is strictly better than killing a working session — the
// worst case is that this one frame keeps the client's ids, which is exactly the
// status quo this function improves on.
func rebindCodexFrame(frame []byte, ident mimicry.CodexFrameIdentity, a *auth.Auth) []byte {
	out, err := mimicry.RewriteCodexClientFrame(frame, ident)
	if err != nil {
		id := "?"
		if a != nil {
			id = a.ID
		}
		log.Warnf("codex ws: client frame rebind via %s failed, forwarding as-is: %v", id, err)
		return frame
	}
	return out
}

func codexBillingTurnModel(fallback string, turn servicetier.Turn) string {
	if turn.Model != "" {
		return turn.Model
	}
	return fallback
}

func codexWSFrameIsResponseCreate(frame []byte) bool {
	var event struct {
		Type string `json:"type"`
	}
	return json.Unmarshal(frame, &event) == nil && event.Type == "response.create"
}
