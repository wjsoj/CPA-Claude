package billing

import (
	"reflect"
	"testing"
	"time"

	"github.com/wjsoj/cc-core/requestlog"
)

// Every other test in this package drives requestlog's JSONL scanning path,
// which needs no SQLite and keeps the fixtures readable. Production does not:
// log_index_disabled defaults to false, so the panel and the statement are
// answered from agg_cube. The two are separate implementations — the cube
// materialises its day label at ingest while the scan derives it from TS at
// query time, and the two build the per-client bucket differently — so "same
// filter semantics" is an assumption this file exists to check rather than
// assert in a comment.
//
// cc-core has the same parity test one layer down (requestlog/dayquery_test.go);
// this one covers the shapes THIS package asks for.
func TestGroupUsageAgreesBetweenIndexAndScan(t *testing.T) {
	base := time.Now().UTC().Add(-48 * time.Hour)
	recs := []requestlog.Record{
		rec(base, tokAlice, "claude-opus-4-7", 3),
		rec(base.Add(time.Hour), tokAlice, "claude-sonnet-5", 1.25),
		legacyRec(base.Add(2*time.Hour), tokBob, "claude-opus-4-7", 2),
		rec(base.AddDate(0, 0, 1), tokBob, "claude-sonnet-5", 0.5),
		// An outsider, so the group scoping is exercised on both paths.
		rec(base.AddDate(0, 0, 1), "sk-outsider-000000000000000000dddd", "claude-opus-4-7", 40),
	}
	in := func(dir string) GroupUsageInput {
		return GroupUsageInput{
			LogDir: dir, LogIndexed: true,
			FromDay: day(base), ToDay: day(base.AddDate(0, 0, 1)),
			Members: []GroupMember{
				{Token: tokAlice}, {Token: tokBob}, {Token: tokIdle},
			},
			PoolByToken:   map[string]float64{tokAlice: 1},
			WantBreakdown: true,
		}
	}

	// Two directories, not one: the 30s query cache is keyed on the directory,
	// so reusing it would hand the second run the first run's answer and the
	// comparison would be with itself.
	scanDir, indexDir := newDir(t), newDir(t)
	writeLog(t, scanDir, recs)
	writeLog(t, indexDir, recs)

	st, err := requestlog.OpenStore(indexDir)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(st.Close)
	deadline := time.Now().Add(10 * time.Second)
	for !st.Ready() {
		if time.Now().After(deadline) {
			t.Fatal("request-log index never became ready")
		}
		time.Sleep(10 * time.Millisecond)
	}

	scanned, err := ComputeGroupUsage(in(scanDir))
	if err != nil {
		t.Fatal(err)
	}
	indexed, err := ComputeGroupUsage(in(indexDir))
	if err != nil {
		t.Fatal(err)
	}
	// LogDir differs by construction; everything that describes the numbers
	// must not.
	if !reflect.DeepEqual(scanned.Total, indexed.Total) {
		t.Fatalf("total:\n  scan  %+v\n  index %+v", scanned.Total, indexed.Total)
	}
	if !reflect.DeepEqual(scanned.ByMember, indexed.ByMember) {
		t.Fatalf("by_member:\n  scan  %+v\n  index %+v", scanned.ByMember, indexed.ByMember)
	}
	if !reflect.DeepEqual(scanned.ByModel, indexed.ByModel) {
		t.Fatalf("by_model:\n  scan  %+v\n  index %+v", scanned.ByModel, indexed.ByModel)
	}
	if !reflect.DeepEqual(scanned.ByDay, indexed.ByDay) {
		t.Fatalf("by_day:\n  scan  %+v\n  index %+v", scanned.ByDay, indexed.ByDay)
	}
	// And the numbers are the ones the fixture describes, so a parity between
	// two identically-broken paths would still fail here. The legacy row's 2 is
	// part of it: both paths must price it off cost_usd.
	if !approx(indexed.Total.BilledUSD, 6.75) || indexed.Total.Count != 4 {
		t.Fatalf("total = %+v, want 6.75 over 4 requests", indexed.Total)
	}
}
