package auth

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// Pool holds all credentials (OAuth + API keys) and assigns them to client
// sessions with slot-based concurrency for OAuth and unlimited for API keys.
//
// Concurrency model:
//   - A "client session" is identified by the client's access token.
//   - When a session makes a request, it is sticky-assigned to one OAuth auth.
//   - The OAuth auth holds at most MaxConcurrent distinct active sessions.
//   - A session is considered active if its last request is within ActiveWindow.
//   - When all OAuth auths are saturated or unhealthy, the session falls back
//     to an API key (unlimited).
type Pool struct {
	mu           sync.Mutex
	oauths       []*Auth
	apikeys      []*Auth
	sessions     map[string]*session // client token -> session
	activeWindow time.Duration
	useUTLS      bool
	defaultProxy string
	// usage24h, when set, returns a cost-weighted token count for the given
	// OAuth auth over the last ~24h (see usage.Counts.WeightedTotal — input
	// 1×, cache_create 1.25×, cache_read 0.1×, output 5×). Drives OAuth
	// selection in pickOAuthLocked: the candidate with the lowest weighted
	// usage wins, so cache-heavy credentials aren't penalized by the near-
	// free cache_read stream and the scarce output tokens dominate.
	usage24h func(authID string) int64
}

type session struct {
	clientToken string
	authID      string // empty = never assigned
	kind        Kind
	lastSeen    time.Time
}

func NewPool(oauths, apikeys []*Auth, activeWindow time.Duration, useUTLS bool, defaultProxy string) *Pool {
	p := &Pool{
		oauths:       append([]*Auth(nil), oauths...),
		apikeys:      append([]*Auth(nil), apikeys...),
		sessions:     make(map[string]*session),
		activeWindow: activeWindow,
		useUTLS:      useUTLS,
		defaultProxy: defaultProxy,
	}
	// Apply default proxy to OAuths that don't specify one.
	for _, a := range p.oauths {
		if a.ProxyURL == "" && defaultProxy != "" {
			a.ProxyURL = defaultProxy
		}
	}
	return p
}

func (p *Pool) UseUTLS() bool               { return p.useUTLS }
func (p *Pool) ActiveWindow() time.Duration { return p.activeWindow }

// SetUsage24hFunc installs a callback used as the load-balancing tiebreaker
// when picking an OAuth credential. fn must be safe for concurrent use and
// should not call back into the pool.
func (p *Pool) SetUsage24hFunc(fn func(authID string) int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.usage24h = fn
}

// gcLocked expires stale sessions whose lastSeen is older than activeWindow.
// Callers must hold p.mu.
func (p *Pool) gcLocked(now time.Time) {
	cutoff := now.Add(-p.activeWindow)
	for k, s := range p.sessions {
		if s.lastSeen.Before(cutoff) {
			delete(p.sessions, k)
		}
	}
}

// activeCountLocked returns how many distinct active sessions are currently
// pinned to the given OAuth auth ID. Caller must hold p.mu.
func (p *Pool) activeCountLocked(authID string, now time.Time) int {
	cutoff := now.Add(-p.activeWindow)
	n := 0
	for _, s := range p.sessions {
		if s.authID == authID && s.kind == KindOAuth && !s.lastSeen.Before(cutoff) {
			n++
		}
	}
	return n
}

// Acquire picks an Auth for this client token and stamps the session.
// clientGroup scopes credential selection: group-matching credentials are
// preferred, falling back to public ("") credentials when the group's
// credentials are exhausted. clientGroup == "" means public-only.
//
// excludeIDs lets a retrying caller skip credentials it has already tried in
// the current request, so a transient connection error on one credential
// doesn't keep selecting the same one (the sticky-session logic would
// otherwise pin the client to the failing auth until its session times out).
func (p *Pool) Acquire(ctx context.Context, clientToken, clientGroup, clientModel string, excludeIDs ...string) *Auth {
	clientGroup = NormalizeGroup(clientGroup)
	excluded := make(map[string]bool, len(excludeIDs))
	for _, id := range excludeIDs {
		excluded[id] = true
	}

	// Allowed groups in preference order: the client's own group first, then
	// public as a fallback. If the client is already public we only try once.
	var tiers []string
	if clientGroup != "" {
		tiers = []string{clientGroup, ""}
	} else {
		tiers = []string{""}
	}
	allowed := func(authGroup string) bool {
		for _, g := range tiers {
			if authGroup == g {
				return true
			}
		}
		return false
	}

	p.mu.Lock()
	now := time.Now()
	p.gcLocked(now)

	s, ok := p.sessions[clientToken]
	if !ok {
		s = &session{clientToken: clientToken}
		p.sessions[clientToken] = s
	}

	// If session has a sticky OAuth assignment, it's still healthy, has
	// capacity for us, AND isn't on the exclude list, reuse it — but only
	// when the sticky credential still matches an allowed group AND, when
	// the client is group-scoped and currently sticky on public, no
	// group-scoped OAuth is available to upgrade to. Without that upgrade
	// check a group client stays pinned to public for the whole active
	// window even if its own credentials regain capacity.
	if s.authID != "" && s.kind == KindOAuth && !excluded[s.authID] {
		if a := p.findOAuthLocked(s.authID); a != nil && allowed(a.Group) && p.oauthUsableLocked(a, now) {
			upgrade := clientGroup != "" && a.Group == "" && p.pickOAuthLocked(now, excluded, clientGroup) != nil
			if !upgrade {
				// Reusing an assignment we already hold a slot for: counts us
				// only once because activeCountLocked scans distinct sessions.
				s.lastSeen = now
				p.mu.Unlock()
				if err := a.EnsureFresh(ctx, 5*time.Minute, p.useUTLS); err != nil {
					log.Warnf("auth: ensure-fresh sticky %s failed, releasing: %v", a.ID, err)
					excluded[a.ID] = true
					p.mu.Lock()
					s.authID = ""
					// fall through to the pick loop below
				} else {
					return a
				}
			} else {
				s.authID = ""
			}
		} else if s.authID != "" {
			// Previous OAuth is unhealthy/gone/group-disallowed; reassign.
			s.authID = ""
		}
	} else if excluded[s.authID] {
		// Sticky pick was just tried and failed — release it so the next
		// pickOAuthLocked is free to pick anything else.
		s.authID = ""
	}

	// OAuth allocation, then API-key fallback. Each tier iterates: within a
	// tier we try OAuth first (slot-based scheduling), then any API key in
	// that tier. If the tier is empty or saturated, fall through to the
	// next tier (public).
	for _, tier := range tiers {
		for {
			chosen := p.pickOAuthLocked(now, excluded, tier)
			if chosen == nil {
				break
			}
			s.authID = chosen.ID
			s.kind = KindOAuth
			s.lastSeen = now
			p.mu.Unlock()
			if err := chosen.EnsureFresh(ctx, 5*time.Minute, p.useUTLS); err != nil {
				log.Warnf("auth: ensure-fresh %s failed, excluding: %v", chosen.ID, err)
				excluded[chosen.ID] = true
				p.mu.Lock()
				s.authID = ""
				continue
			}
			return chosen
		}
		for _, k := range p.apikeys {
			if k.Group != tier {
				continue
			}
			if excluded[k.ID] {
				continue
			}
			if k.Disabled {
				continue
			}
			if k.IsHardFailed() {
				continue
			}
			if k.IsQuotaExceeded(now) {
				continue
			}
			// Per-key model routing: a key with a non-empty ModelMap only
			// serves models listed in it. Empty map = wildcard.
			if !k.AcceptsModel(clientModel) {
				continue
			}
			s.authID = k.ID
			s.kind = KindAPIKey
			s.lastSeen = now
			p.mu.Unlock()
			return k
		}
	}

	p.mu.Unlock()
	return nil
}

// Release stamps the session as seen right now (call at end of request).
// This extends its active window.
func (p *Pool) Release(clientToken string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if s, ok := p.sessions[clientToken]; ok {
		s.lastSeen = time.Now()
	}
}

// Unstick clears the sticky credential binding for a client session so the
// next Acquire picks a fresh credential. Call this when the current credential
// returned an upstream error — otherwise the client keeps hitting the same
// failing auth until the session expires.
func (p *Pool) Unstick(clientToken string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if s, ok := p.sessions[clientToken]; ok {
		s.authID = ""
	}
}

func (p *Pool) findOAuthLocked(id string) *Auth {
	for _, a := range p.oauths {
		if a.ID == id {
			return a
		}
	}
	return nil
}

func (p *Pool) oauthUsableLocked(a *Auth, now time.Time) bool {
	if a.Disabled {
		return false
	}
	if a.IsHardFailed() {
		return false
	}
	if a.IsQuotaExceeded(now) {
		return false
	}
	return true
}

// pickOAuthLocked returns the OAuth in the requested group with the lowest
// cost-weighted 24h token consumption that still has a free slot and isn't
// on the exclude list, or nil if none available. Unlimited credentials
// (cap=0) always have room. excluded may be nil. group is an exact match;
// "" is the public tier.
//
// Selection is purely least-used-first (not spare-slot-first): as long as a
// credential has any free slot, it's a valid candidate, and ties break on
// weighted 24h usage (see usage.Counts.WeightedTotal). This spreads load
// toward credentials doing less real work — cache-heavy clients don't
// starve a credential out just by racking up near-free cache_read volume.
func (p *Pool) pickOAuthLocked(now time.Time, excluded map[string]bool, group string) *Auth {
	type cand struct {
		a      *Auth
		used24 int64 // weighted tokens consumed in the last ~24h (0 if unknown)
	}
	var cands []cand
	for _, a := range p.oauths {
		if a.Group != group {
			continue
		}
		if excluded[a.ID] {
			continue
		}
		if !p.oauthUsableLocked(a, now) {
			continue
		}
		active := p.activeCountLocked(a.ID, now)
		capN := a.MaxConcurrent
		if capN > 0 && active >= capN {
			continue
		}
		var used int64
		if p.usage24h != nil {
			used = p.usage24h(a.ID)
		}
		cands = append(cands, cand{a: a, used24: used})
	}
	if len(cands) == 0 {
		return nil
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].used24 != cands[j].used24 {
			return cands[i].used24 < cands[j].used24
		}
		return cands[i].a.ID < cands[j].a.ID
	})
	return cands[0].a
}

// Status returns a snapshot of all auths and their current active counts.
// ClientTokens holds the raw client tokens currently holding a slot; callers
// decide whether to mask or resolve them to display names.
type Status struct {
	Auth          AuthInfo
	ActiveClients int
	ClientTokens  []string
}

func (p *Pool) Status() []Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	p.gcLocked(now)
	out := make([]Status, 0, len(p.oauths)+len(p.apikeys))
	for _, a := range p.oauths {
		active := 0
		var tokens []string
		for _, s := range p.sessions {
			if s.authID == a.ID {
				active++
				tokens = append(tokens, s.clientToken)
			}
		}
		out = append(out, Status{Auth: a.Snapshot(), ActiveClients: active, ClientTokens: tokens})
	}
	for _, a := range p.apikeys {
		active := 0
		var tokens []string
		for _, s := range p.sessions {
			if s.authID == a.ID {
				active++
				tokens = append(tokens, s.clientToken)
			}
		}
		out = append(out, Status{Auth: a.Snapshot(), ActiveClients: active, ClientTokens: tokens})
	}
	return out
}

// MaskToken returns a display-safe form of a client token. Exposed so admin /
// status consumers can render without leaking the full secret.
func MaskToken(t string) string {
	if len(t) <= 8 {
		return "***"
	}
	return t[:4] + "..." + t[len(t)-4:]
}

// FindByID returns the Auth (OAuth or APIKey) with the given ID, or nil.
func (p *Pool) FindByID(id string) *Auth {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, a := range p.oauths {
		if a.ID == id {
			return a
		}
	}
	for _, a := range p.apikeys {
		if a.ID == id {
			return a
		}
	}
	return nil
}

// AddOAuth registers a newly uploaded OAuth credential into the live pool.
// Any existing auth with the same ID is replaced.
func (p *Pool) AddOAuth(a *Auth) {
	if a == nil || a.Kind != KindOAuth {
		return
	}
	if a.ProxyURL == "" && p.defaultProxy != "" {
		a.ProxyURL = p.defaultProxy
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, existing := range p.oauths {
		if existing.ID == a.ID {
			p.oauths[i] = a
			return
		}
	}
	p.oauths = append(p.oauths, a)
}

// AddAPIKey registers an API-key credential into the live pool. Replaces
// any existing entry with the same ID.
func (p *Pool) AddAPIKey(a *Auth) {
	if a == nil || a.Kind != KindAPIKey {
		return
	}
	if a.ProxyURL == "" && p.defaultProxy != "" {
		a.ProxyURL = p.defaultProxy
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, existing := range p.apikeys {
		if existing.ID == a.ID {
			p.apikeys[i] = a
			return
		}
	}
	p.apikeys = append(p.apikeys, a)
}

// RemoveOAuth detaches an OAuth credential from the pool and drops any
// sticky sessions assigned to it. Returns the removed auth or nil.
func (p *Pool) RemoveOAuth(id string) *Auth {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, a := range p.oauths {
		if a.ID == id {
			p.oauths = append(p.oauths[:i], p.oauths[i+1:]...)
			for k, s := range p.sessions {
				if s.authID == id {
					delete(p.sessions, k)
				}
			}
			return a
		}
	}
	return nil
}

// RemoveAuth detaches any credential (OAuth or API-key) by ID.
func (p *Pool) RemoveAuth(id string) *Auth {
	p.mu.Lock()
	for i, a := range p.apikeys {
		if a.ID == id {
			p.apikeys = append(p.apikeys[:i], p.apikeys[i+1:]...)
			for k, s := range p.sessions {
				if s.authID == id {
					delete(p.sessions, k)
				}
			}
			p.mu.Unlock()
			return a
		}
	}
	p.mu.Unlock()
	return p.RemoveOAuth(id)
}

// RefreshExpiring proactively refreshes any OAuth credential whose access
// token will expire within `leeway`. Skips disabled and hard-failed creds —
// those need manual intervention. Errors are logged, not returned: this is a
// best-effort background pass.
func (p *Pool) RefreshExpiring(ctx context.Context, leeway time.Duration) {
	p.mu.Lock()
	targets := make([]*Auth, 0, len(p.oauths))
	for _, a := range p.oauths {
		if a.Disabled || a.IsHardFailed() {
			continue
		}
		targets = append(targets, a)
	}
	p.mu.Unlock()
	for _, a := range targets {
		if err := a.EnsureFresh(ctx, leeway, p.useUTLS); err != nil {
			log.Warnf("auth: background refresh %s: %v", a.ID, err)
		}
	}
}

// RunRefresher launches a ticker that periodically calls RefreshExpiring.
// Returns when ctx is cancelled. Intended to run in its own goroutine.
func (p *Pool) RunRefresher(ctx context.Context, interval, leeway time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	if leeway <= 0 {
		leeway = 10 * time.Minute
	}
	// Kick once immediately so a fresh start doesn't wait `interval` before
	// noticing tokens that are already past leeway.
	p.RefreshExpiring(ctx, leeway)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.RefreshExpiring(ctx, leeway)
		}
	}
}

// ReportUpstreamError inspects an upstream HTTP error status and marks the
// credential as temporarily unavailable (so Acquire picks a different auth
// on the next attempt). Only hard quota / auth errors set a cooldown; transient
// gateway errors are recorded without cooldown so the credential remains
// available for immediate retry:
//
//	429  → Retry-After (if given) or 30s   (rate limit — usually transient)
//	403  → Retry-After (if given) or 1m    (could be quota or auth-forbidden)
//	401  → 1m                              (token revoked/invalid)
//	529  → MarkFailure only (no cooldown; Anthropic overloaded, transient)
//	5xx  → MarkFailure only (no cooldown; transient gateway error)
//
// The admin panel's "Clear quota" button lets you drop the flag early.
func (p *Pool) ReportUpstreamError(a *Auth, status int, resetAt time.Time) {
	if a == nil {
		return
	}
	now := time.Now()
	setCooldown := func(d time.Duration) {
		until := resetAt
		if until.IsZero() {
			until = now.Add(d)
		}
		a.MarkQuotaExceeded(until)
		log.Warnf("auth: %s flagged unavailable until %s (status %d)", a.ID, until.Format(time.RFC3339), status)
	}
	switch {
	case status == 429:
		// Most 429s from Anthropic are transient rate limits (RPM/TPM),
		// NOT true quota exhaustion. A 10-minute freeze is far too
		// aggressive — it takes the credential offline long after the
		// rate window has reset. Use a short default; if the upstream
		// sends a meaningful Retry-After we'll honour it instead.
		setCooldown(30 * time.Second)
	case status == 403:
		setCooldown(1 * time.Minute)
	case status == 401:
		// Don't honor Retry-After for auth failures — it's typically a rate
		// limit hint unrelated to the bad credential.
		resetAt = time.Time{}
		setCooldown(1 * time.Minute)
	case status == 529:
		// Anthropic overloaded — transient, no cooldown needed; just mark
		// the failure so the admin panel can see it.
		a.MarkFailure(fmt.Sprintf("upstream %d (overloaded)", status))
	case status >= 500:
		a.MarkFailure(fmt.Sprintf("upstream %d", status))
	}
}
