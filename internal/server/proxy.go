package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"github.com/wjsoj/CPA-Claude/internal/auth"
	"github.com/wjsoj/CPA-Claude/internal/requestlog"
	"github.com/wjsoj/CPA-Claude/internal/usage"
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
	s.forward(c, "/v1/messages")
}

func (s *Server) handleCountTokens(c *gin.Context) {
	s.forward(c, "/v1/messages/count_tokens")
}

func (s *Server) forward(c *gin.Context, path string) {
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

	// Weekly-budget pre-check. If this client has a configured budget and
	// their current-week spend already met or exceeded it, reject with 429.
	// A request in flight may slightly overshoot (by one request's cost);
	// that's the acceptable trade-off for not knowing output tokens upfront.
	if budget, ok := s.budgets[clientToken]; ok {
		spent := s.usage.WeeklyCostUSD(clientToken)
		if spent >= budget.WeeklyUSD {
			c.Header("Retry-After", "604800")
			c.AbortWithStatusJSON(429, gin.H{
				"error":     "weekly budget exceeded",
				"spent_usd": spent,
				"limit_usd": budget.WeeklyUSD,
				"week":      s.usage.CurrentWeekKey(),
			})
			s.emitLog(requestlog.Record{
				Client:      clientName,
				ClientToken: maskClientToken(clientToken),
				Model:       model,
				Stream:      peek.Stream,
				Path:        path,
				Status:      429,
				DurationMs:  time.Since(start).Milliseconds(),
				Error:       "weekly budget exceeded",
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
		a := s.pool.Acquire(c.Request.Context(), clientToken)
		if a == nil {
			c.AbortWithStatusJSON(503, gin.H{"error": "no upstream credentials available"})
			s.emitLog(requestlog.Record{
				Client: clientName, ClientToken: maskClientToken(clientToken), Model: model,
				Stream: peek.Stream, Path: path, Status: 503, Attempts: attempts,
				DurationMs: time.Since(start).Milliseconds(), Error: "no upstream credentials available",
			})
			return
		}
		if tried[a.ID] {
			c.AbortWithStatusJSON(503, gin.H{"error": "all upstream credentials exhausted"})
			s.emitLog(requestlog.Record{
				Client: clientName, ClientToken: maskClientToken(clientToken), Model: model,
				Stream: peek.Stream, Path: path, Status: 503, Attempts: attempts,
				DurationMs: time.Since(start).Milliseconds(), Error: "all upstream credentials exhausted",
			})
			return
		}
		tried[a.ID] = true
		attempts++

		retry, done := s.doForward(c, a, path, body, peek.Stream, model, clientToken, clientName, start, attempts)
		if done {
			s.pool.Release(clientToken)
			return
		}
		if !retry {
			s.pool.Release(clientToken)
			return
		}
		log.Warnf("proxy: retrying with a different credential (last auth=%s)", a.ID)
	}
	c.AbortWithStatusJSON(503, gin.H{"error": "upstream retries exhausted"})
	s.emitLog(requestlog.Record{
		Client: clientName, ClientToken: maskClientToken(clientToken), Model: model,
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

func maskClientToken(t string) string {
	if len(t) <= 10 {
		return "***"
	}
	return t[:6] + "…" + t[len(t)-4:]
}

// doForward sends the request with one credential. Returns (retry, done):
//
//	retry=true  → caller should try another credential
//	done=true   → response was delivered successfully (status < 400 or
//	              non-retryable error already written to client)
func (s *Server) doForward(c *gin.Context, a *auth.Auth, path string, body []byte, stream bool, model, clientToken, clientName string, start time.Time, attempts int) (retry bool, done bool) {
	baseURL := s.cfg.AnthropicBaseURL
	// Per-credential base URL override (used for relay/midstream vendors on
	// API-key credentials).
	if ab := strings.TrimRight(a.Snapshot().BaseURL, "/"); ab != "" {
		baseURL = ab
	}
	url := baseURL + path + "?beta=true"

	ctx := c.Request.Context()
	upReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		c.AbortWithStatusJSON(500, gin.H{"error": err.Error()})
		return false, true
	}

	// Forward selected client headers.
	copyForwardableHeaders(c.Request.Header, upReq.Header)

	// Anthropic auth headers.
	applyAnthropicHeaders(upReq, a, stream)

	client := auth.ClientFor(a.ProxyURL, s.cfg.UseUTLS)
	resp, err := client.Do(upReq)
	if err != nil {
		a.MarkFailure(err.Error())
		log.Warnf("proxy: upstream error via %s: %v", a.ID, err)
		return true, false
	}

	// Retryable status codes: 429 (quota), 401/403 (auth bad / oauth needs fallback), 529 (overloaded).
	if resp.StatusCode == 429 || resp.StatusCode == 401 || resp.StatusCode == 403 || resp.StatusCode == 529 {
		resetAt := parseRetryAfter(resp.Header)
		s.pool.ReportUpstreamError(a, resp.StatusCode, resetAt)
		errBody, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		log.Warnf("proxy: %s returned %d — will fall back. body=%s", a.ID, resp.StatusCode, truncate(errBody, 500))
		return true, false
	}

	// Success or non-retryable error — stream response body to client.
	writeResponseHeaders(c, resp)

	var counts usage.Counts
	counts.Requests = 1
	if resp.StatusCode >= 400 {
		counts.Errors = 1
	}

	if stream && strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		streamSSE(c, resp, &counts)
	} else {
		respBody, _ := io.ReadAll(resp.Body)
		c.Writer.Write(respBody)
		counts.Add(extractUsageFromJSON(respBody))
	}
	_ = resp.Body.Close()
	s.usage.Record(a.ID, a.Label, counts)
	// Charge the client for the tokens they actually consumed.
	var costUSD float64
	if resp.StatusCode < 400 && counts.Requests > 0 && clientToken != "" {
		costUSD = s.pricing.Cost(model, counts)
		label := clientName
		if label == "" {
			if b, ok := s.budgets[clientToken]; ok {
				label = b.Label
			}
		}
		s.usage.RecordClient(clientToken, label, counts, costUSD)
	}
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
		Input:       counts.InputTokens,
		Output:      counts.OutputTokens,
		CacheRead:   counts.CacheReadTokens,
		CacheCreate: counts.CacheCreateTokens,
		CostUSD:     costUSD,
		Status:      resp.StatusCode,
		DurationMs:  time.Since(start).Milliseconds(),
		Stream:      stream,
		Path:        path,
		Attempts:    attempts,
	})
	return false, true
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

func applyAnthropicHeaders(req *http.Request, a *auth.Auth, stream bool) {
	token, kind := a.Credentials()

	if kind == auth.KindAPIKey {
		req.Header.Del("Authorization")
		req.Header.Set("x-api-key", token)
	} else {
		req.Header.Del("x-api-key")
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	if req.Header.Get("Anthropic-Version") == "" {
		req.Header.Set("Anthropic-Version", "2023-06-01")
	}
	if req.Header.Get("Anthropic-Beta") == "" && kind == auth.KindOAuth {
		req.Header.Set("Anthropic-Beta", "oauth-2025-04-20,claude-code-20250219,interleaved-thinking-2025-05-14,context-management-2025-06-27,prompt-caching-scope-2026-01-05")
	}
	req.Header.Set("Accept-Encoding", "identity")
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}
}

// streamSSE copies SSE events to the client as they arrive and parses
// message_delta events to accumulate usage.
func streamSSE(c *gin.Context, resp *http.Response, counts *usage.Counts) {
	flusher, _ := c.Writer.(http.Flusher)
	reader := bufio.NewReaderSize(resp.Body, 64*1024)
	var curEvent string
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			c.Writer.Write(line)
			if flusher != nil {
				flusher.Flush()
			}
			// Track event name to parse only data lines that follow.
			trim := bytes.TrimRight(line, "\r\n")
			if bytes.HasPrefix(trim, []byte("event:")) {
				curEvent = strings.TrimSpace(string(trim[6:]))
			} else if bytes.HasPrefix(trim, []byte("data:")) && (curEvent == "message_start" || curEvent == "message_delta") {
				payload := bytes.TrimSpace(trim[5:])
				counts.Add(extractUsageFromSSE(payload))
			}
		}
		if err != nil {
			break
		}
	}
}

type usageJSON struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
}

func (u usageJSON) toCounts() usage.Counts {
	return usage.Counts{
		InputTokens:       u.InputTokens,
		OutputTokens:      u.OutputTokens,
		CacheCreateTokens: u.CacheCreationInputTokens,
		CacheReadTokens:   u.CacheReadInputTokens,
	}
}

// extractUsageFromJSON pulls the top-level "usage" from a non-streaming
// /v1/messages response.
func extractUsageFromJSON(body []byte) usage.Counts {
	var wrap struct {
		Usage usageJSON `json:"usage"`
	}
	_ = json.Unmarshal(body, &wrap)
	return wrap.Usage.toCounts()
}

// extractUsageFromSSE parses a single SSE data payload.
// message_start:    {type: "message_start", message: {usage: {...}}}
// message_delta:    {type: "message_delta", usage: {...}}
func extractUsageFromSSE(payload []byte) usage.Counts {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(payload, &probe); err != nil {
		return usage.Counts{}
	}
	if raw, ok := probe["usage"]; ok {
		var u usageJSON
		if err := json.Unmarshal(raw, &u); err == nil {
			return u.toCounts()
		}
	}
	if raw, ok := probe["message"]; ok {
		var nested struct {
			Usage usageJSON `json:"usage"`
		}
		if err := json.Unmarshal(raw, &nested); err == nil {
			return nested.Usage.toCounts()
		}
	}
	return usage.Counts{}
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

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "...(truncated)"
}

// unused — kept to avoid import churn if future error types are added.
var _ = fmt.Sprintf
var _ = context.Background
