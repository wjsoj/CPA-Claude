package auth

import (
	"fmt"
	"sync"
	"time"
)

// Kind distinguishes OAuth credentials (concurrency-limited) from API keys
// (unlimited; used as fallback).
type Kind int

const (
	KindOAuth Kind = iota
	KindAPIKey
)

// Auth is a single upstream credential.
// For OAuth: AccessToken/RefreshToken/ExpiresAt are managed by the refresher.
// For APIKey: only AccessToken (the literal key) is used.
type Auth struct {
	mu sync.RWMutex

	ID    string // stable identifier (OAuth: file basename; APIKey: "apikey:<label-or-prefix>")
	Kind  Kind
	Label string
	Email string

	// Credentials
	AccessToken  string
	RefreshToken string // OAuth only
	ExpiresAt    time.Time

	// Routing
	ProxyURL      string // per-credential upstream proxy (empty = direct/use default)
	BaseURL       string // per-credential upstream base URL override (API-key only; empty = config.AnthropicBaseURL)
	MaxConcurrent int    // OAuth: max client sessions; 0 = unlimited. APIKey: ignored.

	// Source file for OAuth (empty for APIKey)
	FilePath string

	// Health
	Disabled            bool
	QuotaExceededAt     time.Time // zero = not flagged
	QuotaResetAt        time.Time // when to try again (may be zero = manual reset)
	LastFailure         time.Time
	LastFailureReason   string
	LastSuccess         time.Time // set on every <400 upstream response
	ConsecutiveFailures int       // reset on success; drives auto hard-fail
	HardFailureAt       time.Time // sticky unhealthy; cleared only by ClearFailure
	HardFailureReason   string
}

// healthGrace is how long after an isolated failure we still treat the
// credential as healthy (optimistic recovery). Hard failures and repeated
// failures bypass this.
const healthGrace = 2 * time.Minute

// hardFailureThreshold is the number of consecutive non-cooldown failures
// after which a credential is marked hard-unhealthy and must be manually
// reset from the admin panel.
const hardFailureThreshold = 5

func (a *Auth) Snapshot() AuthInfo {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return AuthInfo{
		ID:              a.ID,
		Kind:            a.Kind,
		Label:           a.Label,
		Email:           a.Email,
		ExpiresAt:       a.ExpiresAt,
		ProxyURL:        a.ProxyURL,
		MaxConcurrent:   a.MaxConcurrent,
		Disabled:        a.Disabled,
		QuotaExceededAt: a.QuotaExceededAt,
		QuotaResetAt:    a.QuotaResetAt,
		FilePath:        a.FilePath,
		BaseURL:         a.BaseURL,
	}
}

type AuthInfo struct {
	ID              string
	Kind            Kind
	Label           string
	Email           string
	ExpiresAt       time.Time
	ProxyURL        string
	MaxConcurrent   int
	Disabled        bool
	QuotaExceededAt time.Time
	QuotaResetAt    time.Time
	FilePath        string
	BaseURL         string
}

// IsQuotaExceeded reports true if Anthropic has signalled this auth is out of
// quota and we should skip it until QuotaResetAt (or until manually cleared).
func (a *Auth) IsQuotaExceeded(now time.Time) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.QuotaExceededAt.IsZero() {
		return false
	}
	if a.QuotaResetAt.IsZero() {
		// No known reset; keep skipping for 1 hour.
		return now.Before(a.QuotaExceededAt.Add(time.Hour))
	}
	return now.Before(a.QuotaResetAt)
}

func (a *Auth) MarkQuotaExceeded(resetAt time.Time) {
	a.mu.Lock()
	a.QuotaExceededAt = time.Now()
	a.QuotaResetAt = resetAt
	a.mu.Unlock()
}

func (a *Auth) MarkFailure(reason string) {
	a.mu.Lock()
	a.LastFailure = time.Now()
	a.LastFailureReason = reason
	a.ConsecutiveFailures++
	if a.ConsecutiveFailures >= hardFailureThreshold && a.HardFailureAt.IsZero() {
		a.HardFailureAt = a.LastFailure
		a.HardFailureReason = fmt.Sprintf("%d consecutive failures: %s", a.ConsecutiveFailures, reason)
	}
	a.mu.Unlock()
}

// MarkHardFailure flags the credential as sticky-unhealthy. The admin panel
// must manually clear it before traffic resumes. Used for obvious terminal
// signals (e.g. account disabled, upstream dead).
func (a *Auth) MarkHardFailure(reason string) {
	a.mu.Lock()
	a.HardFailureAt = time.Now()
	a.HardFailureReason = reason
	a.LastFailure = a.HardFailureAt
	a.LastFailureReason = reason
	a.mu.Unlock()
}

// MarkSuccess records that the most recent upstream request through this
// credential succeeded. Used by the admin panel to compute "healthy" status.
func (a *Auth) MarkSuccess() {
	a.mu.Lock()
	a.LastSuccess = time.Now()
	a.ConsecutiveFailures = 0
	a.mu.Unlock()
}

// ClearFailure wipes transient and hard failure state, returning the
// credential to "healthy". Invoked from the admin panel.
func (a *Auth) ClearFailure() {
	a.mu.Lock()
	a.LastFailure = time.Time{}
	a.LastFailureReason = ""
	a.ConsecutiveFailures = 0
	a.HardFailureAt = time.Time{}
	a.HardFailureReason = ""
	a.LastSuccess = time.Now()
	a.mu.Unlock()
}

// IsHealthy returns true if the credential is enabled, not in cooldown, and
// the most recent observed upstream attempt either succeeded or there has
// been no failure recorded at all. A credential that has never been used is
// considered healthy.
func (a *Auth) IsHealthy() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.Disabled {
		return false
	}
	if !a.HardFailureAt.IsZero() {
		return false
	}
	if !a.QuotaExceededAt.IsZero() {
		// Still in cooldown? Treat as unhealthy.
		if a.QuotaResetAt.IsZero() || time.Now().Before(a.QuotaResetAt) {
			return false
		}
	}
	if a.LastFailure.IsZero() {
		return true
	}
	if a.LastSuccess.After(a.LastFailure) {
		return true
	}
	// Optimistic recovery: a single stale failure no longer counts. Repeated
	// failures within the grace window keep the credential red.
	if a.ConsecutiveFailures < 2 && time.Since(a.LastFailure) > healthGrace {
		return true
	}
	return false
}

// HealthSnapshot returns a copy of the fields the admin panel needs to
// render health state, taken under the read lock.
func (a *Auth) HealthSnapshot() (healthy, hardFailure bool, reason string, consecutive int) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	hardFailure = !a.HardFailureAt.IsZero()
	consecutive = a.ConsecutiveFailures
	switch {
	case hardFailure:
		reason = a.HardFailureReason
	case !a.LastFailure.IsZero() && !a.LastSuccess.After(a.LastFailure):
		reason = a.LastFailureReason
	}
	// Recompute healthy with the same logic as IsHealthy but without
	// re-acquiring the lock.
	switch {
	case a.Disabled:
		healthy = false
	case hardFailure:
		healthy = false
	case !a.QuotaExceededAt.IsZero() && (a.QuotaResetAt.IsZero() || time.Now().Before(a.QuotaResetAt)):
		healthy = false
	case a.LastFailure.IsZero(), a.LastSuccess.After(a.LastFailure):
		healthy = true
	case a.ConsecutiveFailures < 2 && time.Since(a.LastFailure) > healthGrace:
		healthy = true
	default:
		healthy = false
	}
	return
}

// Credentials returns a snapshot of the fields needed to authenticate an
// upstream request. Safe for concurrent callers.
func (a *Auth) Credentials() (accessToken string, kind Kind) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.AccessToken, a.Kind
}

// IsHardFailed reports whether the credential has been flagged sticky-
// unhealthy and must be manually cleared before traffic resumes.
func (a *Auth) IsHardFailed() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return !a.HardFailureAt.IsZero()
}

func (a *Auth) ClearQuota() {
	a.mu.Lock()
	a.QuotaExceededAt = time.Time{}
	a.QuotaResetAt = time.Time{}
	a.mu.Unlock()
}

// SetDisabled toggles the disabled flag.
func (a *Auth) SetDisabled(v bool) {
	a.mu.Lock()
	a.Disabled = v
	a.mu.Unlock()
}

// SetMaxConcurrent updates the slot cap for this credential.
func (a *Auth) SetMaxConcurrent(n int) {
	if n < 0 {
		n = 0
	}
	a.mu.Lock()
	a.MaxConcurrent = n
	a.mu.Unlock()
}

// SetProxyURL updates the per-credential upstream proxy. Empty string clears it.
func (a *Auth) SetProxyURL(u string) {
	a.mu.Lock()
	a.ProxyURL = u
	a.mu.Unlock()
}

// SetBaseURL updates the per-credential upstream base URL (API-key only).
// Empty string reverts to the server-wide default.
func (a *Auth) SetBaseURL(u string) {
	a.mu.Lock()
	a.BaseURL = u
	a.mu.Unlock()
}
