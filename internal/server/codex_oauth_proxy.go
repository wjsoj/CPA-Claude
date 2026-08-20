package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"github.com/wjsoj/cc-core/auth"
	"github.com/wjsoj/cc-core/codexerr"
	"github.com/wjsoj/cc-core/downstream"
	"github.com/wjsoj/cc-core/mimicry"
	"github.com/wjsoj/cc-core/requestlog"
	ccstream "github.com/wjsoj/cc-core/stream"
	"github.com/wjsoj/cc-core/usage"
)

// The ChatGPT Codex backend expects the OpenAI /v1/responses schema with a
// handful of upstream-private fields stripped. The upstream request headers
// (Originator / User-Agent / Version / Chatgpt-Account-Id / x-codex-routing-hint)
// are applied by mimicry.ApplyCodexCLIHeaders — see cc-core/crack/codex/SPEC.md
// and cc-core/mimicry/codex.go. Note there is deliberately no OpenAI-Beta on
// this path: real Codex sets it only on the WS handshake.

// doForwardCodexOAuth forwards the client's /v1/responses request to the
// ChatGPT backend. Behavior matches the vendor Codex CLI: Bearer auth from
// the OAuth access_token, account_id from the cached ID-token claims, a
// fresh per-request session UUID, and the `codex-tui` User-Agent /
// Originator that the backend fingerprints on.
func (s *Server) doForwardCodexOAuth(c *gin.Context, a *auth.Auth, path string, body []byte, stream bool, model, clientToken, clientName string, start time.Time, attempts int) (retry, done bool) {
	if path != "/v1/responses" && path != "/v1/responses/compact" {
		// The ChatGPT backend only hosts /codex/responses{,/compact}; OAuth
		// creds can't serve /v1/chat/completions. Ask the retry loop to try a
		// different credential (API-key creds handle chat/completions fine).
		// Don't MarkFailure — this credential isn't broken, just the wrong
		// kind. forward() has already fast-failed if no API-key alternatives
		// exist.
		log.Debugf("codex oauth: %s skipping %s (OAuth path supports /v1/responses{,/compact} only)", a.ID, path)
		return true, false
	}

	snap := a.Snapshot()
	baseURL := strings.TrimRight(s.cfg.ChatGPTBackendBaseURL, "/") + "/codex"
	// Per-credential base URL override is allowed for vendor-relay setups.
	if ab := strings.TrimRight(snap.BaseURL, "/"); ab != "" {
		baseURL = ab
	}
	upURL := baseURL + mimicry.CodexOAuthPath(path)

	upstreamBody, _, err := mimicry.SanitizeCodexRequestBody(body, path)
	if err != nil {
		log.Warnf("codex oauth: body sanitize failed via %s: %v", a.ID, err)
		upstreamBody = body
	}

	ctx := c.Request.Context()
	upReq, err := http.NewRequestWithContext(ctx, http.MethodPost, upURL, bytes.NewReader(upstreamBody))
	if err != nil {
		c.AbortWithStatusJSON(500, gin.H{"error": err.Error()})
		return false, true
	}
	copyForwardableHeaders(c.Request.Header, upReq.Header)
	stripIngressHeaders(upReq.Header)

	accessToken, _ := a.Credentials()
	accountID, _ := a.CodexIdentity()
	isCompactPath := path == "/v1/responses/compact"
	// Apply the Codex CLI fingerprint — codex-tui identity (Originator /
	// User-Agent / Version) over the HTTP POST /codex/responses{,/compact}
	// path. Centralized in cc-core (mimicry.ApplyCodexCLIHeaders) so every relay
	// stays in lockstep when the version target is bumped. See cc-core/crack/codex/SPEC.md.
	//
	// The routing hint is derived from upstreamBody — the bytes actually going
	// out, after sanitization — so the header can never name a different model
	// than the body does.
	routingModel, routingTier := mimicry.CodexModelAndTier(upstreamBody)
	mimicry.ApplyCodexCLIHeaders(upReq, accessToken, accountID, isCompactPath, routingModel, routingTier)

	// Shared pooled transport (per proxyURL). Reusing HTTP/2 connections is
	// critical here: chatgpt.com's CF edge rate-limits new TCP/TLS connections
	// from VPS/proxy IPs and RSTs the handshake when the per-IP new-connection
	// quota is hit — the classic alternating 200/503 symptom. A pooled h2 conn
	// carries many requests so we stay under the limit. ClientFor's transport
	// has HTTP/2 PING health checks (utls.go) so stale reused conns are
	// detected and re-dialed transparently.
	client := auth.ClientFor(snap.ProxyURL, s.cfg.UseUTLS)
	// Transient wire-level flaps (CF edge RST mid-handshake, h2 PROTOCOL_ERROR /
	// REFUSED_STREAM, `connection reset by peer`, stale pooled h2 conn) are
	// replayed with exponential backoff + jitter inside ClientFor's transport
	// (cc-core auth.retryRoundTripper) on this same credential — see
	// auth.IsTransientNetErr. By the time Do returns an error, that backoff loop
	// is already exhausted, so a transient error surviving to here means the
	// flap is persistent; we defer to the outer loop (another credential)
	// without MarkFailure rather than burning this one.
	resp, err := client.Do(upReq)
	if err != nil {
		if isClientDisconnect(ctx, err) {
			a.MarkClientCancel(err.Error())
			s.emitLog(requestlog.Record{
				Client: clientName, ClientToken: maskClientToken(clientToken), Provider: auth.ProviderOpenAI,
				AuthID: a.ID, AuthLabel: a.Label, AuthKind: "oauth", Model: model,
				Stream: stream, Path: path, Status: 499, Attempts: attempts,
				DurationMs: time.Since(start).Milliseconds(),
				Error:      "client canceled",
			})
			return false, true
		}
		// Transient infra failure that survived the same-cred retry loop:
		// don't MarkFailure (would degrade the credential / show as unhealthy
		// in the admin panel), don't emit a request log row. Just ask the
		// outer loop to try another credential — and if that one is also the
		// only one, it'll come right back here for another round of retries.
		if isTransientNetErr(err) {
			log.Infof("codex oauth: transient net error survived same-cred retries via %s: %v (deferring to outer loop without MarkFailure)", a.ID, err)
			return true, false
		}
		a.MarkFailure(err.Error())
		log.Warnf("codex oauth: upstream error via %s: %v", a.ID, err)
		return true, false
	}

	// Capture rolling primary/secondary quota snapshot from upstream response
	// headers (the `x-codex-*` family). Done unconditionally since 4xx/429
	// responses also carry these — they're what tell us *why* we were blocked.
	a.CaptureCodexRateLimits(resp.Header)

	// Pre-read error bodies to inspect ChatGPT's usage-limit signals.
	switch resp.StatusCode {
	case http.StatusTooManyRequests, http.StatusUnauthorized, http.StatusForbidden:
		errBody, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		// A 429 with an explicit model-capacity body is scoped to this model
		// request, not to the account. Try another credential without setting
		// account quota; the official usage probe remains authoritative for a
		// genuine subscription limit.
		if resp.StatusCode == http.StatusTooManyRequests && isCodexCapacityError(errBody) {
			log.Warnf("codex oauth: model capacity rejection via %s; retrying another credential without cooling account", a.ID)
			return true, false
		}
		resetAt := parseCodexResetAt(errBody)
		if resetAt.IsZero() {
			resetAt = parseRetryAfter(resp.Header)
		}
		s.pool.ReportUpstreamError(a, resp.StatusCode, resetAt)
		log.Warnf("codex oauth: credential %s received %d: %s", a.ID, resp.StatusCode, truncate(errBody, 240))
		return true, false
	}
	// Capacity errors can also come back as non-429 4xx responses; the body
	// message is what we actually key on. This is a
	// model/request-scoped signal, not an account-quota signal: another account
	// may still have capacity, but cooling this credential would also take all
	// of its other models offline. Retry another credential for this request
	// without mutating credential health. Genuine account quota is identified
	// by request-time usage-limit responses and the proactive wham/usage probe.
	if resp.StatusCode >= 400 {
		errBody, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if isCodexCapacityError(errBody) {
			log.Warnf("codex oauth: model capacity rejection via %s; retrying another credential without cooling account", a.ID)
			return true, false
		}
		writeResponseHeaders(c, resp)
		c.Writer.Write(errBody)
		s.emitLog(requestlog.Record{
			Client: clientName, ClientToken: maskClientToken(clientToken), Provider: auth.ProviderOpenAI,
			AuthID: a.ID, AuthLabel: a.Label, AuthKind: "oauth", Model: model,
			Stream: stream, Path: path, Status: resp.StatusCode, Attempts: attempts,
			DurationMs: time.Since(start).Milliseconds(),
			Error:      fmt.Sprintf("upstream %d", resp.StatusCode),
		})
		return false, true
	}

	var counts usage.Counts
	var streamErr string
	// Status recorded in the request log. Defaults to the upstream's, but a
	// mid-stream client hang-up overrides it to 499 — the response was 200 on
	// the wire, yet logging it as a success with an error attached hides it
	// from every "client canceled" view.
	logStatus := resp.StatusCode
	if isCompactPath {
		// /codex/responses/compact returns a single JSON object — no SSE.
		// Read it once, extract usage, pass through verbatim. Matches sub2api's
		// handleNonStreamingResponsePassthrough behavior on this path.
		payload, rerr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if rerr != nil {
			log.Warnf("codex oauth: read compact body via %s: %v", a.ID, rerr)
			c.AbortWithStatusJSON(502, gin.H{"error": "codex upstream: " + rerr.Error()})
			s.emitLog(requestlog.Record{
				Client: clientName, ClientToken: maskClientToken(clientToken), Provider: auth.ProviderOpenAI,
				AuthID: a.ID, AuthLabel: a.Label, AuthKind: "oauth", Model: model,
				Stream: stream, Path: path, Status: 502, Attempts: attempts,
				DurationMs: time.Since(start).Milliseconds(),
				Error:      rerr.Error(),
			})
			return false, true
		}
		counts.Add(extractCodexBackendUsageFromJSON(payload))
		// Allowlist, not a hop-by-hop denylist. Forwarding everything else
		// handed the caller our pool's operational state: the x-codex-*
		// rate-limit headers (the serving account's window utilisation and
		// reset times), openai-organization, x-oai-request-id, set-cookie and
		// cf-ray — whose suffix is the Cloudflare datacentre our egress sits
		// in. The Claude path has used this allowlist since it was written;
		// only Codex was still copying verbatim.
		downstream.CopyResponseHeaders(c.Writer.Header(), resp.Header, time.Now())
		c.Writer.Header().Set("Content-Type", "application/json")
		c.Writer.WriteHeader(resp.StatusCode)
		c.Writer.Write(payload)
	} else if stream {
		// Streaming client: passthrough SSE verbatim (with keepalive + terminal
		// tracking). Headers are committed lazily inside the relay, so a break
		// before the first byte reaches the client is recoverable.
		res := streamSSECodexBackend(c, resp, &counts, func() { writeResponseHeaders(c, resp) })
		// A shed that landed after output started could only be demoted, never
		// withheld. Say so: the demotion works, so the CLI backs off and
		// recovers and nothing else records that upstream refused to serve. The
		// turn otherwise reaches the operator as one that finished with no
		// usage, which reads as a broken relay rather than a busy account.
		if res.demoted.shed {
			log.Warnf("codex oauth: %s shed the turn after output started (capacity=%v); client sees a retryable error", a.ID, res.demoted.capacity)
			if res.demoted.capacity {
				streamErr = "upstream shed the turn (capacity)"
			} else {
				streamErr = "upstream shed the turn (quota/rate)"
			}
		}
		if !res.sawTerminal && !res.wroteAny {
			// Nothing reached the client yet. If the client itself went away,
			// there's nobody to retry for; otherwise transparently fail over to
			// another credential (same contract as the pre-response path above).
			_ = resp.Body.Close()
			if res.shed != "" {
				// Upstream shed this request for capacity/quota inside an
				// otherwise-200 stream. Nothing was forwarded, so failover is
				// clean. Credential health is deliberately NOT touched — this
				// is the same signal as the 429 capacity branch above, which
				// also rotates without cooling: capacity is a property of the
				// model and the moment, and cooling the account would take all
				// of its other models offline too.
				log.Warnf("codex oauth: %s shed the request mid-stream (attempt %d, %s): %s — retrying on another credential",
					a.ID, attempts, time.Since(start).Round(time.Millisecond), res.shed)
				return true, false
			}
			if isClientDisconnect(ctx, res.err) {
				a.MarkClientCancel(errString(res.err))
				s.emitLog(requestlog.Record{
					Client: clientName, ClientToken: maskClientToken(clientToken), Provider: auth.ProviderOpenAI,
					AuthID: a.ID, AuthLabel: a.Label, AuthKind: "oauth", Model: model,
					Stream: stream, Path: path, Status: 499, Attempts: attempts,
					DurationMs: time.Since(start).Milliseconds(),
					Error:      "client canceled before first event",
				})
				return false, true
			}
			log.Warnf("codex oauth: stream broke before any output via %s (attempt %d, %s): %v — retrying on another credential",
				a.ID, attempts, time.Since(start).Round(time.Millisecond), res.err)
			return true, false
		}
		if !res.sawTerminal {
			// Bytes already went downstream — can't restart cleanly. Record what
			// happened richly so it's visible in logs + the request log instead
			// of looking like a clean stream end.
			//
			// Name the two causes apart. Codex CLI aborts the in-flight request
			// on Ctrl-C / ESC, which cancels the context and reaches us as the
			// same read error an upstream hang-up would — so labelling both
			// "truncated" turns ordinary user behaviour into what reads as an
			// upstream incident. In hypitoken's production logs that made ~90%
			// of all recorded Codex errors cancellations in disguise, burying
			// the ~0.05% of genuine h2 truncations.
			if isClientDisconnect(ctx, res.err) {
				streamErr = fmt.Sprintf("client canceled mid-stream after %d event(s)/%dB", res.events, res.bytes)
				// 499 + MarkClientCancel match the two disconnect branches
				// above, so a mid-stream hang-up lands in the same bucket as one
				// a second earlier rather than as a 200 carrying an error.
				// MarkClientCancel is health-neutral: the credential is fine.
				logStatus = 499
				a.MarkClientCancel(errString(res.err))
				log.Infof("codex oauth: client disconnected mid-stream via %s (attempt %d, events=%d, bytes=%d, %s)",
					a.ID, attempts, res.events, res.bytes, time.Since(start).Round(time.Millisecond))
			} else {
				streamErr = fmt.Sprintf("stream truncated mid-flight after %d event(s)/%dB: %v", res.events, res.bytes, res.err)
				log.Warnf("codex oauth: SSE truncated mid-stream via %s (attempt %d, events=%d, bytes=%d, %s): %v",
					a.ID, attempts, res.events, res.bytes, time.Since(start).Round(time.Millisecond), res.err)
			}
		}
	} else {
		// Non-streaming client: aggregate SSE into a single response object
		// (mirrors CLIProxyAPI's CodexExecutor.Execute aggregation).
		payload, aerr := aggregateCodexResponseStream(resp.Body, &counts)
		if aerr != nil {
			_ = resp.Body.Close()
			// The aggregate buffers the whole response before writing anything,
			// so nothing has reached the client — a truncated/transient upstream
			// can be retried cleanly on another credential. Client-gone and
			// genuine parse errors (well-formed but unexpected shape) won't
			// improve on retry, so those are surfaced as 499/502.
			if isClientDisconnect(ctx, aerr) {
				a.MarkClientCancel(aerr.Error())
				s.emitLog(requestlog.Record{
					Client: clientName, ClientToken: maskClientToken(clientToken), Provider: auth.ProviderOpenAI,
					AuthID: a.ID, AuthLabel: a.Label, AuthKind: "oauth", Model: model,
					Stream: stream, Path: path, Status: 499, Attempts: attempts,
					DurationMs: time.Since(start).Milliseconds(),
					Error:      "client canceled",
				})
				return false, true
			}
			if errors.Is(aerr, io.EOF) || errors.Is(aerr, io.ErrUnexpectedEOF) || isTransientNetErr(aerr) {
				log.Warnf("codex oauth: aggregation truncated via %s (attempt %d): %v — retrying on another credential", a.ID, attempts, aerr)
				return true, false
			}
			log.Warnf("codex oauth: aggregation via %s failed: %v", a.ID, aerr)
			c.AbortWithStatusJSON(502, gin.H{"error": "codex upstream: " + aerr.Error()})
			s.emitLog(requestlog.Record{
				Client: clientName, ClientToken: maskClientToken(clientToken), Provider: auth.ProviderOpenAI,
				AuthID: a.ID, AuthLabel: a.Label, AuthKind: "oauth", Model: model,
				Stream: stream, Path: path, Status: 502, Attempts: attempts,
				DurationMs: time.Since(start).Milliseconds(),
				Error:      aerr.Error(),
			})
			return false, true
		}
		// Same allowlist as the non-streaming branch above. Content-Type is
		// overwritten right after: this branch aggregates an SSE stream into a
		// single JSON body, so the upstream's text/event-stream would be a lie.
		downstream.CopyResponseHeaders(c.Writer.Header(), resp.Header, time.Now())
		c.Writer.Header().Set("Content-Type", "application/json")
		c.Writer.WriteHeader(http.StatusOK)
		c.Writer.Write(payload)
	}
	_ = resp.Body.Close()

	s.usage.Record(a.ID, a.Label, counts)
	var costUSD float64
	var multiplier, billed float64 = 1, 0
	if resp.StatusCode < 400 && counts.Requests > 0 && clientToken != "" {
		costUSD = s.pricing.Cost(auth.ProviderOpenAI, model, counts)
		s.usage.RecordClient(clientToken, clientName, counts, costUSD)
		multiplier, billed = s.saas.SettleCharge(context.WithoutCancel(c.Request.Context()),
			clientToken, auth.ProviderOpenAI, model, costUSD,
			apiKeyPriceOverride(a), "codex-oauth:"+a.ID)
	}
	s.emitLog(requestlog.Record{
		Client:      clientName,
		ClientToken: maskClientToken(clientToken),
		Provider:    auth.ProviderOpenAI,
		AuthID:      a.ID,
		AuthLabel:   a.Label,
		AuthKind:    "oauth",
		Model:       model,
		Input:       counts.InputTokens,
		Output:      counts.OutputTokens,
		CacheRead:   counts.CacheReadTokens,
		CostUSD:     costUSD,
		BilledUSD:   billed,
		Multiplier:  multiplier,
		Status:      logStatus,
		DurationMs:  time.Since(start).Milliseconds(),
		Stream:      stream,
		Path:        path,
		Attempts:    attempts,
		Error:       streamErr,
	})
	if resp.StatusCode < 400 {
		a.MarkSuccess()
	}
	return false, true
}

// aggregateCodexResponseStream reads the backend SSE stream and returns
// the final response JSON object for a non-streaming client. Mirrors the
// aggregation in CLIProxyAPI's CodexExecutor.Execute: collects
// `response.output_item.done` items (keyed by output_index when present,
// falling back to arrival order), then on `response.completed` patches
// the response.output field if it arrived empty. Output shape matches
// OpenAI's /v1/responses non-streaming reply: the bare `response` object
// (id, object, output, usage, …) — not the SSE event envelope.
func aggregateCodexResponseStream(r io.Reader, counts *usage.Counts) ([]byte, error) {
	reader := newLineReader(r)
	var byIndex []codexOutputSlot
	var fallback []json.RawMessage

	for {
		line, rerr := reader.readLine()
		if len(line) > 0 {
			trim := bytes.TrimRight(line, "\r\n")
			if bytes.HasPrefix(trim, []byte("data:")) {
				payload := bytes.TrimSpace(trim[5:])
				if len(payload) > 0 && payload[0] == '{' {
					var ev struct {
						Type        string          `json:"type"`
						Item        json.RawMessage `json:"item"`
						OutputIndex *int64          `json:"output_index"`
						Response    json.RawMessage `json:"response"`
					}
					if err := json.Unmarshal(payload, &ev); err == nil {
						switch ev.Type {
						case "response.output_item.done":
							if len(ev.Item) > 0 {
								if ev.OutputIndex != nil {
									byIndex = append(byIndex, codexOutputSlot{idx: *ev.OutputIndex, data: ev.Item})
								} else {
									fallback = append(fallback, ev.Item)
								}
							}
						case "response.completed":
							if len(ev.Response) == 0 {
								return nil, errors.New("response.completed missing response field")
							}
							counts.Add(extractCodexBackendUsageFromJSON(payload))
							return patchResponseOutput(ev.Response, byIndex, fallback)
						}
					}
				}
			}
		}
		if rerr != nil {
			return nil, fmt.Errorf("stream closed before response.completed: %w", rerr)
		}
	}
}

// patchResponseOutput replaces response.output with the collected
// output_item.done events when the completed event arrived with an empty
// or missing output array. Returns the (possibly unchanged) response JSON.
type codexOutputSlot struct {
	idx  int64
	data json.RawMessage
}

func patchResponseOutput(response json.RawMessage, byIndex []codexOutputSlot, fallback []json.RawMessage) ([]byte, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(response, &obj); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	// Only patch if the existing output is missing or empty.
	needsPatch := true
	if cur, ok := obj["output"]; ok {
		t := bytes.TrimSpace(cur)
		if len(t) > 2 && !bytes.Equal(t, []byte("[]")) && !bytes.Equal(t, []byte("null")) {
			needsPatch = false
		}
	}
	if needsPatch && (len(byIndex) > 0 || len(fallback) > 0) {
		sort.SliceStable(byIndex, func(i, j int) bool { return byIndex[i].idx < byIndex[j].idx })
		items := make([]json.RawMessage, 0, len(byIndex)+len(fallback))
		for _, s := range byIndex {
			items = append(items, s.data)
		}
		items = append(items, fallback...)
		patched, err := json.Marshal(items)
		if err != nil {
			return nil, err
		}
		obj["output"] = patched
	}
	return json.Marshal(obj)
}

// codexTerminalEvent reports whether a Codex backend SSE data payload is a
// stream-terminating event. The client (codex-core) waits for one of these; if
// the upstream stream EOFs without it, the client raises
// "stream disconnected before completion".
func codexTerminalEvent(payload []byte) bool {
	var ev struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(payload, &ev) != nil {
		return false
	}
	switch ev.Type {
	case "response.completed", "response.failed", "response.incomplete",
		"response.cancelled", "response.canceled":
		return true
	}
	return false
}

// streamSSECodexBackend is the Codex backend SSE passthrough. The format
// differs from OpenAI's API-key response: events carry JSON payloads
// structured as `response.completed` / `response.output_item.done` etc.
// Usage arrives inside the `response.completed` event as
// `response.usage.{input_tokens, output_tokens, input_tokens_details.cached_tokens}`.
//
// Beyond verbatim passthrough it (a) commits the response headers lazily, via
// commit(), right before the first byte is written downstream — so a break that
// happens before any output reaches the client can be retried by the caller on
// a different credential (the common "RST right after 200" case); (b) emits an
// SSE keepalive comment line during silent gaps so intermediaries (Caddy/
// Cloudflare/the client's own idle timeout) don't cut the long-lived stream
// while the model is mid-think; and (c) tracks the terminal event + relay
// counters so a truncated upstream is reported (logged + retried/surfaced)
// instead of being passed off to the client as a clean end-of-stream — the root
// cause of the "stream disconnected before completion" reports.
//
// gin's ResponseWriter is not goroutine-safe, so the keepalive goroutine and the
// read loop share one mutex around every Write/Flush.
func streamSSECodexBackend(c *gin.Context, resp *http.Response, counts *usage.Counts, commit func()) codexStreamResult {
	flusher, _ := c.Writer.(http.Flusher)
	reader := newLineReader(resp.Body)
	events := 0
	shed := ""
	// demotedShed is the post-output half: a shed that arrived too late to
	// withhold, so it could only be demoted (capacity) or forwarded (quota/rate).
	var demotedShed shedSignal

	// next supplies framing (raw lines) + usage + terminal detection to the
	// shared relay; cc-core/stream.Relay owns keepalive + lazy commit + locking.
	// shedding latches once a capacity/quota error frame is seen before any
	// output has reached the client. From that point the rest of the stream is
	// withheld — including the response.failed that follows — so Relay ends with
	// SawTerminal=false and WroteAny=false and the caller's pre-output failover
	// fires. Without this the error frame itself counts as the first output and
	// permanently forecloses failover; see cc-core/codexerr.
	shedding := false
	sentAny := false // whether we've handed Relay any bytes yet
	// An SSE event is "event: X\ndata: {…}\n\n", and the verdict lives in the
	// data line — but the event line arrives first. Releasing it immediately
	// would commit the response before we know whether the frame is one we mean
	// to withhold, so an event line is held until its data line is classified
	// and then emitted together with it.
	var held []byte
	next := func() (out []byte, terminal bool, err error) {
		for {
			line, rerr := reader.readLine()
			if len(line) > 0 {
				trim := bytes.TrimRight(line, "\r\n")
				switch {
				case bytes.HasPrefix(trim, []byte("event:")):
					if !shedding {
						held = append(held[:0], line...)
					}
					line = nil

				case bytes.HasPrefix(trim, []byte("data:")):
					payload := bytes.TrimSpace(trim[5:])
					if len(payload) > 0 && payload[0] == '{' {
						events++
						counts.Add(extractCodexBackendUsageFromJSON(payload))

						if codexerr.Classify(payload) == codexerr.ClassRetryable {
							if !sentAny {
								// Failover is still possible — withhold this
								// frame and everything after it, including the
								// held event line and the response.failed that
								// follows, so nothing commits the response.
								shedding = true
								shed = truncate(payload, 200)
								held = nil
								line = nil
							} else if demoted, ok := codexerr.DemoteCapacityCode(payload); ok {
								// Output already started, so the client must be
								// told something. Demote the two session-ending
								// capacity codes to one the CLI retries; the
								// message is left untouched.
								//
								// Record BEFORE the rewrite: after
								// DemoteCapacityCode the code no longer says why
								// the turn was refused, and the caller needs
								// that to tell a shed apart from a broken relay.
								demotedShed.shed = true
								demotedShed.capacity = true
								tail := line[len(trim):]
								rebuilt := make([]byte, 0, len("data: ")+len(demoted)+len(tail))
								rebuilt = append(rebuilt, []byte("data: ")...)
								rebuilt = append(rebuilt, demoted...)
								rebuilt = append(rebuilt, tail...)
								line = rebuilt
							} else {
								// Quota/rate after output started: forwarded
								// untouched (the CLI handles those
								// non-terminally and reads its retry delay off
								// the original code), but still a shed and
								// still worth naming.
								demotedShed.shed = true
							}
						}
						// ClassFatal frames are forwarded verbatim: retrying
						// them elsewhere would fail identically, and the client
						// needs the real reason.

						if codexTerminalEvent(payload) && !shedding {
							terminal = true
						}

						// Withhold the pool's state, LAST — usage extraction,
						// error classification and terminal detection above all
						// read `payload` (what upstream said). This is the SSE
						// twin of the WS frame scrub in codex_ws.go.
						//
						// A dropped data line takes its held `event:` line with
						// it: emitting an event with no data is malformed SSE.
						if scrubbed, keep := downstream.ScrubCodexSSELine(line); !keep {
							line = nil
							held = nil
						} else {
							line = scrubbed
						}
					}
				}
			}

			if shedding {
				line = nil
				held = nil
				terminal = false
			}

			// Emit the held event line together with the line that resolved it.
			if len(line) > 0 && len(held) > 0 {
				out = append(append(make([]byte, 0, len(held)+len(line)), held...), line...)
				held = nil
			} else if len(line) > 0 {
				out = line
			} else if rerr != nil && len(held) > 0 && !shedding {
				// Stream ended with an unresolved event line — release it so
				// nothing is silently dropped.
				out, held = held, nil
			}

			if len(out) > 0 {
				sentAny = true
			}
			if len(out) > 0 || rerr != nil {
				return out, terminal, rerr
			}
			// Nothing to emit yet (a held event line) — keep reading.
		}
	}

	// Keepalive: an SSE comment line (":\n\n", ignored by SSE clients) keeps the
	// connection warm during silent gaps.
	r := ccstream.Relay(c.Writer, func() {
		if flusher != nil {
			flusher.Flush()
		}
	}, ccstream.RelayOptions{
		Commit:           commit,
		KeepaliveIdle:    10 * time.Second,
		KeepalivePayload: []byte(":\n\n"),
		Next:             next,
	})
	return codexStreamResult{sawTerminal: r.SawTerminal, wroteAny: r.WroteAny, events: events, bytes: r.Bytes, err: r.Err, shed: shed, demoted: demotedShed}
}

// shedSignal records an in-band shed observed while relaying a Codex SSE
// stream: upstream accepted the request, answered 200, then refused the turn
// with an error frame instead of content.
//
// It exists because the demotion that keeps the client's session alive is
// invisible by design — the CLI backs off and recovers, so nothing downstream
// ever says how often upstream is shedding. Without this the only trace was a
// turn that finished with no usage, which reads as "the relay is broken" rather
// than "the account was at capacity".
type shedSignal struct {
	// shed: an error frame classified as retryable (capacity, quota, or rate).
	shed bool
	// capacity: that shed was one of the two session-terminating capacity
	// codes, and was demoted on the way out so the CLI retries instead of
	// ending the session.
	capacity bool
}

// codexStreamResult reports the outcome of a Codex backend SSE relay so the
// caller can choose between a transparent retry (nothing reached the client yet)
// and a logged give-up (bytes already committed downstream — uninterruptible).
type codexStreamResult struct {
	sawTerminal bool   // a response.{completed,failed,...} event was relayed
	wroteAny    bool   // at least one byte was committed to the client
	events      int    // data: events relayed (diagnostics)
	bytes       int64  // bytes written downstream (diagnostics)
	err         error  // underlying read error when the stream broke early
	shed        string // non-empty when a pre-output capacity/quota frame was withheld
	// demoted: a shed that arrived after output had started, so it could only
	// be demoted (or forwarded) on the way out rather than withheld.
	demoted shedSignal
}

// errString renders an error for a log/record field, tolerating nil.
func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// extractCodexBackendUsageFromJSON reads usage from the ChatGPT Codex
// backend's response/event JSON, covering both shapes:
//
//	{"response":{"usage":{...}}}        ← streaming "response.completed"
//	{"usage":{...}}                     ← non-stream compact wrapper
//
// Cached input tokens are split out into Counts.CacheReadTokens so they're
// billed at the discounted rate.
func extractCodexBackendUsageFromJSON(body []byte) usage.Counts {
	if len(body) == 0 {
		return usage.Counts{}
	}
	var wrap struct {
		Response struct {
			Usage *openaiUsage `json:"usage"`
		} `json:"response"`
		Usage *openaiUsage `json:"usage"`
	}
	if err := json.Unmarshal(body, &wrap); err != nil {
		return usage.Counts{}
	}
	u := wrap.Response.Usage
	if u == nil {
		u = wrap.Usage
	}
	if u == nil {
		return usage.Counts{}
	}
	return u.toCounts()
}

// isCodexCapacityError detects the upstream's "model is at capacity"
// rejection so the current request can try another credential without
// treating a model-scoped outage as account quota. Strings come from
// CLIProxyAPI's codex_executor.go.
func isCodexCapacityError(body []byte) bool {
	lower := bytes.ToLower(body)
	return bytes.Contains(lower, []byte("selected model is at capacity")) ||
		bytes.Contains(lower, []byte("model is at capacity"))
}

// parseCodexResetAt extracts the reset timestamp from a usage_limit_reached
// error body. Supports both epoch-seconds and relative-seconds encodings:
//
//	{"error":{"type":"usage_limit_reached","resets_at":1716000000}}
//	{"error":{"type":"usage_limit_reached","resets_in_seconds":3600}}
func parseCodexResetAt(body []byte) time.Time {
	if len(body) == 0 {
		return time.Time{}
	}
	var wrap struct {
		Error struct {
			Type            string  `json:"type"`
			ResetsAt        int64   `json:"resets_at"`
			ResetsInSeconds float64 `json:"resets_in_seconds"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &wrap); err != nil {
		return time.Time{}
	}
	if wrap.Error.ResetsAt > 0 {
		return time.Unix(wrap.Error.ResetsAt, 0)
	}
	if wrap.Error.ResetsInSeconds > 0 {
		return time.Now().Add(time.Duration(wrap.Error.ResetsInSeconds) * time.Second)
	}
	return time.Time{}
}

// lineReader is a tiny buffered reader that preserves the original
// trailing newline so the passthrough writes the exact bytes the upstream
// sent (SSE is whitespace-sensitive).
type lineReader struct {
	buf []byte
	pos int
	src io.Reader
}

func newLineReader(r io.Reader) *lineReader { return &lineReader{src: r, buf: make([]byte, 0, 8192)} }

func (lr *lineReader) readLine() ([]byte, error) {
	for {
		if idx := bytes.IndexByte(lr.buf[lr.pos:], '\n'); idx >= 0 {
			line := lr.buf[lr.pos : lr.pos+idx+1]
			lr.pos += idx + 1
			if lr.pos >= len(lr.buf) {
				lr.buf = lr.buf[:0]
				lr.pos = 0
			}
			return line, nil
		}
		// Shift remaining unread bytes to the start before the next read
		// so we don't grow the buffer unbounded on a slow stream.
		if lr.pos > 0 {
			copy(lr.buf, lr.buf[lr.pos:])
			lr.buf = lr.buf[:len(lr.buf)-lr.pos]
			lr.pos = 0
		}
		chunk := make([]byte, 4096)
		n, err := lr.src.Read(chunk)
		if n > 0 {
			lr.buf = append(lr.buf, chunk[:n]...)
		}
		if err != nil {
			// Flush any tail bytes without a terminator on EOF.
			if lr.pos < len(lr.buf) {
				rest := lr.buf[lr.pos:]
				lr.pos = len(lr.buf)
				return rest, err
			}
			return nil, err
		}
	}
}
