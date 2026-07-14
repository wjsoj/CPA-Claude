package server

import (
	"strings"
	"testing"
	"time"

	"github.com/wjsoj/cc-core/auth"
)

func codexOAuthCred(id string) *auth.Auth {
	return &auth.Auth{
		ID:            id,
		Kind:          auth.KindOAuth,
		Provider:      auth.ProviderOpenAI,
		Label:         id,
		MaxConcurrent: 10,
	}
}

// Replays the 2026-07-14 outage shape: a transient upstream flap lands enough
// failures on every codex OAuth credential to degrade them, Acquire comes back
// empty, and every client — including ones that did nothing wrong — gets a 503.
//
// The 503 body must say what actually happened and that waiting helps, rather
// than the old opaque "no upstream credentials available".
func TestPoolUnavailableExplainsDegradedCredentials(t *testing.T) {
	a, b := codexOAuthCred("codex-a"), codexOAuthCred("codex-b")
	for _, c := range []*auth.Auth{a, b} {
		c.MarkFailure("http2: client connection lost")
		c.MarkFailure("http2: client connection lost")
		if c.IsHealthy() {
			t.Fatalf("%s should be degraded after 2 consecutive failures", c.ID)
		}
	}
	s := &Server{pool: auth.NewPool([]*auth.Auth{a, b}, nil, 10*time.Minute, false, "")}

	msg, retryAfter := s.poolUnavailable(auth.ProviderOpenAI, false)

	if !strings.Contains(msg, "degraded") {
		t.Fatalf("503 message should name the degraded state, got: %q", msg)
	}
	if !strings.Contains(msg, "2 of 2") {
		t.Fatalf("503 message should quantify how much of the fleet is down, got: %q", msg)
	}
	if retryAfter <= 0 {
		t.Fatalf("degraded credentials are re-probed automatically, so the client must be told to retry (Retry-After=%d)", retryAfter)
	}
}

// A hard-failed fleet is NOT transient — telling the client to retry would be a
// lie, so no Retry-After is offered and the message says an operator is needed.
func TestPoolUnavailableHardFailedOffersNoRetry(t *testing.T) {
	a := codexOAuthCred("codex-a")
	a.MarkHardFailure("upstream 401")
	s := &Server{pool: auth.NewPool([]*auth.Auth{a}, nil, 10*time.Minute, false, "")}

	msg, retryAfter := s.poolUnavailable(auth.ProviderOpenAI, false)

	if retryAfter != 0 {
		t.Fatalf("hard-failed credentials do not recover on their own; Retry-After should be 0, got %d", retryAfter)
	}
	if !strings.Contains(msg, "operator") {
		t.Fatalf("503 message should say an operator must clear it, got: %q", msg)
	}
}

// With no credentials of that provider configured at all, say so plainly — this
// is a deployment problem, not a transient one.
func TestPoolUnavailableNoCredentialsConfigured(t *testing.T) {
	s := &Server{pool: auth.NewPool(nil, nil, 10*time.Minute, false, "")}

	msg, retryAfter := s.poolUnavailable(auth.ProviderOpenAI, false)

	if retryAfter != 0 {
		t.Fatalf("Retry-After should be 0 when nothing is configured, got %d", retryAfter)
	}
	if !strings.Contains(msg, "no openai credentials are configured") {
		t.Fatalf("unexpected message: %q", msg)
	}
}
