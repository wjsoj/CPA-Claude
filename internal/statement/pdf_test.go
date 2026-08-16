package statement

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/signintech/gopdf"
)

func sample(lines int, rate float64) *Statement {
	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	s := &Statement{
		TokenMasked:       "sk-ant…9f2c",
		TokenName:         "物理实验室·科研组",
		Group:             "教育优惠",
		FromDay:           "2026-08-01",
		ToDay:             "2026-08-15",
		TZName:            "Asia/Shanghai",
		CNYPerUSD:         rate,
		GeneratedAt:       base,
		LifetimeRequests:  91234,
		LifetimeBilledCNY: 2963.48,
	}
	models := []string{"claude-opus-4-7", "gpt-5.6-sol", "claude-sonnet-5"}
	for i := range lines {
		ln := Line{
			TS:        base.Add(time.Duration(i) * time.Minute),
			Provider:  "anthropic",
			Model:     models[i%len(models)],
			Input:     int64(1200 + i*7),
			Output:    int64(340 + i*3),
			CacheRead: int64(i * 11),
			BilledCNY: 0.0884 + float64(i)*0.0007,
			Status:    200,
		}
		s.Lines = append(s.Lines, ln)
		s.Observe(ln.Model, ln.BilledCNY)
	}
	s.Rollup()
	return s
}

// tokenlessSample is the historical shape: rows written before token counts
// were recorded at all, so every count is a structural zero rather than a
// request that genuinely used nothing.
func tokenlessSample(lines int) *Statement {
	s := sample(lines, 7.18)
	for i := range s.Lines {
		s.Lines[i].Input, s.Lines[i].Output, s.Lines[i].CacheRead = 0, 0, 0
	}
	s.Rollup()
	return s
}

// A rendered statement must be a real PDF and must actually carry the embedded
// subset, or the CJK on the page silently renders as nothing.
func TestRenderProducesPDF(t *testing.T) {
	buf, err := Render(sample(40, 7.18))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !bytes.HasPrefix(buf, []byte("%PDF-")) {
		t.Fatalf("output is not a PDF: %q", buf[:min(16, len(buf))])
	}
	if !bytes.Contains(buf, []byte("%%EOF")) {
		t.Error("PDF is missing its trailer")
	}
	if !bytes.Contains(buf, []byte("FontFile2")) {
		t.Error("no TrueType font embedded — CJK would render blank")
	}
	if len(buf) < 20_000 {
		t.Errorf("PDF suspiciously small (%d bytes); font subset likely missing", len(buf))
	}
}

// The page count printed in the footer has to be the real one. This is the
// whole reason Render does two passes, so a regression that drops the second
// pass must fail here rather than ship "共 0 页".
func TestPageCountIsResolved(t *testing.T) {
	// Enough rows to guarantee several pages.
	_, pages, err := render(sample(300, 7.18), 0)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if pages < 2 {
		t.Fatalf("expected a multi-page statement, got %d", pages)
	}
	final, again, err := render(sample(300, 7.18), pages)
	if err != nil {
		t.Fatalf("render pass 2: %v", err)
	}
	if again != pages {
		t.Errorf("page count changed between passes: %d then %d", pages, again)
	}
	if len(final) == 0 {
		t.Error("empty output")
	}
}

// Every yuan figure on the page is one multiplication by a rate that is not
// recorded anywhere in the request log, so the same range exported after the
// market moves totals differently. The document is only self-consistent if it
// states which rate produced these numbers.
func TestExportRateIsPrintedOnTheDocument(t *testing.T) {
	s := sample(5, 7.1842)
	r := &renderer{s: s}

	var rateRow string
	for _, kv := range r.identityRows() {
		if kv[0] == "换算汇率" {
			rateRow = kv[1]
		}
	}
	if rateRow == "" {
		t.Fatalf("identity rows = %+v, missing the 换算汇率 row", r.identityRows())
	}
	if !strings.Contains(rateRow, "7.1842") {
		t.Errorf("rate row = %q, want it to state the 7.1842 rate actually used", rateRow)
	}

	// And the standing note that explains why two exports can disagree.
	buf, err := Render(s)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if len(buf) == 0 {
		t.Fatal("empty output")
	}

	// With no rate supplied there is nothing truthful to print, and inventing
	// "1 USD = 0.0000 CNY" would be worse than staying silent.
	for _, kv := range (&renderer{s: sample(5, 0)}).identityRows() {
		if kv[0] == "换算汇率" {
			t.Errorf("a statement with no rate must not print one, got %q", kv[1])
		}
	}
}

// The detail table is yuan-only: no USD column, no rate column. And it never
// carries a cache-write column — OpenAI's usage block has no such field, so it
// could only ever print zero.
func TestDetailTableIsCNYOnly(t *testing.T) {
	for _, s := range []*Statement{sample(3, 7.18), tokenlessSample(3)} {
		r := &renderer{s: s}
		cols := r.detailCols()
		var total float64
		for _, c := range cols {
			total += c.w
			if strings.Contains(c.title, "USD") || strings.Contains(c.title, "$") {
				t.Errorf("column %q still names USD", c.title)
			}
			if strings.Contains(c.title, "缓存写") {
				t.Errorf("column %q is back — the upstream API never reports it", c.title)
			}
		}
		// Columns must tile the content box exactly, or the right-aligned
		// amounts drift off the page edge.
		if d := total - contentW; d > 0.01 || d < -0.01 {
			t.Errorf("columns (%d of them) sum to %v, want contentW %v", len(cols), total, contentW)
		}
	}
	for _, c := range (&renderer{s: sample(3, 7.18)}).modelCols() {
		if strings.Contains(c.title, "USD") {
			t.Errorf("model column %q still names USD", c.title)
		}
	}
}

// Ranges older than token-count capture have no counts to print — the numbers
// never existed upstream. Printing three columns of zeroes would assert those
// requests consumed nothing, so the columns are dropped instead; and they must
// come back the moment any row has a count.
func TestTokenColumnsAppearOnlyWhenThereIsData(t *testing.T) {
	titles := func(s *Statement) string {
		return strings.Join(colTitles((&renderer{s: s}).detailCols()), "|")
	}

	withData := titles(sample(5, 7.18))
	for _, want := range []string{"输入", "输出", "缓存读"} {
		if !strings.Contains(withData, want) {
			t.Errorf("columns %q must include %q when rows carry counts", withData, want)
		}
	}

	empty := titles(tokenlessSample(5))
	for _, unwanted := range []string{"输入", "输出", "缓存读"} {
		if strings.Contains(empty, unwanted) {
			t.Errorf("columns %q must drop %q when every row's counts are zero", empty, unwanted)
		}
	}
	// The columns that remain must be the ones worth widening.
	for _, want := range []string{"时间", "模型", "金额"} {
		if !strings.Contains(empty, want) {
			t.Errorf("columns %q lost %q", empty, want)
		}
	}

	// A single nonzero count anywhere in the range is enough to bring them back:
	// a mixed range must not be rendered as if it had no data at all.
	mixed := tokenlessSample(5)
	mixed.Lines[3].Output = 12
	mixed.Rollup()
	if !strings.Contains(titles(mixed), "输出") {
		t.Errorf("columns %q dropped the token columns despite one row carrying counts", titles(mixed))
	}

	// Both layouts must render.
	for _, s := range []*Statement{sample(5, 7.18), tokenlessSample(5)} {
		if _, err := Render(s); err != nil {
			t.Fatalf("Render: %v", err)
		}
	}
}

// Truncation must be visible on the page. A statement that prints 3000 of
// 50000 requests while its total covers all 50000 is honest only if it says so.
func TestTruncationIsDisclosed(t *testing.T) {
	s := sample(10, 7.18)
	s.Requests = 50_000
	s.BilledCNY = 987.65
	s.LinesTruncated = true

	r := &renderer{s: s}
	// Both the section heading and the closing total must say on their face
	// that the printed rows are a prefix while the total is not.
	title := r.detailTitle()
	if !strings.Contains(title, "10") || !strings.Contains(title, "50,000") {
		t.Errorf("detail title must name both counts, got %q", title)
	}
	if got := r.totalsLabel(); !strings.Contains(got, "未列示") {
		t.Errorf("totals label = %q, want it to flag the unprinted rows", got)
	}

	// And an untruncated statement must not carry either caveat.
	plain := &renderer{s: sample(10, 7.18)}
	if got := plain.detailTitle(); got != "请求明细" {
		t.Errorf("untruncated title = %q, want plain", got)
	}
	if got := plain.totalsLabel(); got != "合计" {
		t.Errorf("untruncated totals label = %q, want 合计", got)
	}

	buf, err := Render(s)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if len(buf) == 0 {
		t.Error("empty output")
	}
}

// Rollup must total the lines it was given and order by spend, since that table
// sits directly above the rows it describes.
func TestRollupTotalsAndOrder(t *testing.T) {
	s := sample(30, 7.18)
	var sum float64
	var n int64
	for _, m := range s.ByModel {
		sum += m.BilledCNY
		n += m.Requests
	}
	if n != int64(len(s.Lines)) {
		t.Errorf("rollup requests = %d, want %d", n, len(s.Lines))
	}
	if diff := sum - s.BilledCNY; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("rollup total %v != statement total %v", sum, s.BilledCNY)
	}
	for i := 1; i < len(s.ByModel); i++ {
		if s.ByModel[i-1].BilledCNY < s.ByModel[i].BilledCNY {
			t.Errorf("ByModel not sorted by spend: %+v", s.ByModel)
		}
	}
}

// The per-model table sits directly above the totals row, so it must sum to
// the range total even when the printed lines are only a prefix of it — and it
// must exist at all on the preview path, which keeps no lines.
func TestRollupCoversRangeNotJustPrintedLines(t *testing.T) {
	s := &Statement{FromDay: "2026-08-01", ToDay: "2026-08-15"}
	// 100 in-range requests, of which only 3 are retained as printable lines.
	for i := range 100 {
		model := []string{"claude-opus-4-7", "gpt-5.6-sol"}[i%2]
		s.Observe(model, 0.25)
		if i < 3 {
			s.Lines = append(s.Lines, Line{Model: model, BilledCNY: 0.25})
		}
	}
	s.LinesTruncated = true
	s.Rollup()

	var sum float64
	var n int64
	for _, m := range s.ByModel {
		sum += m.BilledCNY
		n += m.Requests
	}
	if n != s.Requests {
		t.Errorf("rollup requests = %d, want the full range %d", n, s.Requests)
	}
	if diff := sum - s.BilledCNY; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("rollup total %v != range total %v — the model table would not "+
			"add up to the figure printed below it", sum, s.BilledCNY)
	}

	// And with no lines at all, which is exactly the preview's shape.
	p := &Statement{}
	p.Observe("claude-opus-4-7", 1.5)
	p.Rollup()
	if len(p.ByModel) != 1 || p.ByModel[0].BilledCNY != 1.5 {
		t.Errorf("ByModel with no Lines = %+v, want one row totalling 1.5", p.ByModel)
	}
}

func TestFormatting(t *testing.T) {
	for _, tc := range []struct {
		in   int64
		want string
	}{
		{0, "0"}, {7, "7"}, {999, "999"}, {1000, "1,000"},
		{1234567, "1,234,567"}, {-4567, "-4,567"},
	} {
		if got := fmtInt(tc.in); got != tc.want {
			t.Errorf("fmtInt(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// The rounding case: naive truncation of the fraction prints 1.99 here.
	for _, tc := range []struct {
		in   float64
		want string
	}{
		{0, "0.00"}, {1.999, "2.00"}, {1234.5, "1,234.50"},
		{0.005, "0.01"}, {-12.345, "-12.35"}, {1000000, "1,000,000.00"},
	} {
		if got := fmtMoney(tc.in); got != tc.want {
			t.Errorf("fmtMoney(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Writing a real file is opt-in: it exists so a human can look at the layout,
// which no assertion above can judge.
func TestWriteSampleForInspection(t *testing.T) {
	out := os.Getenv("STATEMENT_PDF_OUT")
	if out == "" {
		t.Skip("set STATEMENT_PDF_OUT=/path/to.pdf to dump a sample")
	}
	buf, err := Render(sample(120, 7.1842))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if err := os.WriteFile(out, buf, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Logf("wrote %s (%d bytes)", out, len(buf))
}

// The reconciliation block only appears when the ledger and the log disagree,
// and it never folds the gap into the itemised figure — the whole point is that
// the two numbers stay distinguishable.
func TestReconciliationBlock(t *testing.T) {
	// Agreement: one closing line, and it is the itemised total.
	s := sample(5, 0)
	s.BilledCNY = 470
	s.ChargedCNY = 470
	r := &renderer{s: s}
	if got := r.totalLines(); len(got) != 1 {
		t.Fatalf("agreeing statement has %d closing lines, want 1: %+v", len(got), got)
	}

	// Shortfall: three lines, and the last one is the money.
	s.UnitemisedCNY = 30
	s.ChargedCNY = 500
	lines := r.totalLines()
	if len(lines) != 3 {
		t.Fatalf("reconciling statement has %d closing lines, want 3: %+v", len(lines), lines)
	}
	if !strings.Contains(lines[0].amount, "470.00") {
		t.Errorf("itemised line = %+v, want ¥470.00", lines[0])
	}
	if !strings.Contains(lines[1].label, "未能明细化") || !strings.Contains(lines[1].amount, "30.00") {
		t.Errorf("gap line = %+v, want the ¥30.00 unitemised row", lines[1])
	}
	if !strings.Contains(lines[2].label, "实际扣款") || !strings.Contains(lines[2].amount, "500.00") {
		t.Errorf("closing line = %+v, want ¥500.00 actually charged", lines[2])
	}
	// The itemised figure must not have absorbed the gap.
	if s.BilledCNY != 470 {
		t.Errorf("BilledCNY = %v, want it left at 470 — the gap must not be distributed into it", s.BilledCNY)
	}

	buf, err := Render(s)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if len(buf) == 0 {
		t.Error("empty output")
	}
}

// A target-derived statement must caption itself as one — on its face, not
// only in a field a renderer could ignore — and must not silently reuse the
// ordinary date-range wording that would misrepresent how the range was
// chosen.
func TestByTargetIdentityAndSummary(t *testing.T) {
	s := sample(5, 0)
	s.ByTarget = true
	s.TargetCNY = 100
	s.BilledCNY = 103.5 // the walk overshoots slightly rather than splitting a row

	r := &renderer{s: s}
	rows := r.identityRows()
	var sawMethod, sawTarget, sawRange bool
	for _, kv := range rows {
		switch kv[0] {
		case "生成方式":
			sawMethod = true
			if !strings.Contains(kv[1], "目标金额") {
				t.Errorf("generation-method row = %q, want it to name target-amount generation", kv[1])
			}
		case "目标金额":
			sawTarget = true
			if !strings.Contains(kv[1], "100.00") {
				t.Errorf("target row = %q, want it to show ¥100.00", kv[1])
			}
		case "覆盖区间":
			sawRange = true
			if !strings.Contains(kv[1], s.FromDay) || !strings.Contains(kv[1], s.ToDay) {
				t.Errorf("range row = %q, want it to name %s and %s", kv[1], s.FromDay, s.ToDay)
			}
		case "统计区间":
			t.Error("a target-derived statement must not print the ordinary 统计区间 caption")
		}
	}
	if !sawMethod || !sawTarget || !sawRange {
		t.Errorf("identity rows = %+v, missing one of 生成方式/目标金额/覆盖区间", rows)
	}

	items := r.summaryItems()
	var sawAchieved bool
	for _, it := range items {
		if it[0] == "实际列示金额" {
			sawAchieved = true
			if !strings.Contains(it[1], "103.50") {
				t.Errorf("achieved amount = %q, want ¥103.50", it[1])
			}
		}
		if it[0] == "区间消费" {
			t.Error("a target-derived statement must relabel 区间消费 as 实际列示金额")
		}
	}
	if !sawAchieved {
		t.Errorf("summary items = %+v, missing 实际列示金额", items)
	}

	// And an ordinary date-range statement must not pick up either caption.
	plain := &renderer{s: sample(5, 0)}
	for _, kv := range plain.identityRows() {
		if kv[0] == "生成方式" || kv[0] == "目标金额" || kv[0] == "覆盖区间" {
			t.Errorf("plain statement must not carry target captions, got %q", kv[0])
		}
	}
	for _, it := range plain.summaryItems() {
		if it[0] == "实际列示金额" {
			t.Error("plain statement must not relabel 区间消费")
		}
	}

	buf, err := Render(s)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if len(buf) == 0 {
		t.Error("empty output")
	}
}

// Truncation and reconciliation are independent captions and must compose:
// a truncated statement that also has a ledger gap needs both.
func TestTruncationAndReconciliationCompose(t *testing.T) {
	s := sample(3, 0)
	s.Requests = 9000
	s.LinesTruncated = true
	s.BilledCNY = 100
	s.UnitemisedCNY = 5
	s.ChargedCNY = 105

	r := &renderer{s: s}
	lines := r.totalLines()
	if len(lines) != 3 {
		t.Fatalf("want 3 closing lines, got %d", len(lines))
	}
	if !strings.Contains(lines[0].label, "未列示") {
		t.Errorf("itemised label = %q, want it to flag truncation too", lines[0].label)
	}
	if got := r.totalsLabel(); got != lines[0].label {
		t.Errorf("totalsLabel = %q, want it to track the first closing line %q", got, lines[0].label)
	}
}

// A target-derived window opens on whichever request carried the running total
// up to the target, i.e. partway through a day. Captioning it with the bare day
// label claims the statement covers all of that day when it covers an afternoon
// of it — an over-claim on a document meant to be handed to a finance
// department, so the exact boundaries are printed instead.
func TestByTargetRangeLineStatesExactBoundaries(t *testing.T) {
	s := sample(5, 0)
	s.ByTarget = true
	s.TargetCNY = 100
	loc := time.FixedZone("CST", 8*3600)
	s.RangeStart = time.Date(2026, 6, 2, 14, 33, 0, 0, loc)
	s.RangeEnd = time.Date(2026, 8, 16, 9, 12, 0, 0, loc)
	s.FromDay, s.ToDay = "2026-06-02", "2026-08-16"

	line := (&renderer{s: s}).targetRangeLine()
	for _, want := range []string{"2026-06-02 14:33", "2026-08-16 09:12"} {
		if !strings.Contains(line, want) {
			t.Errorf("range line = %q, want it to state %q", line, want)
		}
	}

	// Without the instants there is nothing truer to print than the labels, and
	// falling back to them beats rendering a zero time.
	s.RangeStart, s.RangeEnd = time.Time{}, time.Time{}
	line = (&renderer{s: s}).targetRangeLine()
	if !strings.Contains(line, "2026-06-02") || !strings.Contains(line, "2026-08-16") {
		t.Errorf("fallback range line = %q, want the day labels", line)
	}
	if strings.Contains(line, "0001-01-01") {
		t.Errorf("fallback range line = %q, must not print a zero time", line)
	}
}

// drawnBy renders one section of the document and returns every string it put
// on the page, in order. The PDF's own bytes cannot be searched — the font is
// a subset with a private encoding — so this is the only way to assert that a
// figure actually reached the paper rather than merely sitting in a field.
func drawnBy(t *testing.T, s *Statement, section func(*renderer)) []string {
	t.Helper()
	pdf := &gopdf.GoPdf{}
	pdf.Start(gopdf.Config{PageSize: gopdf.Rect{W: pageW, H: pageH}})
	if err := pdf.AddTTFFontData(fontFamily, fontSC); err != nil {
		t.Fatalf("load font: %v", err)
	}
	var got []string
	r := &renderer{pdf: pdf, s: s, drawn: &got}
	r.newPage()
	section(r)
	return got
}

// A range whose request-log rows are gone but whose debit the ledger still
// holds is the exact case the reconciliation exists for, and it is also the
// case where Lines is empty. The empty-range hint must not stand in for the
// closing block: printing "该区间内没有计费请求" and nothing else swallows a
// real charge, and contradicts the JSON preview, which reports the gap.
func TestEmptyDetailStillClosesOnTheLedgerFigure(t *testing.T) {
	s := &Statement{
		TokenMasked: "sk-ant…9f2c", FromDay: "2026-08-01", ToDay: "2026-08-15",
		TZName: "Asia/Shanghai", CNYPerUSD: 7, GeneratedAt: time.Now(),
		BilledCNY: 0, UnitemisedCNY: 21, ChargedCNY: 21,
	}
	s.Rollup()

	drawn := strings.Join(drawnBy(t, s, (*renderer).detailTable), "\n")
	if !strings.Contains(drawn, "该区间内没有计费请求") {
		t.Errorf("an empty range must still say so; drew:\n%s", drawn)
	}
	for _, want := range []string{"未能明细化的消费", "区间实际扣款", "¥21.00"} {
		if !strings.Contains(drawn, want) {
			t.Errorf("missing %q from a statement whose only spend is unitemised; drew:\n%s", want, drawn)
		}
	}

	// And with nothing to reconcile, the hint still closes on a total rather
	// than trailing off — a ¥0.00 range is a claim the document should make
	// explicitly.
	s.UnitemisedCNY, s.ChargedCNY = 0, 0
	drawn = strings.Join(drawnBy(t, s, (*renderer).detailTable), "\n")
	if !strings.Contains(drawn, "合计") {
		t.Errorf("empty range without a gap must still print 合计; drew:\n%s", drawn)
	}
}
