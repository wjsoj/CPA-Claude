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
	m.record(auth.ProviderAnthropic, Sample{TS: day.Add(2 * time.Minute), OK: true})

	st := m.stores[auth.ProviderAnthropic]
	if st == nil {
		t.Fatal("provider store not created")
	}
	d := st.Days["2026-05-29"]
	if d == nil || d.Total != 3 || d.OK != 2 {
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

func TestDeriveStatus(t *testing.T) {
	cases := []struct {
		name string
		ps   ProviderSnapshot
		want string
	}{
		{"no creds", ProviderSnapshot{TotalCreds: 0}, "down"},
		{"all unhealthy", ProviderSnapshot{TotalCreds: 3, HealthyCreds: 0}, "down"},
		{"healthy + slot + probe ok", ProviderSnapshot{TotalCreds: 2, HealthyCreds: 2, SlotAvailable: true, ProbeEnabled: true, LastProbe: &Sample{OK: true}}, "operational"},
		{"healthy + slot + probe fail", ProviderSnapshot{TotalCreds: 2, HealthyCreds: 2, SlotAvailable: true, ProbeEnabled: true, LastProbe: &Sample{OK: false}}, "degraded"},
		{"healthy but saturated", ProviderSnapshot{TotalCreds: 2, HealthyCreds: 2, SlotAvailable: false}, "degraded"},
		{"passive only, slot free", ProviderSnapshot{TotalCreds: 1, HealthyCreds: 1, SlotAvailable: true}, "operational"},
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
