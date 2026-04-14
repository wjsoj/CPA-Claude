package auth

import (
	"context"
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
	mu             sync.Mutex
	oauths         []*Auth
	apikeys        []*Auth
	sessions       map[string]*session // client token -> session
	activeWindow   time.Duration
	useUTLS        bool
	defaultProxy   string
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

func (p *Pool) UseUTLS() bool         { return p.useUTLS }
func (p *Pool) ActiveWindow() time.Duration { return p.activeWindow }

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
// Returns the chosen Auth. If no OAuth has capacity, falls back to API key
// (if configured). Returns nil if no credential is available.
func (p *Pool) Acquire(ctx context.Context, clientToken string) *Auth {
	p.mu.Lock()
	now := time.Now()
	p.gcLocked(now)

	s, ok := p.sessions[clientToken]
	if !ok {
		s = &session{clientToken: clientToken}
		p.sessions[clientToken] = s
	}

	// If session has a sticky OAuth assignment and it's still healthy and has
	// capacity for us, reuse it.
	if s.authID != "" && s.kind == KindOAuth {
		if a := p.findOAuthLocked(s.authID); a != nil {
			if p.oauthUsableLocked(a, now) {
				// Reusing an assignment we already hold a slot for: counts us
				// only once because activeCountLocked scans distinct sessions.
				s.lastSeen = now
				p.mu.Unlock()
				_ = a.EnsureFresh(ctx, 5*time.Minute, p.useUTLS)
				return a
			}
		}
		// Previous OAuth is unhealthy/gone; reassign.
		s.authID = ""
	}

	// Try OAuth allocation: pick the OAuth with the fewest active sessions
	// that still has spare capacity. Tie-break by ID for determinism.
	if chosen := p.pickOAuthLocked(now); chosen != nil {
		s.authID = chosen.ID
		s.kind = KindOAuth
		s.lastSeen = now
		p.mu.Unlock()
		_ = chosen.EnsureFresh(ctx, 5*time.Minute, p.useUTLS)
		return chosen
	}

	// Fallback: API key, round-robin-ish (pick first usable; apikeys have no
	// per-credential concurrency, so order doesn't matter for correctness).
	for _, k := range p.apikeys {
		if k.Disabled {
			continue
		}
		if k.IsQuotaExceeded(now) {
			continue
		}
		s.authID = k.ID
		s.kind = KindAPIKey
		s.lastSeen = now
		p.mu.Unlock()
		return k
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
	if a.IsQuotaExceeded(now) {
		return false
	}
	return true
}

// pickOAuthLocked returns the OAuth with lowest active-session count that
// still has capacity, or nil if none available.
func (p *Pool) pickOAuthLocked(now time.Time) *Auth {
	type cand struct {
		a      *Auth
		active int
		cap    int
	}
	var cands []cand
	for _, a := range p.oauths {
		if !p.oauthUsableLocked(a, now) {
			continue
		}
		active := p.activeCountLocked(a.ID, now)
		capN := a.MaxConcurrent
		if capN > 0 && active >= capN {
			continue
		}
		cands = append(cands, cand{a: a, active: active, cap: capN})
	}
	if len(cands) == 0 {
		return nil
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].active != cands[j].active {
			return cands[i].active < cands[j].active
		}
		return cands[i].a.ID < cands[j].a.ID
	})
	return cands[0].a
}

// Status returns a snapshot of all auths and their current active counts.
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
				tokens = append(tokens, maskToken(s.clientToken))
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
				tokens = append(tokens, maskToken(s.clientToken))
			}
		}
		out = append(out, Status{Auth: a.Snapshot(), ActiveClients: active, ClientTokens: tokens})
	}
	return out
}

func maskToken(t string) string {
	if len(t) <= 8 {
		return "***"
	}
	return t[:4] + "..." + t[len(t)-4:]
}

// ReportUpstreamError inspects an upstream HTTP error status and marks the
// auth as quota-exceeded (so subsequent requests fall through) if appropriate.
func (p *Pool) ReportUpstreamError(a *Auth, status int, resetAt time.Time) {
	if a == nil {
		return
	}
	if status == 429 || (status == 403 && resetAt.IsZero()) {
		if resetAt.IsZero() {
			resetAt = time.Now().Add(time.Hour)
		}
		a.MarkQuotaExceeded(resetAt)
		log.Warnf("auth: %s flagged quota-exceeded until %s (status %d)", a.ID, resetAt.Format(time.RFC3339), status)
	} else if status >= 500 {
		a.MarkFailure("upstream 5xx")
	}
}
