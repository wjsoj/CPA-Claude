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
	r.identityBlock(r.groupIdentityRows())
	r.summaryBlock(r.groupSummaryItems())
	r.memberTable()
	if len(g.ByModel) > 0 {
		r.modelTable(g.ByModel)
	}
	r.groupDetailTable()
	r.groupFooterNote()
	r.pageFooter()

	return pdf.GetBytesPdf(), pdf.GetNumberOfPages(), nil
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
		r.text(margin, r.y, "该区间内没有计费请求。")
		r.y += rowH
		r.totalsRow(cols, r.groupTotalLines())
		return
	}

	r.tableHead(cols)
	for _, ln := range r.g.Lines {
		r.row(cols, []string{
			orDash(ln.Member),
			ln.TS.Format("01-02 15:04:05"),
			orDash(ln.Model),
			cny4(ln.BilledCNY),
		})
	}
	r.totalsRow(cols, r.groupTotalLines())
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
	if r.y+70 > bottomLimit {
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
	notes = append(notes,
		"本对账单为用量凭证，不是增值税发票。如需发票，请在充值记录页面另行申请。")
	for _, n := range notes {
		// Long caveats must not run off the page; the note column is the full
		// content width.
		r.text(margin, r.y, r.fit(n, contentW))
		r.y += rowH - 2
	}
}
