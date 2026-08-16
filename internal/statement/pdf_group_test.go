package statement

import (
	"strings"
	"testing"
	"time"

	"github.com/signintech/gopdf"
)

// drawnByGroup is drawnBy's group counterpart: render one section and return
// every string it put on the page. The embedded font is a subset with a private
// encoding, so the PDF bytes cannot be searched and this is the only way to
// assert a figure reached the paper.
func drawnByGroup(t *testing.T, g *GroupStatement, section func(*renderer)) []string {
	t.Helper()
	pdf := &gopdf.GoPdf{}
	pdf.Start(gopdf.Config{PageSize: gopdf.Rect{W: pageW, H: pageH}})
	if err := pdf.AddTTFFontData(fontFamily, fontSC); err != nil {
		t.Fatalf("load font: %v", err)
	}
	var got []string
	r := &renderer{pdf: pdf, g: g, drawn: &got}
	r.newPage()
	section(r)
	return got
}

func sampleGroup() *GroupStatement {
	g := &GroupStatement{
		WorkspaceID: 7, WorkspaceName: "行知实验室",
		AdminMasked: "sk-adm…aaaa", AdminLabel: "boss",
		FromDay: "2026-08-01", ToDay: "2026-08-15", TZName: "Asia/Shanghai",
		GeneratedAt: time.Date(2026, 8, 16, 9, 0, 0, 0, time.UTC),
		CNYPerUSD:   7.2, LifetimeDays: 90,
	}
	g.SetGroupTotals(30, 300,
		[]MemberRow{
			{Masked: "sk-bbb…bbbb", Label: "worker", Requests: 10, BilledCNY: 100},
			{Masked: "sk-aaa…aaaa", Label: "boss", Requests: 20, BilledCNY: 200},
			{Masked: "sk-ccc…cccc", Label: "idle"},
		},
		[]ModelRow{
			{Model: "claude-sonnet-5", Requests: 10, BilledCNY: 100},
			{Model: "claude-opus-4-7", Requests: 20, BilledCNY: 200},
		})
	g.Rollup()
	return g
}

// Both tables lead with what dominates the bill; a reader scanning a shared
// invoice starts at the top and stops when the numbers get small.
func TestGroupRollupSortsBySpend(t *testing.T) {
	g := sampleGroup()
	if g.ByMember[0].Masked != "sk-aaa…aaaa" || g.ByMember[0].BilledCNY != 200 {
		t.Fatalf("member table not sorted by spend: %+v", g.ByMember)
	}
	if g.ByMember[len(g.ByMember)-1].Requests != 0 {
		t.Fatalf("a member with no traffic must sort last, got %+v", g.ByMember)
	}
	if g.ByModel[0].Model != "claude-opus-4-7" {
		t.Fatalf("model table not sorted by spend: %+v", g.ByModel)
	}
}

// Every column layout must tile contentW exactly. A shortfall drifts the
// right-aligned amounts off the page edge, with no error anywhere.
func TestGroupColumnsTileContentWidth(t *testing.T) {
	r := &renderer{g: sampleGroup()}
	for name, cols := range map[string][]col{
		"member": r.memberCols(),
		"detail": r.groupDetailCols(),
		"model":  r.modelCols(),
	} {
		var sum float64
		for _, c := range cols {
			sum += c.w
		}
		if sum != contentW {
			t.Errorf("%s columns sum to %v, want contentW=%v", name, sum, contentW)
		}
	}
}

// The member table is this document's main table, so every member has to reach
// the page — including one who spent nothing, whose absence a reader would read
// as "not on the team" rather than "spent nothing".
func TestMemberTableListsEveryMemberWithShares(t *testing.T) {
	g := sampleGroup()
	drawn := strings.Join(drawnByGroup(t, g, (*renderer).memberTable), "\n")
	for _, want := range []string{
		"按成员汇总", "sk-aaa…aaaa", "sk-bbb…bbbb", "sk-ccc…cccc",
		"66.7%", "33.3%", "0.0%", "合计",
	} {
		if !strings.Contains(drawn, want) {
			t.Errorf("member table missing %q; drew:\n%s", want, drawn)
		}
	}
}

// An idle member's row must not be mistaken for a member the log cannot see.
// The two look identical on the amount column, so the distinction lives in the
// name column.
func TestMemberTableFlagsUnmeasurableMembers(t *testing.T) {
	g := sampleGroup()
	g.ByMember = append(g.ByMember, MemberRow{Masked: "***", Label: "tiny", Unmeasurable: true})
	drawn := strings.Join(drawnByGroup(t, g, (*renderer).memberTable), "\n")
	if !strings.Contains(drawn, "用量无法统计") {
		t.Errorf("an unmeasurable member must say so rather than read as ¥0; drew:\n%s", drawn)
	}
}

// A team whose log rows are gone but whose ledger still holds the debit is the
// case the reconciliation exists for, and it is also the case where Lines is
// empty. The empty-range hint must not stand in for the closing block.
func TestGroupEmptyRangeStillClosesOnTheLedgerFigure(t *testing.T) {
	g := &GroupStatement{
		WorkspaceName: "行知实验室", FromDay: "2026-08-01", ToDay: "2026-08-15",
		TZName: "Asia/Shanghai", CNYPerUSD: 7, GeneratedAt: time.Now(),
		UnitemisedCNY: 21, ChargedCNY: 21,
		// Itemised: the listing was asked for and came back empty, which is the
		// only reading under which "no billed requests" is a true statement.
		Itemised: true,
	}
	g.Rollup()
	drawn := strings.Join(drawnByGroup(t, g, (*renderer).groupDetailTable), "\n")
	if !strings.Contains(drawn, "该区间内没有计费请求") {
		t.Errorf("an empty range must still say so; drew:\n%s", drawn)
	}
	for _, want := range []string{"未能明细化的消费", "区间实际扣款", "¥21.00"} {
		if !strings.Contains(drawn, want) {
			t.Errorf("missing %q from a group whose only spend is unitemised; drew:\n%s", want, drawn)
		}
	}

	// And with nothing to reconcile, the hint still closes on a total rather
	// than trailing off.
	g.UnitemisedCNY, g.ChargedCNY = 0, 0
	drawn = strings.Join(drawnByGroup(t, g, (*renderer).groupDetailTable), "\n")
	if !strings.Contains(drawn, "合计") {
		t.Errorf("empty range without a gap must still print 合计; drew:\n%s", drawn)
	}
}

// A team that spent nothing has no proportions to report, and the share column
// must say that rather than dividing by zero — "NaN%" on a reimbursement
// attachment is a defect a reader cannot interpret.
func TestMemberSharesOfAZeroTotal(t *testing.T) {
	g := &GroupStatement{CNYPerUSD: 7, GeneratedAt: time.Now()}
	g.SetGroupTotals(0, 0, []MemberRow{
		{Masked: "sk-aaa…aaaa", Label: "boss"},
		{Masked: "sk-bbb…bbbb", Label: "worker"},
	}, nil)
	g.Rollup()
	drawn := strings.Join(drawnByGroup(t, g, (*renderer).memberTable), "\n")
	if !strings.Contains(drawn, "0.0%") || strings.Contains(drawn, "NaN") {
		t.Errorf("a zero-total team must show 0.0%% shares; drew:\n%s", drawn)
	}
}

// A wholly empty group — no members, no rows, no ledger — must still produce a
// document. This is a team created minutes ago, and a 500 on it reads as a bug
// in the export rather than an empty month.
func TestRenderGroupEmptyStatement(t *testing.T) {
	g := &GroupStatement{
		WorkspaceID: 3, FromDay: "2026-08-01", ToDay: "2026-08-01",
		TZName: "Asia/Shanghai", CNYPerUSD: 7.2, GeneratedAt: time.Now(),
	}
	g.Rollup()
	buf, err := RenderGroup(g)
	if err != nil {
		t.Fatalf("RenderGroup on an empty group: %v", err)
	}
	if len(buf) < 1000 {
		t.Fatalf("rendered %d bytes, that is not a PDF", len(buf))
	}
}

// The itemised listing's whole purpose on a shared bill is attribution: a
// column of amounts nobody can assign to a person is not a reimbursement
// record. The member has to be on the row, and it has to be the right one.
func TestGroupDetailRowsCarryTheirMember(t *testing.T) {
	g := sampleGroup()
	ts := time.Date(2026, 8, 3, 14, 30, 0, 0, time.UTC)
	g.Lines = []Line{
		{TS: ts, Model: "claude-opus-4-7", BilledCNY: 12, Member: "sk-aaa…aaaa"},
		{TS: ts.Add(time.Hour), Model: "claude-sonnet-5", BilledCNY: 3, Member: "sk-bbb…bbbb"},
	}
	drawn := drawnByGroup(t, g, (*renderer).groupDetailTable)
	joined := strings.Join(drawn, "\n")
	if !strings.Contains(joined, "成员") {
		t.Errorf("detail table has no 成员 column; drew:\n%s", joined)
	}
	// Attribution is positional: the mask must be drawn immediately before its
	// own timestamp, or the table pairs each amount with the wrong person.
	for _, want := range [][2]string{
		{"sk-aaa…aaaa", "08-03 14:30:00"},
		{"sk-bbb…bbbb", "08-03 15:30:00"},
	} {
		found := false
		for i := 0; i+1 < len(drawn); i++ {
			if drawn[i] == want[0] && drawn[i+1] == want[1] {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("row for %s not drawn beside its own timestamp %s; drew:\n%s", want[0], want[1], joined)
		}
	}
	// Token columns are deliberately absent — the member column took that width.
	for _, unwanted := range []string{"输入", "输出", "缓存读"} {
		if strings.Contains(joined, unwanted) {
			t.Errorf("group detail table must not print token columns, found %q", unwanted)
		}
	}
}

// The footer is where the document states its own scope. Both caveats are ones
// a reader would otherwise have to guess at, and guessing wrong turns the page
// into a claim it does not support.
func TestGroupFooterStatesRosterAndFundingScope(t *testing.T) {
	g := sampleGroup()
	drawn := strings.Join(drawnByGroup(t, g, (*renderer).groupFooterNote), "\n")
	for _, want := range []string{"成员名单以导出时为准", "个人余额", "不是增值税发票"} {
		if !strings.Contains(drawn, want) {
			t.Errorf("footer missing %q; drew:\n%s", want, drawn)
		}
	}
}

// A truncated listing must say so on its own heading, and the closing total
// must still be the range's — the rollups come from the cube and do not care
// what the listing showed.
func TestGroupTruncatedListingDisclosesItself(t *testing.T) {
	g := sampleGroup()
	g.LinesTruncated = true
	g.Lines = []Line{{TS: time.Now(), Model: "m", BilledCNY: 1, Member: "sk-aaa…aaaa"}}
	if title := g.renderer().groupDetailTitle(); !strings.Contains(title, "最近") {
		t.Errorf("truncated listing title = %q, want it to say 最近", title)
	}
	if got := g.renderer().groupTotalLines()[0]; !strings.Contains(got.label, "未列示") {
		t.Errorf("closing label = %q, want it to cover the unlisted part", got.label)
	}
	if got := g.renderer().groupTotalLines()[0].amount; got != "¥300.00" {
		t.Errorf("closing amount = %q, want the range total regardless of truncation", got)
	}
}

// renderer is a test-only shorthand for the pure label helpers, which need no
// PDF handle.
func (g *GroupStatement) renderer() *renderer { return &renderer{g: g} }

// A summary export carries a five-figure request count in its headline and no
// listing beneath it. Saying "no billed requests" there contradicts the same
// page, so the two empty states have to read differently.
func TestSummaryExportDoesNotClaimTheRangeIsEmpty(t *testing.T) {
	g := &GroupStatement{
		WorkspaceName: "行知实验室", FromDay: "2026-08-01", ToDay: "2026-08-15",
		TZName: "Asia/Shanghai", CNYPerUSD: 7, GeneratedAt: time.Now(),
		Requests: 82420, BilledCNY: 968.89,
	}
	g.Rollup()
	drawn := strings.Join(drawnByGroup(t, g, (*renderer).groupDetailTable), "\n")
	if strings.Contains(drawn, "没有计费请求") {
		t.Errorf("a summary export claimed the range was empty while reporting %d requests; drew:\n%s",
			g.Requests, drawn)
	}
	if !strings.Contains(drawn, "汇总版") {
		t.Errorf("a summary export must say the listing was omitted; drew:\n%s", drawn)
	}
}
