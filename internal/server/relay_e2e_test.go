package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/wjsoj/cc-core/auth"
	"github.com/wjsoj/cc-core/relay"
)

// End-to-end over the real header pipeline a Codex API-key forward runs:
// copy the client's headers, strip ingress, then stamp. Two downstream users
// must arrive at the peer as two identities, and neither may carry anything
// the client asserted itself.
func TestCodexRelayHeaderPipelineEndToEnd(t *testing.T) {
	seen := make(chan http.Header, 4)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Clone()
		w.WriteHeader(200)
	}))
	defer peer.Close()

	send := func(downstreamToken, session string, forge bool) http.Header {
		in := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		in.Header.Set("Session_id", session)
		in.Header.Set("User-Agent", "codex-tui/0.147.0")
		if forge {
			in.Header.Set(relay.HeaderClient, "forged")
			in.Header.Set(relay.HeaderSession, "forged")
		}
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = in

		up, err := http.NewRequest(http.MethodPost, peer.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		copyForwardableHeaders(in.Header, up.Header)
		stripIngressHeaders(up.Header)
		applyRelayIdentity(up.Header, &auth.Auth{Kind: auth.KindAPIKey, RelayPeer: true}, c, downstreamToken, nil)
		resp, err := http.DefaultClient.Do(up)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return <-seen
	}

	a := send("sk-user-a", "win-1", false)
	b := send("sk-user-b", "win-1", true)

	idA, okA := relay.Read(a)
	idB, okB := relay.Read(b)
	if !okA || !okB {
		t.Fatalf("identity missing at the peer: A=%v B=%v", a, b)
	}
	if idA.SlotID() == idB.SlotID() {
		t.Fatalf("two users landed on one slot: %q", idA.SlotID())
	}
	if idB.Client == "forged" || idB.Session == "forged" {
		t.Fatalf("client-forged identity reached the peer: %+v", idB)
	}
	if idA.Session != "win-1" || idA.Client != relay.ClientID("sk-user-a") {
		t.Fatalf("wrong identity: %+v", idA)
	}
	// The peer must be able to name us.
	if idA.Peer != RelayPeerName {
		t.Errorf("peer = %q, want %q", idA.Peer, RelayPeerName)
	}
	t.Logf("peer saw: %s=%s %s=%s", relay.HeaderClient, idA.Client, relay.HeaderSession, idA.Session)
}
