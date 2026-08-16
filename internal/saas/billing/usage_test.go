package billing

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wjsoj/CPA-Claude/internal/tokenmask"
	"github.com/wjsoj/cc-core/requestlog"
)

// writeLog lays down a requests-YYYY-MM-DD.jsonl archive by hand. No index is
// opened, so requestlog.Query takes its scanning path — which shares the filter
// semantics with the SQL path and needs no SQLite here.
func writeLog(t *testing.T, dir string, recs []requestlog.Record) {
	t.Helper()
	byFile := map[string][]requestlog.Record{}
	for _, r := range recs {
		name := "requests-" + r.TS.UTC().Format("2006-01-02") + ".jsonl"
		byFile[name] = append(byFile[name], r)
	}
	for name, rs := range byFile {
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		enc := json.NewEncoder(f)
		for _, r := range rs {
			if err := enc.Encode(r); err != nil {
				t.Fatal(err)
			}
		}
		f.Close()
	}
}

func rec(ts time.Time, token, model string, billed float64) requestlog.Record {
	return requestlog.Record{
		TS: ts,
		// The write side masks; that is the only client identity the log keeps.
		ClientToken: tokenmask.Mask(token),
		Client:      "name-that-should-not-be-the-key",
		Provider:    "anthropic",
		Model:       model,
		Input:       100,
		Output:      20,
		CostUSD:     billed,
		BilledUSD:   billed,
		Status:      200,
	}
}

// legacyRec is a row written before cc-core v0.8.61 split cost from billed:
// the charge lives in cost_usd alone and billed_usd is absent. Retention is 90
// days, so both conventions are live in production at once and every money
// figure here has to read them through Record.BilledOrCost.
func legacyRec(ts time.Time, token, model string, cost float64) requestlog.Record {
	r := rec(ts, token, model, cost)
	r.BilledUSD = 0
	return r
}

// day returns a day label for a timestamp in the log's display zone.
func day(ts time.Time) string {
	return ts.In(requestlog.BucketLocation()).Format(DayLayout)
}

func newDir(t *testing.T) string {
	t.Helper()
	// A fresh directory per test also gives a fresh cache key, so the shared
	// 30s query cache can't leak one test's numbers into another's.
	return t.TempDir()
}

const (
	tokAlice = "sk-alice-0000000000000000000000aaaa"
	tokBob   = "sk-bobbb-0000000000000000000000bbbb"
	tokIdle  = "sk-idle0-0000000000000000000000cccc"
)

// TestGroupUsageIgnoresPoolLedger is mode B: nobody funded the shared pool, so
// every request was paid from a personal wallet and workspace_tx is empty. The
// old member view read only workspace_tx and therefore showed zero for
// everyone; usage must show the real spend regardless.
func TestGroupUsageIgnoresPoolLedger(t *testing.T) {
	dir := newDir(t)
	now := time.Now().UTC().Add(-24 * time.Hour)
	writeLog(t, dir, []requestlog.Record{
		rec(now, tokAlice, "claude-opus-4-7", 3),
		rec(now, tokAlice, "claude-opus-4-7", 1.5),
		rec(now, tokBob, "claude-sonnet-5", 2),
	})
	gu, err := ComputeGroupUsage(GroupUsageInput{
		LogDir:     dir,
		LogIndexed: true,
		FromDay:    day(now),
		ToDay:      day(now),
		Members: []GroupMember{
			{Token: tokAlice, Label: "alice"},
			{Token: tokBob, Label: "bob"},
			{Token: tokIdle, Label: "idle"},
		},
		PoolByToken:   nil, // no pool: mode B
		WantBreakdown: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if gu.PoolBilledUSD != 0 {
		t.Fatalf("pool spend = %v, want 0", gu.PoolBilledUSD)
	}
	if got := gu.Total.BilledUSD; !approx(got, 6.5) {
		t.Fatalf("total billed = %v, want 6.5", got)
	}
	if gu.Total.Count != 3 {
		t.Fatalf("total requests = %d, want 3", gu.Total.Count)
	}
	byMask := map[string]MemberUsage{}
	for _, m := range gu.ByMember {
		byMask[m.Masked] = m
	}
	if got := byMask[tokenmask.Mask(tokAlice)].Agg.BilledUSD; !approx(got, 4.5) {
		t.Fatalf("alice = %v, want 4.5", got)
	}
	if got := byMask[tokenmask.Mask(tokBob)].Agg.BilledUSD; !approx(got, 2) {
		t.Fatalf("bob = %v, want 2", got)
	}
	// The idle member must still be listed — "this one really spent nothing"
	// is the signal that got lost when the whole table read zero.
	idle, ok := byMask[tokenmask.Mask(tokIdle)]
	if !ok {
		t.Fatal("idle member missing from by_member")
	}
	if idle.Agg.Count != 0 {
		t.Fatalf("idle member has %d requests", idle.Agg.Count)
	}
	if gu.Partial {
		t.Fatalf("unexpected partial: %v", gu.Notes)
	}
}

// TestGroupUsageIsScopedToMembers guards the reason ByClient is read instead of
// the query's fleet-wide Summary: another team's traffic sits in the same log.
func TestGroupUsageIsScopedToMembers(t *testing.T) {
	dir := newDir(t)
	now := time.Now().UTC().Add(-24 * time.Hour)
	writeLog(t, dir, []requestlog.Record{
		rec(now, tokAlice, "claude-opus-4-7", 4),
		rec(now, "sk-outsider-000000000000000000dddd", "claude-opus-4-7", 100),
	})
	gu, err := ComputeGroupUsage(GroupUsageInput{
		LogDir: dir, LogIndexed: true, FromDay: day(now), ToDay: day(now),
		Members:       []GroupMember{{Token: tokAlice}},
		WantBreakdown: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := gu.Total.BilledUSD; !approx(got, 4) {
		t.Fatalf("total = %v, want 4 (outsider traffic leaked in?)", got)
	}
	for _, m := range gu.ByModel {
		if !approx(m.Agg.BilledUSD, 4) {
			t.Fatalf("by_model %s = %v, want 4", m.Model, m.Agg.BilledUSD)
		}
	}
}

// TestGroupUsageBreakdownsReconcile is the self-consistency the panel and any
// invoice attachment depend on: the three views are three cuts of one number.
func TestGroupUsageBreakdownsReconcile(t *testing.T) {
	dir := newDir(t)
	base := time.Now().UTC().Add(-72 * time.Hour)
	var recs []requestlog.Record
	for i := 0; i < 3; i++ {
		ts := base.AddDate(0, 0, i)
		recs = append(recs,
			rec(ts, tokAlice, "claude-opus-4-7", 1+float64(i)),
			rec(ts, tokBob, "claude-sonnet-5", 0.5),
		)
	}
	writeLog(t, dir, recs)

	gu, err := ComputeGroupUsage(GroupUsageInput{
		LogDir: dir, LogIndexed: true, FromDay: day(base), ToDay: day(base.AddDate(0, 0, 2)),
		// Deliberately handed in ascending spend order, so the descending
		// assertion below can only pass because sortMemberUsage ran.
		Members: []GroupMember{
			{Token: tokIdle}, {Token: tokBob}, {Token: tokAlice},
		},
		PoolByToken:   map[string]float64{tokAlice: 2},
		WantBreakdown: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	const want = 1 + 2 + 3 + 0.5*3
	if !approx(gu.Total.BilledUSD, want) {
		t.Fatalf("total = %v, want %v", gu.Total.BilledUSD, want)
	}
	sum := func(label string, get func() float64) {
		t.Helper()
		if got := get(); !approx(got, want) {
			t.Fatalf("%s sums to %v, want %v", label, got, want)
		}
	}
	sum("by_member", func() (s float64) {
		for _, m := range gu.ByMember {
			s += m.Agg.BilledUSD
		}
		return
	})
	sum("by_model", func() (s float64) {
		for _, m := range gu.ByModel {
			s += m.Agg.BilledUSD
		}
		return
	})
	sum("by_day", func() (s float64) {
		for _, d := range gu.ByDay {
			s += d.Agg.BilledUSD
		}
		return
	})
	if len(gu.ByDay) != 3 {
		t.Fatalf("by_day has %d entries, want 3 zero-filled days", len(gu.ByDay))
	}
	for i := 1; i < len(gu.ByDay); i++ {
		if gu.ByDay[i-1].Day >= gu.ByDay[i].Day {
			t.Fatalf("by_day not ascending: %v", gu.ByDay)
		}
	}
	// by_member is billed-desc so the panel doesn't have to sort. Asserted as
	// the exact sequence: a first-vs-last comparison passes on any fixture whose
	// input order already happens to satisfy it, which is how this stopped
	// testing anything the first time round.
	wantOrder := []string{
		tokenmask.Mask(tokAlice), tokenmask.Mask(tokBob), tokenmask.Mask(tokIdle),
	}
	for i, want := range wantOrder {
		if gu.ByMember[i].Masked != want {
			t.Fatalf("by_member[%d] = %q, want %q (full order: %v)", i, gu.ByMember[i].Masked, want, masks(gu.ByMember))
		}
	}
	// The pool figure rides alongside; it never changes the real total.
	if !approx(gu.PoolBilledUSD, 2) {
		t.Fatalf("pool = %v, want 2", gu.PoolBilledUSD)
	}
}

// TestGroupUsageZeroFillsEmptyDays keeps hole-filling on the server so two
// clients can't draw different charts from the same data.
func TestGroupUsageZeroFillsEmptyDays(t *testing.T) {
	dir := newDir(t)
	base := time.Now().UTC().Add(-96 * time.Hour)
	writeLog(t, dir, []requestlog.Record{rec(base, tokAlice, "claude-opus-4-7", 1)})
	gu, err := ComputeGroupUsage(GroupUsageInput{
		LogDir: dir, LogIndexed: true, FromDay: day(base), ToDay: day(base.AddDate(0, 0, 3)),
		Members: []GroupMember{{Token: tokAlice}}, WantBreakdown: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(gu.ByDay) != 4 {
		t.Fatalf("by_day = %d days, want 4", len(gu.ByDay))
	}
	nonZero := 0
	for _, d := range gu.ByDay {
		if d.Agg.Count > 0 {
			nonZero++
		}
	}
	if nonZero != 1 {
		t.Fatalf("expected exactly one day with traffic, got %d", nonZero)
	}
}

// TestGroupUsageUnmeasurableMember covers the operator-assigned short token:
// every such token masks to the same opaque string, so reporting a number would
// mean reporting someone else's spend.
func TestGroupUsageUnmeasurableMember(t *testing.T) {
	dir := newDir(t)
	now := time.Now().UTC().Add(-24 * time.Hour)
	writeLog(t, dir, []requestlog.Record{
		rec(now, "short", "claude-opus-4-7", 9),
		rec(now, tokAlice, "claude-opus-4-7", 1),
	})
	gu, err := ComputeGroupUsage(GroupUsageInput{
		LogDir: dir, LogIndexed: true, FromDay: day(now), ToDay: day(now),
		Members: []GroupMember{{Token: "short"}, {Token: tokAlice}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !gu.Partial || len(gu.Notes) == 0 {
		t.Fatal("unmeasurable member should mark the result partial")
	}
	if !approx(gu.Total.BilledUSD, 1) {
		t.Fatalf("total = %v, want 1 (opaque bucket must not be attributed)", gu.Total.BilledUSD)
	}
	for _, m := range gu.ByMember {
		if m.Masked == tokenmask.Opaque && !m.Unmeasurable {
			t.Fatal("opaque member not flagged")
		}
	}
}

// TestUsageWindowRejectsTimestamps is the guard on the one query rule that
// costs two orders of magnitude when broken: the window is day labels only.
func TestUsageWindowRejectsTimestamps(t *testing.T) {
	for _, bad := range []string{
		"2026-08-16T00:00:00Z",
		"1786887076",
		"2026/08/16",
		"16-08-2026",
	} {
		if _, err := ParseDay(bad); err == nil {
			t.Fatalf("ParseDay(%q) accepted a non-day-label", bad)
		}
	}
	if _, err := ParseDay("2026-08-16"); err != nil {
		t.Fatalf("ParseDay rejected a valid label: %v", err)
	}
}

func TestUsageWindowBounds(t *testing.T) {
	if _, _, err := normalizeWindow("2026-08-16", "2026-08-01"); err == nil {
		t.Fatal("reversed window accepted")
	}
	if _, _, err := normalizeWindow("2026-01-01", "2026-08-16"); err == nil {
		t.Fatal("over-wide window accepted")
	}
	from, to, err := normalizeWindow("", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := countDays(from, to); got != 30 {
		t.Fatalf("default window = %d days, want 30", got)
	}
}

// TestMemberSpendToDateSplitsDayFromMonth checks the member-list figures: the
// current day and the month to date are two separate windows, because only the
// per-client bucket of a query is group-scoped (ByDay is fleet-wide, so the day
// cannot be sliced out of the month's result).
func TestMemberSpendToDateSplitsDayFromMonth(t *testing.T) {
	loc := requestlog.BucketLocation()
	now := time.Now().In(loc)
	if now.Day() == 1 {
		t.Skip("month-to-date and today coincide on the 1st")
	}
	dir := newDir(t)
	earlier := time.Date(now.Year(), now.Month(), 1, 12, 0, 0, 0, loc)
	// Midday today, not now-1h: MemberSpendToDate cuts "today" off the real
	// clock, so a relative offset walks the record into yesterday for the hour
	// after local midnight and the test fails once a day.
	today := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, loc)
	if today.After(now) {
		today = now
	}
	writeLog(t, dir, []requestlog.Record{
		rec(today, tokAlice, "claude-opus-4-7", 2),
		rec(earlier, tokAlice, "claude-opus-4-7", 5),
	})
	spend, ok := MemberSpendToDate(dir, []GroupMember{{Token: tokAlice}, {Token: "short"}})
	if !ok {
		t.Fatal("MemberSpendToDate reported the log unreadable")
	}
	a := spend[tokenmask.Mask(tokAlice)]
	if !a.Measurable {
		t.Fatal("alice should be measurable")
	}
	if !approx(a.Day.BilledUSD, 2) {
		t.Fatalf("today = %v, want 2", a.Day.BilledUSD)
	}
	if !approx(a.Month.BilledUSD, 7) {
		t.Fatalf("month-to-date = %v, want 7", a.Month.BilledUSD)
	}
	if spend[tokenmask.Opaque].Measurable {
		t.Fatal("short token reported as measurable")
	}
}

// TestMemberSpendToDateWithoutLog: no request log configured means "we don't
// know", which callers must be able to tell apart from "they spent nothing".
func TestMemberSpendToDateWithoutLog(t *testing.T) {
	spend, ok := MemberSpendToDate("", []GroupMember{{Token: tokAlice}})
	if ok {
		t.Fatal("expected ok=false without a log dir")
	}
	if len(spend) != 1 {
		t.Fatalf("expected the member to still be listed, got %v", spend)
	}
}

func approx(a, b float64) bool {
	d := a - b
	return d < 1e-9 && d > -1e-9
}

func masks(ms []MemberUsage) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.Masked)
	}
	return out
}

// TestUsageFilterStatesTheWindowInDays pins the one query rule this package
// cannot afford to break and cannot notice breaking: the window must reach
// requestlog as FromDay/ToDay labels, never as a From/To instant pair. Only the
// labels are answerable from the pre-summed cube — a timestamp pair returns the
// same numbers off a row-by-row scan, which is why no assertion about amounts
// can catch it (measured ~3.4s vs ~30ms on a million-row archive).
//
// Modelled on internal/admin/datebounds_test.go, which pins the same rule one
// layer up.
func TestUsageFilterStatesTheWindowInDays(t *testing.T) {
	f := usageFilter("/logs", "2026-08-01", "2026-08-16", "sk-abc…wxyz")
	if f.FromDay != "2026-08-01" || f.ToDay != "2026-08-16" {
		t.Fatalf("day labels not passed through: %+v", f)
	}
	if !f.From.IsZero() || !f.To.IsZero() {
		t.Fatalf("filter carries timestamp bounds (%v..%v) — that forfeits the cube", f.From, f.To)
	}
	if f.ClientToken != "sk-abc…wxyz" {
		t.Fatalf("client token = %q", f.ClientToken)
	}
	if f.PageOnly {
		t.Fatal("PageOnly zeroes exactly the aggregates this package reads")
	}
	if f.Limit != 1 {
		t.Fatalf("limit = %d, want 1 (aggregates only)", f.Limit)
	}
}

// A member whose token is too short to mask is invisible in the log, but their
// pool draw is perfectly visible in the ledger. Counting the second without the
// first made the group's pool exceed its own total spend, and the derived
// "personal wallet" half — total minus pool — went negative and clamped to zero,
// erasing real money on the one screen built to stop the panel lying.
func TestGroupUsagePoolNeverExceedsMeasuredTotal(t *testing.T) {
	dir := newDir(t)
	now := time.Now().UTC().Add(-24 * time.Hour)
	writeLog(t, dir, []requestlog.Record{rec(now, tokAlice, "claude-opus-4-7", 10)})

	gu, err := ComputeGroupUsage(GroupUsageInput{
		LogDir: dir, LogIndexed: true, FromDay: day(now), ToDay: day(now),
		Members: []GroupMember{{Token: tokAlice}, {Token: "short"}},
		PoolByToken: map[string]float64{
			tokAlice: 4,
			"short":  20, // a real debit against a member nobody can measure
		},
		PersonalByToken: map[string]float64{"short": 7},
		WantBreakdown:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if gu.PoolBilledUSD > gu.Total.BilledUSD {
		t.Fatalf("pool %v exceeds measured total %v", gu.PoolBilledUSD, gu.Total.BilledUSD)
	}
	if !approx(gu.PoolBilledUSD, 4) {
		t.Fatalf("group pool = %v, want only the measurable member's 4", gu.PoolBilledUSD)
	}
	body := GroupUsageJSON(gu)
	if got := body["personal_billed_usd"].(float64); !approx(got, 6) {
		t.Fatalf("personal = %v, want 10-4=6 (clamped to zero by an inflated pool?)", got)
	}
	if !gu.Partial || len(gu.Notes) == 0 {
		t.Fatal("a member excluded from the totals must be reported as partial")
	}
	// The excluded figures survive on the member's own row, so an operator can
	// still reconcile them by hand.
	var found bool
	for _, m := range gu.ByMember {
		if m.Masked != tokenmask.Opaque {
			continue
		}
		found = true
		if !approx(m.PoolBilledUSD, 20) || !approx(m.PersonalLedgerUSD, 7) {
			t.Fatalf("unmeasurable row lost its ledger figures: %+v", m)
		}
	}
	if !found {
		t.Fatal("the unmeasurable member vanished from by_member")
	}
}

// The two books can disagree even with every member measurable — a charge
// settled either side of a range edge, or a log line lost. Say so rather than
// print a pool bigger than the total it is part of and a silent ¥0 beside it.
func TestGroupUsageFlagsPoolExceedingLog(t *testing.T) {
	dir := newDir(t)
	now := time.Now().UTC().Add(-24 * time.Hour)
	writeLog(t, dir, []requestlog.Record{rec(now, tokAlice, "claude-opus-4-7", 1)})
	gu, err := ComputeGroupUsage(GroupUsageInput{
		LogDir: dir, LogIndexed: true, FromDay: day(now), ToDay: day(now),
		Members:     []GroupMember{{Token: tokAlice}},
		PoolByToken: map[string]float64{tokAlice: 5},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !gu.Partial {
		t.Fatalf("pool %v > total %v went unreported", gu.PoolBilledUSD, gu.Total.BilledUSD)
	}
}

// Without the SQL index every per-member query is a full pass over the JSONL
// archive (~7s for a 90-day window in production), and the fan-out has no early
// exit. The headline totals cost one query either way, so they stay; only the
// cross-tabs are given up, and the caller is told.
func TestGroupUsageSkipsFanoutWithoutIndex(t *testing.T) {
	dir := newDir(t)
	now := time.Now().UTC().Add(-24 * time.Hour)
	writeLog(t, dir, []requestlog.Record{
		rec(now, tokAlice, "claude-opus-4-7", 3),
		rec(now, tokBob, "claude-sonnet-5", 2),
	})
	in := GroupUsageInput{
		LogDir: dir, FromDay: day(now), ToDay: day(now),
		Members:       []GroupMember{{Token: tokAlice}, {Token: tokBob}},
		WantBreakdown: true,
	}
	gu, err := ComputeGroupUsage(in) // LogIndexed false
	if err != nil {
		t.Fatal(err)
	}
	if !approx(gu.Total.BilledUSD, 5) || gu.Total.Count != 2 {
		t.Fatalf("totals must survive the degradation: %+v", gu.Total)
	}
	if len(gu.ByModel) != 0 {
		t.Fatalf("by_model was computed without an index: %+v", gu.ByModel)
	}
	if len(gu.ByDay) != 1 {
		t.Fatalf("by_day should still be zero-filled for the range, got %+v", gu.ByDay)
	}
	if !gu.Partial || len(gu.Notes) == 0 {
		t.Fatal("dropping the breakdowns must be reported, not silent")
	}

	in.LogIndexed = true
	gu, err = ComputeGroupUsage(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(gu.ByModel) != 2 || gu.Partial {
		t.Fatalf("indexed run should produce the full breakdown: %+v %v", gu.ByModel, gu.Notes)
	}
}

// The fan-out is linear in roster size and nothing caps roster size, so the cap
// lives here — the same bound /api/team/requests has always had. Members are
// taken highest-spend-first and the remainder is reported, never dropped in
// silence.
func TestGroupUsageFanoutIsCappedByMemberCount(t *testing.T) {
	dir := newDir(t)
	now := time.Now().UTC().Add(-24 * time.Hour)
	n := maxFanoutMembers + 3
	recs := make([]requestlog.Record, 0, n)
	members := make([]GroupMember, 0, n)
	for i := 0; i < n; i++ {
		tok := fmt.Sprintf("sk-m%03d-000000000000000000000%03d", i, i)
		members = append(members, GroupMember{Token: tok})
		// Descending spend, so the cap has an unambiguous "top N".
		recs = append(recs, rec(now, tok, "claude-opus-4-7", float64(n-i)))
	}
	writeLog(t, dir, recs)

	gu, err := ComputeGroupUsage(GroupUsageInput{
		LogDir: dir, LogIndexed: true, FromDay: day(now), ToDay: day(now),
		Members: members, WantBreakdown: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Totals are the whole roster's — they came from the single fleet-wide pass,
	// which is exactly why truncating the detail is safe.
	if gu.Total.Count != int64(n) {
		t.Fatalf("total requests = %d, want the whole roster's %d", gu.Total.Count, n)
	}
	if len(gu.ByMember) != n {
		t.Fatalf("by_member dropped members: %d of %d", len(gu.ByMember), n)
	}
	// The breakdown covers only the capped set: n - maxFanoutMembers members'
	// worth of spend is missing from it, by design.
	var byDay float64
	for _, d := range gu.ByDay {
		byDay += d.Agg.BilledUSD
	}
	if byDay >= gu.Total.BilledUSD {
		t.Fatalf("breakdown covers %v of a %v total — the cap did not bite", byDay, gu.Total.BilledUSD)
	}
	if !gu.Partial || len(gu.Notes) == 0 {
		t.Fatal("a truncated breakdown must say so")
	}
}

// The cache used to drop every entry when it filled. A fan-out inserts one key
// per member at once, so the last member of a big team routinely triggered that
// — throwing away the fleet-wide entry every single group query starts from,
// precisely when concurrency made it worth having.
func TestUsageCacheEvictsByAgeNotWholesale(t *testing.T) {
	usageQueryCache.mu.Lock()
	defer usageQueryCache.mu.Unlock()
	saved := usageQueryCache.m
	defer func() { usageQueryCache.m = saved }()

	usageQueryCache.m = map[string]usageCacheEntry{}
	fresh := &usageResult{}
	usageQueryCache.m["fleet"] = usageCacheEntry{res: fresh, at: time.Now()}
	for i := 0; i < usageCacheMaxEntries; i++ {
		usageQueryCache.m[fmt.Sprintf("member-%d", i)] = usageCacheEntry{
			res: &usageResult{}, at: time.Now().Add(-2 * usageCacheTTL),
		}
	}
	evictExpiredUsageEntries()

	if e, ok := usageQueryCache.m["fleet"]; !ok || e.res != fresh {
		t.Fatal("eviction threw away the live fleet-wide entry")
	}
	if len(usageQueryCache.m) != 1 {
		t.Fatalf("%d entries left, want only the fresh one", len(usageQueryCache.m))
	}
}
