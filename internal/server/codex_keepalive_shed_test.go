package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/wjsoj/cc-core/auth"
)

// A production capacity shed does not arrive on a silent stream. When the
// backend cannot schedule a turn it parks the request and heartbeats about
// every 30s, then sheds a fraction of a second after one of those beats. The
// heartbeat carries no output and no response id, but it used to commit the
// response, so the shed behind it could only be demoted and shown to the user
// as a reconnect — which is what made the overwhelming majority of sheds
// unrecoverable rather than an invisible retry.
func TestCodexKeepaliveDoesNotForecloseFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: response.created\ndata: {\"type\":\"response.created\"}\n\n")
		_, _ = io.WriteString(w, "event: response.in_progress\ndata: {\"type\":\"response.in_progress\"}\n\n")
		_, _ = io.WriteString(w, "event: keepalive\ndata: {\"type\":\"keepalive\",\"sequence_number\":2}\n\n")
		_, _ = io.WriteString(w, "event: error\ndata: {\"type\":\"error\",\"error\":{\"code\":\"server_is_overloaded\",\"message\":\"Selected model is at capacity. Please try a different model.\"}}\n\n")
		_, _ = io.WriteString(w, "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\"}}\n\n")
	}))
	defer upstream.Close()

	cred := &auth.Auth{
		ID: "keepalive-shed.json", Kind: auth.KindOAuth, Provider: auth.ProviderOpenAI,
		AccessToken: "token", AccountID: "account", BaseURL: upstream.URL,
		ExpiresAt: time.Now().Add(time.Hour),
	}
	s := codexHTTPTestServer(upstream.URL, cred)

	body := []byte(`{"model":"gpt-6-astra","input":"hello","stream":true}`)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(body)))

	retry, done := s.doForwardCodexOAuth(c, cred, "/v1/responses", body, true,
		"gpt-6-astra", "client-token", "client", "", time.Now(), 1)
	if !retry || done {
		t.Fatalf("a shed behind a keepalive is still pre-output; retry=%v done=%v", retry, done)
	}
	if got := w.Body.String(); got != "" {
		t.Fatalf("nothing may reach the client before failover, got %q", got)
	}
}
