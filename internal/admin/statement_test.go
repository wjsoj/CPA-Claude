package admin

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/wjsoj/cc-core/requestlog"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Skipf("timezone %s unavailable: %v", name, err)
	}
	return loc
}

// The window must be half-open at the start of the day AFTER `to`, or every
// statement silently drops the last day's requests — the single most likely
// way for an export to under-report and the hardest for a user to notice.
func TestStatementRangeIsInclusiveOfBothDays(t *testing.T) {
	loc := mustLoc(t, "Asia/Shanghai")
	fromDay, toDay, start, end, err := statementRange("2026-08-01", "2026-08-15", loc)
	if err != nil {
		t.Fatalf("statementRange: %v", err)
	}
	if fromDay != "2026-08-01" || toDay != "2026-08-15" {
		t.Errorf("labels = %q..%q, want 2026-08-01..2026-08-15", fromDay, toDay)
	}
	wantStart := time.Date(2026, 8, 1, 0, 0, 0, 0, loc)
	wantEnd := time.Date(2026, 8, 16, 0, 0, 0, 0, loc)
	if !start.Equal(wantStart) {
		t.Errorf("start = %s, want %s", start, wantStart)
	}
	if !end.Equal(wantEnd) {
		t.Errorf("end = %s, want %s", end, wantEnd)
	}

	// A request at the last instant of the final day is inside the range.
	last := time.Date(2026, 8, 15, 23, 59, 59, 0, loc)
	if last.Before(start) || !last.Before(end) {
		t.Error("23:59:59 on the closing day must fall inside the range")
	}
	// Midnight opening the day after is outside it.
	next := time.Date(2026, 8, 16, 0, 0, 0, 0, loc)
	if next.Before(end) {
		t.Error("midnight after the closing day must fall outside the range")
	}
}

// Day labels are resolved in the display zone, not UTC. Reading "2026-08-01"
// as UTC in a +08:00 deployment shifts the boundary eight hours and moves a
// whole evening of requests into the neighbouring statement.
func TestStatementRangeHonoursDisplayZone(t *testing.T) {
	sh := mustLoc(t, "Asia/Shanghai")
	_, _, shStart, _, err := statementRange("2026-08-01", "2026-08-01", sh)
	if err != nil {
		t.Fatalf("statementRange: %v", err)
	}
	_, _, utcStart, _, err := statementRange("2026-08-01", "2026-08-01", time.UTC)
	if err != nil {
		t.Fatalf("statementRange: %v", err)
	}
	if shStart.Equal(utcStart) {
		t.Fatal("the same label in two zones must not resolve to the same instant")
	}
	if diff := utcStart.Sub(shStart); diff != 8*time.Hour {
		t.Errorf("offset = %v, want 8h", diff)
	}
}

// Reversed input is a user picking the dates in the wrong order in a dialog,
// not an error worth refusing.
func TestStatementRangeSwapsReversedBounds(t *testing.T) {
	loc := mustLoc(t, "Asia/Shanghai")
	fromDay, toDay, start, end, err := statementRange("2026-08-15", "2026-08-01", loc)
	if err != nil {
		t.Fatalf("statementRange: %v", err)
	}
	if fromDay != "2026-08-01" || toDay != "2026-08-15" {
		t.Errorf("labels = %q..%q, want them swapped into order", fromDay, toDay)
	}
	if !start.Before(end) {
		t.Error("start must precede end after the swap")
	}
}

func TestStatementRangeDefaults(t *testing.T) {
	loc := mustLoc(t, "Asia/Shanghai")

	// No bounds at all: a trailing window ending today.
	fromDay, toDay, start, end, err := statementRange("", "", loc)
	if err != nil {
		t.Fatalf("statementRange: %v", err)
	}
	today := time.Now().In(loc).Format("2006-01-02")
	if toDay != today {
		t.Errorf("default to = %q, want today %q", toDay, today)
	}
	if days := int(end.Sub(start).Hours() / 24); days != statementDefaultDays {
		t.Errorf("default span = %d days, want %d", days, statementDefaultDays)
	}
	if fromDay >= toDay {
		t.Errorf("default from %q must precede to %q", fromDay, toDay)
	}

	// Only `to` given: the window still spans the default length, ending there.
	fromDay, toDay, _, _, err = statementRange("", "2026-08-15", loc)
	if err != nil {
		t.Fatalf("statementRange: %v", err)
	}
	if toDay != "2026-08-15" || fromDay != "2026-07-17" {
		t.Errorf("got %q..%q, want 2026-07-17..2026-08-15", fromDay, toDay)
	}
}

func TestStatementRangeRejectsBadInput(t *testing.T) {
	loc := time.UTC
	for _, tc := range []struct{ from, to, want string }{
		{"08/01/2026", "2026-08-15", "from"},
		{"2026-08-01", "not-a-date", "to"},
		{"2026-08-15", "2026-08-15", ""}, // single day is fine
	} {
		_, _, _, _, err := statementRange(tc.from, tc.to, loc)
		if tc.want == "" {
			if err != nil {
				t.Errorf("statementRange(%q,%q) unexpected error: %v", tc.from, tc.to, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("statementRange(%q,%q) error = %v, want it to name %q", tc.from, tc.to, err, tc.want)
		}
	}

	// A range wider than the cap is refused rather than silently clamped: the
	// user asked for a span, and a document quietly covering less than the one
	// requested is the failure mode this whole feature exists to avoid.
	if _, _, _, _, err := statementRange("2020-01-01", "2026-08-15", loc); err == nil {
		t.Error("an absurdly wide range must be rejected")
	} else if !strings.Contains(err.Error(), "too wide") {
		t.Errorf("error = %v, want it to say the range is too wide", err)
	}
}

// A single day must produce a 24h window, not an empty one.
func TestStatementRangeSingleDay(t *testing.T) {
	loc := mustLoc(t, "Asia/Shanghai")
	_, _, start, end, err := statementRange("2026-08-15", "2026-08-15", loc)
	if err != nil {
		t.Fatalf("statementRange: %v", err)
	}
	if got := end.Sub(start); got != 24*time.Hour {
		t.Errorf("single-day span = %v, want 24h", got)
	}
}

// statementRangeForTarget walks the newest-first slice forward (i.e. from
// newest to oldest, which is the order it already arrives in), skipping rows
// that belong to another token, until this token's own running total reaches
// the target — and stops there rather than overshooting further than needed.
func TestStatementRangeForTargetWalksNewestFirst(t *testing.T) {
	masked := maskToken(testToken)
	now := time.Now()
	// Newest-first, exactly as cachedQueryShared returns entries: three ¥7
	// rows for our token, interleaved with a foreign token's row that must
	// be skipped entirely.
	entries := []requestlog.Record{
		{TS: now.Add(-1 * time.Hour), ClientToken: masked, BilledUSD: 1,},
		{TS: now.Add(-2 * time.Hour), ClientToken: "sk-oth…9999", BilledUSD: 100,},
		{TS: now.Add(-3 * time.Hour), ClientToken: masked, BilledUSD: 1,},
		{TS: now.Add(-4 * time.Hour), ClientToken: masked, BilledUSD: 1,},
	}
	// ¥10 needs two of our ¥7 rows (7, then 14 >= 10) — the walk must stop at
	// the second matching row, not consume the third.
	start, end, achieved, err := statementRangeForTarget(entries, masked, 10, 7, now)
	if err != nil {
		t.Fatalf("statementRangeForTarget: %v", err)
	}
	if !start.Equal(entries[2].TS) {
		t.Errorf("start = %s, want the second matching row's TS %s", start, entries[2].TS)
	}
	if !end.Equal(now) {
		t.Errorf("end = %s, want now %s", end, now)
	}
	if achieved != 14 {
		t.Errorf("achieved = %v, want 14 (two ¥7 rows, not three)", achieved)
	}
}

// A target the entire supplied window can't reach, even summing every row,
// must be reported as unreachable rather than silently served short — and it
// must still report how much was actually there, so the caller can explain
// the shortfall.
func TestStatementRangeForTargetUnreachable(t *testing.T) {
	masked := maskToken(testToken)
	now := time.Now()
	entries := []requestlog.Record{
		{TS: now.Add(-time.Hour), ClientToken: masked, BilledUSD: 1,},
	}
	_, _, achieved, err := statementRangeForTarget(entries, masked, 100, 7, now)
	if !errors.Is(err, errStatementTargetUnreachable) {
		t.Fatalf("err = %v, want errStatementTargetUnreachable", err)
	}
	if achieved != 7 {
		t.Errorf("achieved = %v, want 7 — the whole window's total even though it fell short", achieved)
	}
}

// The scan and the statement's own accumulation loop must agree on which rows
// exist, or the printed total silently lands under the target the document is
// captioned with. buildStatement bounds rows at [start, end); a row at or after
// end can never be printed, so counting it here would choose a start that falls
// short. Rows dated ahead of the generating clock only take one host in a
// multi-writer deployment stepping its clock forward.
func TestStatementRangeForTargetIgnoresRowsAtOrAfterEnd(t *testing.T) {
	masked := "sk-e2e…0001"
	now := time.Date(2026, 8, 16, 0, 19, 0, 0, time.UTC)
	mk := func(ts time.Time) requestlog.Record {
		return requestlog.Record{
			TS: ts, ClientToken: masked, BilledUSD: 1, Status: 200,
		}
	}
	// Newest-first, as the query returns them: four rows dated later today than
	// the generating clock, then four genuinely past ones.
	entries := []requestlog.Record{
		mk(now.Add(18 * time.Hour)), mk(now.Add(15 * time.Hour)),
		mk(now.Add(12 * time.Hour)), mk(now.Add(9 * time.Hour)),
		mk(now.Add(-1 * time.Hour)), mk(now.Add(-2 * time.Hour)),
		mk(now.Add(-3 * time.Hour)), mk(now.Add(-4 * time.Hour)),
	}

	start, end, achieved, err := statementRangeForTarget(entries, masked, 25, 10, now)
	if err != nil {
		t.Fatalf("statementRangeForTarget: %v", err)
	}
	if achieved < 25 {
		t.Errorf("achieved = %v, want >= the ¥25 target", achieved)
	}
	// Only the past rows count, so ¥25 needs three of them, not one future row
	// plus two past ones.
	if want := now.Add(-3 * time.Hour); !start.Equal(want) {
		t.Errorf("start = %v, want %v — future-dated rows must not shift it", start, want)
	}

	// The invariant the caption depends on: everything buildStatement would
	// actually print sums to at least the target.
	var printed float64
	for _, rec := range entries {
		if rec.TS.Before(start) || !rec.TS.Before(end) {
			continue
		}
		printed += rec.BilledOrCost() * 10
	}
	if printed < 25 {
		t.Errorf("rows in [start, end) sum to %v, under the ¥25 target the document is captioned with", printed)
	}
}
