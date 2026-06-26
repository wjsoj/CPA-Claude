package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/wjsoj/CPA-Claude/internal/saas/billing"
	saasdb "github.com/wjsoj/CPA-Claude/internal/saas/db"
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

// TestAllowAPIKeyFallback verifies the per-token gate: non-SaaS always allows
// (legacy operator behaviour), SaaS defaults off and honours the opt-in.
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
	if !off.allowAPIKeyFallback("t1") {
		t.Fatal("non-SaaS mode must keep legacy always-fall-back")
	}

	// SaaS enabled → default ON, explicit opt-out respected.
	on := &Server{tokens: store, billing: &billing.Handler{}}
	if !on.allowAPIKeyFallback("t1") {
		t.Fatal("unset token must default to fall-back ON")
	}
	if on.allowAPIKeyFallback("t2") {
		t.Fatal("opted-out token must NOT fall back")
	}
	if !on.allowAPIKeyFallback("t3") {
		t.Fatal("opted-in token must fall back")
	}
	if !on.allowAPIKeyFallback("unknown") {
		t.Fatal("unknown token defaults to fall-back ON")
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
