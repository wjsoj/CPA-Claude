package statement

import (
	"fmt"
	"strconv"

	"github.com/signintech/gopdf"
)

// RenderGroup produces the team statement PDF.
//
// Two passes, for the same reason Render takes two: gopdf streams pages out with
// no way to revisit an earlier one, so the page count has to be known before the
// footers are drawn. A group document is the one most likely to run long — a
// member table, a model table and up to MaxDetailLines rows — which makes
// "共 0 页" on it more likely, not less.
func RenderGroup(g *GroupStatement) ([]byte, error) {
	_, pages, err := renderGroup(g, 0)
	if err != nil {
		return nil, err
	}
	buf, _, err := renderGroup(g, pages)
	return buf, err
}

func renderGroup(g *GroupStatement, totalPages int) ([]byte, int, error) {
	pdf := &gopdf.GoPdf{}
	pdf.Start(gopdf.Config{PageSize: gopdf.Rect{W: pageW, H: pageH}})
	if err := pdf.AddTTFFontData(fontFamily, fontSC); err != nil {
		return nil, 0, fmt.Errorf("statement: load embedded font: %w", err)
	}

	r := &renderer{pdf: pdf, g: g, totalPages: totalPages}
	r.foot = r.teamLine()
	r.newPage()
	r.header("团队用量消费对账单", g.GeneratedAt)

	// Page one is the document someone signs: what it is for, what it totals,
	// who spent it, and room for a seal. The seal band is drawn FIRST, at a
	// fixed position, and reserves the bottom of this page — so it is always on
	// page one however long the roster runs, and the tables above it flow onto
	// page two instead of colliding with it.
	r.reserveGroupSeal()
	r.identityBlock(r.groupIdentityRows())
	r.purposeBlock()
	r.summaryBlock(r.groupSummaryItems())
	r.memberTable()
	if len(g.ByModel) > 0 {
		r.modelTable(g.ByModel)
	}
	r.groupFooterNote()

	// The itemised listing starts on its own page. It is an appendix — pages of
	// one-line-per-request evidence behind the overview — and beginning it
	// halfway down the signed page would make both harder to read. A summary
	// export has no appendix to open, so it keeps its one-line "no detail
	// requested" note where the flow left it rather than opening a page to
	// hold it.
	if g.Itemised {
		if len(g.Lines) > 0 {
			r.startAppendix()
		}
		r.groupDetailTable()
	}
	r.pageFooter()

	return pdf.GetBytesPdf(), pdf.GetNumberOfPages(), nil
}

// startAppendix opens the page the request listing begins on, unless the
// content already happens to sit at the top of a fresh one — breaking then would
// emit a blank page.
func (r *renderer) startAppendix() {
	if r.y > margin+0.5 {
		r.newPage()
	}
}

func (r *renderer) teamLine() string {
	if r.g.WorkspaceName == "" {
		return fmt.Sprintf("团队 #%d", r.g.WorkspaceID)
	}
	return r.g.WorkspaceName
}

func (r *renderer) adminLine() string {
	if r.g.AdminLabel == "" {
		return orDash(r.g.AdminMasked)
	}
	return fmt.Sprintf("%s（%s）", r.g.AdminLabel, r.g.AdminMasked)
}

func (r *renderer) groupIdentityRows() [][2]string {
	rows := [][2]string{
		{"团队", r.teamLine()},
		{"申请人", r.adminLine()},
		{"统计区间", fmt.Sprintf("%s 至 %s（%s，含首尾两日）", r.g.FromDay, r.g.ToDay, orDash(r.g.TZName))},
		{"成员人数", fmt.Sprintf("%d 人", r.g.MemberCount())},
	}
	// The rate is identity, not a footnote: the ledger is in USD and nothing in
	// the request log records what a charge settled at, so every yuan figure
	// here is this one multiplication. Printing it is what lets two copies of
	// the same range be reconciled after the rate has moved.
	if r.g.CNYPerUSD > 0 {
		rows = append(rows, [2]string{
			"换算汇率",
			fmt.Sprintf("1 USD = %s CNY（导出时汇率）", strconv.FormatFloat(r.g.CNYPerUSD, 'f', 4, 64)),
		})
	}
	return rows
}

func (r *renderer) groupSummaryItems() [][2]string {
	return [][2]string{
		{"区间请求数", fmtInt(r.g.Requests) + " 笔"},
		{"区间消费", "¥" + fmtMoney(r.g.BilledCNY)},
		{r.groupLifetimeLabel(), "¥" + fmtMoney(r.g.LifetimeBilledCNY)},
	}
}

// groupLifetimeLabel names the running total's window rather than calling it
// "累计": the log is retention-bounded, so an all-time reading is only correct
// for a team younger than retention.
func (r *renderer) groupLifetimeLabel() string {
	if r.g.LifetimeDays > 0 {
		return fmt.Sprintf("该团队累计消费（近 %d 天）", r.g.LifetimeDays)
	}
	return "该团队累计消费"
}

// memberCols tiles contentW exactly (110 + 148.28 + 75 + 100 + 90 = 523.28).
// A shortfall drifts the right-aligned amounts off the page edge.
func (r *renderer) memberCols() []col {
	return []col{
		{title: "成员", w: 110},
		{title: "名称", w: contentW - 375},
		{title: "请求数", w: 75, right: true},
		{title: "金额 (元)", w: 100, right: true},
		{title: "占比", w: 90, right: true},
	}
}

// memberTable is this document's main table: a shared bill is read to find out
// who spent what.
//
// It carries no pool/personal split. Those figures come from the wallet ledger
// while the amount beside them comes from the request log, and a row mixing the
// two invites an addition that does not balance — the difference is exactly the
// unitemised gap the closing block already reports, in its own labelled place.
//
// Members with no traffic keep their row at ¥0.00 rather than being dropped:
// "this person spent nothing" is an answer, and an absent row is not.
func (r *renderer) memberTable() {
	r.sectionTitle("按成员汇总")
	cols := r.memberCols()
	r.tableHead(cols)
	for _, m := range r.g.ByMember {
		label := m.Label
		if m.Unmeasurable {
			// Said on the row itself, not only in the footer: this member's
			// amount is zero because the log cannot tell their traffic apart,
			// which is a different claim from having spent nothing.
			label = appendNote(label, "令牌过短，用量无法统计")
		}
		r.row(cols, []string{
			m.Masked,
			orDash(label),
			fmtInt(m.Requests),
			cny4(m.BilledCNY),
			sharePct(m.BilledCNY, r.g.BilledCNY),
		})
	}
	// The table's own total, so a reader can check the rows add up to the
	// headline without leaving the table.
	r.row(cols, []string{"合计", "", fmtInt(r.g.Requests), cny4(r.g.BilledCNY), sharePct(r.g.BilledCNY, r.g.BilledCNY)})
	// A ledger-confirmed debit with no log rows behind it belongs on the page
	// being signed, not only at the end of the appendix thirty pages later. The
	// signature above attests to what the team spent, and what they spent is the
	// last of these three lines.
	if r.g.UnitemisedCNY > 0 {
		r.row(cols, []string{"未能明细化的消费", "账本有扣款、日志无对应记录", "", cny4(r.g.UnitemisedCNY), ""})
		r.row(cols, []string{"区间实际扣款", "", "", cny4(r.g.ChargedCNY), ""})
	}
	r.y += 10
}

func appendNote(label, note string) string {
	if label == "" {
		return note
	}
	return label + "（" + note + "）"
}

// sharePct renders part/whole as a percentage. A zero total makes every share
// zero rather than undefined — a team that spent nothing has no proportions to
// report, and 0.0% says that without a division by zero.
func sharePct(part, whole float64) string {
	if whole <= 0 {
		return "0.0%"
	}
	return strconv.FormatFloat(part/whole*100, 'f', 1, 64) + "%"
}

// groupDetailCols tiles contentW exactly (110 + 105 + 188.28 + 120 = 523.28).
//
// No token columns, in either shape: the member column has taken the width the
// per-token document spends on 输入/输出/缓存读, and a shared bill is read for
// money. Fixing the layout also means the group listing has no equivalent of
// HasTokenDetail to get wrong.
func (r *renderer) groupDetailCols() []col {
	return []col{
		{title: "成员", w: 110},
		{title: "时间", w: 105},
		{title: "模型", w: contentW - 335},
		{title: "金额 (元)", w: 120, right: true},
	}
}

func (r *renderer) groupDetailTitle() string {
	if !r.g.LinesTruncated {
		return "请求明细"
	}
	// "最近" rather than "前": truncation keeps the newest rows, so the listing
	// starts partway into the range.
	return fmt.Sprintf("请求明细（列示最近 %s 笔，区间共 %s 笔）",
		fmtInt(int64(len(r.g.Lines))), fmtInt(r.g.Requests))
}

// groupDetailTable prints the itemised rows when there are any, and closes the
// document on the totals block either way.
//
// An empty range still gets the closing block. A team that made no requests but
// whose ledger holds a debit — a retention prune, a lost log line — must not
// read as ¥0.00 with nothing else said; the reconciliation exists precisely to
// surface that, and a page that renders "该区间内没有计费请求" and stops would
// deny a charge the JSON preview reports.
func (r *renderer) groupDetailTable() {
	r.sectionTitle(r.groupDetailTitle())
	cols := r.groupDetailCols()

	if len(r.g.Lines) == 0 {
		r.setFont(bodySize)
		r.ink(130)
		// Only reachable for an itemised export over an empty range — a summary
		// export renders no appendix at all and says so in the notes instead.
		r.text(margin, r.y, "该区间内没有计费请求。")
		r.y += rowH
		r.totalsRow(cols, r.groupTotalLines())
		return
	}

	r.tableHead(cols)
	for _, ln := range r.g.Lines {
		r.row(cols, []string{
			lineMember(ln),
			ln.TS.Format("01-02 15:04:05"),
			orDash(ln.Model),
			cny4(ln.BilledCNY),
		})
	}
	r.totalsRow(cols, r.groupTotalLines())
}

// lineMember is how one appendix row names who made the request: the person's
// name when the token has one, the mask otherwise. The mask stays the fallback
// rather than the primary because these pages are read to attribute spend, and
// an unnamed token is the only case where a reader has nothing better to go on.
func lineMember(ln Line) string {
	if ln.MemberLabel != "" {
		return ln.MemberLabel
	}
	return orDash(ln.Member)
}

func (r *renderer) groupTotalLines() []totalLine {
	label := "合计"
	if r.g.LinesTruncated {
		label = "区间合计（含未列示部分）"
	}
	return closingLines(label, r.g.BilledCNY, r.g.UnitemisedCNY, r.g.ChargedCNY)
}

// groupFooterNote says what the document covers and, just as importantly, what
// its boundaries are. Both of the caveats below are ones a reader would
// otherwise have to guess at, and guessing wrong turns the page into a claim it
// does not support.
func (r *renderer) groupFooterNote() {
	if r.y+70 > r.bottom() {
		r.newPage()
	}
	r.y += 6
	r.rule(r.y, 200, 0.5)
	r.y += 10
	r.setFont(smallSize)
	r.ink(125)
	notes := []string{
		"本对账单汇总该团队全部成员在所选区间内实际发生的 API 调用及其扣费金额，成员消费包含由团队额度支付与由成员个人余额支付的两部分。",
		"成员名单以导出时为准：区间内加入或退出团队的成员，按导出时是否在团队内整体计入或整体不计入。",
	}
	if r.g.CNYPerUSD > 0 {
		notes = append(notes,
			"人民币金额按导出时汇率（见上方“换算汇率”）由实际结算的美元金额折算，不同时间导出的同一区间总额可能因汇率变动而略有差异。")
	}
	if r.g.UnitemisedCNY > 0 {
		notes = append(notes,
			"“未能明细化的消费”为账本确有扣款、但请求日志未留存对应记录的部分，金额真实，仅明细缺失。")
	}
	for _, n := range r.g.Notes {
		notes = append(notes, "说明："+n)
	}
	if !r.g.Itemised {
		// Said here because a summary export prints no appendix at all now. The
		// sentence has to exist somewhere: a five-figure request count above an
		// absent listing otherwise reads as a document that lost its pages.
		notes = append(notes, "本对账单为汇总版，未列示逐笔请求明细；如需逐笔明细，请在导出时选择“含请求明细”。")
	}
	notes = append(notes,
		"本对账单为用量凭证，不是增值税发票。如需发票，请在充值记录页面另行申请。")
	for _, n := range notes {
		// Long caveats must not run off the page; the note column is the full
		// content width.
		r.text(margin, r.y, r.fit(n, contentW))
		r.y += rowH - 2
	}
}

// --- page one: declaration + seal ---------------------------------------

const (
	// sealBandH is the height reserved at the bottom of page one for the
	// declaration, the signature lines and the seal box.
	sealBandH = 136
	// sealBoxW / sealBoxH is the empty square a 40mm round company seal is
	// stamped into. 42mm ≈ 119pt, plus a little room so a slightly-off stamp
	// still lands inside the frame.
	sealBoxW = 132.0
	sealBoxH = 108.0
)

// purposeBlock states what the API spend was for, in the group admin's own
// words, and is the reason this document can be filed as a research expense
// rather than just a usage dump.
//
// The system cannot derive this. Everything else on the page comes from the
// request log or the ledger; what the calls were FOR is knowledge only the
// person exporting has, so it is asked for at export time and reproduced here
// verbatim inside a declaration the signature block below refers back to.
func (r *renderer) purposeBlock() {
	// Font first: wrap() measures, so laying out at one size and drawing at
	// another would break the lines in the wrong places — and a caller that has
	// not set a font at all makes the measurement panic.
	r.setFont(bodySize)
	lines := r.wrap(r.purposeSentence(), contentW-20)

	h := float64(len(lines))*(rowH-1) + 16
	if r.y+h+6 > r.bottom() {
		r.newPage()
	}
	r.pdf.SetFillColor(246, 247, 245)
	r.pdf.RectFromUpperLeftWithStyle(margin, r.y-2, contentW, h, "F")
	r.pdf.SetLineWidth(0.5)
	r.pdf.SetStrokeColor(205, 208, 203)
	r.pdf.RectFromUpperLeftWithStyle(margin, r.y-2, contentW, h, "D")

	y := r.y + 8
	r.ink(35)
	for _, ln := range lines {
		r.text(margin+10, y, ln)
		y += rowH - 1
	}
	r.y += h + 8
}

// purposeSentence is the declaration itself. With no purpose given it keeps the
// same sentence and rules a blank through it: the document is then a form to be
// completed by hand, which is a usable outcome — a sentence that quietly drops
// the clause would read as a claim that no purpose exists.
func (r *renderer) purposeSentence() string {
	purpose := r.g.Purpose
	if purpose == "" {
		purpose = "____________________________________"
	}
	return fmt.Sprintf(
		"用途说明：本对账单所列 API 调用由「%s」用于完成“%s”课题（项目）的研究工作，"+
			"所列费用均为该工作在本平台实际发生的模型调用支出。",
		r.teamLine(), purpose)
}

// reserveGroupSeal draws the signature-and-seal band at a fixed position on the
// current page and stops the page's flowing content above it.
//
// Fixed, not appended after the tables, because where it lands is the whole
// point: a stamped page is the one the reader treats as the document, and a
// seal box that drifts onto page four of a long roster is not that page. The
// reservation belongs to this page alone — newPage resets the limit, so the
// roster simply continues overleaf.
func (r *renderer) reserveGroupSeal() {
	top := bottomLimit - sealBandH
	r.limit = top - 8

	r.rule(top, 190, 0.5)
	y := top + 14

	r.setFont(bodySize)
	r.ink(60)
	r.text(margin, y, "声明：以上用量与费用系本单位（课题组）为完成上述工作实际发生，情况属实。")
	y += rowH + 8

	// Signature lines on the left, seal box on the right. The underscores are
	// the blank: this band is filled in on paper.
	r.setFont(bodySize)
	r.ink(45)
	for _, ln := range []string{
		"经办人（签字）：______________________",
		"负责人（签字）：______________________",
		"日　　　　期：________ 年 ______ 月 ______ 日",
	} {
		r.text(margin, y, ln)
		y += rowH + 6
	}

	// The box is drawn dashed so nobody mistakes the frame for part of the
	// stamp, and left empty — a seal is applied to paper, so all this page owes
	// it is space and a label.
	bx := margin + contentW - sealBoxW
	by := top + 16
	r.pdf.SetLineWidth(0.6)
	r.pdf.SetStrokeColor(170, 174, 168)
	r.pdf.SetLineType("dashed")
	r.pdf.RectFromUpperLeftWithStyle(bx, by, sealBoxW, sealBoxH, "D")
	// Back to solid: the line type is content-stream state and would otherwise
	// dash every rule drawn after this on the document.
	r.pdf.SetLineType("solid")

	r.setFont(smallSize)
	r.ink(150)
	label := "（此处加盖单位公章）"
	r.text(bx+(sealBoxW-r.width(label))/2, by+sealBoxH/2-4, label)
}
