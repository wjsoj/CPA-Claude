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
}

// --- primitives ---------------------------------------------------------

func (r *renderer) setFont(size float64) { _ = r.pdf.SetFont(fontFamily, "", size) }

func (r *renderer) ink(gray uint8) { r.pdf.SetTextColor(gray, gray, gray) }

func (r *renderer) text(x, y float64, s string) {
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
	r.y += boxH + 8

	if note := r.unratedNote(); note != "" {
		r.setFont(smallSize)
		r.ink(120)
		r.text(margin, r.y, note)
		r.y += rowH + 6
	} else {
		r.y += 4
	}
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

// unratedNote appears only while rows written before per-row rate capture are
// still inside the retention window. Those rows' yuan amounts had to be derived
// at today's rate rather than their own, which is a real (if shrinking) caveat
// on the total — and saying so beats a permanent footnote that outlives the
// condition by years. Empty once every row in range carries its own rate.
func (r *renderer) unratedNote() string {
	if r.s.UnratedRequests <= 0 {
		return ""
	}
	return fmt.Sprintf(
		"注：本区间内有 %s 笔请求早于逐笔汇率记录功能上线，其金额按当前汇率折算，可能与当时实际结算略有出入。",
		fmtInt(r.s.UnratedRequests))
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

func (r *renderer) detailCols() []col {
	return []col{
		{title: "时间", w: 100},
		{title: "模型", w: 163},
		{title: "输入", w: 52, right: true},
		{title: "输出", w: 52, right: true},
		{title: "缓存读", w: 56, right: true},
		{title: "缓存写", w: 56, right: true},
		{title: "金额 (元)", w: 44.28, right: true},
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
// below are only a prefix of the range.
func (r *renderer) detailTitle() string {
	if !r.s.LinesTruncated {
		return "请求明细"
	}
	return fmt.Sprintf("请求明细（列示前 %s 笔，区间共 %s 笔）",
		fmtInt(int64(len(r.s.Lines))), fmtInt(r.s.Requests))
}

// totalsLabel marks the itemised figure as covering the whole range rather than
// just the printed rows, whenever those differ.
func (r *renderer) totalsLabel() string { return r.totalLines()[0].label }

func (r *renderer) detailTable() {
	r.sectionTitle(r.detailTitle())

	if len(r.s.Lines) == 0 {
		r.setFont(bodySize)
		r.ink(130)
		r.text(margin, r.y, "该区间内没有计费请求。")
		r.y += rowH
		return
	}

	cols := r.detailCols()
	r.tableHead(cols)
	for _, ln := range r.s.Lines {
		vals := []string{
			ln.TS.Format("01-02 15:04:05"),
			orDash(ln.Model),
			fmtInt(ln.Input),
			fmtInt(ln.Output),
			fmtInt(ln.CacheRead),
			fmtInt(ln.CacheCreate),
			cny4(ln.BilledCNY),
		}
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
