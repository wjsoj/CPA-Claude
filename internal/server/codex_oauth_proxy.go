package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"github.com/wjsoj/cc-core/auth"
	"github.com/wjsoj/cc-core/mimicry"
	"github.com/wjsoj/cc-core/requestlog"
	"github.com/wjsoj/cc-core/usage"
)

// The ChatGPT Codex backend expects the OpenAI /v1/responses schema with a
// handful of upstream-private fields stripped. The upstream request headers
// (Originator / User-Agent / Version / OpenAI-Beta / Chatgpt-Account-Id) are
// applied by mimicry.ApplyCodexCLIHeaders, pinned to codex-tui/0.135.0 — see
// cc-core/crack/codex/SPEC.md and cc-core/mimicry/codex.go.

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
	// Apply the Codex CLI fingerprint — codex-tui/0.135.0 identity (Originator /
	// User-Agent / Version) over the legacy HTTP POST /codex/responses{,/compact}
	// path. Centralized in cc-core (mimicry.ApplyCodexCLIHeaders) so every relay
	// stays in lockstep when the version target is bumped. See cc-core/crack/codex/SPEC.md.
	mimicry.ApplyCodexCLIHeaders(upReq, accessToken, accountID, isCompactPath)

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
		resetAt := parseCodexResetAt(errBody)
		if resetAt.IsZero() {
			resetAt = parseRetryAfter(resp.Header)
		}
		s.pool.ReportUpstreamError(a, resp.StatusCode, resetAt)
		log.Warnf("codex oauth: credential %s received %d: %s", a.ID, resp.StatusCode, truncate(errBody, 240))
		return true, false
	}
	// Capacity errors come back with 200+JSON on some edge deployments or
	// as 4xx; the body message is what we actually key on.
	if resp.StatusCode >= 400 {
		errBody, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if isCodexCapacityError(errBody) {
			resetAt := parseCodexResetAt(errBody)
			s.pool.ReportUpstreamError(a, http.StatusTooManyRequests, resetAt)
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
		// Drop hop-by-hop / encoding headers; we've already consumed and may
		// be sending different bytes than the upstream advertised.
		for k, v := range resp.Header {
			if strings.EqualFold(k, "Content-Length") || strings.EqualFold(k, "Transfer-Encoding") || strings.EqualFold(k, "Content-Encoding") {
				continue
			}
			for _, val := range v {
				c.Writer.Header().Add(k, val)
			}
		}
		c.Writer.Header().Set("Content-Type", "application/json")
		c.Writer.WriteHeader(resp.StatusCode)
		c.Writer.Write(payload)
	} else if stream {
		// Streaming client: passthrough SSE verbatim (with keepalive + terminal
		// tracking). Headers are committed lazily inside the relay, so a break
		// before the first byte reaches the client is recoverable.
		res := streamSSECodexBackend(c, resp, &counts, func() { writeResponseHeaders(c, resp) })
		if !res.sawTerminal && !res.wroteAny {
			// Nothing reached the client yet. If the client itself went away,
			// there's nobody to retry for; otherwise transparently fail over to
			// another credential (same contract as the pre-response path above).
			_ = resp.Body.Close()
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
			// Bytes already went downstream — can't restart cleanly. Record the
			// truncation richly so it's visible in logs + the request log
			// instead of looking like a clean stream end.
			streamErr = fmt.Sprintf("stream truncated mid-flight after %d event(s)/%dB: %v", res.events, res.bytes, res.err)
			if isClientDisconnect(ctx, res.err) {
				log.Infof("codex oauth: client disconnected mid-stream via %s (attempt %d, events=%d, bytes=%d, %s)",
					a.ID, attempts, res.events, res.bytes, time.Since(start).Round(time.Millisecond))
			} else {
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
		// Drop the upstream's Content-Type: we're returning JSON, not SSE.
		for k, v := range resp.Header {
			if strings.EqualFold(k, "Content-Type") || strings.EqualFold(k, "Content-Length") || strings.EqualFold(k, "Transfer-Encoding") {
				continue
			}
			for _, val := range v {
				c.Writer.Header().Add(k, val)
			}
		}
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
		multiplier, billed = s.saas.SettleCharge(c.Request.Context(),
			clientToken, auth.ProviderOpenAI, model, costUSD,
			"codex-oauth:"+a.ID)
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
		Status:      resp.StatusCode,
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
	var res codexStreamResult
	var mu sync.Mutex
	committed := false
	lastWrite := time.Now()

	// write commits the headers on first use (so the caller can still retry up
	// to that point), then writes + flushes under the mutex.
	write := func(b []byte) {
		mu.Lock()
		defer mu.Unlock()
		if !committed {
			if commit != nil {
				commit()
			}
			committed = true
			res.wroteAny = true
		}
		n, _ := c.Writer.Write(b)
		res.bytes += int64(n)
		if flusher != nil {
			flusher.Flush()
		}
		lastWrite = time.Now()
	}

	// Keepalive: after >=10s of downstream silence, emit an SSE comment line
	// (":\n\n", ignored by SSE clients) to keep the connection warm. Only runs
	// once the stream has started — before the first real event the window must
	// stay write-free so the caller can still fail over to another credential.
	const keepaliveIdle = 10 * time.Second
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				mu.Lock()
				idle := time.Since(lastWrite)
				active := committed
				mu.Unlock()
				if active && idle >= keepaliveIdle {
					write([]byte(":\n\n"))
				}
			}
		}
	}()
	// LIFO: close(done) first, then wait for the goroutine to exit before
	// returning, so no keepalive write races the caller's resp.Body.Close().
	defer wg.Wait()
	defer close(done)

	reader := newLineReader(resp.Body)
	for {
		line, err := reader.readLine()
		if len(line) > 0 {
			trim := bytes.TrimRight(line, "\r\n")
			if bytes.HasPrefix(trim, []byte("data:")) {
				payload := bytes.TrimSpace(trim[5:])
				if len(payload) > 0 && payload[0] == '{' {
					res.events++
					counts.Add(extractCodexBackendUsageFromJSON(payload))
					if codexTerminalEvent(payload) {
						res.sawTerminal = true
					}
				}
			}
			write(line)
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				res.err = err
			} else if !res.sawTerminal {
				// Clean EOF but no terminal event → upstream truncated us.
				res.err = io.ErrUnexpectedEOF
			}
			return res
		}
	}
}

// codexStreamResult reports the outcome of a Codex backend SSE relay so the
// caller can choose between a transparent retry (nothing reached the client yet)
// and a logged give-up (bytes already committed downstream — uninterruptible).
type codexStreamResult struct {
	sawTerminal bool  // a response.{completed,failed,...} event was relayed
	wroteAny    bool  // at least one byte was committed to the client
	events      int   // data: events relayed (diagnostics)
	bytes       int64 // bytes written downstream (diagnostics)
	err         error // underlying read error when the stream broke early
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
// rejection so the picker cools down this credential without giving up on
// the request. Strings come from CLIProxyAPI's codex_executor.go.
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
