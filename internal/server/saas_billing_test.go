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
	"github.com/wjsoj/CPA-Claude/internal/config"
	"github.com/wjsoj/CPA-Claude/internal/saas/billing"
	saasdb "github.com/wjsoj/CPA-Claude/internal/saas/db"
	"github.com/wjsoj/cc-core/auth"
	"github.com/wjsoj/cc-core/clienttoken"
)

// TestSettleChargeOverride verifies the per-key price override replaces the
// pricing-group multiplier when > 0, and that 0 falls back to the group rate.
func TestSettleChargeOverride(t *testing.T) {
	db, err := saasdb.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	b := &saasBilling{db: db}
	ctx := context.Background()
	if _, err := db.EnsureWallet(ctx, "tok"); err != nil {
		t.Fatalf("ensure wallet: %v", err)
	}

	const official = 10.0

	// No override → the wallet's default pricing group (claude = 0.05).
	mult, billed := b.SettleCharge(ctx, "tok", "anthropic", "claude-sonnet-4-6", official, 0, "req:1")
	if mult != saasdb.DefaultClaudeMultiplier {
		t.Fatalf("group multiplier: got %v want %v", mult, saasdb.DefaultClaudeMultiplier)
	}
	if want := official * saasdb.DefaultClaudeMultiplier; billed != want {
		t.Fatalf("group billed: got %v want %v", billed, want)
	}

	// Override > 0 → bypass the group entirely: official × override.
	mult2, billed2 := b.SettleCharge(ctx, "tok", "anthropic", "claude-sonnet-4-6", official, 1.2, "req:2")
	if mult2 != 1.2 {
		t.Fatalf("override multiplier: got %v want 1.2", mult2)
	}
	if want := official * 1.2; billed2 != want {
		t.Fatalf("override billed: got %v want %v", billed2, want)
	}
}

// TestAllowAPIKeyFallback verifies the per-token gate. Non-SaaS always allows
// (legacy operator behaviour); under SaaS the switch defaults ON, and when a
// user turns it OFF it only withholds channels that would charge them MORE than
// their own group rate — a parity-priced channel still serves them, because
// refusing it costs them a request and saves them nothing.
func TestAllowAPIKeyFallback(t *testing.T) {
	optOut := false
	optIn := true
	store := clienttoken.OpenInMemory()
	if err := store.Add(clienttoken.Token{Token: "t1"}); err != nil { // unset → default ON
		t.Fatal(err)
	}
	if err := store.Add(clienttoken.Token{Token: "t2", UpstreamFallback: &optOut}); err != nil {
		t.Fatal(err)
	}
	if err := store.Add(clienttoken.Token{Token: "t3", UpstreamFallback: &optIn}); err != nil {
		t.Fatal(err)
	}

	// SaaS disabled (billing == nil) → always allowed.
	off := &Server{tokens: store}
	if !off.allowAPIKeyFallback(context.Background(), auth.ProviderAnthropic, "t1") {
		t.Fatal("non-SaaS mode must keep legacy always-fall-back")
	}

	db, err := saasdb.Open(filepath.Join(t.TempDir(), "fallback.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	ctx := context.Background()
	for _, tok := range []string{"t1", "t2", "t3"} {
		if _, err := db.EnsureWallet(ctx, tok); err != nil {
			t.Fatalf("ensure wallet %s: %v", tok, err)
		}
	}
	// A marked-up relay (0.12) next to a parity one (no override), mirroring
	// production: anthropic has both, and the group rate is 0.05.
	marked := &auth.Auth{ID: "apikey-marked", Kind: auth.KindAPIKey, Provider: auth.ProviderAnthropic, PriceMultiplier: 0.12}
	parity := &auth.Auth{ID: "apikey-parity", Kind: auth.KindAPIKey, Provider: auth.ProviderOpenAI}

	on := &Server{
		tokens:  store,
		billing: &billing.Handler{},
		saas:    &saasBilling{db: db},
		pool:    auth.NewPool(nil, []*auth.Auth{marked, parity}, 10*time.Minute, false, ""),
	}

	if !on.allowAPIKeyFallback(ctx, auth.ProviderAnthropic, "t1") {
		t.Error("unset token must default to fall-back ON")
	}
	if !on.allowAPIKeyFallback(ctx, auth.ProviderAnthropic, "t3") {
		t.Error("opted-in token must fall back — including to the marked-up channel")
	}
	if !on.allowAPIKeyFallback(ctx, auth.ProviderAnthropic, "unknown") {
		t.Error("unknown token defaults to fall-back ON")
	}

	// The opt-out, per provider.
	if on.allowAPIKeyFallback(ctx, auth.ProviderAnthropic, "t2") {
		t.Error("opted-out token must NOT be served by a channel billed above its group rate (0.12 > 0.05)")
	}
	if !on.allowAPIKeyFallback(ctx, auth.ProviderOpenAI, "t2") {
		t.Error("opted-out token MUST still fall back to a parity-priced channel: " +
			"withholding it costs the user a 503 and saves them nothing — this is the 2026-08-12 regression")
	}
}

// A promotion outranks per-key overrides, so while one runs every channel bills
// the same and even a marked-up one cannot be the expensive choice.
func TestAllowAPIKeyFallbackDuringPromotion(t *testing.T) {
	optOut := false
	store := clienttoken.OpenInMemory()
	if err := store.Add(clienttoken.Token{Token: "t2", UpstreamFallback: &optOut}); err != nil {
		t.Fatal(err)
	}
	db, err := saasdb.Open(filepath.Join(t.TempDir(), "promo.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	ctx := context.Background()
	if _, err := db.EnsureWallet(ctx, "t2"); err != nil {
		t.Fatal(err)
	}
	marked := &auth.Auth{ID: "apikey-marked", Kind: auth.KindAPIKey, Provider: auth.ProviderAnthropic, PriceMultiplier: 0.12}
	s := &Server{
		tokens:  store,
		billing: &billing.Handler{},
		saas: &saasBilling{db: db, promos: []config.PricingPromotion{{
			Provider:   "anthropic",
			Multiplier: 0.001,
			Start:      time.Now().Add(-time.Hour),
			End:        time.Now().Add(time.Hour),
		}}},
		pool: auth.NewPool(nil, []*auth.Auth{marked}, 10*time.Minute, false, ""),
	}
	if !s.allowAPIKeyFallback(ctx, auth.ProviderAnthropic, "t2") {
		t.Error("during a promotion every channel bills the same, so the opt-out has nothing to protect against")
	}
}

// TestWalletSettingsEndpoints exercises the self-service GET/PATCH wiring.
func TestWalletSettingsEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := clienttoken.OpenInMemory()
	const tok = "tok-secret-1234567890"
	if err := store.Add(clienttoken.Token{Token: tok}); err != nil {
		t.Fatal(err)
	}
	s := &Server{tokens: store}
	r := gin.New()
	g := r.Group("/api/wallet")
	g.GET("/settings", s.handleWalletSettingsGet)
	g.PATCH("/settings", s.handleWalletSettingsPatch)

	do := func(method, body, bearer string) *httptest.ResponseRecorder {
		var rdr *strings.Reader
		if body != "" {
			rdr = strings.NewReader(body)
		} else {
			rdr = strings.NewReader("")
		}
		req := httptest.NewRequest(method, "/api/wallet/settings", rdr)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	// GET default → 200 + true (default ON).
	if w := do("GET", "", tok); w.Code != 200 || !strings.Contains(w.Body.String(), `"upstream_fallback":true`) {
		t.Fatalf("GET default: code=%d body=%s", w.Code, w.Body.String())
	}
	// PATCH false (opt out) → 200, persisted as explicit false.
	if w := do("PATCH", `{"upstream_fallback":false}`, tok); w.Code != 200 {
		t.Fatalf("PATCH false: code=%d body=%s", w.Code, w.Body.String())
	}
	if v, _ := store.Lookup(tok); v.UpstreamFallbackEnabled() {
		t.Fatal("PATCH did not persist the opt-out")
	}
	// GET now reflects false.
	if w := do("GET", "", tok); !strings.Contains(w.Body.String(), `"upstream_fallback":false`) {
		t.Fatalf("GET after opt-out: body=%s", w.Body.String())
	}
	// Missing/unknown bearer → 401.
	if w := do("GET", "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("no bearer: want 401 got %d", w.Code)
	}
	if w := do("PATCH", `{"upstream_fallback":true}`, "wrong-token"); w.Code != http.StatusUnauthorized {
		t.Fatalf("unknown bearer: want 401 got %d", w.Code)
	}
}
