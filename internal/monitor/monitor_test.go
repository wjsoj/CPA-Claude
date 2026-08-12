package monitor

import (
	"testing"
	"time"

	"github.com/wjsoj/cc-core/auth"
)

func TestRecordRollupAndPrune(t *testing.T) {
	m := &Monitor{stores: map[string]*provStore{}, cfg: Config{}}
	day := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)

	m.record(auth.ProviderAnthropic, Sample{TS: day, OK: true, LatencyMs: 100})
	m.record(auth.ProviderAnthropic, Sample{TS: day.Add(time.Minute), OK: false, Status: 500})
	// A 4xx is a failed request and now docks the day like any other. The old
	// rule counted it as "no signal" and green, which is how the strip stayed
	// at 100% through outages.
	m.record(auth.ProviderAnthropic, Sample{TS: day.Add(2 * time.Minute), OK: false, Status: 400})
	m.record(auth.ProviderAnthropic, Sample{TS: day.Add(3 * time.Minute), OK: true})

	st := m.stores[auth.ProviderAnthropic]
	if st == nil {
		t.Fatal("provider store not created")
	}
	d := st.Days["2026-05-29"]
	if d == nil || d.Total != 4 || d.OK != 2 {
		t.Fatalf("day rollup wrong: %+v", d)
	}
	if st.Last == nil || !st.Last.OK {
		t.Fatalf("last sample wrong: %+v", st.Last)
	}

	// A sample older than recentRetention must be pruned from Recent but the
	// older day rollup is retained until it falls past dayRetention.
	old := day.Add(-3 * recentRetention)
	m.record(auth.ProviderAnthropic, Sample{TS: old, OK: true})
	for _, s := range st.Recent {
		if s.TS.Equal(old) {
			t.Fatal("stale recent sample was not pruned")
		}
	}
}

// A sample is green only if the request actually succeeded. Every "no signal"
// excuse the old rule carried — pool-healthy override, CF 520–527, transport
// timeout, 4xx — is gone: those are failed requests and the uptime history has
// to say so.
func TestHealthySignalIsSuccessOnly(t *testing.T) {
	cases := []struct {
		name string
		s    Sample
		want bool
	}{
		{"2xx ok", Sample{OK: true, Status: 200}, true},
		{"5xx with stale PoolHealthy stamp → red", Sample{OK: false, Status: 500, PoolHealthy: true}, false},
		{"5xx → red", Sample{OK: false, Status: 500}, false},
		{"CF 52x transport failure → red", Sample{OK: false, Status: 522}, false},
		{"transport error / timeout → red", Sample{OK: false, Status: 0, Err: "context deadline exceeded"}, false},
		{"4xx probe rejection → red", Sample{OK: false, Status: 400}, false},
		{"503 passive sample, no creds → red", Sample{OK: false, Status: 503}, false},
	}
	for _, c := range cases {
		if got := c.s.healthySignal(); got != c.want {
			t.Errorf("%s: healthySignal = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestPruneDaysKeeps90(t *testing.T) {
	m := &Monitor{stores: map[string]*provStore{}, cfg: Config{}}
	base := time.Date(2026, 5, 29, 0, 0, 0, 0, time.UTC)
	// Record one probe per day across 200 days.
	for i := 0; i < 200; i++ {
		m.record(auth.ProviderOpenAI, Sample{TS: base.AddDate(0, 0, -i), OK: true})
	}
	st := m.stores[auth.ProviderOpenAI]
	if len(st.Days) > dayRetention {
		t.Fatalf("expected <= %d days retained, got %d", dayRetention, len(st.Days))
	}
}

func TestLastDaysFillsGaps(t *testing.T) {
	now := time.Date(2026, 5, 29, 8, 0, 0, 0, time.UTC)
	days := map[string]*DayStat{
		"2026-05-29": {Date: "2026-05-29", Total: 10, OK: 10},
		"2026-05-27": {Date: "2026-05-27", Total: 4, OK: 2},
	}
	out := lastDays(days, 5, now)
	if len(out) != 5 {
		t.Fatalf("want 5 days, got %d", len(out))
	}
	// Oldest first; index 4 is today.
	if out[4].Date != "2026-05-29" || out[4].Total != 10 {
		t.Fatalf("today bucket wrong: %+v", out[4])
	}
	// The gap day (2026-05-28) must be a zero-total placeholder.
	if out[3].Date != "2026-05-28" || out[3].Total != 0 {
		t.Fatalf("gap day not filled: %+v", out[3])
	}
}

func TestUptimePct(t *testing.T) {
	days := []DayStat{{Total: 100, OK: 99}, {Total: 100, OK: 100}}
	if got := uptimePct(days); got < 99.4 || got > 99.6 {
		t.Fatalf("uptimePct = %v, want ~99.5", got)
	}
	if got := uptimePct([]DayStat{{Total: 0}}); got != 0 {
		t.Fatalf("no-data uptime = %v, want 0", got)
	}
}

// snap builds a ProviderSnapshot from a state census, keeping the derived
// fields consistent the way GetSnapshot does.
func snap(slot bool, byState map[auth.HealthState]int) ProviderSnapshot {
	ps := ProviderSnapshot{SlotAvailable: slot, ByState: map[string]int{}}
	for _, s := range AllStates {
		n := byState[s]
		ps.ByState[string(s)] = n
		ps.TotalCreds += n
		switch s {
		case auth.HealthHealthy, auth.HealthHalfOpen, auth.HealthDegraded:
			ps.ServingCreds += n
		case auth.HealthCooling:
			ps.CoolingCreds += n
		}
	}
	ps.HealthyCreds = ps.ByState[string(auth.HealthHealthy)]
	return ps
}

// deriveStatus has exactly three branches and they key off serving, never off
// the "healthy" count (which used to include a credential whose circuit breaker
// had merely expired).
func TestDeriveStatus(t *testing.T) {
	cases := []struct {
		name string
		ps   ProviderSnapshot
		want string
	}{
		{"no creds", ProviderSnapshot{TotalCreds: 0}, "down"},

		// --- branch 1: serving == 0 → down ---
		{"all hard-failed", snap(true, map[auth.HealthState]int{auth.HealthHardFailed: 3}), "down"},
		{"all cooling", snap(true, map[auth.HealthState]int{auth.HealthCooling: 2}), "down"},
		{"all quota", snap(true, map[auth.HealthState]int{auth.HealthQuota: 2}), "down"},
		{"all disabled", snap(true, map[auth.HealthState]int{auth.HealthDisabled: 2}), "down"},

		// --- branch 2: serving > 0 with half_open/degraded/cooling → degraded ---
		{"one half-open among the dead", snap(true, map[auth.HealthState]int{auth.HealthHardFailed: 4, auth.HealthHalfOpen: 1}), "degraded"},
		{"healthy + degraded", snap(true, map[auth.HealthState]int{auth.HealthHealthy: 2, auth.HealthDegraded: 1}), "degraded"},
		{"healthy + one cooling", snap(true, map[auth.HealthState]int{auth.HealthHealthy: 2, auth.HealthCooling: 1}), "degraded"},

		// --- branch 3: all verified-good ---
		{"all healthy + slot", snap(true, map[auth.HealthState]int{auth.HealthHealthy: 2}), "operational"},
		{"all healthy but saturated", snap(false, map[auth.HealthState]int{auth.HealthHealthy: 2}), "degraded"},
		{"healthy + quota sibling, slot free", snap(true, map[auth.HealthState]int{auth.HealthHealthy: 1, auth.HealthQuota: 1}), "operational"},

		// The active probe never factors in — it hits ONE api-key credential
		// and never OAuth, so it cannot speak for pool capacity.
		{"probe 5xx but pool fine", func() ProviderSnapshot {
			ps := snap(true, map[auth.HealthState]int{auth.HealthHealthy: 2})
			ps.ProbeEnabled = true
			ps.LastProbe = &Sample{OK: false, Status: 500}
			return ps
		}(), "operational"},
	}
	for _, c := range cases {
		if got := deriveStatus(c.ps); got != c.want {
			t.Errorf("%s: deriveStatus = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestRecentWindow(t *testing.T) {
	now := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	samples := []Sample{
		{TS: now.Add(-48 * time.Hour)},
		{TS: now.Add(-2 * time.Hour)},
		{TS: now.Add(-30 * time.Minute)},
	}
	out := recentWindow(samples, 24*time.Hour, now)
	if len(out) != 2 {
		t.Fatalf("want 2 in-window samples, got %d", len(out))
	}
}
