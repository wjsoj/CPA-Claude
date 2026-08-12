package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/wjsoj/cc-core/auth"
	"github.com/wjsoj/cc-core/clienttoken"
	"github.com/wjsoj/cc-core/pricing"
	"github.com/wjsoj/cc-core/usage"

	"github.com/wjsoj/CPA-Claude/internal/saas/billing"
	saasdb "github.com/wjsoj/CPA-Claude/internal/saas/db"

	"github.com/wjsoj/CPA-Claude/internal/config"
)

// A Codex weekly limit resets ~5 days out. Reporting that verbatim as
// Retry-After tells the client to stop trying until next week over a pool that
// is normally back in minutes — a quota reset is the last of the ways it
// recovers, not the first (a degraded credential re-probes itself, a saturated
// one frees a slot, an operator adds an account).
func TestPoolUnavailableCapsRetryAfterOnLongQuotaReset(t *testing.T) {
	cred := codexOAuthCred("codex-weekly")
	cred.MarkUsageLimitReached(time.Now().Add(137 * time.Hour))
	s := &Server{pool: auth.NewPool([]*auth.Auth{cred}, nil, 10*time.Minute, false, "")}

	msg, retryAfter := s.poolUnavailable(auth.ProviderOpenAI, false)

	if retryAfter > poolRetryAfterCapSeconds {
		t.Errorf("Retry-After = %ds, want <= %ds — a 5-day hint parks the client for the week",
			retryAfter, poolRetryAfterCapSeconds)
	}
	if retryAfter <= 0 {
		t.Errorf("Retry-After = %d, want a positive hint: waiting genuinely does help here", retryAfter)
	}
	// The honest deadline still belongs in the message a human reads.
	if !strings.Contains(msg, "resets in") {
		t.Errorf("message should still carry the real reset time, got %q", msg)
	}
}

// The single most actionable 503 we serve: the pool is empty only because this
// token opted out of the upstream fallback, while a healthy paid channel sits
// there able to serve the request. 375 of the last 24h's 503s were this, and
// none of them said so — the user reads "all 3 credentials are out of quota"
// and waits for an operator who has nothing to fix.
func TestForwardTellsTheUserTheirOwnFallbackSwitchIsOff(t *testing.T) {
	gin.SetMode(gin.TestMode)

	oauth := codexOAuthCred("codex-a")
	oauth.MarkUsageLimitReached(time.Now().Add(2 * time.Hour))
	// Marked up above the group rate — the only case in which an opted-out
	// token is left with nothing (a parity channel would just serve it).
	relay := &auth.Auth{
		ID: "apikey-self.json", Kind: auth.KindAPIKey, Provider: auth.ProviderOpenAI,
		Label: "self", AccessToken: "sk-relay", BaseURL: "https://peer.example.com",
		PriceMultiplier: 0.5,
	}
	tokens := clienttoken.OpenInMemory()
	if err := tokens.Add(clienttoken.Token{Token: "sk-token", Name: "client"}); err != nil {
		t.Fatal(err)
	}
	if err := tokens.SetUpstreamFallback("sk-token", false); err != nil {
		t.Fatal(err)
	}
	s := &Server{
		cfg:     &config.Config{},
		pool:    auth.NewPool([]*auth.Auth{oauth}, []*auth.Auth{relay}, 10*time.Minute, false, ""),
		usage:   usage.OpenInMemory(),
		pricing: pricing.NewCatalog(pricing.Config{}),
		tokens:  tokens,
		// Non-nil billing is what puts the per-token opt-out in play at all
		// (operator mode always falls back); saas answers what the user's own
		// rate is, which is what the opt-out is compared against.
		billing: &billing.Handler{},
		saas:    newTestBilling(t),
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	// allowFallback=false is what the wallet switch produces; drive it directly
	// so the test doesn't depend on the SaaS DB.
	s.forwardWithFailover(c, auth.ProviderOpenAI, "/v1/responses", "gpt-5.6-sol",
		"sk-token", "", "client", "", []byte(`{"model":"gpt-5.6-sol"}`), false, time.Now())

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "fallback") {
		t.Errorf("the 503 must name the switch the user can flip; got %q", body)
	}
}

func TestForwardFallbackHintBodyShape(t *testing.T) {
	// Sanity: the hint names both the state and the remedy, and the capped
	// Retry-After rides along on the same response.
	gin.SetMode(gin.TestMode)
	oauth := codexOAuthCred("codex-a")
	oauth.MarkUsageLimitReached(time.Now().Add(137 * time.Hour))
	relay := &auth.Auth{
		ID: "apikey-self.json", Kind: auth.KindAPIKey, Provider: auth.ProviderOpenAI,
		AccessToken: "sk-relay", BaseURL: "https://peer.example.com",
		PriceMultiplier: 0.5,
	}
	tokens := clienttoken.OpenInMemory()
	if err := tokens.Add(clienttoken.Token{Token: "sk-token"}); err != nil {
		t.Fatal(err)
	}
	if err := tokens.SetUpstreamFallback("sk-token", false); err != nil {
		t.Fatal(err)
	}
	s := &Server{
		cfg: &config.Config{}, pool: auth.NewPool([]*auth.Auth{oauth}, []*auth.Auth{relay}, 10*time.Minute, false, ""),
		usage: usage.OpenInMemory(), pricing: pricing.NewCatalog(pricing.Config{}),
		tokens: tokens, billing: &billing.Handler{}, saas: newTestBilling(t),
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	s.forwardWithFailover(c, auth.ProviderOpenAI, "/v1/responses", "gpt-5.6-sol",
		"sk-token", "", "client", "", []byte(`{"model":"gpt-5.6-sol"}`), false, time.Now())

	body := w.Body.String()
	for _, want := range []string{"charge above your current rate", "opted out", "wallet settings"} {
		if !strings.Contains(body, want) {
			t.Errorf("503 body missing %q: %s", want, body)
		}
	}
	if ra := w.Header().Get("Retry-After"); ra != "300" {
		t.Errorf("Retry-After = %q, want the 300s cap rather than the 5-day reset", ra)
	}
	t.Logf("client sees: %s (Retry-After: %s)", body, w.Header().Get("Retry-After"))
}

// newTestBilling opens a throwaway SaaS DB and pre-creates the wallet the two
// tests above bill against, so GroupRate can answer with the default group rate
// instead of failing closed.
func newTestBilling(t *testing.T) *saasBilling {
	t.Helper()
	db, err := saasdb.Open(filepath.Join(t.TempDir(), "ux.db"))
	if err != nil {
		t.Fatalf("open saas db: %v", err)
	}
	if _, err := db.EnsureWallet(context.Background(), "sk-token"); err != nil {
		t.Fatalf("ensure wallet: %v", err)
	}
	return &saasBilling{db: db}
}
