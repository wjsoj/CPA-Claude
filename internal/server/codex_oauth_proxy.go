package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"github.com/wjsoj/CPA-Claude/internal/auth"
	"github.com/wjsoj/CPA-Claude/internal/requestlog"
	"github.com/wjsoj/CPA-Claude/internal/usage"
)

// The ChatGPT Codex backend expects the OpenAI /v1/responses schema with a
// handful of upstream-private fields stripped. These names mirror the
// headers and behavior of the vendor Codex CLI (version 0.118.0 at the
// time of writing) — not copying them will get the request rejected.
const (
	codexBackendUserAgent  = "codex-tui/0.118.0 (Mac OS 26.3.1; arm64) iTerm.app/3.6.9 (codex-tui; 0.118.0)"
	codexBackendOriginator = "codex-tui"
)

// codexOAuthPath maps the client-facing route to the ChatGPT backend
// suffix. Only /v1/responses is supported on the OAuth path — the backend
// doesn't host a /v1/chat/completions endpoint and we can't fake one
// without a full OpenAI-chat → Codex-responses translator (out of scope).
func codexOAuthPath(stream bool) string {
	if stream {
		return "/responses"
	}
	return "/responses/compact"
}

// sanitizeCodexRequestBody shapes the client's /v1/responses body into what
// the ChatGPT Codex backend expects. Behavior is modeled directly on
// CLIProxyAPI's Execute / executeCompact bodies (codex_executor.go:176-183,
// 327-330): strip the thinking suffix off `model`, force-enable streaming
// on the /responses path, force-disable on /responses/compact, drop four
// upstream-private fields, backfill empty `instructions`, and ensure an
// image_generation tool is registered (skipped for *-spark models the
// backend rejects it on).
func sanitizeCodexRequestBody(body []byte, stream bool) ([]byte, error) {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return body, err
	}
	// Strip thinking suffix from model. CLIProxyAPI uses "model-name(value)"
	// convention (e.g. gpt-5.3-codex(high)); the backend wants just the
	// base model name. Plain model names are passed through untouched.
	baseModel := ""
	if m, ok := raw["model"].(string); ok {
		baseModel = stripThinkingSuffix(m)
		raw["model"] = baseModel
	}
	if stream {
		raw["stream"] = true
	} else {
		delete(raw, "stream")
	}
	for _, k := range []string{"previous_response_id", "prompt_cache_retention", "safety_identifier", "stream_options"} {
		delete(raw, k)
	}
	if v, ok := raw["instructions"]; !ok || v == nil {
		raw["instructions"] = ""
	}
	raw["tools"] = ensureImageGenerationTool(raw["tools"], baseModel)
	return json.Marshal(raw)
}

// stripThinkingSuffix mirrors thinking.ParseSuffix from CLIProxyAPI: a
// trailing "(value)" group (e.g. "gpt-5.3-codex(high)") is removed and the
// bare model name returned. Names without the suffix form are untouched.
func stripThinkingSuffix(model string) string {
	if !strings.HasSuffix(model, ")") {
		return model
	}
	i := strings.LastIndex(model, "(")
	if i <= 0 {
		return model
	}
	return model[:i]
}

// ensureImageGenerationTool guarantees the tools array has an entry of
// type=image_generation. The ChatGPT backend injects this server-side on
// the vendor CLI's requests; if we strip it (or the client omits it)
// responses with image-generation prompts fail. Skipped for "*-spark"
// models the backend rejects the tool on (matches CLIProxyAPI).
func ensureImageGenerationTool(current any, baseModel string) any {
	if strings.HasSuffix(baseModel, "spark") {
		if current == nil {
			return []any{}
		}
		return current
	}
	imageTool := map[string]any{"type": "image_generation", "output_format": "png"}
	arr, ok := current.([]any)
	if !ok || arr == nil {
		return []any{imageTool}
	}
	for _, t := range arr {
		if tm, _ := t.(map[string]any); tm != nil {
			if typ, _ := tm["type"].(string); typ == "image_generation" {
				return arr
			}
		}
	}
	return append(arr, imageTool)
}

// doForwardCodexOAuth forwards the client's /v1/responses request to the
// ChatGPT backend. Behavior matches the vendor Codex CLI: Bearer auth from
// the OAuth access_token, account_id from the cached ID-token claims, a
// fresh per-request session UUID, and the `codex-tui` User-Agent /
// Originator that the backend fingerprints on.
func (s *Server) doForwardCodexOAuth(c *gin.Context, a *auth.Auth, path string, body []byte, stream bool, model, clientToken, clientName string, start time.Time, attempts int) (retry, done bool) {
	if path != "/v1/responses" {
		c.AbortWithStatusJSON(http.StatusNotImplemented, gin.H{
			"error": "codex OAuth backend only supports /v1/responses (clients using chat/completions must use an API-key credential)",
		})
		s.emitLog(requestlog.Record{
			Client: clientName, ClientToken: maskClientToken(clientToken), Provider: auth.ProviderOpenAI,
			AuthID: a.ID, AuthLabel: a.Label, AuthKind: "oauth", Model: model,
			Stream: stream, Path: path, Status: http.StatusNotImplemented, Attempts: attempts,
			DurationMs: time.Since(start).Milliseconds(),
			Error:      "codex oauth path not supported",
		})
		return false, true
	}

	snap := a.Snapshot()
	baseURL := strings.TrimRight(s.cfg.ChatGPTBackendBaseURL, "/") + "/codex"
	// Per-credential base URL override is allowed for vendor-relay setups.
	if ab := strings.TrimRight(snap.BaseURL, "/"); ab != "" {
		baseURL = ab
	}
	upURL := baseURL + codexOAuthPath(stream)

	upstreamBody, err := sanitizeCodexRequestBody(body, stream)
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

	accessToken, _ := a.Credentials()
	upReq.Header.Set("Authorization", "Bearer "+accessToken)
	upReq.Header.Set("Content-Type", "application/json")
	if stream {
		upReq.Header.Set("Accept", "text/event-stream")
	} else {
		upReq.Header.Set("Accept", "application/json")
	}
	upReq.Header.Set("Connection", "Keep-Alive")
	upReq.Header.Set("Session_id", newRequestUUID())
	upReq.Header.Set("Originator", codexBackendOriginator)
	if ua := upReq.Header.Get("User-Agent"); ua == "" {
		upReq.Header.Set("User-Agent", codexBackendUserAgent)
	}
	if accountID, _ := a.CodexIdentity(); accountID != "" {
		upReq.Header.Set("Chatgpt-Account-Id", accountID)
	}

	client := auth.ClientFor(snap.ProxyURL, false)
	resp, err := client.Do(upReq)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || isClientDisconnect(err) {
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
		a.MarkFailure(err.Error())
		log.Warnf("codex oauth: upstream error via %s: %v", a.ID, err)
		return true, false
	}

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

	writeResponseHeaders(c, resp)
	var counts usage.Counts
	if stream && strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		streamSSECodexBackend(c, resp, &counts)
	} else {
		respBody, _ := io.ReadAll(resp.Body)
		c.Writer.Write(respBody)
		counts.Add(extractCodexBackendUsageFromJSON(respBody))
	}
	_ = resp.Body.Close()

	s.usage.Record(a.ID, a.Label, counts)
	var costUSD float64
	if resp.StatusCode < 400 && counts.Requests > 0 && clientToken != "" {
		costUSD = s.pricing.Cost(auth.ProviderOpenAI, model, counts)
		s.usage.RecordClient(clientToken, clientName, counts, costUSD)
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
		Status:      resp.StatusCode,
		DurationMs:  time.Since(start).Milliseconds(),
		Stream:      stream,
		Path:        path,
		Attempts:    attempts,
	})
	if resp.StatusCode < 400 {
		a.MarkSuccess()
	}
	return false, true
}

// streamSSECodexBackend is the Codex backend SSE passthrough. The format
// differs from OpenAI's API-key response: events carry JSON payloads
// structured as `response.completed` / `response.output_item.done` etc.
// Usage arrives inside the `response.completed` event as
// `response.usage.{input_tokens, output_tokens, input_tokens_details.cached_tokens}`.
func streamSSECodexBackend(c *gin.Context, resp *http.Response, counts *usage.Counts) {
	flusher, _ := c.Writer.(http.Flusher)
	reader := newLineReader(resp.Body)
	for {
		line, err := reader.readLine()
		if len(line) > 0 {
			trim := bytes.TrimRight(line, "\r\n")
			if bytes.HasPrefix(trim, []byte("data:")) {
				payload := bytes.TrimSpace(trim[5:])
				if len(payload) > 0 && payload[0] == '{' {
					counts.Add(extractCodexBackendUsageFromJSON(payload))
				}
			}
			c.Writer.Write(line)
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			break
		}
	}
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
