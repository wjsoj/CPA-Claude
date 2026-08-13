package monitor

import (
	"testing"
	"time"

	"github.com/wjsoj/cc-core/auth"
)

// apiKey builds an API-key credential in a chosen health state. The fields are
// set directly rather than driven through Mark* so a test can express "the
// circuit breaker expired a minute ago" without sleeping through the backoff.
func apiKey(id string) *auth.Auth {
	return &auth.Auth{ID: id, Kind: auth.KindAPIKey, Provider: auth.ProviderAnthropic, Label: id}
}

func hardFailed(id string) *auth.Auth {
	a := apiKey(id)
	a.HardFailureAt = time.Now().Add(-time.Hour)
	a.HardFailureReason = "revoked"
	a.LastFailure = a.HardFailureAt
	return a
}

func cooling(id string) *auth.Auth {
	a := apiKey(id)
	a.QuarantineStrikes = 2
	a.QuarantineUntil = time.Now().Add(30 * time.Second)
	a.LastFailure = time.Now().Add(-time.Second)
	a.LastFailureReason = "http 500"
	return a
}

// halfOpen is a channel whose pause elapsed but which has not succeeded since
// the failure that opened it. It is routable and it is NOT recovered.
func halfOpen(id string) *auth.Auth {
	a := apiKey(id)
	a.QuarantineStrikes = 2
	a.QuarantineUntil = time.Now().Add(-time.Minute) // expired
	a.LastFailure = time.Now().Add(-2 * time.Minute)
	a.LastFailureReason = "http 500"
	return a
}

func poolOf(creds ...*auth.Auth) *auth.Pool {
	return auth.NewPool(nil, creds, 10*time.Minute, false, "")
}

// The whole point of the rewrite: a pool of nothing but hard-failed keys plus a
// single half-open one is AVAILABLE (traffic still routes there) while its
// healthy count is zero (nothing has proven it works). The old bool could say
// only one of those two things, and said the wrong one.
func TestPoolAvailableWithHalfOpenButZeroHealthy(t *testing.T) {
	p := poolOf(hardFailed("a"), hardFailed("b"), halfOpen("c"))
	ph, slot := PoolHealthFor(p, auth.ProviderAnthropic)

	if !ph.Available() {
		t.Fatalf("pool must be available with one half-open credential: %+v", ph)
	}
	if ph.Serving != 1 {
		t.Errorf("serving = %d, want 1", ph.Serving)
	}
	if got := ph.ByState[auth.HealthHealthy]; got != 0 {
		t.Errorf("healthy = %d, want 0 — a half-open credential is not healthy", got)
	}
	if got := ph.ByState[auth.HealthHalfOpen]; got != 1 {
		t.Errorf("half_open = %d, want 1", got)
	}
	if !slot {
		t.Error("uncapped serving credential must report a free slot")
	}
	if ph.Worst != auth.HealthHardFailed {
		t.Errorf("worst = %q, want hard_failed", ph.Worst)
	}
}

// Every credential paused by the circuit breaker means nothing can take a
// request, even though not one of them is hard-failed (API keys never are).
func TestPoolUnavailableWhenAllCooling(t *testing.T) {
	p := poolOf(cooling("a"), cooling("b"))
	ph, slot := PoolHealthFor(p, auth.ProviderAnthropic)

	if ph.Available() {
		t.Fatalf("all-cooling pool must not be available: %+v", ph)
	}
	if ph.Serving != 0 {
		t.Errorf("serving = %d, want 0", ph.Serving)
	}
	if got := ph.ByState[auth.HealthCooling]; got != 2 {
		t.Errorf("cooling = %d, want 2", got)
	}
	if slot {
		t.Error("no serving credential can have a free slot")
	}
}

// by_state partitions the pool exactly. The old census dropped a cooling API
// key on the floor — it was neither "healthy" (quarantined) nor "unhealthy"
// (hard_failure is always false for a key), so the buckets summed to less than
// total and the panel silently lost credentials.
func TestPoolViewByStateSumsToTotal(t *testing.T) {
	disabled := apiKey("d")
	disabled.Disabled = true
	quota := apiKey("q")
	quota.QuotaExceededAt = time.Now()
	quota.QuotaResetAt = time.Now().Add(10 * time.Minute)
	degraded := apiKey("g")
	degraded.ConsecutiveFailures = 3
	degraded.LastFailure = time.Now()
	degraded.LastFailureReason = "http 500"

	p := poolOf(apiKey("ok"), halfOpen("h"), cooling("c"), hardFailed("f"), disabled, quota, degraded)
	ph, _ := PoolHealthFor(p, auth.ProviderAnthropic)
	view := NewPoolHealthView(ph)

	if view.Total != 7 {
		t.Fatalf("total = %d, want 7", view.Total)
	}
	if len(view.ByState) != len(AllStates) {
		t.Errorf("by_state must carry all %d states (zero-filled), got %d", len(AllStates), len(view.ByState))
	}
	sum := 0
	for _, s := range AllStates {
		sum += view.ByState[string(s)]
	}
	if sum != view.Total {
		t.Fatalf("by_state sums to %d, want total %d: %+v", sum, view.Total, view.ByState)
	}
	for _, s := range []auth.HealthState{
		auth.HealthHealthy, auth.HealthHalfOpen, auth.HealthCooling,
		auth.HealthHardFailed, auth.HealthDisabled, auth.HealthQuota, auth.HealthDegraded,
	} {
		if view.ByState[string(s)] != 1 {
			t.Errorf("%s = %d, want 1", s, view.ByState[string(s)])
		}
	}
	// healthy + half_open + degraded are serving; quota/cooling/hard/disabled are not.
	if view.Serving != 3 {
		t.Errorf("serving = %d, want 3", view.Serving)
	}
	if !view.Available {
		t.Error("available must be true while three credentials serve")
	}
	// One credential in every state, so this pins which one wins. hard_failed,
	// not disabled: an operator switching a channel off is not the worst thing
	// happening in a pool that also has a credential auto-retired under it.
	// This asserted `disabled` while cc-core's Severity ladder ranked it top
	// (fixed in v0.8.84) — the inverted answer reached users through the 503
	// body of an exhausted pool and the monitor's per-provider error line.
	if view.WorstState != string(auth.HealthHardFailed) {
		t.Errorf("worst_state = %q, want hard_failed", view.WorstState)
	}
}

// An empty provider is "down", not "healthy": NewPoolHealthView must not report
// a pool with nothing in it as available.
func TestPoolViewEmptyProvider(t *testing.T) {
	view := NewPoolHealthView(auth.NewPoolHealth(auth.ProviderOpenAI, nil))
	if view.Available || view.Total != 0 || view.Serving != 0 {
		t.Fatalf("empty pool view wrong: %+v", view)
	}
	if view.WorstState != string(auth.HealthHealthy) {
		t.Errorf("worst_state = %q, want healthy for an empty pool", view.WorstState)
	}
	if len(view.ByState) != len(AllStates) {
		t.Errorf("by_state must still be zero-filled, got %+v", view.ByState)
	}
}

// PoolHealthFor filters by provider — an unhealthy Codex pool must not drag the
// Anthropic badge down (and vice versa).
func TestPoolHealthForIsPerProvider(t *testing.T) {
	openaiDead := apiKey("openai-dead")
	openaiDead.Provider = auth.ProviderOpenAI
	openaiDead.HardFailureAt = time.Now()

	p := poolOf(apiKey("anthropic-ok"), openaiDead)

	if ph, _ := PoolHealthFor(p, auth.ProviderAnthropic); !ph.Available() || ph.Total != 1 {
		t.Errorf("anthropic pool wrong: %+v", ph)
	}
	if ph, _ := PoolHealthFor(p, auth.ProviderOpenAI); ph.Available() || ph.Total != 1 {
		t.Errorf("openai pool wrong: %+v", ph)
	}
}

// passiveSample is the OAuth-only provider's substitute for a probe. It must
// only go green on a verified credential: a half-open pool is routable but
// unproven, and painting it green is the original sin this whole change undoes.
func TestPassiveSampleHonesty(t *testing.T) {
	t.Run("healthy", func(t *testing.T) {
		m := &Monitor{pool: poolOf(apiKey("ok")), stores: map[string]*provStore{}}
		if s := m.passiveSample(auth.ProviderAnthropic); !s.OK || s.Status != 200 {
			t.Fatalf("want green sample, got %+v", s)
		}
	})
	t.Run("half-open only", func(t *testing.T) {
		m := &Monitor{pool: poolOf(halfOpen("h")), stores: map[string]*provStore{}}
		s := m.passiveSample(auth.ProviderAnthropic)
		if s.OK || s.Status != 503 {
			t.Fatalf("half-open pool must not be recorded green: %+v", s)
		}
	})
	t.Run("nothing serving", func(t *testing.T) {
		m := &Monitor{pool: poolOf(cooling("c")), stores: map[string]*provStore{}}
		s := m.passiveSample(auth.ProviderAnthropic)
		if s.OK || s.Status != 503 || s.Err != "no serving credentials" {
			t.Fatalf("want red sample, got %+v", s)
		}
	})
}

// HealthReportFor falls back to the AuthInfo snapshot when the credential has
// left the pool between Status() and the lookup, rather than reporting a
// removed credential as healthy-by-default.
func TestHealthReportForFallsBackToSnapshot(t *testing.T) {
	info := hardFailed("gone").Snapshot()
	r := HealthReportFor(poolOf(), info)
	if r.State != auth.HealthHardFailed || r.Serving {
		t.Fatalf("fallback report wrong: %+v", r)
	}

	coolInfo := cooling("c").Snapshot()
	cr := HealthReportFor(nil, coolInfo)
	if cr.State != auth.HealthCooling || cr.Serving {
		t.Fatalf("cooling fallback wrong: %+v", cr)
	}
	if cr.RetryAfter <= 0 {
		t.Error("cooling fallback must carry a retry-after derived from QuarantineUntil")
	}
}
