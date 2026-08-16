package billing

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/wjsoj/CPA-Claude/internal/saas/db"
	"github.com/wjsoj/CPA-Claude/internal/tokenmask"
	"github.com/wjsoj/cc-core/requestlog"
)

// teamRequests calls the drill-down and returns the decoded body.
func teamRequests(t *testing.T, e *gin.Engine, tok, query string) (rows []map[string]any, truncated bool, tz string) {
	t.Helper()
	w := do(e, "GET", "/api/team/requests"+query, tok, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("requests%s → %d (%s)", query, w.Code, w.Body.String())
	}
	var out struct {
		Requests  []map[string]any `json:"requests"`
		Truncated bool             `json:"truncated"`
		Timezone  string           `json:"timezone"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Requests, out.Truncated, out.Timezone
}

// The window is the whole point of the drill-down: a console showing "this
// month" must not answer with rows from three months ago, and — the reason the
// contract is day labels rather than timestamps — the query that answers it has
// to stay on the pre-summed index (see usage.go's header).
func TestTeamRequestsHonoursTheDayWindow(t *testing.T) {
	e, _, logDir := newUsageTeam(t)
	old := time.Now().UTC().Add(-72 * time.Hour)
	recent := time.Now().UTC().Add(-1 * time.Hour)
	writeLog(t, logDir, []requestlog.Record{
		rec(old, tokMember, "claude-opus-4-7", 3),
		rec(recent, tokMember, "claude-sonnet-5", 5),
	})

	// Unbounded: both rows, the endpoint's historical behaviour.
	if rows, _, _ := teamRequests(t, e, tokAdmin, ""); len(rows) != 2 {
		t.Fatalf("unbounded returned %d rows, want 2", len(rows))
	}
	// Bounded to the older day: only that day's row.
	rows, _, tz := teamRequests(t, e, tokAdmin, "?from="+day(old)+"&to="+day(old))
	if len(rows) != 1 {
		t.Fatalf("windowed returned %d rows, want only the old day's 1: %v", len(rows), rows)
	}
	if rows[0]["model"] != "claude-opus-4-7" {
		t.Fatalf("windowed row = %v, want the old day's", rows[0])
	}
	if tz == "" {
		t.Fatal("response omits the timezone the day labels are cut in")
	}
	// And to the recent day: only the other one.
	rows, _, _ = teamRequests(t, e, tokAdmin, "?from="+day(recent)+"&to="+day(recent))
	if len(rows) != 1 || rows[0]["model"] != "claude-sonnet-5" {
		t.Fatalf("recent window = %v, want only the recent row", rows)
	}
}

func TestTeamRequestsRejectsATimestampWindow(t *testing.T) {
	e, _, _ := newUsageTeam(t)
	ts := url.QueryEscape(time.Now().UTC().Format(time.RFC3339))
	if w := do(e, "GET", "/api/team/requests?from="+ts, tokAdmin, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("timestamp window → %d, want 400", w.Code)
	}
}

// Rows written before cc-core v0.8.61 carry the charge in cost_usd with
// billed_usd absent, and 90-day retention keeps both conventions live at once.
// Reading the raw field renders a legacy row as free — on the one column a
// member takes to finance.
func TestTeamRequestsPriceLegacyRows(t *testing.T) {
	e, _, logDir := newUsageTeam(t)
	now := time.Now().UTC().Add(-time.Hour)
	writeLog(t, logDir, []requestlog.Record{legacyRec(now, tokMember, "claude-opus-4-7", 4)})

	rows, _, _ := teamRequests(t, e, tokAdmin, "")
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if got := rows[0]["billed_usd"].(float64); !approx(got, 4) {
		t.Fatalf("billed_usd = %v, want the legacy row's 4 (cost_usd)", got)
	}
	// The yuan column converts the same figure, at the statement's rate.
	usd := rows[0]["billed_usd"].(float64)
	cny := rows[0]["billed_cny"].(float64)
	if cny <= 0 || !approx(cny, usd*config5DefaultRate()) {
		t.Fatalf("billed_cny = %v, want %v × the statement rate", cny, usd)
	}
}

// config5DefaultRate is the rate a handler with no exchange wiring converts at —
// the same fallback the team statement uses, which is the point: the drill-down
// and the document it is reconciled against must quote one number.
func config5DefaultRate() float64 {
	th := &TeamHandler{}
	return th.statementRate()
}

// ?member= is the only identifier on this API that a caller constructs and that
// reaches a request-log query key, and masks are visible to anyone who can read
// a member list. Passing it through unresolved would make the endpoint a
// cross-tenant read of any team whose masks you have seen.
func TestTeamRequestsMemberFilterRefusesAnotherTeamsMask(t *testing.T) {
	e, logDir := newTwoTeams(t)
	now := time.Now().UTC().Add(-time.Hour)
	writeLog(t, logDir, []requestlog.Record{
		rec(now, tokMember, "claude-opus-4-7", 3),
		rec(now, tokMemberB, "claude-opus-4-7", 8),
	})
	rows, _, _ := teamRequests(t, e, tokAdmin, "?member="+url.QueryEscape(tokenmask.Mask(tokMemberB)))
	if len(rows) != 0 {
		t.Fatalf("cross-tenant read: alpha's admin got %d of beta's rows (%v)", len(rows), rows)
	}
}

// A single-member drill-down must query that member and no one else. There is
// no counter to assert on, so the observable form is used: another member of the
// caller's OWN roster has rows in the same window, and none of them may appear.
// Dropping the roster narrowing (fanning out and filtering afterwards, or not
// filtering at all) fails here.
func TestTeamRequestsSingleMemberDoesNotFanOut(t *testing.T) {
	e, _, logDir := newUsageTeam(t)
	now := time.Now().UTC().Add(-time.Hour)
	writeLog(t, logDir, []requestlog.Record{
		rec(now, tokMember, "claude-opus-4-7", 3),
		rec(now.Add(-time.Minute), tokAdmin, "claude-sonnet-5", 9),
	})
	// Both are visible without the filter, so their absence below is the filter's
	// doing rather than a fixture that never had them.
	if rows, _, _ := teamRequests(t, e, tokAdmin, ""); len(rows) != 2 {
		t.Fatalf("unfiltered returned %d rows, want both members' 2", len(rows))
	}
	mask := tokenmask.Mask(tokMember)
	rows, _, _ := teamRequests(t, e, tokAdmin, "?member="+url.QueryEscape(mask))
	if len(rows) != 1 {
		t.Fatalf("single-member returned %d rows, want 1", len(rows))
	}
	for _, r := range rows {
		if r["member"] != mask {
			t.Fatalf("row from another member leaked into a single-member query: %v", r)
		}
	}
}

// limit bounds the page and truncated reports that there is more behind it — a
// team that made 4000 requests this month must not read as having made exactly
// `limit`.
func TestTeamRequestsLimitBoundsThePage(t *testing.T) {
	e, _, logDir := newUsageTeam(t)
	base := time.Now().UTC().Add(-time.Hour)
	var recs []requestlog.Record
	for i := 0; i < 12; i++ {
		recs = append(recs, rec(base.Add(-time.Duration(i)*time.Minute), tokMember, "claude-opus-4-7", 1))
	}
	writeLog(t, logDir, recs)

	rows, truncated, _ := teamRequests(t, e, tokAdmin, "?limit=5")
	if len(rows) != 5 {
		t.Fatalf("limit=5 returned %d rows", len(rows))
	}
	if !truncated {
		t.Fatal("limit=5 over 12 rows reported truncated=false")
	}
	// Newest first, so the page is the head of the window and not an arbitrary
	// slice of it.
	if rows[0]["ts"].(float64) < rows[len(rows)-1]["ts"].(float64) {
		t.Fatalf("page is not newest-first: %v", rows)
	}
	rows, truncated, _ = teamRequests(t, e, tokAdmin, "?limit=50")
	if len(rows) != 12 || truncated {
		t.Fatalf("limit=50 → %d rows truncated=%v, want all 12 and no truncation", len(rows), truncated)
	}
	if w := do(e, "GET", "/api/team/requests?limit=0", tokAdmin, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("limit=0 → %d, want 400", w.Code)
	}
	if w := do(e, "GET", "/api/team/requests?limit=x", tokAdmin, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("limit=x → %d, want 400", w.Code)
	}
}

// The cap is asserted directly: producing 500+ rows through the HTTP fixture
// would prove the same thing at fifty times the cost, and the number itself is
// the memory bound (limit rows are held per member for the whole fan-out).
func TestTeamRequestsLimitIsCapped(t *testing.T) {
	get := func(q string) (int, bool) {
		gin.SetMode(gin.TestMode)
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("GET", "/api/team/requests"+q, nil)
		return teamRequestsLimit(c)
	}
	if n, ok := get(""); !ok || n != teamRequestsDefaultLimit {
		t.Fatalf("default limit = %d (ok=%v), want %d", n, ok, teamRequestsDefaultLimit)
	}
	if n, ok := get("?limit=100000"); !ok || n != teamRequestsMaxLimit {
		t.Fatalf("limit=100000 → %d (ok=%v), want the cap %d", n, ok, teamRequestsMaxLimit)
	}
}

// teamRequestsBody decodes the fields teamRequests drops, for the cases that
// are about what the endpoint *refused* to look at.
func teamRequestsBody(t *testing.T, e *gin.Engine, tok, query string) (rows []map[string]any, unmeasurable int) {
	t.Helper()
	w := do(e, "GET", "/api/team/requests"+query, tok, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("requests%s → %d (%s)", query, w.Code, w.Body.String())
	}
	var out struct {
		Requests            []map[string]any `json:"requests"`
		UnmeasurableMembers int              `json:"unmeasurable_members"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Requests, out.UnmeasurableMembers
}

const (
	tokShortA = "sk-shrt"
	tokShortB = "sk-tiny"
)

// newTwoTeamsShort is newTwoTeams with a short-token member in each workspace —
// the shape the Opaque mask actually appears in, since a custom token given at
// creation time is not length-checked.
func newTwoTeamsShort(t *testing.T) (*gin.Engine, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	d, err := db.Open(filepath.Join(t.TempDir(), "team.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	ctx := context.Background()
	for _, tc := range []struct{ name, admin, short string }{
		{"alpha", tokAdmin, tokShortA},
		{"beta", tokAdminB, tokShortB},
	} {
		ws, err := d.CreateWorkspace(ctx, tc.name)
		if err != nil {
			t.Fatal(err)
		}
		if err := d.AddMember(ctx, ws.ID, tc.admin, db.WSRoleAdmin, 0, 0); err != nil {
			t.Fatal(err)
		}
		if err := d.AddMember(ctx, ws.ID, tc.short, db.WSRoleMember, 0, 0); err != nil {
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

// tokenmask.Opaque is one string shared by every short token on the relay, so
// it identifies nobody and querying it reads whoever's rows happen to be short.
// Owning a short token is not owning that mask: without the opacity rule, both
// teams below "own" it and both would be handed the other's spend — under the
// team's own member label, on the column an invoice is reconciled against.
// /usage already reports such members as unmeasurable; the drill-down must not
// contradict it.
func TestTeamRequestsNeverQueriesTheOpaqueMask(t *testing.T) {
	e, logDir := newTwoTeamsShort(t)
	now := time.Now().UTC().Add(-time.Hour)
	// One row belonging to *some* short token — which one is unknowable, and
	// that is the whole problem.
	writeLog(t, logDir, []requestlog.Record{rec(now, tokShortA, "claude-opus-4-7", 99)})
	if tokenmask.Mask(tokShortA) != tokenmask.Opaque || tokenmask.Mask(tokShortB) != tokenmask.Opaque {
		t.Fatal("fixture tokens are long enough to mask distinguishably")
	}

	for _, tc := range []struct{ who, tok string }{{"alpha", tokAdmin}, {"beta", tokAdminB}} {
		for _, q := range []string{"", "?member=" + url.QueryEscape(tokenmask.Opaque)} {
			rows, unmeasurable := teamRequestsBody(t, e, tc.tok, q)
			if len(rows) != 0 {
				t.Fatalf("%s%s returned %d rows keyed on the opaque mask: %v", tc.who, q, len(rows), rows)
			}
			if unmeasurable != 1 {
				t.Fatalf("%s%s reported unmeasurable_members=%d, want the 1 member it skipped", tc.who, q, unmeasurable)
			}
		}
	}
}

// A full page is not proof of a fuller window: a member with exactly `limit`
// requests in it has been shown everything, and saying otherwise sends the user
// hunting for rows that don't exist — and quietly suggests the sum below
// disagrees with the total above, when they are equal.
func TestTeamRequestsExactPageIsNotTruncated(t *testing.T) {
	e, _, logDir := newUsageTeam(t)
	base := time.Now().UTC().Add(-time.Hour)
	var recs []requestlog.Record
	for i := 0; i < 4; i++ {
		recs = append(recs, rec(base.Add(-time.Duration(i)*time.Minute), tokMember, "claude-opus-4-7", 1))
	}
	writeLog(t, logDir, recs)

	mask := url.QueryEscape(tokenmask.Mask(tokMember))
	rows, truncated, _ := teamRequests(t, e, tokAdmin, "?member="+mask+"&limit=4")
	if len(rows) != 4 || truncated {
		t.Fatalf("limit=4 over exactly 4 rows → %d rows truncated=%v, want 4 and no truncation", len(rows), truncated)
	}
	// One fewer than the window holds still reports there is more behind it.
	rows, truncated, _ = teamRequests(t, e, tokAdmin, "?member="+mask+"&limit=3")
	if len(rows) != 3 || !truncated {
		t.Fatalf("limit=3 over 4 rows → %d rows truncated=%v, want 3 and truncation", len(rows), truncated)
	}
}

// Over the fan-out cap the members that get detailed are the ones the bill is
// about — the same highest-spend-first rule /usage's activeMasks applies.
// Roster order is join order, so the period's biggest spender is exactly who a
// join-order cap drops when a team has churned through fifty seats.
func TestTeamRequestsFanoutKeepsTheTopSpenders(t *testing.T) {
	logDir := t.TempDir()
	th := &TeamHandler{LogDir: logDir, LogIndexed: true}
	var (
		ms    []*db.WorkspaceMember
		recs  []requestlog.Record
		now   = time.Now().UTC().Add(-time.Hour)
		whale = ""
	)
	for i := 0; i < maxFanoutMembers+5; i++ {
		// Distinct in both the 6-byte prefix and the 4-byte suffix the mask is
		// cut from — tokens that mask alike would make this a test about
		// collisions instead of about ordering.
		tok := fmt.Sprintf("sk-f%02d-00000000000000000000%04d", i, i)
		ms = append(ms, &db.WorkspaceMember{Token: tok})
		// The last-joined member spends the most, so join order and spend order
		// disagree on exactly the member the cap decides about.
		usd := 1.0
		if i == maxFanoutMembers+4 {
			usd, whale = 500, tok
		}
		recs = append(recs, rec(now, tok, "claude-opus-4-7", usd))
	}
	writeLog(t, logDir, recs)

	sel, capped, opaque := th.resolveRosterMember(ms, "", "", "")
	if !capped || opaque != 0 || len(sel) != maxFanoutMembers {
		t.Fatalf("cap → %d members capped=%v opaque=%d, want %d/true/0", len(sel), capped, opaque, maxFanoutMembers)
	}
	if sel[0].Token != whale {
		t.Fatalf("fan-out led with %q, want the window's biggest spender %q", sel[0].Token, whale)
	}
}

// Without the index requestlog.Query scans the archive and PageOnly's early
// exit buys nothing for a member with no rows in the window, so the roster
// fan-out is up to fifty full re-decodes. /statement refuses outright in the
// same situation; here the single-member query is still one scan — the price
// /usage's fleet-wide pass already pays — so only the fan-out is refused.
func TestTeamRequestsRefusesTheFanoutWithoutAnIndex(t *testing.T) {
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
		DB: d, LogDir: logDir, // LogIndexed deliberately false
		Auth:        func(c *gin.Context) string { return c.GetHeader("X-Tok") },
		TokenExists: func(tok string) bool { return true },
	}
	e := gin.New()
	th.Routes(e.Group("/api/team"))
	writeLog(t, logDir, []requestlog.Record{rec(time.Now().UTC().Add(-time.Hour), tokMember, "claude-opus-4-7", 3)})

	if w := do(e, "GET", "/api/team/requests", tokAdmin, nil); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("un-indexed fan-out → %d, want 503", w.Code)
	}
	q := "?member=" + url.QueryEscape(tokenmask.Mask(tokMember))
	if rows, _, _ := teamRequests(t, e, tokAdmin, q); len(rows) != 1 {
		t.Fatalf("un-indexed single member → %d rows, want the 1 it costs one scan to fetch", len(rows))
	}
}

// The page is the globally newest rows, not a per-member slice: the merge trims
// to `limit` as each member lands (so the working set is a page rather than a
// page per member), and that is only equivalent to trimming at the end because
// every member is asked for the full limit. Interleaving two members' rows and
// asking for fewer than either holds is what tells the two apart.
func TestTeamRequestsMergesToTheGloballyNewest(t *testing.T) {
	e, _, logDir := newUsageTeam(t)
	base := time.Now().UTC().Add(-time.Hour)
	var recs []requestlog.Record
	for i := 0; i < 6; i++ {
		tok := tokMember
		if i%2 == 1 {
			tok = tokAdmin
		}
		recs = append(recs, rec(base.Add(-time.Duration(i)*time.Minute), tok, "claude-opus-4-7", float64(i+1)))
	}
	writeLog(t, logDir, recs)

	rows, truncated, _ := teamRequests(t, e, tokAdmin, "?limit=3")
	if len(rows) != 3 || !truncated {
		t.Fatalf("limit=3 over 6 interleaved rows → %d rows truncated=%v", len(rows), truncated)
	}
	// The three newest are the 1st, 2nd and 3rd minute back, one member's and
	// the other's alternating — a per-member trim would return only one of them.
	want := []float64{1, 2, 3}
	for i, r := range rows {
		if got := r["billed_usd"].(float64); !approx(got, want[i]) {
			t.Fatalf("row %d billed_usd = %v, want the globally %d-newest row's %v (%v)", i, got, i+1, want[i], rows)
		}
	}
}
