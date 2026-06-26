package billing

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/wjsoj/CPA-Claude/internal/saas/db"
)

// Realistic-length tokens: maskToken collapses anything ≤10 chars to "***",
// so short fixtures would all collide on the masked URL identifier.
const (
	tokAdmin  = "sk-admin-0000000000000000000000000000aaaa"
	tokMember = "sk-membr-0000000000000000000000000000bbbb"
	tokNew    = "sk-newxx-0000000000000000000000000000cccc"
)

func newTeamTest(t *testing.T) (*gin.Engine, *db.DB) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	d, err := db.Open(filepath.Join(t.TempDir(), "team.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	ctx := context.Background()
	ws, err := d.CreateWorkspace(ctx, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if err := d.AddMember(ctx, ws.ID, tokAdmin, db.WSRoleAdmin, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := d.AddMember(ctx, ws.ID, tokMember, db.WSRoleMember, 0, 0); err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{tokAdmin: true, tokMember: true, tokNew: true}
	th := &TeamHandler{
		DB:          d,
		Auth:        func(c *gin.Context) string { return c.GetHeader("X-Tok") },
		TokenExists: func(tok string) bool { return known[tok] },
	}
	e := gin.New()
	th.Routes(e.Group("/api/team"))
	return e, d
}

func do(e *gin.Engine, method, path, tok string, body any) *httptest.ResponseRecorder {
	var r *http.Request
	if body != nil {
		b, _ := json.Marshal(body)
		r = httptest.NewRequest(method, path, bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	if tok != "" {
		r.Header.Set("X-Tok", tok)
	}
	w := httptest.NewRecorder()
	e.ServeHTTP(w, r)
	return w
}

func TestTeamAuth(t *testing.T) {
	e, _ := newTeamTest(t)

	if w := do(e, "GET", "/api/team/me", "", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("no token → %d, want 401", w.Code)
	}
	if w := do(e, "GET", "/api/team/me", tokMember, nil); w.Code != http.StatusForbidden {
		t.Fatalf("member token → %d, want 403", w.Code)
	}
	if w := do(e, "GET", "/api/team/me", "sk-unknown", nil); w.Code != http.StatusForbidden {
		t.Fatalf("unknown token → %d, want 403", w.Code)
	}
	w := do(e, "GET", "/api/team/me", tokAdmin, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("admin token → %d, want 200", w.Code)
	}
	var me struct {
		Workspace struct {
			Name string `json:"name"`
		} `json:"workspace"`
		Role string `json:"role"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &me)
	if me.Workspace.Name != "acme" || me.Role != db.WSRoleAdmin {
		t.Fatalf("me = %+v", me)
	}
}

func TestTeamMemberLifecycle(t *testing.T) {
	e, d := newTeamTest(t)

	// Add a new member.
	if w := do(e, "POST", "/api/team/members", tokAdmin, gin.H{"token": tokNew, "daily_usd_cap": 5}); w.Code != http.StatusOK {
		t.Fatalf("add member → %d (%s)", w.Code, w.Body.String())
	}
	// Unknown token rejected.
	if w := do(e, "POST", "/api/team/members", tokAdmin, gin.H{"token": "sk-ghost"}); w.Code != http.StatusBadRequest {
		t.Fatalf("add phantom → %d, want 400", w.Code)
	}
	// Adding an existing member → 409.
	if w := do(e, "POST", "/api/team/members", tokAdmin, gin.H{"token": tokMember}); w.Code != http.StatusConflict {
		t.Fatalf("add dup → %d, want 409", w.Code)
	}
	// List shows three members now.
	w := do(e, "GET", "/api/team/members", tokAdmin, nil)
	var list struct {
		Members []map[string]any `json:"members"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.Members) != 3 {
		t.Fatalf("members = %d, want 3", len(list.Members))
	}
	// Patch the new member's cap via its masked id.
	masked := maskToken(tokNew)
	if w := do(e, "PATCH", "/api/team/members/"+masked, tokAdmin, gin.H{"monthly_usd_cap": 50}); w.Code != http.StatusOK {
		t.Fatalf("patch member → %d (%s)", w.Code, w.Body.String())
	}
	m, _ := d.MemberFor(context.Background(), tokNew)
	if m.MonthlyUSDCap != 50 {
		t.Fatalf("monthly cap = %.2f, want 50", m.MonthlyUSDCap)
	}
	// Remove it.
	if w := do(e, "DELETE", "/api/team/members/"+masked, tokAdmin, nil); w.Code != http.StatusOK {
		t.Fatalf("remove member → %d", w.Code)
	}
	if _, err := d.MemberFor(context.Background(), tokNew); err == nil {
		t.Fatal("member still present after delete")
	}
}

func TestTeamCannotRemoveSoleAdmin(t *testing.T) {
	e, _ := newTeamTest(t)
	masked := maskToken(tokAdmin)
	if w := do(e, "DELETE", "/api/team/members/"+masked, tokAdmin, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("remove sole admin → %d, want 400", w.Code)
	}
}
