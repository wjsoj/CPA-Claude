package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/wjsoj/CPA-Claude/internal/saas/billing"
	saasdb "github.com/wjsoj/CPA-Claude/internal/saas/db"
	"github.com/wjsoj/CPA-Claude/internal/tokenmask"
	"github.com/wjsoj/cc-core/auth"
	"github.com/wjsoj/cc-core/clienttoken"
	"github.com/wjsoj/cc-core/requestlog"
)

// The operator's workspace-usage endpoint reads every member of any team, which
// makes "it is inside the admin-auth group" its only boundary — and admin.go
// registers it as one line in a long list of api.GET calls, where a stray
// registration outside the group would look identical. It also promises the
// customer-facing /api/team/usage and this one are the same numbers, a claim
// nothing asserted.

const (
	wsAdminToken  = "sk-wsadm-000000000000000000000000aaaa"
	wsMemberToken = "sk-wsmem-000000000000000000000000bbbb"
	wsOtherToken  = "sk-wsoth-000000000000000000000000cccc"
	testAdminKey  = "admin-key-for-tests"
)

// wsUsageFixture builds an admin handler with SaaS wired over two workspaces
// sharing one request log, plus the team console over the same stores so the
// two answers can be compared field by field.
func wsUsageFixture(t *testing.T) (*gin.Engine, *gin.Engine, int64, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dir := t.TempDir()
	loc := requestlog.BucketLocation()
	today := time.Now().In(loc)
	writeLog(t, dir, today, []requestlog.Record{
		{
			TS: today.Add(-2 * time.Hour), ClientToken: tokenmask.Mask(wsMemberToken),
			Provider: "anthropic", Model: "claude-opus-4-7", AuthID: "a1", AuthKind: "oauth",
			Input: 1000, Output: 200, CostUSD: 60, BilledUSD: 3, Status: 200,
		},
		{
			// Another team on the same relay: never part of our totals.
			TS: today.Add(-2 * time.Hour), ClientToken: tokenmask.Mask(wsOtherToken),
			Provider: "anthropic", Model: "claude-opus-4-7", AuthID: "a1", AuthKind: "oauth",
			CostUSD: 2000, BilledUSD: 100, Status: 200,
		},
	})

	d, err := saasdb.Open(filepath.Join(t.TempDir(), "saas.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	ctx := context.Background()
	ws, err := d.CreateWorkspace(ctx, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if err := d.AddMember(ctx, ws.ID, wsAdminToken, saasdb.WSRoleAdmin, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := d.AddMember(ctx, ws.ID, wsMemberToken, saasdb.WSRoleMember, 0, 0); err != nil {
		t.Fatal(err)
	}
	other, err := d.CreateWorkspace(ctx, "rival")
	if err != nil {
		t.Fatal(err)
	}
	if err := d.AddMember(ctx, other.ID, wsOtherToken, saasdb.WSRoleAdmin, 0, 0); err != nil {
		t.Fatal(err)
	}

	tokens := clienttoken.OpenInMemory()
	for _, tok := range []string{wsAdminToken, wsMemberToken, wsOtherToken} {
		if err := tokens.Add(clienttoken.Token{Token: tok, Group: "default"}); err != nil {
			t.Fatal(err)
		}
	}

	cfg := statementCfg(dir)
	cfg.AdminToken = testAdminKey
	cfg.AdminPath = "/mgmt-console"
	h := New(cfg, auth.NewPool(nil, nil, time.Minute, false, ""), nil, nil, tokens).
		WithLogIndex(true)
	h.WithSaaS(d, nil)
	adminEngine := gin.New()
	h.Register(adminEngine)

	th := &billing.TeamHandler{
		DB: d, LogDir: dir, LogIndexed: true,
		Auth:        func(c *gin.Context) string { return c.GetHeader("X-Tok") },
		TokenExists: func(string) bool { return true },
	}
	teamEngine := gin.New()
	th.Routes(teamEngine.Group("/api/team"))
	return adminEngine, teamEngine, ws.ID, dir
}

func adminGET(e *gin.Engine, path, adminTok string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if adminTok != "" {
		req.Header.Set("X-Admin-Token", adminTok)
	}
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)
	return w
}

func TestWorkspaceUsageIsAdminOnly(t *testing.T) {
	e, _, wsID, _ := wsUsageFixture(t)
	path := "/mgmt-console/api/workspaces/" + itoa(wsID) + "/usage"

	if w := adminGET(e, path, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("no admin token → %d, want 401", w.Code)
	}
	// A client token — even a workspace admin's own — is not an operator
	// credential. This is the assertion that keeps the route inside the
	// admin-auth group.
	if w := adminGET(e, path, wsAdminToken); w.Code != http.StatusUnauthorized {
		t.Fatalf("client token as admin key → %d, want 401", w.Code)
	}
	if w := adminGET(e, path, testAdminKey); w.Code != http.StatusOK {
		t.Fatalf("admin key → %d (%s)", w.Code, w.Body.String())
	}
}

func TestWorkspaceUsageRejectsBadInput(t *testing.T) {
	e, _, wsID, _ := wsUsageFixture(t)
	base := "/mgmt-console/api/workspaces/"
	// A timestamp window is refused rather than silently costing a full row
	// scan, the same contract /api/team/usage publishes.
	if w := adminGET(e, base+itoa(wsID)+"/usage?from=2026-08-01T00:00:00Z", testAdminKey); w.Code != http.StatusBadRequest {
		t.Fatalf("timestamp window → %d, want 400", w.Code)
	}
	if w := adminGET(e, base+"abc/usage", testAdminKey); w.Code != http.StatusBadRequest {
		t.Fatalf("non-numeric id → %d, want 400", w.Code)
	}
	// An unknown workspace is an empty team, not an error — the operator gets a
	// zeroed view rather than a stack trace.
	if w := adminGET(e, base+"9999/usage", testAdminKey); w.Code != http.StatusOK {
		t.Fatalf("unknown workspace → %d (%s)", w.Code, w.Body.String())
	}
}

// With SaaS off there are no workspaces at all, and the endpoint must say so
// rather than dereference a nil store.
func TestWorkspaceUsageWithoutSaaS(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := statementCfg(t.TempDir())
	cfg.AdminToken = testAdminKey
	cfg.AdminPath = "/mgmt-console"
	h := New(cfg, auth.NewPool(nil, nil, time.Minute, false, ""), nil, nil, clienttoken.OpenInMemory())
	e := gin.New()
	h.Register(e)
	if w := adminGET(e, "/mgmt-console/api/workspaces/1/usage", testAdminKey); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("saas disabled → %d, want 503 (%s)", w.Code, w.Body.String())
	}
}

// The operator view and the customer view are one producer, and a support
// conversation depends on that: if the two ever diverge, the operator is
// telling a customer their bill is something the customer's own console does
// not show.
func TestWorkspaceUsageMatchesTheTeamConsole(t *testing.T) {
	adminEngine, teamEngine, wsID, _ := wsUsageFixture(t)
	today := time.Now().In(requestlog.BucketLocation()).Format("2006-01-02")
	q := "?from=" + today + "&to=" + today

	w := adminGET(adminEngine, "/mgmt-console/api/workspaces/"+itoa(wsID)+"/usage"+q, testAdminKey)
	if w.Code != http.StatusOK {
		t.Fatalf("operator usage → %d (%s)", w.Code, w.Body.String())
	}
	req := httptest.NewRequest(http.MethodGet, "/api/team/usage"+q, nil)
	req.Header.Set("X-Tok", wsAdminToken)
	tw := httptest.NewRecorder()
	teamEngine.ServeHTTP(tw, req)
	if tw.Code != http.StatusOK {
		t.Fatalf("team usage → %d (%s)", tw.Code, tw.Body.String())
	}

	var opsView, teamView map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &opsView); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(tw.Body.Bytes(), &teamView); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"total", "by_member", "by_model", "by_day", "pool_billed_usd", "personal_billed_usd"} {
		if !reflect.DeepEqual(opsView[key], teamView[key]) {
			t.Fatalf("%s differs:\n operator: %v\n customer: %v", key, opsView[key], teamView[key])
		}
	}
	// And it really is this team's own spend, not the fleet's.
	total := opsView["total"].(map[string]any)
	if got := total["billed_usd"].(float64); got != 3 {
		t.Fatalf("total = %v, want the team's own 3 (the rival's 100 leaked in?)", got)
	}
	members := opsView["by_member"].([]any)
	if len(members) != 2 {
		t.Fatalf("by_member has %d rows, want this workspace's roster of 2", len(members))
	}
	for _, m := range members {
		masked := m.(map[string]any)["masked"].(string)
		if masked == tokenmask.Mask(wsOtherToken) {
			t.Fatalf("another workspace's member appears in by_member: %v", members)
		}
	}
}

// The two member lists are rendered by different code from the same producer;
// a column added to one panel must find its data on the other.
func TestWorkspaceMembersCarryTheSameSpendShape(t *testing.T) {
	e, _, wsID, _ := wsUsageFixture(t)
	w := adminGET(e, "/mgmt-console/api/workspaces/"+itoa(wsID)+"/members", testAdminKey)
	if w.Code != http.StatusOK {
		t.Fatalf("members → %d (%s)", w.Code, w.Body.String())
	}
	var body struct {
		Members      []map[string]any `json:"members"`
		Timezone     string           `json:"timezone"`
		SpendPartial bool             `json:"spend_partial"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Timezone == "" || body.SpendPartial {
		t.Fatalf("member list envelope = %+v", body)
	}
	var seen bool
	for _, m := range body.Members {
		if m["masked"] != tokenmask.Mask(wsMemberToken) {
			continue
		}
		seen = true
		for _, key := range []string{
			"spend_day_usd", "spend_month_usd", "spend_day_requests", "spend_month_requests",
		} {
			if _, ok := m[key]; !ok {
				t.Fatalf("member row missing %s (team console renders it): %v", key, m)
			}
		}
		if m["spend_month_usd"].(float64) != 3 {
			t.Fatalf("spend_month_usd = %v, want 3", m["spend_month_usd"])
		}
		if m["used_month_usd"].(float64) != 0 {
			t.Fatalf("pool draw = %v, want 0 — no pool was funded", m["used_month_usd"])
		}
	}
	if !seen {
		t.Fatalf("the member with traffic is missing: %v", body.Members)
	}
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }
