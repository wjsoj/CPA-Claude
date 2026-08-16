package statement

import (
	"sort"
	"time"
)

// A group statement is the reimbursement attachment for a team that bills as a
// unit: one document covering every member's API spend over a range, so the
// finance department gets a single artifact instead of one per person.
//
// It is deliberately NOT the per-token statement run N times and stapled
// together. Two things differ, and both are about what a reader can do with the
// page:
//
//   - The member rollup is the main table, not the model rollup. "Who spent
//     what" is the question a shared invoice has to answer; the model split is
//     context underneath it.
//   - The itemised section, when asked for at all, is capped for the whole
//     document rather than per member. A thirty-person month is six figures of
//     requests, so a per-member cap would produce a PDF nobody opens and still
//     not be complete. The rollups come from pre-summed aggregates and cover the
//     whole range regardless of what the listing shows — the same reason
//     Statement.SetRangeTotals exists.
//
// The roster is the one at export time. A member who joined or left mid-range is
// counted whole or not at all, on both the log side and the ledger side, so the
// two halves of the document always describe the same set of people. That is a
// choice, not an oversight, and the renderer prints it on the page — an
// attachment whose scope a reader has to guess at is worse than one whose scope
// is narrower than they expected.

// MemberRow is one member's line in the group rollup.
//
// Requests/BilledCNY come from the request log; PoolLedgerCNY/PersonalLedgerCNY
// come from the wallet ledger. Those are two different books written by
// different code on different paths, so they are never added together — see the
// doc on PoolLedgerCNY.
type MemberRow struct {
	Masked string
	// Label is the client token's display name, empty when it has none.
	Label string
	Role  string

	Requests  int64
	BilledCNY float64

	// PoolLedgerCNY and PersonalLedgerCNY split the ledger's view of this
	// member: what the shared workspace pool covered, and what fell back to
	// their own wallet after a cap ran out. Both are money that actually moved,
	// which is what "ledger" in the name is there to keep distinct from the
	// usage API's personal_billed_usd — that one is BilledCNY minus the pool
	// half, a derivation from the log that exists even where no wallet row does.
	//
	// They are carried for the JSON preview (the console renders the split) and
	// deliberately kept off the PDF's member table. Their source is the ledger
	// while BilledCNY's is the log, so a reader who found all three in one row
	// would try to add the first two up to the third and land on a discrepancy
	// the document already reports elsewhere, as UnitemisedCNY.
	PoolLedgerCNY     float64
	PersonalLedgerCNY float64

	// Unmeasurable marks a member whose token is too short to mask
	// distinguishably. Their spend cannot be told apart from any other such
	// token's in the log, so BilledCNY stays zero rather than carrying
	// somebody else's traffic.
	Unmeasurable bool
}

// GroupStatement is everything RenderGroup needs.
type GroupStatement struct {
	WorkspaceID   int64
	WorkspaceName string
	// AdminMasked/AdminLabel identify who exported the document. It is a
	// management artifact and the person who produced it is part of its
	// provenance.
	AdminMasked string
	AdminLabel  string

	// Range, as inclusive day labels in the deployment's display timezone, and
	// the zone those labels mean. Printed for the same reason the per-token
	// document prints them: "2026-08-15" covers a different set of requests in
	// Shanghai than in UTC.
	FromDay string
	ToDay   string
	TZName  string

	GeneratedAt time.Time

	// CNYPerUSD is the single rate every yuan figure converts at. Never zero —
	// a zero would render the entire document as ¥0.00, which is wrong in the
	// direction a reader is least likely to question.
	CNYPerUSD float64

	// Range totals, covering every request in the window including any the
	// itemised listing dropped.
	Requests  int64
	BilledCNY float64

	// Running totals for the same roster across the whole retained log.
	// LifetimeDays is the retention window they cover, printed beside them so a
	// 90-day figure is not read as all-time.
	LifetimeRequests  int64
	LifetimeBilledCNY float64
	LifetimeDays      int

	// UnitemisedCNY is ledger-confirmed spend across the roster that no
	// request-log row accounts for; ChargedCNY is the ledger's own range total.
	// Both zero when the two agree, when the ledger could not be read, or when
	// the log accounts for more than the ledger (an overage is not evidence of
	// a missing charge and is never shown as one).
	UnitemisedCNY float64
	ChargedCNY    float64

	// Partial reports that the figures are known to be incomplete — a log query
	// failed, or some members are unmeasurable. Printed as a caveat rather than
	// suppressing the document: a team waiting on an invoice is better served
	// by a statement that says what it is missing.
	Partial bool
	Notes   []string

	ByMember []MemberRow
	ByModel  []ModelRow

	// Lines is the itemised listing, only when the caller asked for detail. It
	// is capped for the document as a whole (MaxDetailLines), never per member.
	Lines          []Line
	LinesTruncated bool
}

// MemberCount is the roster size the document was built from — every member,
// including the ones with no traffic, because "this person spent nothing" is
// information a reimbursement reviewer needs and an omitted row does not carry.
func (g *GroupStatement) MemberCount() int { return len(g.ByMember) }

// SetGroupTotals fills the range totals and both rollups from figures summed
// elsewhere — one pre-aggregated query, so a statement over a hundred thousand
// requests costs no more than one over ten.
//
// It replaces rather than accumulates, for the same reason
// Statement.SetRangeTotals does: a caller that ran it twice must print the
// figures once, not double them.
//
// byMember must be the whole roster and byModel the whole range; both tables sit
// above the total and a reader adds them up.
func (g *GroupStatement) SetGroupTotals(requests int64, billedCNY float64, byMember []MemberRow, byModel []ModelRow) {
	g.Requests = requests
	g.BilledCNY = billedCNY
	g.ByMember = append([]MemberRow(nil), byMember...)
	g.ByModel = append([]ModelRow(nil), byModel...)
}

// Rollup finalises both tables, sorted by spend descending so the members and
// models that dominate the bill lead.
//
// Unlike the per-token Rollup it decides nothing about the itemised columns: the
// group listing has a fixed layout (member / time / model / amount) and never
// prints token counts, because the member column has already taken the width and
// a shared bill is read for money rather than tokens. Someone who wants the
// token detail exports their own per-token statement.
func (g *GroupStatement) Rollup() {
	sort.SliceStable(g.ByMember, func(i, j int) bool {
		if g.ByMember[i].BilledCNY != g.ByMember[j].BilledCNY {
			return g.ByMember[i].BilledCNY > g.ByMember[j].BilledCNY
		}
		return g.ByMember[i].Masked < g.ByMember[j].Masked
	})
	for i := range g.ByModel {
		if g.ByModel[i].Model == "" {
			g.ByModel[i].Model = "(unknown)"
		}
	}
	sort.SliceStable(g.ByModel, func(i, j int) bool {
		if g.ByModel[i].BilledCNY != g.ByModel[j].BilledCNY {
			return g.ByModel[i].BilledCNY > g.ByModel[j].BilledCNY
		}
		return g.ByModel[i].Model < g.ByModel[j].Model
	})
}
