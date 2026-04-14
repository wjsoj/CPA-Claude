package auth

import (
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
	Disabled          bool
	QuotaExceededAt   time.Time // zero = not flagged
	QuotaResetAt      time.Time // when to try again (may be zero = manual reset)
	LastFailure       time.Time
	LastFailureReason string
}

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
	a.mu.Unlock()
}

// Credentials returns a snapshot of the fields needed to authenticate an
// upstream request. Safe for concurrent callers.
func (a *Auth) Credentials() (accessToken string, kind Kind) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.AccessToken, a.Kind
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
