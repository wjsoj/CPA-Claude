package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/wjsoj/cc-core/auth"
	"github.com/wjsoj/cc-core/relay"
)

func relayTestCtx(sessionID string, inbound http.Header) *gin.Context {
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	for k, vs := range inbound {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if sessionID != "" {
		req.Header.Set("X-Claude-Code-Session-Id", sessionID)
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req
	return c
}

// The identity is only useful to a peer we run, and actively unwanted anywhere
// else: to a vendor it is an unexplained header describing our client base.
func TestApplyRelayIdentityOnlyForPeers(t *testing.T) {
	c := relayTestCtx("sess-1", nil)

	cases := []struct {
		name  string
		a     *auth.Auth
		stamp bool
	}{
		{"our own peer", &auth.Auth{Kind: auth.KindAPIKey, RelayPeer: true}, true},
		{"third-party api key", &auth.Auth{Kind: auth.KindAPIKey}, false},
		{"oauth credential", &auth.Auth{Kind: auth.KindOAuth, RelayPeer: true}, false},
		{"no credential", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			applyRelayIdentity(h, tc.a, c, "sk-downstream")
			_, got := relay.Read(h)
			if got != tc.stamp {
				t.Fatalf("stamped = %t, want %t (headers: %v)", got, tc.stamp, h)
			}
		})
	}
}

// A downstream caller must not be able to hand-write these headers and have
// them survive the hop — that would let anyone forge an identity at the peer,
// and would put an unexplained header on requests to Anthropic.
func TestClientSuppliedRelayHeadersNeverSurvive(t *testing.T) {
	forged := http.Header{}
	forged.Set(relay.HeaderClient, "forged-client")
	forged.Set(relay.HeaderSession, "forged-session")
	forged.Set(relay.HeaderPeer, "cpa-claude/9.9.9")

	// The copy a forward makes of the client's headers, before stripping.
	up := http.Header{}
	copyForwardableHeaders(forged, up)
	if up.Get(relay.HeaderClient) == "" {
		t.Fatal("precondition: the forged header should reach the copy")
	}

	stripIngressHeaders(up)
	if _, ok := relay.Read(up); ok {
		t.Fatalf("forged relay identity survived stripIngressHeaders: %v", up)
	}

	// And on a peer credential, our own value replaces it rather than
	// appending to it.
	applyRelayIdentity(up, &auth.Auth{Kind: auth.KindAPIKey, RelayPeer: true},
		relayTestCtx("real-session", forged), "sk-real")
	id, ok := relay.Read(up)
	if !ok {
		t.Fatal("peer credential did not get an identity")
	}
	if id.Client == "forged-client" || id.Session == "forged-session" {
		t.Fatalf("forged values leaked through: %+v", id)
	}
	if id.Client != relay.ClientID("sk-real") || id.Session != "real-session" {
		t.Fatalf("stamped the wrong identity: %+v", id)
	}
}

// The live relay is the Codex endpoint (apikey-self.json → api.novadiffusion.com),
// where the CLI names its session in Session_id rather than the Claude header.
// Stamping must read the same header clientSlotID does, or Codex traffic would
// arrive at the peer with a per-user but session-less identity.
func TestRelayIdentityUsesCodexSessionHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	req.Header.Set("Session_id", "codex-window-7")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req

	h := http.Header{}
	applyRelayIdentity(h, &auth.Auth{Kind: auth.KindAPIKey, RelayPeer: true}, c, "sk-downstream")
	id, ok := relay.Read(h)
	if !ok {
		t.Fatal("no identity stamped for a Codex request")
	}
	if id.Session != "codex-window-7" {
		t.Fatalf("session = %q, want the Session_id value", id.Session)
	}
}

// Two downstream users, and one user's two CLI windows, must reach the peer as
// distinct slots — that is the entire point of the hop.
func TestRelayIdentityDistinguishesCallers(t *testing.T) {
	peer := &auth.Auth{Kind: auth.KindAPIKey, RelayPeer: true}
	slot := func(token, session string) string {
		h := http.Header{}
		applyRelayIdentity(h, peer, relayTestCtx(session, nil), token)
		id, _ := relay.Read(h)
		return id.SlotID()
	}

	a1, a2 := slot("tok-a", "win-1"), slot("tok-a", "win-2")
	b1 := slot("tok-b", "win-1")
	if a1 == a2 {
		t.Error("one user's two sessions collapsed to one slot")
	}
	if a1 == b1 {
		t.Error("two users collapsed to one slot")
	}
	// A raw API caller has no session of its own but is still its own user.
	if slot("tok-a", "") == slot("tok-b", "") {
		t.Error("sessionless callers collapsed onto one slot")
	}
}
