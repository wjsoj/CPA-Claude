package billing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/wjsoj/CPA-Claude/internal/saas/db"
)

// /api/wallet/balance carries the caller's workspace seat because a member's
// spendable amount is not their wallet balance — the pool pays first, bounded
// by their own share. The status SPA also decides from `role` whether to offer
// the group console, so the shape of this block is load-bearing in two places.
func TestBalanceCarriesWorkspaceSeat(t *testing.T) {
	gin.SetMode(gin.TestMode)
	d, err := db.Open(filepath.Join(t.TempDir(), "bal.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = d.Close() }()
	ctx := context.Background()

	ws, err := d.CreateWorkspace(ctx, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.AdjustWorkspaceBalance(ctx, ws.ID, 50, "topup", "seed", ""); err != nil {
		t.Fatal(err)
	}
	for _, tok := range []string{tokAdmin, tokMember, tokNew} {
		if _, err := d.EnsureWallet(ctx, tok); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.AddMember(ctx, ws.ID, tokAdmin, db.WSRoleAdmin, 0, 0); err != nil {
		t.Fatal(err)
	}
	// A daily cap of $4 with $3 already drawn leaves $1 of the $50 pool
	// reachable — the whole point of reporting pool_avail_usd separately.
	if err := d.AddMember(ctx, ws.ID, tokMember, db.WSRoleMember, 4, 100); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.ChargeMemberFirst(ctx, tokMember, 3, "seed", ""); err != nil {
		t.Fatal(err)
	}

	h := NewHandler(d, NewRate("", 7.2), &MockGateway{}, "test",
		func(c *gin.Context) string { return c.GetHeader("X-Tok") })
	e := gin.New()
	h.UserRoutes(e.Group("/api/wallet"))

	get := func(tok string) map[string]any {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/api/wallet/balance", nil)
		r.Header.Set("X-Tok", tok)
		w := httptest.NewRecorder()
		e.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	member, _ := get(tokMember)["workspace"].(map[string]any)
	if member == nil {
		t.Fatal("member: no workspace block")
	}
	if member["role"] != db.WSRoleMember {
		t.Errorf("role = %v, want member", member["role"])
	}
	if got := member["pool_avail_usd"].(float64); got < 0.99 || got > 1.01 {
		t.Errorf("pool_avail_usd = %v, want ~1 (cap 4 − used 3)", got)
	}
	if got := member["used_day_usd"].(float64); got < 2.99 || got > 3.01 {
		t.Errorf("used_day_usd = %v, want ~3", got)
	}
	if member["name"] != "acme" {
		t.Errorf("name = %v", member["name"])
	}

	if role := get(tokAdmin)["workspace"].(map[string]any)["role"]; role != db.WSRoleAdmin {
		t.Errorf("admin role = %v, want admin", role)
	}

	// A token in no workspace must not grow the block at all — the SPA reads
	// its absence as "no seat, no console".
	if _, ok := get(tokNew)["workspace"]; ok {
		t.Error("non-member got a workspace block")
	}
}
