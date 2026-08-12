package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/wjsoj/cc-core/auth"
	"github.com/wjsoj/cc-core/mimicry"
	"github.com/wjsoj/cc-core/pricing"
	"github.com/wjsoj/cc-core/thinkingsig"
	"github.com/wjsoj/cc-core/usage"

	"github.com/wjsoj/CPA-Claude/internal/config"
)

// These tests pin the upstream *health signal*, which is a different question
// from "what did the client get". The rule under test throughout:
//
//	a <400 status line is NOT evidence the exchange worked
//
// A relay that has broken after committing its status line can only report the
// failure in the body — an error envelope under a 200, or an SSE stream whose
// only terminal event is `error`. Recording those as successes is what let a
// channel alternating between a real 500 and a 200-wrapped error run forever
// without accumulating a single strike: MarkSuccess wipes
// ConsecutiveFailures/Consecutive429s/Consecutive401s AND the API-key breaker's
// quarantine state, so every second request reset the evidence.

// newHealthTestServer is newDoForwardTestServer plus the billing machinery the
// <400 success path touches (usage ledger + pricing catalog). The failover-only
// tests can leave those nil; these tests run responses to completion, which is
// the whole point.
func newHealthTestServer(upstreamURL string, creds ...*auth.Auth) *Server {
	return &Server{
		cfg:           &config.Config{AnthropicBaseURL: upstreamURL, UseUTLS: false},
		pool:          auth.NewPool(creds, nil, 10*time.Minute, false, ""),
		usage:         usage.OpenInMemory(),
		pricing:       pricing.NewCatalog(pricing.Config{}),
		switchTracker: thinkingsig.NewSwitchTracker(),
	}
}

func consecutiveFailures(t *testing.T, a *auth.Auth) int {
	t.Helper()
	_, _, _, n := a.HealthSnapshot()
	return n
}

const (
	sseErrorOnly = "event: error\n" +
		`data: {"type":"error","error":{"type":"overloaded_error","message":"upstream backend unavailable"}}` + "\n\n"
	sseComplete = "event: message_start\n" +
		`data: {"type":"message_start","message":{"model":"claude-haiku-4-5-20251001","usage":{"input_tokens":12,"output_tokens":1}}}` + "\n\n" +
		"event: message_delta\n" +
		`data: {"type":"message_delta","usage":{"output_tokens":7}}` + "\n\n" +
		"event: message_stop\n" +
		`data: {"type":"message_stop"}` + "\n\n"
	// A stream that stops after message_start: no message_stop, no error.
	sseTruncated = "event: message_start\n" +
		`data: {"type":"message_start","message":{"model":"claude-haiku-4-5-20251001","usage":{"input_tokens":12,"output_tokens":1}}}` + "\n\n"
)

var streamingHaikuBody = []byte(`{"model":"claude-haiku-4-5-20251001","stream":true,"messages":[{"role":"user","content":"hi"}]}`)

func sseUpstream(body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}))
}

func jsonUpstream(status int, body string, hdr map[string]string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		for k, v := range hdr {
			w.Header().Set(k, v)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

func callAPIKey(t *testing.T, s *Server, cred *auth.Auth, body []byte, stream bool) (bool, bool, *httptest.ResponseRecorder) {
	t.Helper()
	c, w := newMessagesContext(t, body)
	retry, done, _ := s.doForwardAnthropicAPIKey(c, cred, "/v1/messages", body, stream,
		"claude-haiku-4-5-20251001", "tok-abcdef123456", "client", time.Now(), 1)
	return retry, done, w
}

func callOAuth(t *testing.T, s *Server, cred *auth.Auth, body []byte, stream bool) (bool, bool, *httptest.ResponseRecorder) {
	t.Helper()
	c, w := newMessagesContext(t, body)
	retry, done, _ := s.doForward(c, cred, "/v1/messages", body, stream,
		"claude-haiku-4-5-20251001", "tok-abcdef123456", "slot-1", "client", time.Now(), 1, false,
		mimicry.BodyTransformResult{})
	return retry, done, w
}

// ---------------------------------------------------------------------------
// 200 + error body
// ---------------------------------------------------------------------------

// A 200 carrying an Anthropic error envelope is a failure. This is the single
// most consequential case: it is how a relay reports its backend dying once it
// has already committed the status line, and it used to reset the credential's
// entire failure history.
func TestAPIKey200WithErrorBodyIsAFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	up := jsonUpstream(http.StatusOK,
		`{"type":"error","error":{"type":"api_error","message":"backend unavailable"}}`, nil)
	defer up.Close()

	cred := apiKeyTestCred("relayErrBody")
	s := newHealthTestServer(up.URL, cred)

	_, done, _ := callAPIKey(t, s, cred, haikuBody, false)
	if !done {
		t.Fatal("a 200 is still forwarded to the client — only the health verdict changes")
	}
	if got := consecutiveFailures(t, cred); got != 1 {
		t.Fatalf("ConsecutiveFailures = %d, want 1 — a 200 wrapping an error envelope is a failed exchange", got)
	}
}

func TestOAuth200WithErrorBodyIsAFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	up := jsonUpstream(http.StatusOK,
		`{"type":"error","error":{"type":"api_error","message":"backend unavailable"}}`, nil)
	defer up.Close()

	cred := oauthTestCredID("credErrBody", "tokenA")
	s := newHealthTestServer(up.URL, cred)

	callOAuth(t, s, cred, haikuBody, false)
	if got := consecutiveFailures(t, cred); got != 1 {
		t.Fatalf("ConsecutiveFailures = %d, want 1 — a 200 wrapping an error envelope is a failed exchange", got)
	}
}

// The looser relay shape (`{"error":…}` with no "type") counts too — several
// gateways emit exactly that instead of the Anthropic envelope.
func TestAPIKey200WithBareErrorFieldIsAFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	up := jsonUpstream(http.StatusOK, `{"error":{"message":"no channel available"}}`, nil)
	defer up.Close()

	cred := apiKeyTestCred("relayBareErr")
	s := newHealthTestServer(up.URL, cred)

	callAPIKey(t, s, cred, haikuBody, false)
	if got := consecutiveFailures(t, cred); got != 1 {
		t.Fatalf("ConsecutiveFailures = %d, want 1 — a bare {\"error\":…} under a 200 is still an error", got)
	}
}

// The counterpart guard: an ordinary Messages response must never trip the
// error-envelope detector, or every successful request would count as a
// failure. `stop_reason` and friends are not errors.
func TestOrdinaryResponseIsNotAnErrorEnvelope(t *testing.T) {
	for _, body := range []string{
		`{"id":"msg_1","type":"message","role":"assistant","stop_reason":"end_turn","usage":{"input_tokens":9,"output_tokens":3}}`,
		`{"id":"msg_1","type":"message","error":null,"usage":{"input_tokens":9,"output_tokens":3}}`,
	} {
		if isErr, _ := bodyLooksLikeAPIError([]byte(body)); isErr {
			t.Fatalf("a normal Messages response must not read as an error envelope: %s", body)
		}
	}
	if isErr, _ := bodyLooksLikeAPIError([]byte(`{"type":"error","error":{"message":"x"}}`)); !isErr {
		t.Fatal("the Anthropic error envelope must be detected")
	}
}

// ---------------------------------------------------------------------------
// 200 + `event: error` SSE
// ---------------------------------------------------------------------------

// SSE cannot retract a 200, so `event: error` is the ONLY way an upstream can
// report a failure mid-stream. It is terminal — the relay stops — but terminal
// and healthy are not the same thing, and treating them as one meant a stream
// consisting of nothing but an error frame was recorded as a clean success.
func TestAPIKeyErrorOnlySSEIsAFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	up := sseUpstream(sseErrorOnly)
	defer up.Close()

	cred := apiKeyTestCred("relayErrSSE")
	s := newHealthTestServer(up.URL, cred)

	_, _, w := callAPIKey(t, s, cred, streamingHaikuBody, true)
	if got := consecutiveFailures(t, cred); got != 1 {
		t.Fatalf("ConsecutiveFailures = %d, want 1 — an error-only SSE stream is a failed exchange", got)
	}
	// The client still receives the frame: this changes bookkeeping, not what
	// is served.
	if w.Body.Len() == 0 {
		t.Fatal("the error frame must still reach the client")
	}
}

func TestOAuthErrorOnlySSEIsAFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	up := sseUpstream(sseErrorOnly)
	defer up.Close()

	cred := oauthTestCredID("credErrSSE", "tokenA")
	s := newHealthTestServer(up.URL, cred)

	callOAuth(t, s, cred, streamingHaikuBody, true)
	if got := consecutiveFailures(t, cred); got != 1 {
		t.Fatalf("ConsecutiveFailures = %d, want 1 — an error-only SSE stream is a failed exchange", got)
	}
}

// streamSSE must report the two facts separately, which is what makes the
// distinction above expressible at all.
func TestStreamSSEReportsErrorTerminalSeparately(t *testing.T) {
	c, _ := newClaudeStreamCtx()
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(sseErrorOnly))}
	var counts usage.Counts
	res := streamSSE(c, resp, &counts, nil, "", nil)

	if !res.sawTerminal {
		t.Error("an error frame ends the stream, so sawTerminal must stay true")
	}
	if !res.sawError {
		t.Error("sawError must distinguish `event: error` from `message_stop`")
	}
	if res.errDetail == "" {
		t.Error("the error frame should be captured for the health note")
	}
}

// ---------------------------------------------------------------------------
// truncation and clean completion
// ---------------------------------------------------------------------------

// A stream cut off before its terminal event is health-NEUTRAL: the cut
// usually happens several hops upstream, so blaming this credential convicts
// the wrong party — but it must not count as a success either, or a channel
// that truncates every response looks permanently healthy.
func TestTruncatedStreamIsNeitherSuccessNorFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	up := sseUpstream(sseTruncated)
	defer up.Close()

	cred := apiKeyTestCred("relayTruncated")
	s := newHealthTestServer(up.URL, cred)
	// Pre-load a failure so a stray MarkSuccess would be visible as a reset.
	cred.MarkFailure("seed")

	callAPIKey(t, s, cred, streamingHaikuBody, true)

	if got := consecutiveFailures(t, cred); got != 1 {
		t.Fatalf("ConsecutiveFailures = %d, want 1 — a truncation must neither clear nor add to the failure history", got)
	}
}

// The control case for everything above: a complete stream ending in
// message_stop is a success and clears the failure history.
func TestCompleteStreamMarksSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	up := sseUpstream(sseComplete)
	defer up.Close()

	cred := apiKeyTestCred("relayHealthy")
	s := newHealthTestServer(up.URL, cred)
	cred.MarkFailure("seed")

	_, done, _ := callAPIKey(t, s, cred, streamingHaikuBody, true)
	if !done {
		t.Fatal("a complete stream terminates the request")
	}
	if got := consecutiveFailures(t, cred); got != 0 {
		t.Fatalf("ConsecutiveFailures = %d, want 0 — a stream ending in message_stop is a genuine success", got)
	}
}

func TestCompleteNonStreamMarksSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	up := jsonUpstream(http.StatusOK,
		`{"id":"msg_1","type":"message","role":"assistant","usage":{"input_tokens":9,"output_tokens":3}}`, nil)
	defer up.Close()

	cred := apiKeyTestCred("relayHealthyJSON")
	s := newHealthTestServer(up.URL, cred)
	cred.MarkFailure("seed")

	callAPIKey(t, s, cred, haikuBody, false)
	if got := consecutiveFailures(t, cred); got != 0 {
		t.Fatalf("ConsecutiveFailures = %d, want 0 — a well-formed 200 is a success", got)
	}
}

// ---------------------------------------------------------------------------
// transport errors: failover, not a bare 502
// ---------------------------------------------------------------------------

// A transport error on an API-key credential used to end the request with a
// 502, so a fleet of keys behaved like a fleet of one — the remaining keys were
// never tried. It must roll back to the failover loop instead.
func TestAPIKeyTransportErrorRetries(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := dead.URL
	dead.Close() // nothing is listening now: client.Do fails at the transport

	cred := apiKeyTestCred("relayDeadSocket")
	s := newHealthTestServer(deadURL, cred)

	retry, done, w := callAPIKey(t, s, cred, haikuBody, false)
	if !retry || done {
		t.Fatalf("a transport error must roll back to the failover loop; got retry=%v done=%v", retry, done)
	}
	if w.Body.Len() != 0 {
		t.Fatalf("nothing may be written downstream, or the retry stops being transparent; got %q", w.Body.String())
	}
	if got := consecutiveFailures(t, cred); got != 1 {
		t.Fatalf("ConsecutiveFailures = %d, want 1 — an unreachable upstream is a fault", got)
	}
}

// End to end through the failover loop: the first API key's socket is dead, the
// second serves the request. Before the fix the client got the first key's 502
// and the second key was never asked.
func TestAPIKeyTransportErrorFailsOverToSecondKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var served atomic.Int32
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		served.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","usage":{"input_tokens":9,"output_tokens":3}}`))
	}))
	defer good.Close()

	deadSrv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := deadSrv.URL
	deadSrv.Close()

	// Per-credential BaseURL: keyA points at the dead socket, keyB at the live
	// upstream, so the two are distinguishable through the real pool.
	keyA := apiKeyTestCred("keyA")
	keyA.BaseURL = deadURL
	keyB := apiKeyTestCred("keyB")
	keyB.BaseURL = good.URL

	s := &Server{
		cfg:           &config.Config{AnthropicBaseURL: good.URL, UseUTLS: false},
		pool:          auth.NewPool(nil, []*auth.Auth{keyA, keyB}, 10*time.Minute, false, ""),
		usage:         usage.OpenInMemory(),
		pricing:       pricing.NewCatalog(pricing.Config{}),
		switchTracker: thinkingsig.NewSwitchTracker(),
	}
	c, w := newMessagesContext(t, haikuBody)
	s.forwardWithFailover(c, auth.ProviderAnthropic, "/v1/messages",
		"claude-haiku-4-5-20251001", "tok-abcdef123456", "", "client", "slot-1", haikuBody, false, time.Now())

	if served.Load() == 0 {
		t.Fatal("the second key was never tried — failover did not happen")
	}
	if w.Code != http.StatusOK {
		t.Fatalf("the client should have received the healthy key's 200; got %d body=%q", w.Code, w.Body.String())
	}
}

// A client that hangs up is not a channel fault: MarkClientCancel only, no
// failure, and no retry (every other credential would hit the same dead
// context and be blamed for it).
func TestAPIKeyClientDisconnectIsNotAFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	release := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	defer up.Close()
	defer close(release)

	cred := apiKeyTestCred("relayCancel")
	s := newHealthTestServer(up.URL, cred)

	c, w := newMessagesContext(t, haikuBody)
	ctx, cancel := context.WithCancel(context.Background())
	c.Request = c.Request.WithContext(ctx)
	cancel() // the caller walked away before the upstream answered

	retry, done, _ := s.doForwardAnthropicAPIKey(c, cred, "/v1/messages", haikuBody, false,
		"claude-haiku-4-5-20251001", "tok-abcdef123456", "client", time.Now(), 1)

	if retry || !done {
		t.Fatalf("a client cancellation must not be retried across the fleet; got retry=%v done=%v", retry, done)
	}
	if got := consecutiveFailures(t, cred); got != 0 {
		t.Fatalf("ConsecutiveFailures = %d, want 0 — the user pressing Ctrl-C is not a credential fault", got)
	}
	if !cred.IsHealthy() {
		t.Fatal("a cancelled request must leave the credential healthy")
	}
	_ = w
}

// ---------------------------------------------------------------------------
// 429 / Retry-After
// ---------------------------------------------------------------------------

// A 429 must go through the pool's throttling path with the upstream's own
// Retry-After, not the generic MarkFailure counter. The upstream answered the
// "how long" question; inferring our own interval on top of it is how two
// layers end up disagreeing about when the channel may be probed again.
func TestAPIKey429HonoursRetryAfter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const retryAfterSecs = 900
	up := jsonUpstream(http.StatusTooManyRequests,
		`{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`,
		map[string]string{"Retry-After": strconv.Itoa(retryAfterSecs)})
	defer up.Close()

	cred := apiKeyTestCred("relay429")
	s := newHealthTestServer(up.URL, cred)

	before := time.Now()
	retry, _, _ := callAPIKey(t, s, cred, haikuBody, false)
	if !retry {
		t.Fatal("a 429 is withheld and retried on another credential")
	}
	// The upstream's Retry-After has to survive all the way into the cooldown.
	// The escalating default for a first 429 is 30s, so a 15m deadline can only
	// have come from the header.
	reset := cred.Snapshot().QuotaResetAt
	if reset.Before(before.Add(10 * time.Minute)) {
		t.Fatalf("quota reset = %v, want ~%ds out — the upstream's Retry-After was discarded",
			reset.Sub(before), retryAfterSecs)
	}
	if got := consecutiveFailures(t, cred); got != 0 {
		t.Fatalf("ConsecutiveFailures = %d, want 0 — 429 is throttling, handled by the cooldown path", got)
	}
}

// ---------------------------------------------------------------------------
// plain 4xx stays a zero-signal event
// ---------------------------------------------------------------------------

// The rule that must NOT change: a malformed request is the client's fault.
// Counting it toward credential health lets one broken caller trip the breaker
// for every other caller sharing the channel.
func TestPlainClientErrorsLeaveHealthUntouched(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, status := range []int{400, 404, 413, 422} {
		t.Run(fmt.Sprintf("status-%d", status), func(t *testing.T) {
			up := jsonUpstream(status,
				`{"type":"error","error":{"type":"invalid_request_error","message":"bad"}}`, nil)
			defer up.Close()

			cred := apiKeyTestCred(fmt.Sprintf("relay%d", status))
			s := newHealthTestServer(up.URL, cred)

			retry, done, w := callAPIKey(t, s, cred, haikuBody, false)
			if retry || !done {
				t.Fatalf("a client-side %d must terminate the request; got retry=%v done=%v", status, retry, done)
			}
			if w.Body.Len() == 0 {
				t.Fatal("the client must see its own error")
			}
			if got := consecutiveFailures(t, cred); got != 0 {
				t.Fatalf("ConsecutiveFailures = %d, want 0 — a %d is the caller's fault, not the channel's", got, status)
			}
			if until, _ := cred.QuarantineSnapshot(); !until.IsZero() {
				t.Fatalf("a %d must never open the breaker", status)
			}
		})
	}
}
