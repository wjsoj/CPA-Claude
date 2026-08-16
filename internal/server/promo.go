package server

import (
	"time"

	"github.com/wjsoj/cc-core/auth"

	"github.com/wjsoj/CPA-Claude/internal/config"
)

// Time-boxed promotional pricing.
//
// A promotion replaces the multiplier a request would otherwise be billed at —
// including the per-key override that upstream API-key capacity normally
// carries. That is deliberate and it is the expensive choice: during a
// promotion, traffic served by a marked-up upstream key is sold at the
// promotional rate, i.e. below what it cost us. The alternative was worse for
// the user, who cannot see which credential served them and would find the same
// model billed at two very different rates within one session.
//
// The window is wall-clock and comes from config, so a promotion starts and
// ends on its own. Nothing has to be deployed to end one, which matters: the
// failure mode of a hand-rolled promotion is forgetting to turn it off.

// promoFor returns the promotional multiplier in force for a provider at time
// now, or ok=false when no promotion applies.
//
// Overlapping promotions resolve to the cheapest, not the first: if two windows
// are live the user was promised both, and honouring the more generous one is
// the only answer that cannot be read as a bait-and-switch.
func promoFor(promos []config.PricingPromotion, provider string, now time.Time) (float64, bool) {
	provider = auth.NormalizeProvider(provider)
	best, found := 0.0, false
	for _, p := range promos {
		if p.Multiplier <= 0 || !p.Covers(provider, now) {
			continue
		}
		if !found || p.Multiplier < best {
			best, found = p.Multiplier, true
		}
	}
	return best, found
}

// promoMultiplier is the server-level accessor used by the billing and display
// paths. Kept as a method so both read the same config and the same clock.
func (s *Server) promoMultiplier(provider string) (float64, bool) {
	if s == nil || s.cfg == nil {
		return 0, false
	}
	return promoFor(s.cfg.SaaS.Promotions, provider, time.Now())
}

// displayMultiplier is what the panel and the wallet API should show for a
// provider: the promotional rate while one is live, otherwise the group's own.
//
// Showing the stored value during a promotion would tell users they are paying
// twenty times what they are actually charged — the discount would be invisible
// to exactly the people it was announced to.
func (s *Server) displayMultiplier(provider string, groupMult float64) float64 {
	if mult, ok := s.promoMultiplier(provider); ok {
		return mult
	}
	return groupMult
}
