package statement

import (
	_ "embed"
	"fmt"
	"strconv"
	"strings"

	"github.com/signintech/gopdf"
)

// NotoSansSC-Statement.ttf is a GB2312 subset of Noto Sans CJK SC, converted
// from CFF to TrueType outlines because gopdf embeds glyf only. Provenance,
// licence and the regeneration recipe are in fonts/README.md.
//
//go:embed fonts/NotoSansSC-Statement.ttf
var fontSC []byte

const fontFamily = "sc"

// A4 in points, with a margin wide enough that a stapled or hole-punched copy
// loses nothing.
const (
	pageW       = 595.28
	pageH       = 841.89
	margin      = 36.0
	contentW    = pageW - 2*margin
	bottomLimit = pageH - margin - 20 // room for the footer rule + page number
)

const (
	titleSize = 17.0
	headSize  = 9.5
	bodySize  = 8.5
	smallSize = 7.5
	// rowH is tuned so CJK glyphs at bodySize keep visible leading; tighter
	// than this and a column of Chinese model labels reads as a solid block.
	rowH = 13.5
	// cellPad keeps a right-aligned number off the next column's left edge.
	cellPad = 6.0
)

// col is one table column. Numbers right-align, which is the only way a column
// of amounts is scannable.
type col struct {
	title string
	w     float64
	right bool
}

// Render produces the statement PDF.
//
// It renders twice: the first pass only counts pages so the second can print
// "第 N 页 / 共 M 页". gopdf streams pages out with no way to revisit an earlier
// one, and "共 0 页" on a document headed for a finance department is worth more
// than the cost of a second pass.
func Render(s *Statement) ([]byte, error) {
	_, pages, err := render(s, 0)
	if err != nil {
		return nil, err
	}
	buf, _, err := render(s, pages)
	return buf, err
}

func render(s *Statement, totalPages int) ([]byte, int, error) {
	pdf := &gopdf.GoPdf{}
	pdf.Start(gopdf.Config{PageSize: gopdf.Rect{W: pageW, H: pageH}})
	if err := pdf.AddTTFFontData(fontFamily, fontSC); err != nil {
		return nil, 0, fmt.Errorf("statement: load embedded font: %w", err)
	}

	r := &renderer{pdf: pdf, s: s, totalPages: totalPages}
	r.newPage()
	r.header()
	r.identity()
	r.summary()
	if len(s.ByModel) > 0 {
		r.modelTable()
	}
	r.detailTable()
	r.footerNote()
	r.pageFooter()

	return pdf.GetBytesPdf(), pdf.GetNumberOfPages(), nil
}

type renderer struct {
	pdf        *gopdf.GoPdf
	s          *Statement
	y          float64
	page       int
	totalPages int
	// drawn, when non-nil, records every string written to the page. Only
	// tests set it: the output embeds a subsetted font with a private
	// encoding, so searching the PDF bytes for "¥21.00" finds nothing whether
	// the line was printed or not, and a test of "does this figure reach the
	// page" would otherwise have no way to fail. One nil check per draw.
	drawn *[]string
}

// --- primitives ---------------------------------------------------------

func (r *renderer) setFont(size float64) { _ = r.pdf.SetFont(fontFamily, "", size) }

func (r *renderer) ink(gray uint8) { r.pdf.SetTextColor(gray, gray, gray) }

func (r *renderer) text(x, y float64, s string) {
	if r.drawn != nil {
		*r.drawn = append(*r.drawn, s)
	}
	r.pdf.SetXY(x, y)
	_ = r.pdf.Cell(nil, s)
}

func (r *renderer) width(s string) float64 {
	w, err := r.pdf.MeasureTextWidth(s)
	if err != nil {
		return 0
	}
	return w
}

// textRight draws s so its right edge lands on x+w.
func (r *renderer) textRight(x, w, y float64, s string) {
	r.text(x+w-r.width(s), y, s)
}

// fit truncates s with an ellipsis until it measures under w. Model identifiers
// are the long ones, and a name that overruns its column collides with the next
// column's digits — which turns an amount into gibberish without any error.
func (r *renderer) fit(s string, w float64) string {
	if r.width(s) <= w {
		return s
	}
	runes := []rune(s)
	for len(runes) > 1 {
		runes = runes[:len(runes)-1]
		if cand := string(runes) + "…"; r.width(cand) <= w {
			return cand
		}
	}
	return string(runes)
}

func (r *renderer) rule(y float64, gray uint8, width float64) {
	r.pdf.SetLineWidth(width)
	r.pdf.SetStrokeColor(gray, gray, gray)
	r.pdf.Line(margin, y, margin+contentW, y)
}

func (r *renderer) newPage() {
	if r.page > 0 {
		r.pageFooter()
	}
	r.pdf.AddPage()
	r.page++
	r.y = margin
}

// --- tables -------------------------------------------------------------

func (r *renderer) tableHead(cols []col) {
	r.pdf.SetFillColor(240, 240, 242)
	r.pdf.RectFromUpperLeftWithStyle(margin, r.y-2, contentW, rowH+2, "F")
	r.setFont(smallSize)
	r.ink(90)
	r.drawCells(cols, colTitles(cols))
	r.y += rowH + 2
}

// drawCells lays one row of values across cols at the current y, without
// advancing it. Callers set font and colour first.
func (r *renderer) drawCells(cols []col, vals []string) {
	x := margin
	for i, c := range cols {
		if i < len(vals) {
			if c.right {
				r.textRight(x, c.w-cellPad, r.y, vals[i])
			} else {
				r.text(x, r.y, r.fit(vals[i], c.w-cellPad))
			}
		}
		x += c.w
	}
}

// row draws one body row, breaking to a fresh page (and repeating the header)
// when it would cross the bottom margin.
func (r *renderer) row(cols []col, vals []string) {
	if r.y+rowH > bottomLimit {
		r.newPage()
		r.tableHead(cols)
	}
	r.setFont(bodySize)
	r.ink(45)
	r.drawCells(cols, vals)
	r.y += rowH
}

func colTitles(cols []col) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = c.title
	}
	return out
}

// --- sections -----------------------------------------------------------

func (r *renderer) header() {
	r.setFont(titleSize)
	r.ink(20)
	r.text(margin, r.y, "API 用量消费对账单")

	r.setFont(smallSize)
	r.ink(110)
	r.textRight(margin, contentW, r.y+7, "生成时间 "+r.s.GeneratedAt.Format("2006-01-02 15:04:05"))

	r.y += 24
	r.rule(r.y, 60, 1.0)
	r.y += 12
}

// identity prints who the statement is for and what window it covers. The
// timezone is on the page because a day label means a different set of requests
// in Shanghai than in UTC, and a reader reconciling against another system has
// no way to tell which was meant.
func (r *renderer) identity() {
	for _, kv := range r.identityRows() {
		r.setFont(bodySize)
		r.ink(120)
		r.text(margin, r.y, kv[0])
		r.ink(30)
		r.text(margin+64, r.y, kv[1])
		r.y += rowH
	}
	r.y += 6
}

// identityRows is what identity() prints, split out so the target-vs-range
// captioning can be tested without rendering a PDF.
//
// A target-derived statement gets its own row layout rather than reusing
// "统计区间" verbatim: the range was not named by the caller, it fell out of
// walking the log backward until spend reached the target, and a reader must
// not mistake it for an ordinary date-range export.
func (r *renderer) identityRows() [][2]string {
	rows := [][2]string{
		{"令牌", r.tokenLine()},
		{"计费分组", orDash(r.s.Group)},
	}
	if r.s.ByTarget {
		rows = append(rows,
			[2]string{"生成方式", "按目标金额生成（非常规日期区间）"},
			[2]string{"目标金额", "¥" + fmtMoney(r.s.TargetCNY)},
			[2]string{"覆盖区间", r.targetRangeLine()},
		)
	} else {
		rows = append(rows,
			[2]string{"统计区间", fmt.Sprintf("%s 至 %s（%s，含首尾两日）", r.s.FromDay, r.s.ToDay, orDash(r.s.TZName))},
		)
	}
	// The rate is part of the document's identity, not a footnote: the ledger
	// is denominated in USD and nothing in the request log records the rate a
	// charge settled at, so every yuan figure here is this one multiplication.
	// Printing it is what lets two copies of the same range be reconciled
	// against each other after the rate has moved.
	if r.s.CNYPerUSD > 0 {
		rows = append(rows,
			[2]string{"换算汇率", fmt.Sprintf("1 USD = %s CNY（导出时汇率）", strconv.FormatFloat(r.s.CNYPerUSD, 'f', 4, 64))},
		)
	}
	return rows
}

// targetRangeLine states the window a target-derived statement really covers.
//
// The backward walk stops on whichever request carried the running total up to
// the target, so the window opens partway through a day. Printing the bare day
// label would over-claim it — "2026-06-02 至 2026-08-16" reads as covering all
// of 06-02 when the statement begins at 14:33 that afternoon. Minute precision
// is enough to make the boundary checkable against the first detail row, and
// matches the granularity that row is printed at.
//
// Falls back to the day labels if the instants were not supplied, so a caller
// that sets only FromDay/ToDay still renders something truthful rather than a
// zero-time boundary.
func (r *renderer) targetRangeLine() string {
	const suffix = "为达到目标金额自动回溯得到"
	if r.s.RangeStart.IsZero() || r.s.RangeEnd.IsZero() {
		return fmt.Sprintf("%s 至 %s（%s，%s）", r.s.FromDay, r.s.ToDay, orDash(r.s.TZName), suffix)
	}
	const layout = "2006-01-02 15:04"
	return fmt.Sprintf("%s 至 %s（%s，%s）",
		r.s.RangeStart.Format(layout), r.s.RangeEnd.Format(layout), orDash(r.s.TZName), suffix)
}

func (r *renderer) tokenLine() string {
	if r.s.TokenName == "" {
		return r.s.TokenMasked
	}
	return fmt.Sprintf("%s（%s）", r.s.TokenName, r.s.TokenMasked)
}

// summary is the block a reimbursement reviewer actually reads: the range
// total, the token's all-time total, and the rate behind every CNY figure here.
func (r *renderer) summary() {
	const boxH = 58.0
	r.pdf.SetFillColor(246, 246, 247)
	r.pdf.RectFromUpperLeftWithStyle(margin, r.y, contentW, boxH, "F")

	cellW := contentW / 3
	items := r.summaryItems()
	for i, it := range items {
		x := margin + float64(i)*cellW + 12
		r.setFont(smallSize)
		r.ink(120)
		r.text(x, r.y+11, it[0])
		r.setFont(headSize + 1.5)
		r.ink(25)
		r.text(x, r.y+29, it[1])
	}
	r.y += boxH + 12
}

// summaryItems is the summary box's three figures. On a target-derived
// statement the middle figure is relabelled "实际列示金额" — it is exactly
// BilledCNY, the same field an ordinary range prints as "区间消费", but the
// label has to say it is what the target walk landed on rather than what
// happened to fall inside caller-chosen dates.
func (r *renderer) summaryItems() [][2]string {
	label := "区间消费"
	if r.s.ByTarget {
		label = "实际列示金额"
	}
	return [][2]string{
		{"区间请求数", fmtInt(r.s.Requests) + " 笔"},
		{label, "¥" + fmtMoney(r.s.BilledCNY)},
		{r.lifetimeLabel(), "¥" + fmtMoney(r.s.LifetimeBilledCNY)},
	}
}

func (r *renderer) lifetimeLabel() string {
	if r.s.LifetimeDays > 0 {
		return fmt.Sprintf("该令牌累计消费（近 %d 天）", r.s.LifetimeDays)
	}
	return "该令牌累计消费"
}

func (r *renderer) modelCols() []col {
	return []col{
		{title: "模型", w: contentW - 170},
		{title: "请求数", w: 80, right: true},
		{title: "金额 (元)", w: 90, right: true},
	}
}

// detailCols is the itemised table's layout, in two shapes.
//
// There is no 缓存写 column in either: OpenAI's usage block has no cache-write
// concept (only cached_tokens, a read), and this deployment relays OpenAI
// almost exclusively, so the column was structurally zero rather than
// occasionally empty.
//
// The three token columns are dropped whole when no printed row carries a
// count. Rows older than 2026-08-09 have none — the counts never existed
// upstream and cannot be reconstructed — and a table of zeroes asserts that
// the requests consumed nothing, which is a stronger and falser claim than
// saying nothing. The freed width goes to 模型 and 金额, where it is readable.
//
// Both variants must tile contentW exactly; a shortfall drifts the
// right-aligned amounts off the page edge.
func (r *renderer) detailCols() []col {
	if !r.s.HasTokenDetail {
		return []col{
			{title: "时间", w: 120},
			{title: "模型", w: 283.28},
			{title: "金额 (元)", w: 120, right: true},
		}
	}
	return []col{
		{title: "时间", w: 100},
		{title: "模型", w: 190},
		{title: "输入", w: 58, right: true},
		{title: "输出", w: 58, right: true},
		{title: "缓存读", w: 62, right: true},
		{title: "金额 (元)", w: 55.28, right: true},
	}
}

func (r *renderer) modelTable() {
	r.sectionTitle("按模型汇总")
	cols := r.modelCols()
	r.tableHead(cols)
	for _, m := range r.s.ByModel {
		r.row(cols, []string{m.Model, fmtInt(m.Requests), cny4(m.BilledCNY)})
	}
	r.y += 10
}

// detailTitle names the itemised section, disclosing on its face when the rows
// below are only part of the range.
//
// "最近" rather than "前": truncation keeps the newest rows, so the itemised
// block starts partway into the range. Saying "前" would have the reader look
// for the range's opening day among rows that deliberately do not include it.
func (r *renderer) detailTitle() string {
	if !r.s.LinesTruncated {
		return "请求明细"
	}
	return fmt.Sprintf("请求明细（列示最近 %s 笔，区间共 %s 笔）",
		fmtInt(int64(len(r.s.Lines))), fmtInt(r.s.Requests))
}

// totalsLabel marks the itemised figure as covering the whole range rather than
// just the printed rows, whenever those differ.
func (r *renderer) totalsLabel() string { return r.totalLines()[0].label }

func (r *renderer) detailTable() {
	r.sectionTitle(r.detailTitle())

	cols := r.detailCols()

	// No rows to itemise still closes on the totals block. It used to return
	// here, which meant that when the log had lost the range's lines but the
	// ledger still held the debit, the page printed "该区间内没有计费请求" and
	// nothing else — swallowing a real charge, in exactly the direction the
	// reconciliation exists to prevent, while the JSON preview reported the
	// gap and the PDF denied it.
	if len(r.s.Lines) == 0 {
		r.setFont(bodySize)
		r.ink(130)
		r.text(margin, r.y, "该区间内没有计费请求。")
		r.y += rowH
		r.totalsRow(cols)
		return
	}


	r.tableHead(cols)
	for _, ln := range r.s.Lines {
		vals := []string{ln.TS.Format("01-02 15:04:05"), orDash(ln.Model)}
		if r.s.HasTokenDetail {
			vals = append(vals, fmtInt(ln.Input), fmtInt(ln.Output), fmtInt(ln.CacheRead))
		}
		vals = append(vals, cny4(ln.BilledCNY))
		r.row(cols, vals)
	}
	r.totalsRow(cols)
}

// totalsRow closes the itemised section on figures the reader can check against
// the summary block.
//
// When the ledger recorded spend the log cannot account for, the reconciliation
// is shown as its own three-line block — itemised, unitemised, charged — rather
// than folded into one number. The gap is a real debit with no evidence behind
// it, and a document that hides it inside "合计" is claiming an itemisation it
// does not have.
func (r *renderer) totalsRow(cols []col) {
	lines := r.totalLines()
	if r.y+rowH*float64(len(lines))+10 > bottomLimit {
		r.newPage()
	}
	r.rule(r.y+1, 150, 0.5)
	r.y += 7

	for i, tl := range lines {
		// The closing figure is the one that matches the money; intermediate
		// rows sit back a shade so the eye lands on it.
		if i == len(lines)-1 {
			r.setFont(headSize)
			r.ink(25)
		} else {
			r.setFont(bodySize)
			r.ink(95)
		}
		r.text(margin, r.y, tl.label)
		vals := make([]string, len(cols))
		vals[len(cols)-1] = tl.amount
		r.drawCells(cols, vals)
		r.y += rowH
	}
	r.y += 4
}

type totalLine struct{ label, amount string }

// totalLines is the closing block. One line when the log and the ledger agree,
// three when they do not.
func (r *renderer) totalLines() []totalLine {
	label := "合计"
	if r.s.LinesTruncated {
		label = "区间合计（含未列示部分）"
	}
	if r.s.UnitemisedCNY <= 0 {
		return []totalLine{{label, "¥" + fmtMoney(r.s.BilledCNY)}}
	}
	return []totalLine{
		{label, "¥" + fmtMoney(r.s.BilledCNY)},
		{"未能明细化的消费", "¥" + fmtMoney(r.s.UnitemisedCNY)},
		{"区间实际扣款", "¥" + fmtMoney(r.s.ChargedCNY)},
	}
}

func (r *renderer) sectionTitle(t string) {
	if r.y+34 > bottomLimit {
		r.newPage()
	}
	r.setFont(headSize + 1)
	r.ink(25)
	r.text(margin, r.y, t)
	r.y += rowH + 2
}

// footerNote says what this document is and, just as importantly, what it is
// not. Someone filing it for reimbursement should not learn that it isn't an
// invoice from their finance department.
func (r *renderer) footerNote() {
	if r.y+56 > bottomLimit {
		r.newPage()
	}
	r.y += 6
	r.rule(r.y, 200, 0.5)
	r.y += 10
	r.setFont(smallSize)
	r.ink(125)
	notes := []string{
		"本对账单由系统依据请求日志自动生成，列示该令牌在所选区间内实际发生的 API 调用及其扣费金额。",
		"金额为实际结算金额，已按该令牌适用倍率计算，与账户扣款一致。",
	}
	// Said plainly rather than left to be inferred from the rate row above: the
	// underlying ledger is in USD, so re-exporting the same range after the
	// rate moves yields a different yuan total. A reader holding two copies
	// needs to know that is expected rather than an error in one of them.
	if r.s.CNYPerUSD > 0 {
		notes = append(notes,
			"人民币金额按导出时汇率（见上方“换算汇率”）由实际结算的美元金额折算，不同时间导出的同一区间总额可能因汇率变动而略有差异。")
	}
	if r.s.UnitemisedCNY > 0 {
		notes = append(notes,
			"“未能明细化的消费”为账本确有扣款、但请求日志未留存对应记录的部分，金额真实，仅明细缺失。")
	}
	notes = append(notes,
		"本对账单为用量凭证，不是增值税发票。如需发票，请在充值记录页面另行申请。")
	for _, n := range notes {
		r.text(margin, r.y, n)
		r.y += rowH - 2
	}
}

func (r *renderer) pageFooter() {
	y := pageH - margin
	r.pdf.SetLineWidth(0.5)
	r.pdf.SetStrokeColor(220, 220, 220)
	r.pdf.Line(margin, y-12, margin+contentW, y-12)

	r.setFont(smallSize)
	r.ink(150)
	r.text(margin, y-6, r.tokenLine())
	label := fmt.Sprintf("第 %d 页", r.page)
	if r.totalPages > 0 {
		label = fmt.Sprintf("第 %d 页 / 共 %d 页", r.page, r.totalPages)
	}
	r.textRight(margin, contentW, y-6, label)
}

// --- formatting ---------------------------------------------------------

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

// cny4 is the per-request precision. A single cheap call costs a fraction of a
// fen, so two decimals would print a page of "0.00" and a column that appears
// to sum to nothing. Totals round to cents; line items do not.
func cny4(v float64) string { return strconv.FormatFloat(v, 'f', 4, 64) }

// fmtInt groups thousands. Token counts run to seven digits and are unreadable
// otherwise.
func fmtInt(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var b strings.Builder
	for i := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteByte(s[i])
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// fmtMoney is CNY at two decimals with thousands separators.
func fmtMoney(v float64) string {
	neg := v < 0
	if neg {
		v = -v
	}
	// Round before splitting, or 1.999 prints as "1.99" instead of "2.00".
	s := strconv.FormatFloat(v, 'f', 2, 64)
	dot := strings.IndexByte(s, '.')
	whole, _ := strconv.ParseInt(s[:dot], 10, 64)
	out := fmtInt(whole) + s[dot:]
	if neg {
		return "-" + out
	}
	return out
}
