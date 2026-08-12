package server

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	gorillaws "github.com/gorilla/websocket"

	"github.com/wjsoj/cc-core/auth"
	"github.com/wjsoj/cc-core/clienttoken"
	"github.com/wjsoj/cc-core/codexws"
	"github.com/wjsoj/cc-core/pricing"
	"github.com/wjsoj/cc-core/relay"
	"github.com/wjsoj/cc-core/usage"

	"github.com/wjsoj/CPA-Claude/internal/config"
)

// The Codex WS path used to be OAuth-only: AllowAPIKeyFallback was hardcoded
// false and any non-OAuth candidate was skipped outright. Once the OAuth fleet
// hit its quota, the handshake had already completed, so there was no HTTP
// status left to send and the CLI simply saw the socket close — while the very
// same account served HTTP /v1/responses fine through the relay peer.
//
// These tests pin the fallback: a relay peer (a cooperating proxy on this same
// cc-core stack, which therefore has its own WS ingress) is a valid WS upstream,
// a plain vendor API key is not, and the downstream caller's identity crosses
// the hop so the peer spreads our users across its fleet.

func codexRelayCred(id, baseURL string, relayPeer bool) *auth.Auth {
	return &auth.Auth{
		ID:          id,
		Kind:        auth.KindAPIKey,
		Provider:    auth.ProviderOpenAI,
		Label:       id,
		AccessToken: "sk-peer-" + id,
		BaseURL:     baseURL,
		RelayPeer:   relayPeer,
	}
}

func codexWSTestServer(creds ...*auth.Auth) *Server {
	return &Server{
		cfg: &config.Config{
			ChatGPTBackendBaseURL: "https://chatgpt.com/backend-api",
			OpenAIBaseURL:         "https://api.openai.com",
			CodexWS:               config.CodexWSConfig{Enabled: true, ReadLimitBytes: 1 << 20},
			UseUTLS:               false,
		},
		pool:             auth.NewPool(nil, creds, 10*time.Minute, false, ""),
		usage:            usage.OpenInMemory(),
		pricing:          pricing.NewCatalog(pricing.Config{}),
		tokens:           clienttoken.OpenInMemory(),
		codexRespAccount: newCodexRespAccountStore(codexRespAccountTTL),
	}
}

func TestCodexWSDialTargetRelayPeer(t *testing.T) {
	cred := codexRelayCred("peer1", "https://api.example.com", true)
	s := codexWSTestServer(cred)

	target, ok := s.codexWSDialTarget(cred, "sk-downstream-user", "win-7", codexws.CodexOpenAIBetaWS, "gpt-5-codex", "")
	if !ok {
		t.Fatal("a relay peer must be dialable over WS — it runs this same stack")
	}
	if target.URL != "wss://api.example.com/v1/responses" {
		t.Errorf("URL = %q, want wss://api.example.com/v1/responses", target.URL)
	}
	if got := target.Header.Get("Authorization"); got != "Bearer sk-peer-peer1" {
		t.Errorf("Authorization = %q — the peer authenticates us by our API key", got)
	}
	if target.Header.Get("Chatgpt-Account-Id") != "" {
		t.Error("Chatgpt-Account-Id names an upstream ChatGPT account; that is the peer's choice, not ours")
	}
	if target.UseUTLS {
		t.Error("a cooperating peer is not Cloudflare-fronted; the HTTP relay path dials it without uTLS too")
	}
	// Identity must cross the hop, or every one of our users collapses onto one
	// of the peer's credentials.
	id, ok := relay.Read(target.Header)
	if !ok {
		t.Fatal("the downstream caller's relay identity must be stamped on the handshake")
	}
	if id.Peer != RelayPeerName || id.Session != "win-7" {
		t.Errorf("relay identity = %+v, want peer=%s session=win-7", id, RelayPeerName)
	}
	if id.Client != relay.ClientID("sk-downstream-user") {
		t.Errorf("relay client id = %q, want the hashed downstream token", id.Client)
	}
}

func TestCodexWSDialTargetRejectsPlainAPIKey(t *testing.T) {
	cred := codexRelayCred("vendor", "https://vendor.example.com", false)
	s := codexWSTestServer(cred)
	if _, ok := s.codexWSDialTarget(cred, "tok", "slot", codexws.CodexOpenAIBetaWS, "gpt-5-codex", ""); ok {
		t.Fatal("a third-party OpenAI-compatible relay serves HTTP POST only; dialing it would only collect a 404")
	}
}

func TestCodexWSDialTargetOAuthUnchanged(t *testing.T) {
	cred := &auth.Auth{
		ID: "oauth1", Kind: auth.KindOAuth, Provider: auth.ProviderOpenAI,
		AccessToken: "tok", ExpiresAt: time.Now().Add(time.Hour),
	}
	s := codexWSTestServer()
	target, ok := s.codexWSDialTarget(cred, "tok", "slot", codexws.CodexOpenAIBetaWS, "gpt-5-codex", "")
	if !ok {
		t.Fatal("OAuth must remain dialable")
	}
	if target.URL != "wss://chatgpt.com/backend-api/codex/responses" {
		t.Errorf("URL = %q — the OAuth path must still go straight to the ChatGPT backend", target.URL)
	}
	if _, stamped := relay.Read(target.Header); stamped {
		t.Error("relay identity must never be stamped on api.openai/chatgpt.com — that leaks our topology")
	}
}

func TestCodexWSRelayURLJoinRules(t *testing.T) {
	cases := map[string]string{
		// Bare origin keeps /v1 (new-api / one-api serve under it).
		"https://api.example.com": "wss://api.example.com/v1/responses",
		// A base that already carries a path is authoritative.
		"https://api.example.com/v1": "wss://api.example.com/v1/responses",
		"http://127.0.0.1:8318":      "ws://127.0.0.1:8318/v1/responses",
	}
	for in, want := range cases {
		if got := codexWSRelayURL(in); got != want {
			t.Errorf("codexWSRelayURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// A peer whose WS ingress is switched off answers the handshake with a 404. That
// says nothing about the credential — it still serves HTTP fine — so it must not
// count against its health, or one config flag on the peer would quarantine the
// only fallback channel we have.
func TestCodexWSDialFaultLeavesHealthAloneOnRouteMiss(t *testing.T) {
	cred := codexRelayCred("peer1", "https://api.example.com", true)
	s := codexWSTestServer(cred)

	if unstick := s.reportCodexWSDialFault(context.Background(), cred, http.StatusNotFound, time.Time{}, http.ErrNotSupported); unstick {
		t.Error("a route miss must not break the sticky assignment")
	}
	if _, _, _, consecutive := cred.HealthSnapshot(); consecutive != 0 {
		t.Errorf("ConsecutiveFailures = %d, want 0 — the peer's WS ingress being off is not a credential fault", consecutive)
	}
	if _, hardFailed, _, _ := cred.HealthSnapshot(); hardFailed {
		t.Error("a 404 must never hard-fail an API key")
	}
}

func TestCodexWSDialFaultCountsGatewayErrorsOnRelay(t *testing.T) {
	cred := codexRelayCred("peer1", "https://api.example.com", true)
	s := codexWSTestServer(cred)
	// Transport failure: no status at all.
	if unstick := s.reportCodexWSDialFault(context.Background(), cred, 0, time.Time{}, http.ErrServerClosed); !unstick {
		t.Error("a dead peer must break the sticky assignment so the next dial can land elsewhere")
	}
	if _, _, _, consecutive := cred.HealthSnapshot(); consecutive != 1 {
		t.Errorf("ConsecutiveFailures = %d, want 1", consecutive)
	}
	if _, hardFailed, _, _ := cred.HealthSnapshot(); hardFailed {
		t.Error("an API key must never be hard-failed by a gateway error — that is what the breaker ladder is for")
	}
}

// A WS handshake that never got an HTTP response says nothing about the
// credential until the network has been ruled out. codexws.Dial has a 10s budget
// and no internal retry, so one slow TLS handshake surfaces here as `i/o
// timeout` on the very first try — and counting those toward health is how a
// network hiccup takes subscription accounts offline: two in a row degrade a
// credential, five hard-fail it. Seen live on 2026-08-12 right after the deploy,
// twice within ten seconds, against two different Codex accounts.
func TestCodexWSDialTimeoutDoesNotFaultTheCredential(t *testing.T) {
	oauth := &auth.Auth{
		ID: "codex-a.json", Kind: auth.KindOAuth, Provider: auth.ProviderOpenAI,
		AccessToken: "tok", ExpiresAt: time.Now().Add(time.Hour),
	}
	s := codexWSTestServer()

	// Exactly what the production log carried.
	timeout := &net.OpError{
		Op: "read", Net: "tcp",
		Err: &timeoutError{},
	}
	if unstick := s.reportCodexWSDialFault(context.Background(), oauth, 0, time.Time{}, timeout); unstick {
		t.Error("a network flap must not break the sticky assignment either")
	}
	if _, _, _, consecutive := oauth.HealthSnapshot(); consecutive != 0 {
		t.Errorf("ConsecutiveFailures = %d, want 0 — an i/o timeout on the handshake is not the credential's fault", consecutive)
	}

	// A client that closed the tab mid-dial is likewise blameless.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if unstick := s.reportCodexWSDialFault(ctx, oauth, 0, time.Time{}, context.Canceled); unstick {
		t.Error("a client hang-up must not break the sticky assignment")
	}
	if _, _, _, consecutive := oauth.HealthSnapshot(); consecutive != 0 {
		t.Errorf("ConsecutiveFailures = %d, want 0 — the user closed the tab", consecutive)
	}

	// But a genuine transport failure still counts: the point is to classify,
	// not to stop counting. (It leaves the sticky assignment alone — only the
	// credential-scoped statuses 401/403/429 break that.)
	if unstick := s.reportCodexWSDialFault(context.Background(), oauth, 0, time.Time{}, errors.New("no route to host")); unstick {
		t.Error("only a credential-scoped status should break the sticky assignment")
	}
	if _, _, _, consecutive := oauth.HealthSnapshot(); consecutive != 1 {
		t.Errorf("ConsecutiveFailures = %d, want 1", consecutive)
	}
}

// timeoutError is a net.Error whose Timeout() is true, standing in for the
// *net.OpError the dialer returns on `i/o timeout`.
type timeoutError struct{}

func (*timeoutError) Error() string   { return "i/o timeout" }
func (*timeoutError) Timeout() bool   { return true }
func (*timeoutError) Temporary() bool { return true }

func TestIsCodexWSDialFlap(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"i/o timeout", &net.OpError{Op: "read", Err: &timeoutError{}}, true},
		{"deadline exceeded", context.DeadlineExceeded, true},
		{"connection reset", errors.New("read tcp: connection reset by peer"), true},
		{"h2 flap", errors.New("http2: client connection lost"), true},
		// A cancellation is the client's doing, classified by isClientDisconnect;
		// it must not be swept in here, where it would look like a network fault.
		{"client cancel", context.Canceled, false},
		{"anything else", errors.New("no route to host"), false},
	}
	for _, tc := range cases {
		if got := isCodexWSDialFlap(tc.err); got != tc.want {
			t.Errorf("isCodexWSDialFlap(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// The relay is a LAST RESORT, not a route. A healthy OAuth credential must win
// every time one exists — the peer costs money and adds a hop, and the whole
// point of the self-run subscription pool is to be used first. The ordering
// itself lives in cc-core (pickOAuthLocked runs to exhaustion before the API-key
// loop is even reached), so what this pins is that the WS path really does go
// through that same gate rather than choosing its own upstream.
func TestCodexWSPrefersOAuthAndNeverTouchesRelayWhenHealthy(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upgrader := gorillaws.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	oauthHit := make(chan string, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		oauthHit <- r.Header.Get("Authorization")
		_ = conn.WriteMessage(gorillaws.TextMessage,
			[]byte(`{"type":"response.completed","response":{"id":"resp_oauth","usage":{"input_tokens":1,"output_tokens":1}}}`))
		_ = conn.WriteMessage(gorillaws.CloseMessage,
			gorillaws.FormatCloseMessage(gorillaws.CloseNormalClosure, "done"))
	}))
	defer backend.Close()

	relayTouched := make(chan struct{}, 1)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		relayTouched <- struct{}{}
		http.Error(w, "the relay must not be dialled while OAuth is healthy", http.StatusTeapot)
	}))
	defer peer.Close()

	oauth := &auth.Auth{
		ID: "codex-healthy.json", Kind: auth.KindOAuth, Provider: auth.ProviderOpenAI,
		AccessToken: "oauth-token", ExpiresAt: time.Now().Add(time.Hour),
	}
	cred := codexRelayCred("peer1", peer.URL, true)
	s := codexWSTestServer(cred)
	s.pool = auth.NewPool([]*auth.Auth{oauth}, []*auth.Auth{cred}, 10*time.Minute, false, "")
	// codexWSUpstreamURL appends /codex/responses; the stub accepts any path.
	s.cfg.ChatGPTBackendBaseURL = backend.URL

	engine := gin.New()
	engine.GET("/v1/responses", func(c *gin.Context) {
		c.Set("client_token", "sk-downstream-user")
		c.Set("client_name", "tester")
		s.handleCodexResponsesWS(c)
	})
	front := httptest.NewServer(engine)
	defer front.Close()

	client, _, err := gorillaws.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(front.URL, "http")+"/v1/responses",
		http.Header{"Session_id": []string{"win-1"}})
	if err != nil {
		t.Fatalf("client upgrade failed: %v", err)
	}
	defer client.Close()
	if err := client.WriteMessage(gorillaws.TextMessage,
		[]byte(`{"type":"response.create","model":"gpt-5-codex"}`)); err != nil {
		t.Fatalf("write first frame: %v", err)
	}

	select {
	case got := <-oauthHit:
		if got != "Bearer oauth-token" {
			t.Errorf("upstream saw %q, want the OAuth token", got)
		}
	case <-relayTouched:
		t.Fatal("ROUTING REGRESSION: the relay was dialled while a healthy OAuth credential was available")
	case <-time.After(10 * time.Second):
		t.Fatal("no upstream was dialled at all")
	}

	// And nothing reaches the peer afterwards either.
	select {
	case <-relayTouched:
		t.Fatal("the relay was dialled after the OAuth session had already started")
	case <-time.After(300 * time.Millisecond):
	}
}

// The real production shape, between the two extremes above: the OAuth
// credentials are present but out of quota. That is exactly when the relay is
// supposed to take over — and before this change it did not, because the WS path
// asked the pool for OAuth only and then closed the socket.
func TestCodexWSFallsBackToRelayWhenOAuthIsOutOfQuota(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upgrader := gorillaws.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	reached := make(chan struct{}, 1)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		reached <- struct{}{}
		_ = conn.WriteMessage(gorillaws.CloseMessage,
			gorillaws.FormatCloseMessage(gorillaws.CloseNormalClosure, "done"))
	}))
	defer peer.Close()

	// A weekly Codex limit: healthy credential, no capacity for ~5 days.
	exhausted := &auth.Auth{
		ID: "codex-exhausted.json", Kind: auth.KindOAuth, Provider: auth.ProviderOpenAI,
		AccessToken: "oauth-token", ExpiresAt: time.Now().Add(time.Hour),
	}
	exhausted.MarkUsageLimitReached(time.Now().Add(137 * time.Hour))

	cred := codexRelayCred("peer1", peer.URL, true)
	s := codexWSTestServer(cred)
	s.pool = auth.NewPool([]*auth.Auth{exhausted}, []*auth.Auth{cred}, 10*time.Minute, false, "")

	engine := gin.New()
	engine.GET("/v1/responses", func(c *gin.Context) {
		c.Set("client_token", "sk-downstream-user")
		c.Set("client_name", "tester")
		s.handleCodexResponsesWS(c)
	})
	front := httptest.NewServer(engine)
	defer front.Close()

	client, _, err := gorillaws.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(front.URL, "http")+"/v1/responses",
		http.Header{"Session_id": []string{"win-1"}})
	if err != nil {
		t.Fatalf("client upgrade failed: %v", err)
	}
	defer client.Close()
	if err := client.WriteMessage(gorillaws.TextMessage,
		[]byte(`{"type":"response.create","model":"gpt-5-codex"}`)); err != nil {
		t.Fatalf("write first frame: %v", err)
	}

	select {
	case <-reached:
	case <-time.After(10 * time.Second):
		t.Fatal("the session died instead of failing over to the relay — this is the 55×/day close the fix is for")
	}
}

// End-to-end: with no OAuth credential in the pool at all, a codex-tui WS
// session must still be served — through the relay peer, frames intact.
func TestCodexWSSessionServedByRelayPeerWhenNoOAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)

	type peerSaw struct {
		auth    string
		relayID relay.Identity
		frame   string
	}
	saw := make(chan peerSaw, 1)
	upgrader := gorillaws.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

	// The peer is another proxy on this same stack: same WS ingress path, same
	// bearer-token auth, same relay-header contract.
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			http.NotFound(w, r)
			return
		}
		id, _ := relay.Read(r.Header)
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_, first, err := conn.ReadMessage()
		if err != nil {
			return
		}
		saw <- peerSaw{auth: r.Header.Get("Authorization"), relayID: id, frame: string(first)}
		// Answer with a terminal event carrying usage, then close cleanly.
		_ = conn.WriteMessage(gorillaws.TextMessage, []byte(
			`{"type":"response.completed","response":{"id":"resp_1","usage":{"input_tokens":10,"output_tokens":5}}}`))
		_ = conn.WriteMessage(gorillaws.CloseMessage,
			gorillaws.FormatCloseMessage(gorillaws.CloseNormalClosure, "done"))
	}))
	defer peer.Close()

	cred := codexRelayCred("peer1", peer.URL, true)
	s := codexWSTestServer(cred)

	// Serve the WS ingress through a real HTTP server so the client can upgrade.
	engine := gin.New()
	engine.GET("/v1/responses", func(c *gin.Context) {
		c.Set("client_token", "sk-downstream-user")
		c.Set("client_name", "tester")
		s.handleCodexResponsesWS(c)
	})
	front := httptest.NewServer(engine)
	defer front.Close()

	client, resp, err := gorillaws.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(front.URL, "http")+"/v1/responses",
		http.Header{"Session_id": []string{"win-42"}})
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("client upgrade failed (status=%d): %v", status, err)
	}
	defer client.Close()

	firstFrame := `{"type":"response.create","model":"gpt-5-codex"}`
	if err := client.WriteMessage(gorillaws.TextMessage, []byte(firstFrame)); err != nil {
		t.Fatalf("write first frame: %v", err)
	}

	var got peerSaw
	select {
	case got = <-saw:
	case <-time.After(10 * time.Second):
		t.Fatal("the peer never received the turn — the WS session was not relayed")
	}
	if got.auth != "Bearer sk-peer-peer1" {
		t.Errorf("peer saw Authorization %q, want our API key", got.auth)
	}
	if got.frame != firstFrame {
		t.Errorf("peer saw frame %q, want it forwarded verbatim", got.frame)
	}
	if got.relayID.Client != relay.ClientID("sk-downstream-user") || got.relayID.Session != "win-42" {
		t.Errorf("peer saw relay identity %+v, want our downstream user's slot", got.relayID)
	}

	// The upstream's terminal event must reach the client unchanged.
	_ = client.SetReadDeadline(time.Now().Add(10 * time.Second))
	_, out, err := client.ReadMessage()
	if err != nil {
		t.Fatalf("client read: %v", err)
	}
	var ev struct {
		Type     string `json:"type"`
		Response struct {
			ID string `json:"id"`
		} `json:"response"`
	}
	if err := json.Unmarshal(out, &ev); err != nil {
		t.Fatalf("client got a non-JSON frame %q: %v", out, err)
	}
	if ev.Type != "response.completed" || ev.Response.ID != "resp_1" {
		t.Errorf("client saw %q, want the upstream's response.completed", out)
	}
}
