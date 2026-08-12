package admin

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/wjsoj/CPA-Claude/internal/config"
	"github.com/wjsoj/cc-core/auth"
	"github.com/wjsoj/cc-core/pricing"
)

func testKey(id string) *auth.Auth {
	return &auth.Auth{ID: id, Kind: auth.KindAPIKey, Provider: auth.ProviderAnthropic, Label: id}
}

func testHardFailed(id string) *auth.Auth {
	a := testKey(id)
	a.HardFailureAt = time.Now().Add(-time.Hour)
	a.HardFailureReason = "revoked"
	a.LastFailure = a.HardFailureAt
	return a
}

func testCooling(id string) *auth.Auth {
	a := testKey(id)
	a.QuarantineStrikes = 2
	a.QuarantineUntil = time.Now().Add(45 * time.Second)
	a.LastFailure = time.Now().Add(-time.Second)
	a.LastFailureReason = "http 500"
	return a
}

// testHalfOpen: the pause elapsed, nothing has succeeded since. Routable, not
// recovered.
func testHalfOpen(id string) *auth.Auth {
	a := testKey(id)
	a.QuarantineStrikes = 2
	a.QuarantineUntil = time.Now().Add(-time.Minute)
	a.LastFailure = time.Now().Add(-2 * time.Minute)
	a.LastFailureReason = "http 500"
	return a
}

func testHandler(creds ...*auth.Auth) *Handler {
	return &Handler{
		cfg:     &config.Config{},
		pool:    auth.NewPool(nil, creds, 10*time.Minute, false, ""),
		pricing: pricing.NewCatalog(pricing.Config{}),
	}
}

// A pool that is entirely hard-failed except for one half-open credential is
// available (something serves) while counts.healthy is zero (nothing is
// verified). The single `healthy` bool could not express that, and reported the
// wrong half of it.
func TestCensusHalfOpenAvailableButNotHealthy(t *testing.T) {
	h := testHandler(testHardFailed("a"), testHardFailed("b"), testHalfOpen("c"))
	census := h.poolCensus()

	pool, ok := census.Pools[auth.ProviderAnthropic]
	if !ok {
		t.Fatalf("no anthropic pool in %+v", census.Pools)
	}
	if !pool.Available {
		t.Fatal("pool.available must be true — a half-open credential still takes traffic")
	}
	if pool.Serving != 1 {
		t.Errorf("pool.serving = %d, want 1", pool.Serving)
	}
	if census.Counts.Healthy != 0 {
		t.Errorf("counts.healthy = %d, want 0", census.Counts.Healthy)
	}
	if census.Counts.HalfOpen != 1 {
		t.Errorf("counts.half_open = %d, want 1", census.Counts.HalfOpen)
	}
	if census.Counts.Serving != 1 {
		t.Errorf("counts.serving = %d, want 1", census.Counts.Serving)
	}
	if pool.WorstState != string(auth.HealthHardFailed) {
		t.Errorf("worst_state = %q, want hard_failed", pool.WorstState)
	}

	// The half-open credential must not be published as healthy, and must carry
	// its strike count so the panel can explain why it is amber.
	var row statusOverviewAuth
	for _, a := range census.Auths {
		if a.State == string(auth.HealthHalfOpen) {
			row = a
		}
	}
	if row.State == "" {
		t.Fatal("no half_open credential in auths[]")
	}
	if row.Healthy {
		t.Error("legacy healthy bool must be false for a half-open credential")
	}
	if !row.Serving {
		t.Error("serving must be true for a half-open credential")
	}
	if row.QuarantineStrikes != 2 {
		t.Errorf("quarantine_strikes = %d, want 2", row.QuarantineStrikes)
	}
	if row.QuarantinedUntil != nil {
		t.Error("quarantined_until must be absent once the pause has elapsed")
	}
	if row.Reason == "" {
		t.Error("a non-healthy credential must carry a reason")
	}
}

// Everything paused by the circuit breaker: nothing serves, so the pool is
// unavailable — even though not one credential is hard-failed (an API key never
// is).
func TestCensusAllCoolingIsUnavailable(t *testing.T) {
	h := testHandler(testCooling("a"), testCooling("b"))
	census := h.poolCensus()

	pool := census.Pools[auth.ProviderAnthropic]
	if pool.Available {
		t.Fatalf("all-cooling pool must be unavailable: %+v", pool)
	}
	if census.Counts.Serving != 0 || census.Counts.Cooling != 2 {
		t.Errorf("counts wrong: serving=%d cooling=%d", census.Counts.Serving, census.Counts.Cooling)
	}
	if census.Counts.Unhealthy != 0 {
		t.Errorf("counts.unhealthy = %d, want 0 — cooling is not hard-failed", census.Counts.Unhealthy)
	}
	for _, a := range census.Auths {
		if a.Serving {
			t.Errorf("credential %q must not be serving while cooling", a.Label)
		}
		if a.RetryAfterSeconds <= 0 {
			t.Errorf("credential %q must publish retry_after_seconds while cooling", a.Label)
		}
	}
}

// The state buckets partition the pool: their sum is always total. The old
// ladder lost a cooling API key entirely (not healthy, and hard_failure is
// always false for a key), so the panel's numbers did not add up.
func TestCensusCountsSumToTotal(t *testing.T) {
	disabled := testKey("d")
	disabled.Disabled = true
	quota := testKey("q")
	quota.QuotaExceededAt = time.Now()
	quota.QuotaResetAt = time.Now().Add(10 * time.Minute)
	degraded := testKey("g")
	degraded.ConsecutiveFailures = 3
	degraded.LastFailure = time.Now()
	degraded.LastFailureReason = "http 500"

	h := testHandler(testKey("ok"), testHalfOpen("h"), testCooling("c"),
		testHardFailed("f"), disabled, quota, degraded)
	c := h.poolCensus().Counts

	sum := c.Healthy + c.HalfOpen + c.Degraded + c.Quota + c.Cooling + c.Unhealthy + c.Disabled
	if sum != c.Total {
		t.Fatalf("state buckets sum to %d, want total %d: %+v", sum, c.Total, c)
	}
	if c.Total != 7 {
		t.Fatalf("total = %d, want 7", c.Total)
	}
	if c.Serving != 3 {
		t.Errorf("serving = %d, want 3 (healthy + half_open + degraded)", c.Serving)
	}
	if c.APIKey+c.OAuth != c.Total {
		t.Errorf("kind counts %d+%d != total %d", c.APIKey, c.OAuth, c.Total)
	}
}

// The wire contract the status SPA is written against. Key names are load
// bearing: the frontend is developed in parallel against this exact shape.
func TestOverviewJSONContract(t *testing.T) {
	h := testHandler(testHalfOpen("h"), testCooling("c"))
	census := h.poolCensus()

	var out statusOverview
	out.Counts = census.Counts
	out.Pool = census.Pools
	out.Auths = census.Auths

	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}

	counts := got["counts"].(map[string]any)
	for _, k := range []string{
		"total", "healthy", "half_open", "degraded", "quota", "cooling",
		"unhealthy", "disabled", "serving", "oauth", "apikey", "models",
	} {
		if _, ok := counts[k]; !ok {
			t.Errorf("counts.%s missing", k)
		}
	}

	pool := got["pool"].(map[string]any)[auth.ProviderAnthropic].(map[string]any)
	for _, k := range []string{"available", "total", "serving", "worst_state", "by_state"} {
		if _, ok := pool[k]; !ok {
			t.Errorf("pool.%s missing", k)
		}
	}
	byState := pool["by_state"].(map[string]any)
	for _, k := range []string{"healthy", "half_open", "degraded", "quota", "cooling", "hard_failed", "disabled"} {
		if _, ok := byState[k]; !ok {
			t.Errorf("pool.by_state.%s missing", k)
		}
	}

	rows := got["auths"].([]any)
	if len(rows) != 2 {
		t.Fatalf("auths length = %d, want 2", len(rows))
	}
	for _, r := range rows {
		row := r.(map[string]any)
		// New contract.
		for _, k := range []string{"state", "serving", "reason", "consecutive_failures", "quarantine_strikes"} {
			if _, ok := row[k]; !ok {
				t.Errorf("auths[].%s missing (row=%v)", k, row)
			}
		}
		// Legacy fields must survive untouched.
		if _, ok := row["healthy"]; !ok {
			t.Error("auths[].healthy (legacy) must be preserved")
		}
		if row["state"] == string(auth.HealthCooling) {
			if _, ok := row["retry_after_seconds"]; !ok {
				t.Error("a cooling credential must publish retry_after_seconds")
			}
			if _, ok := row["quarantined_until"]; !ok {
				t.Error("a cooling credential must publish quarantined_until")
			}
		}
	}
}

// The admin summary's rows carry the same embedded health block as the public
// ones, under the same names — including the circuit-breaker fields that used
// to live directly on authRow.
func TestAuthRowCarriesHealthBlock(t *testing.T) {
	rep := auth.HealthReport{
		State:               auth.HealthHalfOpen,
		Serving:             true,
		Reason:              "unverified after pause (strike 2)",
		ConsecutiveFailures: 3,
		QuarantineStrikes:   2,
		LastSuccess:         time.Now().Add(-time.Hour),
	}
	row := authRow{ID: "x", Healthy: rep.Healthy(), authHealth: newAuthHealth(rep)}
	b, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got["state"] != string(auth.HealthHalfOpen) {
		t.Errorf("state = %v, want half_open", got["state"])
	}
	if got["serving"] != true {
		t.Errorf("serving = %v, want true", got["serving"])
	}
	if got["healthy"] != false {
		t.Errorf("legacy healthy = %v, want false for half_open", got["healthy"])
	}
	if got["quarantine_strikes"].(float64) != 2 {
		t.Errorf("quarantine_strikes = %v, want 2", got["quarantine_strikes"])
	}
	if got["consecutive_failures"].(float64) != 3 {
		t.Errorf("consecutive_failures = %v, want 3", got["consecutive_failures"])
	}
	if _, ok := got["last_success_at"]; !ok {
		t.Error("last_success_at missing")
	}
}

// retry_after_seconds rounds up: a client retrying at the truncated second is
// early, which is how a stampede back onto a still-paused channel starts.
func TestRetryAfterRoundsUp(t *testing.T) {
	v := newAuthHealth(auth.HealthReport{State: auth.HealthCooling, RetryAfter: 1500 * time.Millisecond})
	if v.RetryAfterSeconds != 2 {
		t.Fatalf("retry_after_seconds = %d, want 2", v.RetryAfterSeconds)
	}
	if got := newAuthHealth(auth.HealthReport{State: auth.HealthHealthy}).RetryAfterSeconds; got != 0 {
		t.Fatalf("healthy credential must not carry a retry hint, got %d", got)
	}
}
