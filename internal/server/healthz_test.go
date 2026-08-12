package server

import (
	"net/http"
	"testing"
	"time"

	"github.com/wjsoj/CPA-Claude/internal/monitor"
	"github.com/wjsoj/cc-core/auth"
)

func healthzKey(id string) *auth.Auth {
	return &auth.Auth{ID: id, Kind: auth.KindAPIKey, Provider: auth.ProviderAnthropic, Label: id}
}

func healthzCooling(id string) *auth.Auth {
	a := healthzKey(id)
	a.QuarantineStrikes = 2
	a.QuarantineUntil = time.Now().Add(time.Minute)
	a.LastFailure = time.Now()
	a.LastFailureReason = "http 500"
	return a
}

func healthzHalfOpen(id string) *auth.Auth {
	a := healthzKey(id)
	a.QuarantineStrikes = 2
	a.QuarantineUntil = time.Now().Add(-time.Minute)
	a.LastFailure = time.Now().Add(-2 * time.Minute)
	return a
}

// BEHAVIOUR CHANGE under test: /healthz used to be a hardcoded 200. It now
// reflects the credential pool, so a pool-wide outage takes the instance out of
// a load balancer's rotation instead of silently serving errors.
func TestHealthzReflectsPool(t *testing.T) {
	cases := []struct {
		name  string
		creds []*auth.Auth
		want  int
	}{
		{"healthy pool", []*auth.Auth{healthzKey("a")}, http.StatusOK},
		// Half-open is routable: traffic still flows, so the probe stays green.
		{"only half-open", []*auth.Auth{healthzHalfOpen("h")}, http.StatusOK},
		{"all cooling", []*auth.Auth{healthzCooling("a"), healthzCooling("b")}, http.StatusServiceUnavailable},
		{"no credentials", nil, http.StatusServiceUnavailable},
	}
	for _, c := range cases {
		pool := auth.NewPool(nil, c.creds, 10*time.Minute, false, "")
		code, body := healthzPayload(pool, "claude", auth.ProviderAnthropic)
		if code != c.want {
			t.Errorf("%s: status = %d, want %d (body=%v)", c.name, code, c.want, body)
		}
		if code == http.StatusOK && body["status"] != "ok" {
			t.Errorf("%s: body status = %v, want ok", c.name, body["status"])
		}
		if code == http.StatusServiceUnavailable {
			if body["status"] != "unavailable" {
				t.Errorf("%s: body status = %v, want unavailable", c.name, body["status"])
			}
			if body["reason"] == "" || body["reason"] == nil {
				t.Errorf("%s: a 503 must explain itself", c.name)
			}
		}
		view, ok := body["pool"].(monitor.PoolHealthView)
		if !ok {
			t.Fatalf("%s: pool payload missing/mistyped: %#v", c.name, body["pool"])
		}
		if view.Total != len(c.creds) {
			t.Errorf("%s: pool.total = %d, want %d", c.name, view.Total, len(c.creds))
		}
	}
}

// A dead Anthropic pool must not fail the Codex endpoint's probe: the two
// endpoints are independently routable and a shared /healthz verdict would pull
// a working listener out of rotation.
func TestHealthzIsPerProvider(t *testing.T) {
	dead := healthzCooling("anthropic-dead")
	codex := &auth.Auth{ID: "codex", Kind: auth.KindAPIKey, Provider: auth.ProviderOpenAI}
	pool := auth.NewPool(nil, []*auth.Auth{dead, codex}, 10*time.Minute, false, "")

	if code, _ := healthzPayload(pool, "claude", auth.ProviderAnthropic); code != http.StatusServiceUnavailable {
		t.Errorf("claude /healthz = %d, want 503", code)
	}
	if code, _ := healthzPayload(pool, "codex", auth.ProviderOpenAI); code != http.StatusOK {
		t.Errorf("codex /healthz = %d, want 200", code)
	}
}
