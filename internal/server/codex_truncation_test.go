package server

import (
	"bufio"
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/wjsoj/cc-core/auth"
	"github.com/wjsoj/cc-core/clienttoken"
	"github.com/wjsoj/cc-core/pricing"
	"github.com/wjsoj/cc-core/usage"

	"github.com/wjsoj/CPA-Claude/internal/config"
)

// A stream that ends without its terminal event has no usage for the same
// reason it has no `response.completed`: it was cut off in flight, several hops
// upstream of the credential we dialled. Treating that as "this relay cannot
// account for what it serves" convicts the wrong party — and on 2026-08-12 it
// did exactly that: ~2% of the relay's turns truncated, a run of them tripped
// the breaker on the only OpenAI API key configured, and 44 requests got a 503
// from a channel that was serving everyone else at that moment.
//
// The customer is still charged nothing either way. What changes is whose fault
// it is recorded as.

func codexAPIKeyStreamServer(t *testing.T, sse string) (*Server, *auth.Auth, *httptest.Server) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(sse))
	}))
	t.Cleanup(upstream.Close)

	cred := &auth.Auth{
		ID:          "apikey-self.json",
		Kind:        auth.KindAPIKey,
		Provider:    auth.ProviderOpenAI,
		Label:       "self",
		AccessToken: "sk-relay",
		BaseURL:     upstream.URL,
		RelayPeer:   true,
	}
	s := &Server{
		cfg:     &config.Config{OpenAIBaseURL: upstream.URL},
		pool:    auth.NewPool(nil, []*auth.Auth{cred}, 10*time.Minute, false, ""),
		usage:   usage.OpenInMemory(),
		pricing: pricing.NewCatalog(pricing.Config{}),
		tokens:  clienttoken.OpenInMemory(),
	}
	return s, cred, upstream
}

func runCodexAPIKeyForward(t *testing.T, s *Server, cred *auth.Auth) (retry, done bool) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := []byte(`{"model":"gpt-5.6-sol","input":"hi","stream":true}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	return s.doForwardCodex(c, cred, "/v1/responses", body, true,
		"gpt-5.6-sol", "sk-client-token-abcdef", "client", time.Now(), 1)
}

func TestCodexAPIKeyTruncatedStreamDoesNotCoolCredential(t *testing.T) {
	// Content was delivered, then the upstream went away: no response.completed,
	// no usage.
	s, cred, _ := codexAPIKeyStreamServer(t, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n")

	if retry, done := runCodexAPIKeyForward(t, s, cred); retry || !done {
		t.Fatalf("a truncated stream is already committed to the client; got retry=%v done=%v", retry, done)
	}
	_, hardFailed, _, consecutive := cred.HealthSnapshot()
	if consecutive != 0 {
		t.Errorf("ConsecutiveFailures = %d, want 0 — an upstream cut is not this credential's fault", consecutive)
	}
	if hardFailed {
		t.Error("a truncation must never hard-fail the fallback channel")
	}
	if cred.IsQuarantined(time.Now()) {
		t.Error("BREAKER REGRESSION: truncations run ~2% of normal traffic; quarantining on them takes the only fallback channel dark")
	}
}

// The protection that motivated the no-usage rule must survive: a relay that
// finishes a turn properly but reports no usage is still cooled, because that is
// a relay we cannot bill through.
func TestCodexAPIKeyCompletedStreamWithoutUsageStillCools(t *testing.T) {
	s, cred, _ := codexAPIKeyStreamServer(t,
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n"+
			"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\"}}\n\n")

	if retry, done := runCodexAPIKeyForward(t, s, cred); retry || !done {
		t.Fatalf("got retry=%v done=%v", retry, done)
	}
	if _, _, _, consecutive := cred.HealthSnapshot(); consecutive != 1 {
		t.Errorf("ConsecutiveFailures = %d, want 1 — a completed turn with no usage is unbillable and must cool the relay", consecutive)
	}
}

// And a healthy turn is still a success, so the failure counter resets.
func TestCodexAPIKeyCompleteStreamWithUsageMarksSuccess(t *testing.T) {
	s, cred, _ := codexAPIKeyStreamServer(t,
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"usage\":{\"input_tokens\":10,\"output_tokens\":4}}}\n\n")
	cred.MarkFailure("earlier blip")

	if retry, done := runCodexAPIKeyForward(t, s, cred); retry || !done {
		t.Fatalf("got retry=%v done=%v", retry, done)
	}
	if _, _, _, consecutive := cred.HealthSnapshot(); consecutive != 0 {
		t.Errorf("ConsecutiveFailures = %d, want 0 — a billable turn is a success", consecutive)
	}
}

func TestStreamSSEOpenAITerminalDetection(t *testing.T) {
	cases := []struct {
		name string
		sse  string
		want bool
	}{
		{"responses completed", "data: {\"type\":\"response.completed\",\"response\":{}}\n\n", true},
		{"responses failed", "data: {\"type\":\"response.failed\",\"response\":{}}\n\n", true},
		{"chat completions DONE", "data: {\"choices\":[]}\n\ndata: [DONE]\n\n", true},
		{"delta only", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"x\"}\n\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			var cnt usage.Counts
			sawTerminal := streamSSEOpenAI(c, bufio.NewReader(strings.NewReader(tc.sse)), &cnt, "").sawTerminal
			if sawTerminal != tc.want {
				t.Errorf("sawTerminal = %v, want %v", sawTerminal, tc.want)
			}
		})
	}
}
