package server

import (
	"strings"
	"testing"
	"time"

	saasdb "github.com/wjsoj/CPA-Claude/internal/saas/db"

	"github.com/wjsoj/CPA-Claude/internal/config"
)

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// A promotion is a promise with a deadline. Both edges matter: starting early
// gives away money nobody promised, and ending late keeps giving it away.
func TestPromoWindowEdges(t *testing.T) {
	start := mustTime(t, "2026-08-09T12:00:00+08:00")
	end := start.Add(48 * time.Hour)
	promos := []config.PricingPromotion{{
		Name: "codex-48h", Provider: "openai", Multiplier: 0.001, Start: start, End: end,
	}}

	cases := []struct {
		name string
		now  time.Time
		want bool
	}{
		{"a second before the start", start.Add(-time.Second), false},
		{"exactly at the start", start, true},
		{"halfway through", start.Add(24 * time.Hour), true},
		{"a second before the end", end.Add(-time.Second), true},
		{"exactly at the end", end, false},
		{"a day after", end.Add(24 * time.Hour), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mult, ok := promoFor(promos, "openai", tc.now)
			if ok != tc.want {
				t.Fatalf("active = %t, want %t", ok, tc.want)
			}
			if ok && mult != 0.001 {
				t.Fatalf("multiplier = %v, want 0.001", mult)
			}
		})
	}
}

// The promotion is Codex-only; Anthropic traffic must bill exactly as before.
func TestPromoIsScopedToItsProvider(t *testing.T) {
	now := mustTime(t, "2026-08-10T00:00:00+08:00")
	promos := []config.PricingPromotion{{
		Provider: "openai", Multiplier: 0.001,
		Start: mustTime(t, "2026-08-09T12:00:00+08:00"),
		End:   mustTime(t, "2026-08-11T12:00:00+08:00"),
	}}
	if _, ok := promoFor(promos, "openai", now); !ok {
		t.Error("codex traffic is not in the promotion")
	}
	if _, ok := promoFor(promos, "anthropic", now); ok {
		t.Error("the codex promotion leaked onto anthropic traffic")
	}
	// Aliases resolve to the same provider — a config saying "codex" must not
	// silently apply to nothing.
	aliased := []config.PricingPromotion{{
		Provider: "openai", Multiplier: 0.001,
		Start: mustTime(t, "2026-08-09T12:00:00+08:00"),
		End:   mustTime(t, "2026-08-11T12:00:00+08:00"),
	}}
	if _, ok := promoFor(aliased, "codex", now); !ok {
		t.Error("a request tagged with the friendly alias missed the promotion")
	}
}

// Precedence is the decision that costs money: during a promotion even
// marked-up upstream API-key traffic bills at the promotional rate, because the
// user cannot see which credential served them.
func TestEffectiveMultiplierPrecedence(t *testing.T) {
	start := mustTime(t, "2026-08-09T12:00:00+08:00")
	live := []config.PricingPromotion{{
		Provider: "openai", Multiplier: 0.001, Start: start, End: start.Add(48 * time.Hour),
	}}
	expired := []config.PricingPromotion{{
		Provider: "openai", Multiplier: 0.001,
		Start: start.Add(-72 * time.Hour), End: start.Add(-24 * time.Hour),
	}}
	group := &saasdb.PricingGroup{CodexMultiplier: 0.02, ClaudeMultiplier: 0.05}

	t.Run("expired promotion leaves the per-key override in charge", func(t *testing.T) {
		b := &saasBilling{promos: expired}
		got, note := b.effectiveMultiplier(group, "openai", "gpt-5.6-sol", 0.9)
		if got != 0.9 {
			t.Fatalf("multiplier = %v, want the 0.9 upstream-key override", got)
		}
		if !strings.Contains(note, "upstream key") {
			t.Errorf("note = %q, want it to name the upstream key", note)
		}
	})

	t.Run("no promotion and no override falls back to the group", func(t *testing.T) {
		b := &saasBilling{promos: expired}
		if got, _ := b.effectiveMultiplier(group, "openai", "gpt-5.6-sol", 0); got != 0.02 {
			t.Fatalf("multiplier = %v, want the group's 0.02", got)
		}
	})

	t.Run("anthropic is untouched while the codex promotion runs", func(t *testing.T) {
		b := &saasBilling{promos: live}
		if got, _ := b.effectiveMultiplier(group, "anthropic", "claude-opus-4-7", 0); got != 0.05 {
			t.Fatalf("multiplier = %v, want the group's 0.05", got)
		}
	})
}

// Overlapping windows resolve to the cheapest: the user was promised both.
func TestOverlappingPromotionsPickTheCheapest(t *testing.T) {
	now := mustTime(t, "2026-08-10T00:00:00+08:00")
	win := func(m float64) config.PricingPromotion {
		return config.PricingPromotion{
			Provider: "openai", Multiplier: m,
			Start: mustTime(t, "2026-08-09T12:00:00+08:00"),
			End:   mustTime(t, "2026-08-11T12:00:00+08:00"),
		}
	}
	got, ok := promoFor([]config.PricingPromotion{win(0.01), win(0.001)}, "openai", now)
	if !ok || got != 0.001 {
		t.Fatalf("multiplier = %v (ok=%t), want the cheaper 0.001", got, ok)
	}
}

// A promotion nobody can see is a promotion nobody acts on.
func TestDisplayMultiplierFollowsTheCharge(t *testing.T) {
	start := time.Now().Add(-time.Hour)
	s := &Server{cfg: &config.Config{}}
	s.cfg.SaaS.Promotions = []config.PricingPromotion{{
		Provider: "openai", Multiplier: 0.001, Start: start, End: start.Add(48 * time.Hour),
	}}
	if got := s.displayMultiplier("openai", 0.02); got != 0.001 {
		t.Errorf("codex display = %v, want the promotional 0.001", got)
	}
	if got := s.displayMultiplier("anthropic", 0.05); got != 0.05 {
		t.Errorf("claude display = %v, want the stored 0.05", got)
	}

	// With no promotion configured the stored value is quoted verbatim.
	plain := &Server{cfg: &config.Config{}}
	if got := plain.displayMultiplier("openai", 0.02); got != 0.02 {
		t.Errorf("display = %v, want the stored 0.02", got)
	}
}
