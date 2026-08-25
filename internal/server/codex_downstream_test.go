package server

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/wjsoj/cc-core/auth"
	"github.com/wjsoj/cc-core/clienttoken"
	"github.com/wjsoj/cc-core/codexws"
	"github.com/wjsoj/cc-core/pricing"
	"github.com/wjsoj/cc-core/usage"

	"github.com/wjsoj/CPA-Claude/internal/config"
)

// codexLeakyUpstream answers with a realistic Codex SSE stream, plus every
// response header the real backend sends that describes OUR pool rather than
// the caller's request.
func codexLeakyUpstream() *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Drain the request body before answering. A handler that returns
		// without reading it leaves Go's server to abort the connection, which
		// truncates the response we just wrote — the relay then reported the
		// stream as broken before its terminal event and withheld everything
		// for a failover with no second credential to try.
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()

		h := w.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("X-Codex-Primary-Used-Percent", "42")
		h.Set("X-Codex-Primary-Reset-After-Seconds", "604739")
		h.Set("Openai-Organization", "org-secret")
		h.Set("X-Oai-Request-Id", "req-abc")
		h.Set("Cf-Ray", "a2b157912d8fad9f-AMS")
		h.Set("Set-Cookie", "__cf_bm=leak; HttpOnly")
		h.Set("Server", "cloudflare")
		h.Set("Retry-After", "120")

		// An in-band rate-limit frame and a telemetry frame, then a terminal
		// event carrying usage — the shape crack/codexapp0.147.0/rows/13 shows.
		//
		// Written as ONE Write. Emitting them event-by-event raced with the
		// reader: the handler returned, closing the connection, before the last
		// event had been drained, so the relay saw EOF without a terminal event,
		// called the stream truncated and retried onto another credential — and
		// the test then asserted against an empty response, about one run in
		// five. One write is one chunk, complete before the handler returns.
		var body strings.Builder
		for _, line := range []string{
			`data: {"type":"codex.rate_limits","plan_type":"plus","rate_limits":{"allowed":true,"limit_reached":false,"primary":{"used_percent":42,"reset_at":1787329759}}}`,
			`data: {"type":"responsesapi.websocket_timing","timing_metrics":{"engine_ids":"gpt56sol-codex-a-c321"}}`,
			`data: {"type":"response.completed","response":{"id":"resp_1","safety_identifier":"user-SECRET","usage":{"input_tokens":11,"output_tokens":7,"total_tokens":18}}}`,
		} {
			body.WriteString(line)
			body.WriteString("\n\n")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, body.String())
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		// Hold the stream open until the reader is done. Returning immediately
		// closes the connection, and the relay could see EOF before it had
		// drained the events — it then reported the stream truncated before its
		// terminal event and withheld the whole response for a failover with no
		// second credential to try, so the test asserted against an empty
		// recorder. A real SSE server likewise does not hang up the instant it
		// finishes writing.
		select {
		case <-r.Context().Done():
		case <-time.After(150 * time.Millisecond):
		}
	}))
	return srv
}

func codexLeakTestServer(upstreamURL string) (*Server, *auth.Auth) {
	cred := &auth.Auth{
		ID: "codex-hdr.json", Kind: auth.KindOAuth, Provider: auth.ProviderOpenAI,
		AccessToken: "token", AccountID: "account", BaseURL: upstreamURL,
		ExpiresAt: time.Now().Add(time.Hour),
	}
	return &Server{
		cfg:              &config.Config{ChatGPTBackendBaseURL: upstreamURL},
		pool:             auth.NewPool([]*auth.Auth{cred}, nil, 10*time.Minute, false, ""),
		usage:            usage.OpenInMemory(),
		pricing:          pricing.NewCatalog(pricing.Config{}),
		tokens:           clienttoken.OpenInMemory(),
		codexRespAccount: newCodexRespAccountStore(codexRespAccountTTL),
		codexSessions:    codexws.NewSessionRegistry(0),
	}, cred
}

func runCodexLeakStream(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	upstream := codexLeakyUpstream()
	t.Cleanup(upstream.Close)
	return runCodexLeakStreamAt(t, upstream.URL)
}

func runCodexLeakStreamAt(t *testing.T, upstreamURL string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	s, cred := codexLeakTestServer(upstreamURL)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := []byte(`{"model":"gpt-5.6-sol","input":"hi","stream":true}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	s.doForwardCodexOAuth(c, cred, "/v1/responses", body, true,
		"gpt-5.6-sol", "client-token", "client", "", time.Now(), 1)
	return w
}

// The Codex HTTP path used to copy every upstream response header except three
// hop-by-hop ones, handing the caller our pool's operational state: the serving
// account's rate-limit windows, its organization, the upstream correlator, and
// cf-ray — whose suffix names the Cloudflare datacentre our egress sits in. The
// Claude path has used the cc-core allowlist since it was written; only Codex
// was still forwarding verbatim.
func TestCodexOAuthStreamHeadersAreAllowlisted(t *testing.T) {
	got := runCodexLeakStream(t).Result().Header
	for _, leaked := range []string{
		"X-Codex-Primary-Used-Percent", "X-Codex-Primary-Reset-After-Seconds",
		"Openai-Organization", "X-Oai-Request-Id", "Cf-Ray", "Set-Cookie", "Server",
	} {
		if v := got.Get(leaked); v != "" {
			t.Errorf("%s reached the client with %q — it describes our pool, not the request", leaked, v)
		}
	}
	// Retry-After is on the allowlist precisely so scrubbing cannot leave a
	// throttled client retrying blind.
	if v := got.Get("Retry-After"); v != "120" {
		t.Errorf("Retry-After = %q, want it preserved", v)
	}
}

// The SSE body leaks the same way the WS frames do, and cc-core has the same
// remedy for it.
func TestCodexOAuthStreamBodyIsScrubbed(t *testing.T) {
	out := runCodexLeakStream(t).Body.String()
	for _, leaked := range []string{
		"responsesapi.websocket_timing", "gpt56sol-codex-a-c321",
		"plan_type", "used_percent", "reset_at",
		"safety_identifier", "user-SECRET",
	} {
		if strings.Contains(out, leaked) {
			t.Errorf("stream still leaks %q:\n%s", leaked, out)
		}
	}
	if !strings.Contains(out, `"type":"response.completed"`) {
		t.Errorf("the terminal event did not reach the client:\n%s", out)
	}
	if !strings.Contains(out, `"total_tokens":18`) {
		t.Errorf("usage was damaged:\n%s", out)
	}
	// A throttled client still needs to know it is throttled.
	if !strings.Contains(out, `"type":"codex.rate_limits"`) {
		t.Errorf("the rate-limit frame should be rewritten, not dropped:\n%s", out)
	}
}
