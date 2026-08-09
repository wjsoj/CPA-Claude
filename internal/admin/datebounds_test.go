package admin

import (
	"testing"
	"time"

	"github.com/wjsoj/cc-core/requestlog"
)

// The panel's date pickers only ever send bare calendar days, and that form
// has to survive as day labels all the way into requestlog — converting it
// to instants here is what used to push every panel query onto the
// row-by-row path (1.8s on the production archive, against 48ms).
func TestApplyDateBoundsKeepsWholeDaysAsLabels(t *testing.T) {
	cases := []struct {
		name             string
		from, to         string
		wantFrom, wantTo string // expected day labels
		wantStamps       bool   // expected to fall back to timestamp bounds
	}{
		{name: "both ends", from: "2026-08-02", to: "2026-08-09", wantFrom: "2026-08-02", wantTo: "2026-08-09"},
		{name: "open end", from: "2026-08-02", wantFrom: "2026-08-02"},
		{name: "open start", to: "2026-08-09", wantTo: "2026-08-09"},
		{name: "unbounded"},
		// A sub-day window cannot be expressed in day labels; it must keep
		// its instants and take the path that can answer it.
		{name: "rfc3339 start", from: "2026-08-02T13:00:00+08:00", to: "2026-08-09", wantStamps: true},
		{name: "rfc3339 end", from: "2026-08-02", to: "2026-08-09T13:00:00+08:00", wantStamps: true},
		{name: "garbage is ignored", from: "last tuesday", wantStamps: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var f requestlog.Filter
			applyDateBounds(&f, tc.from, tc.to)
			if f.FromDay != tc.wantFrom || f.ToDay != tc.wantTo {
				t.Errorf("labels = %q..%q, want %q..%q", f.FromDay, f.ToDay, tc.wantFrom, tc.wantTo)
			}
			gotStamps := !f.From.IsZero() || !f.To.IsZero()
			if gotStamps != tc.wantStamps {
				t.Errorf("timestamp bounds present = %t, want %t (from=%v to=%v)",
					gotStamps, tc.wantStamps, f.From, f.To)
			}
			// Labels and timestamps together are a contradiction requestlog
			// resolves by discarding the labels — so never send both.
			if f.FromDay != "" && gotStamps {
				t.Error("sent both a day label and a timestamp bound")
			}
		})
	}
}

func TestIsDayLabel(t *testing.T) {
	for _, s := range []string{"2026-08-02", "2028-02-29"} {
		if !isDayLabel(s) {
			t.Errorf("%q rejected", s)
		}
	}
	for _, s := range []string{"", "2026-8-2", "2026-08-02T00:00:00Z", "2026-13-01", "today"} {
		if isDayLabel(s) {
			t.Errorf("%q accepted", s)
		}
	}
}

// The cache is keyed by filter, so any field that changes the answer has to
// be in the key. UserID is the one where a collision is not merely stale
// data but one customer being shown another's spend.
func TestReqCacheKeySeparatesEveryFilterDimension(t *testing.T) {
	base := requestlog.Filter{
		FromDay: "2026-08-02", ToDay: "2026-08-09",
		Client: "alpha", ClientToken: "sk-...aaaa", Model: "gpt-5.6-sol",
		Provider: "openai", AuthID: "auth-1.json",
		UserID: 42, Status: 200, Limit: 50, Offset: 0,
	}
	variants := map[string]func(*requestlog.Filter){
		"FromDay":     func(f *requestlog.Filter) { f.FromDay = "2026-08-03" },
		"ToDay":       func(f *requestlog.Filter) { f.ToDay = "2026-08-08" },
		"From":        func(f *requestlog.Filter) { f.From = time.Unix(1, 0) },
		"To":          func(f *requestlog.Filter) { f.To = time.Unix(2, 0) },
		"Client":      func(f *requestlog.Filter) { f.Client = "beta" },
		"ClientToken": func(f *requestlog.Filter) { f.ClientToken = "sk-...bbbb" },
		"Model":       func(f *requestlog.Filter) { f.Model = "gpt-5.5" },
		"Provider":    func(f *requestlog.Filter) { f.Provider = "anthropic" },
		"AuthID":      func(f *requestlog.Filter) { f.AuthID = "auth-2.json" },
		"UserID":      func(f *requestlog.Filter) { f.UserID = 43 },
		"Status":      func(f *requestlog.Filter) { f.Status = 429 },
		"Limit":       func(f *requestlog.Filter) { f.Limit = 100 },
		"Offset":      func(f *requestlog.Filter) { f.Offset = 50 },
		"PageOnly":    func(f *requestlog.Filter) { f.PageOnly = true },
	}
	want := reqCacheKey(base)
	for name, mutate := range variants {
		f := base
		mutate(&f)
		if got := reqCacheKey(f); got == want {
			t.Errorf("%s does not affect the cache key: both are %q", name, got)
		}
	}
	// Dir is per-process and deliberately excluded.
	withDir := base
	withDir.Dir = "/somewhere/else"
	if reqCacheKey(withDir) != want {
		t.Error("Dir changed the key; it is constant per process and inflates it for nothing")
	}
}
