package billing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/wjsoj/CPA-Claude/internal/saas/db"
	"github.com/wjsoj/CPA-Claude/internal/tokenmask"
	"github.com/wjsoj/cc-core/requestlog"
)

// newUsageTeam builds the team console over a workspace whose members have
// request-log traffic but an unfunded pool — mode B, the case the old member
// view reported as all zeros.
func newUsageTeam(t *testing.T) (*gin.Engine, *db.DB, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	d, err := db.Open(filepath.Join(t.TempDir(), "team.db"))
	if err != nil {
		t.Fatal(err)
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
	logDir := t.TempDir()
	th := &TeamHandler{
		DB:     d,
		LogDir: logDir,
		// The fixture drives requestlog's scanning path, but LogIndexed is a
		// permission gate on the per-member fan-out rather than a claim about
		// which path a query takes — production runs indexed, so the tests do
		// too, and the un-indexed degradation has its own case.
		LogIndexed:  true,
		Auth:        func(c *gin.Context) string { return c.GetHeader("X-Tok") },
		TokenExists: func(tok string) bool { return true },
		TokenLabel: func(tok string) string {
			if tok == tokAdmin {
				return "boss"
			}
			return "worker"
		},
	}
	e := gin.New()
	th.Routes(e.Group("/api/team"))
	return e, d, logDir
}

func TestTeamUsageRequiresGroupAdmin(t *testing.T) {
	e, _, _ := newUsageTeam(t)
	if w := do(e, "GET", "/api/team/usage", "", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("no token → %d, want 401", w.Code)
	}
	// A plain member of the very workspace being asked about is still refused:
	// per-member spend is management information.
	if w := do(e, "GET", "/api/team/usage", tokMember, nil); w.Code != http.StatusForbidden {
		t.Fatalf("member token → %d, want 403", w.Code)
	}
	if w := do(e, "GET", "/api/team/usage", "sk-nobody-000000000000000000000000", nil); w.Code != http.StatusForbidden {
		t.Fatalf("outsider → %d, want 403", w.Code)
	}
	if w := do(e, "GET", "/api/team/usage", tokAdmin, nil); w.Code != http.StatusOK {
		t.Fatalf("admin → %d (%s)", w.Code, w.Body.String())
	}
}

func TestTeamUsageRejectsTimestampWindow(t *testing.T) {
	e, _, _ := newUsageTeam(t)
	w := do(e, "GET", "/api/team/usage?from=2026-08-01T00:00:00Z&to=2026-08-16", tokAdmin, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("timestamp window → %d, want 400", w.Code)
	}
}

// TestTeamUsageModeB is the whole point: no pool money moved, so every
// used_*_usd is zero, and the spend_*/usage figures must still be right.
func TestTeamUsageModeB(t *testing.T) {
	e, _, logDir := newUsageTeam(t)
	now := time.Now().UTC()
	writeLog(t, logDir, []requestlog.Record{
		rec(now, tokAdmin, "claude-opus-4-7", 3),
		rec(now, tokMember, "claude-sonnet-5", 7),
	})
	today := day(now)

	// Member list: pool spend zero, real spend not.
	w := do(e, "GET", "/api/team/members", tokAdmin, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("members → %d (%s)", w.Code, w.Body.String())
	}
	var list struct {
		Members      []map[string]any `json:"members"`
		SpendPartial bool             `json:"spend_partial"`
		Timezone     string           `json:"timezone"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if list.SpendPartial {
		t.Fatal("spend_partial set with a readable log")
	}
	if list.Timezone == "" {
		t.Fatal("member list omits the display timezone the windows are cut on")
	}
	seen := map[string]map[string]any{}
	for _, m := range list.Members {
		seen[m["masked"].(string)] = m
	}
	worker := seen[tokenmask.Mask(tokMember)]
	if worker == nil {
		t.Fatalf("member missing from list: %v", list.Members)
	}
	if got := worker["used_day_usd"].(float64); got != 0 {
		t.Fatalf("pool spend = %v, want 0 in mode B", got)
	}
	if got := worker["spend_day_usd"].(float64); !approx(got, 7) {
		t.Fatalf("real day spend = %v, want 7", got)
	}
	if got := worker["spend_source"].(string); got != "requestlog" {
		t.Fatalf("spend_source = %q", got)
	}

	// Group usage over the same day.
	w = do(e, "GET", "/api/team/usage?from="+today+"&to="+today, tokAdmin, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("usage → %d (%s)", w.Code, w.Body.String())
	}
	var usage struct {
		Total struct {
			BilledUSD float64 `json:"billed_usd"`
			Requests  int64   `json:"requests"`
		} `json:"total"`
		PoolBilledUSD     float64 `json:"pool_billed_usd"`
		PersonalBilledUSD float64 `json:"personal_billed_usd"`
		Partial           bool    `json:"partial"`
		ByMember          []struct {
			Masked            string  `json:"masked"`
			Label             string  `json:"label"`
			BilledUSD         float64 `json:"billed_usd"`
			PoolBilledUSD     float64 `json:"pool_billed_usd"`
			PersonalBilledUSD float64 `json:"personal_billed_usd"`
		} `json:"by_member"`
		ByModel []struct {
			Model     string  `json:"model"`
			BilledUSD float64 `json:"billed_usd"`
		} `json:"by_model"`
		ByDay []struct {
			Day string `json:"day"`
		} `json:"by_day"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &usage); err != nil {
		t.Fatal(err)
	}
	if usage.Partial {
		t.Fatal("unexpected partial")
	}
	if !approx(usage.Total.BilledUSD, 10) || usage.Total.Requests != 2 {
		t.Fatalf("total = %+v, want 10 / 2 requests", usage.Total)
	}
	if usage.PoolBilledUSD != 0 || !approx(usage.PersonalBilledUSD, 10) {
		t.Fatalf("pool/personal split = %v / %v, want 0 / 10", usage.PoolBilledUSD, usage.PersonalBilledUSD)
	}
	// by_member must sum to the headline total, or the panel and any invoice
	// attachment built from it disagree.
	var sum float64
	for _, m := range usage.ByMember {
		sum += m.BilledUSD
	}
	if !approx(sum, usage.Total.BilledUSD) {
		t.Fatalf("by_member sums to %v, total says %v", sum, usage.Total.BilledUSD)
	}
	if usage.ByMember[0].BilledUSD < usage.ByMember[len(usage.ByMember)-1].BilledUSD {
		t.Fatalf("by_member not sorted by spend: %+v", usage.ByMember)
	}
	if usage.ByMember[0].Label == "" {
		t.Fatal("by_member rows lost their labels")
	}
	sum = 0
	for _, m := range usage.ByModel {
		sum += m.BilledUSD
	}
	if !approx(sum, 10) {
		t.Fatalf("by_model sums to %v, want 10", sum)
	}
	if len(usage.ByDay) != 1 || usage.ByDay[0].Day != today {
		t.Fatalf("by_day = %+v, want the single requested day", usage.ByDay)
	}
}

func ptrF(v float64) *float64 { return &v }

// TestTeamUsageSplitsPoolFromPersonal is mode A past a cap: the pool covered
// part of a member's spend and their own wallet covered the rest. Both numbers
// have to survive to the wire, since the caps only meter the pool half.
func TestTeamUsageSplitsPoolFromPersonal(t *testing.T) {
	e, d, logDir := newUsageTeam(t)
	now := time.Now().UTC()
	writeLog(t, logDir, []requestlog.Record{rec(now, tokMember, "claude-opus-4-7", 10)})

	ctx := context.Background()
	ws, err := d.WorkspaceAdminFor(ctx, tokAdmin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.AdjustWorkspaceBalance(ctx, ws.ID, 100, db.TxKindTopup, "seed", ""); err != nil {
		t.Fatal(err)
	}
	// A cap of 4 makes the pool cover exactly 4 of the 10 and the member's own
	// wallet the rest — the fallback that workspace_tx never records.
	if err := d.UpdateMember(ctx, tokMember, nil, ptrF(4), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := d.AddBalance(ctx, tokMember, db.TxKindTopup, 50, "seed", "", true); err != nil {
		t.Fatal(err)
	}
	pool, personal, err := d.ChargeMemberFirst(ctx, tokMember, 10, "test", "spend")
	if err != nil {
		t.Fatal(err)
	}
	if !approx(pool, 4) || !approx(personal, 6) {
		t.Fatalf("charge split = %v / %v, want 4 / 6", pool, personal)
	}

	today := day(now)
	w := do(e, "GET", "/api/team/usage?from="+today+"&to="+today, tokAdmin, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("usage → %d (%s)", w.Code, w.Body.String())
	}
	var usage struct {
		Total struct {
			BilledUSD float64 `json:"billed_usd"`
		} `json:"total"`
		PoolBilledUSD     float64 `json:"pool_billed_usd"`
		PersonalBilledUSD float64 `json:"personal_billed_usd"`
		ByMember          []struct {
			Masked            string  `json:"masked"`
			PoolBilledUSD     float64 `json:"pool_billed_usd"`
			PersonalBilledUSD float64 `json:"personal_billed_usd"`
		} `json:"by_member"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &usage); err != nil {
		t.Fatal(err)
	}
	if !approx(usage.Total.BilledUSD, 10) {
		t.Fatalf("total = %v, want 10", usage.Total.BilledUSD)
	}
	if !approx(usage.PoolBilledUSD, 4) || !approx(usage.PersonalBilledUSD, 6) {
		t.Fatalf("split = %v pool / %v personal, want 4 / 6", usage.PoolBilledUSD, usage.PersonalBilledUSD)
	}
	// found, not a bare continue: if the masked form ever drifts, a loop that
	// simply skips every row passes while asserting nothing.
	found := false
	for _, m := range usage.ByMember {
		if m.Masked != tokenmask.Mask(tokMember) {
			continue
		}
		found = true
		if !approx(m.PoolBilledUSD, 4) || !approx(m.PersonalBilledUSD, 6) {
			t.Fatalf("member split = %v / %v, want 4 / 6", m.PoolBilledUSD, m.PersonalBilledUSD)
		}
	}
	if !found {
		t.Fatalf("the charged member is missing from by_member: %+v", usage.ByMember)
	}
}

// ---- cross-tenant scoping -------------------------------------------------

const (
	tokAdminB  = "sk-admn2-0000000000000000000000000000dddd"
	tokMemberB = "sk-memb2-0000000000000000000000000000eeee"
)

// newTwoTeams puts two workspaces on one relay, each with its own admin and
// member, sharing one request log — which is what production looks like and
// what makes every group-scoped query a tenancy boundary.
func newTwoTeams(t *testing.T) (*gin.Engine, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	d, err := db.Open(filepath.Join(t.TempDir(), "team.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	ctx := context.Background()
	for _, tc := range []struct{ name, admin, member string }{
		{"alpha", tokAdmin, tokMember},
		{"beta", tokAdminB, tokMemberB},
	} {
		ws, err := d.CreateWorkspace(ctx, tc.name)
		if err != nil {
			t.Fatal(err)
		}
		if err := d.AddMember(ctx, ws.ID, tc.admin, db.WSRoleAdmin, 0, 0); err != nil {
			t.Fatal(err)
		}
		if err := d.AddMember(ctx, ws.ID, tc.member, db.WSRoleMember, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	logDir := t.TempDir()
	th := &TeamHandler{
		DB: d, LogDir: logDir, LogIndexed: true,
		Auth:        func(c *gin.Context) string { return c.GetHeader("X-Tok") },
		TokenExists: func(tok string) bool { return true },
	}
	e := gin.New()
	th.Routes(e.Group("/api/team"))
	return e, logDir
}

// ?member= is the only identifier on the team API that the caller constructs
// and that reaches a request-log query key. Passing it straight through would be
// the obvious "skip a loop" optimisation, and it would turn this endpoint into a
// cross-tenant read: masked tokens are visible to anyone who can see a member
// list. The roster check is the entire boundary, so it gets an explicit test.
func TestTeamRequestsMemberFilterIsScopedToOwnRoster(t *testing.T) {
	e, logDir := newTwoTeams(t)
	now := time.Now().UTC().Add(-time.Hour)
	writeLog(t, logDir, []requestlog.Record{
		rec(now, tokMember, "claude-opus-4-7", 3),
		rec(now, tokMemberB, "claude-opus-4-7", 8),
	})
	get := func(tok, member string) []map[string]any {
		t.Helper()
		w := do(e, "GET", "/api/team/requests?member="+url.QueryEscape(member), tok, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("requests → %d (%s)", w.Code, w.Body.String())
		}
		var out struct {
			Requests []map[string]any `json:"requests"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out.Requests
	}
	// Team alpha's admin asking about team beta's member, by the mask beta's own
	// console displays.
	if rows := get(tokAdmin, tokenmask.Mask(tokMemberB)); len(rows) != 0 {
		t.Fatalf("cross-tenant read: alpha's admin got %d of beta's rows (%v)", len(rows), rows)
	}
	// And the filter still does its job within the caller's own roster.
	rows := get(tokAdmin, tokenmask.Mask(tokMember))
	if len(rows) != 1 {
		t.Fatalf("own member filter returned %d rows, want 1", len(rows))
	}
	if got := rows[0]["member"]; got != tokenmask.Mask(tokMember) {
		t.Fatalf("row attributed to %v", got)
	}
	if got := rows[0]["billed_usd"].(float64); !approx(got, 3) {
		t.Fatalf("billed = %v, want alpha's own 3 (not beta's 8)", got)
	}
}

// The same boundary one level up: /usage must describe this workspace's roster
// and nothing else. TestGroupUsageIsScopedToMembers covers the pure function;
// this covers the handler actually resolving ws.ID from the bearer token.
func TestTeamUsageByMemberMatchesOwnRosterExactly(t *testing.T) {
	e, logDir := newTwoTeams(t)
	now := time.Now().UTC().Add(-time.Hour)
	writeLog(t, logDir, []requestlog.Record{
		rec(now, tokMember, "claude-opus-4-7", 3),
		rec(now, tokMemberB, "claude-opus-4-7", 8),
	})
	today := day(now)
	w := do(e, "GET", "/api/team/usage?from="+today+"&to="+today, tokAdmin, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("usage → %d (%s)", w.Code, w.Body.String())
	}
	var usage struct {
		Total struct {
			BilledUSD float64 `json:"billed_usd"`
		} `json:"total"`
		ByMember []struct {
			Masked string `json:"masked"`
		} `json:"by_member"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &usage); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, m := range usage.ByMember {
		got[m.Masked] = true
	}
	want := map[string]bool{tokenmask.Mask(tokAdmin): true, tokenmask.Mask(tokMember): true}
	if len(got) != len(want) {
		t.Fatalf("by_member = %v, want exactly this workspace's roster %v", got, want)
	}
	for m := range want {
		if !got[m] {
			t.Fatalf("by_member = %v, missing %s", got, m)
		}
	}
	if !approx(usage.Total.BilledUSD, 3) {
		t.Fatalf("total = %v, want alpha's own 3", usage.Total.BilledUSD)
	}
}

// ---- degradation shapes ---------------------------------------------------

// "We could not measure it" and "they spent nothing" must not render the same,
// which means the unavailable/unmeasurable branches have to omit the figures
// rather than send zeros. Asserted on the wire, because that is where a client
// decides what to draw.
func TestTeamMembersOmitSpendWhenItCannotBeMeasured(t *testing.T) {
	gin.SetMode(gin.TestMode)
	d, err := db.Open(filepath.Join(t.TempDir(), "team.db"))
	if err != nil {
		t.Fatal(err)
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
	// An operator-assigned short token: every such token masks to the same
	// opaque string, so its rows cannot be told from anyone else's.
	const shortTok = "sk-short1"
	if err := d.AddMember(ctx, ws.ID, shortTok, db.WSRoleMember, 0, 0); err != nil {
		t.Fatal(err)
	}

	newEngine := func(logDir string) *gin.Engine {
		th := &TeamHandler{
			DB: d, LogDir: logDir, LogIndexed: true,
			Auth:        func(c *gin.Context) string { return c.GetHeader("X-Tok") },
			TokenExists: func(tok string) bool { return true },
		}
		e := gin.New()
		th.Routes(e.Group("/api/team"))
		return e
	}
	list := func(e *gin.Engine) (map[string]map[string]any, bool) {
		t.Helper()
		w := do(e, "GET", "/api/team/members", tokAdmin, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("members → %d (%s)", w.Code, w.Body.String())
		}
		var out struct {
			Members      []map[string]any `json:"members"`
			SpendPartial bool             `json:"spend_partial"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		by := map[string]map[string]any{}
		for _, m := range out.Members {
			by[m["masked"].(string)] = m
		}
		return by, out.SpendPartial
	}

	// (a) No request log at all: nobody's spend is knowable.
	rows, partial := list(newEngine(""))
	if !partial {
		t.Fatal("spend_partial must be set when the log cannot be read at all")
	}
	for mask, row := range rows {
		if row["spend_source"] != "unavailable" {
			t.Fatalf("%s: spend_source = %v, want unavailable", mask, row["spend_source"])
		}
		if _, present := row["spend_day_usd"]; present {
			t.Fatalf("%s: spend_day_usd sent as %v — an unknown must be absent, not zero",
				mask, row["spend_day_usd"])
		}
	}

	// (b) A readable log: the short token is still unmeasurable, on its own, and
	// the ordinary member is unaffected.
	logDir := t.TempDir()
	writeLog(t, logDir, []requestlog.Record{rec(time.Now().UTC(), tokAdmin, "claude-opus-4-7", 2)})
	rows, partial = list(newEngine(logDir))
	if partial {
		t.Fatal("a readable log is not partial just because one token is short")
	}
	short := rows[tokenmask.Opaque]
	if short == nil {
		t.Fatalf("short-token member missing: %v", rows)
	}
	if short["spend_source"] != "unmeasurable" {
		t.Fatalf("short token: spend_source = %v, want unmeasurable", short["spend_source"])
	}
	if _, present := short["spend_month_usd"]; present {
		t.Fatal("short token reported a spend figure that cannot be attributed to it")
	}
	ok := rows[tokenmask.Mask(tokAdmin)]
	if ok["spend_source"] != "requestlog" || !approx(ok["spend_day_usd"].(float64), 2) {
		t.Fatalf("measurable member: %v", ok)
	}
}

// A log directory that exists but cannot be read is neither "no log configured"
// nor a working one; it must degrade the same way, not 500.
func TestTeamUsageReportsPartialWhenTheLogQueryFails(t *testing.T) {
	_, d, _ := newUsageTeam(t)
	unreadable := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(unreadable, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	th := &TeamHandler{
		DB: d, LogDir: unreadable, LogIndexed: true,
		Auth:        func(c *gin.Context) string { return c.GetHeader("X-Tok") },
		TokenExists: func(tok string) bool { return true },
	}
	e := gin.New()
	th.Routes(e.Group("/api/team"))

	w := do(e, "GET", "/api/team/usage", tokAdmin, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("usage → %d (%s), want a degraded 200", w.Code, w.Body.String())
	}
	var usage struct {
		Partial bool     `json:"partial"`
		Notes   []string `json:"notes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &usage); err != nil {
		t.Fatal(err)
	}
	if !usage.Partial || len(usage.Notes) == 0 {
		t.Fatalf("an unreadable log must be reported as partial: %+v", usage)
	}
}

// A single-member lookup — what add/edit member renders — filters the query on
// that member instead of asking for every client bucket in the window. The
// saving only matters if the answer is still that member's own, so this pins
// the number rather than the query shape.
func TestMemberSpendToDateForOneMemberIsThatMembersOwn(t *testing.T) {
	dir := newDir(t)
	now := time.Now().In(requestlog.BucketLocation())
	at := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, now.Location())
	if at.After(now) {
		at = now
	}
	writeLog(t, dir, []requestlog.Record{
		rec(at, tokMember, "claude-opus-4-7", 4),
		rec(at, tokAdmin, "claude-opus-4-7", 90),
	})
	one, ok := MemberSpendToDate(dir, []GroupMember{{Token: tokMember}})
	if !ok {
		t.Fatal("log reported unreadable")
	}
	got := one[tokenmask.Mask(tokMember)]
	if !approx(got.Day.BilledUSD, 4) || !approx(got.Month.BilledUSD, 4) {
		t.Fatalf("single-member spend = %+v, want 4 / 4 (someone else's rows counted?)", got)
	}
	// And the whole-team call agrees about that member, so the two shapes are
	// interchangeable for the caller.
	all, ok := MemberSpendToDate(dir, []GroupMember{{Token: tokMember}, {Token: tokAdmin}})
	if !ok {
		t.Fatal("log reported unreadable")
	}
	if all[tokenmask.Mask(tokMember)].Day.BilledUSD != got.Day.BilledUSD {
		t.Fatalf("one-member path %v disagrees with the team path %v",
			got.Day.BilledUSD, all[tokenmask.Mask(tokMember)].Day.BilledUSD)
	}
	if !approx(all[tokenmask.Mask(tokAdmin)].Day.BilledUSD, 90) {
		t.Fatalf("team path lost the other member: %+v", all)
	}
}
