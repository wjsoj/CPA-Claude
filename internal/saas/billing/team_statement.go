package billing

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"github.com/wjsoj/CPA-Claude/internal/config"
	"github.com/wjsoj/CPA-Claude/internal/statement"
	"github.com/wjsoj/cc-core/requestlog"
)

// The team statement is the reimbursement attachment for mode B — a workspace
// whose members each fund their own wallet and only want one document to hand
// to finance. /status/api/statement already answers that per token; stapling N
// of those together is what this replaces.
//
// It lives on /api/team rather than /status/api because the two prove entitlement
// differently. A /status export proves "I hold this token, so I may see this
// token's data" — possession is the whole argument. A team export shows other
// people's spend, which only a workspace admin may do, and workspace-admin is a
// judgement only TeamHandler.authMW makes. Mounting it beside /status would mean
// reimplementing that check somewhere it does not belong.
//
// There is deliberately no target-amount mode. The per-token version of that
// stands up because every line it prints is a charge the requester themselves
// incurred; at group scope it would become "assemble ¥N out of other people's
// consumption", and the honesty argument that carries the per-token feature does
// not survive the translation. A target_cny in the body is refused outright
// rather than ignored, so a caller cannot believe they got one.

// teamStatementBody is the shared request shape for both endpoints.
type teamStatementBody struct {
	// From/To are inclusive day labels, YYYY-MM-DD, in the display timezone —
	// the same contract as /api/team/usage, and for the same reason: only a day
	// label is answerable from the pre-summed cube.
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
	// Detail is "summary" (default) or "full". Summary is the default because a
	// thirty-person month is six figures of requests and the itemised listing
	// can only ever show a few thousand of them — the per-member and per-model
	// rollups are what a reimbursement package actually needs.
	Detail string `json:"detail,omitempty"`
	// TargetCNY exists only to be refused; see the package note above.
	TargetCNY float64 `json:"target_cny,omitempty"`
}

// statementRoutes is called from Routes, so the endpoints inherit authMW.
func (t *TeamHandler) statementRoutes(g *gin.RouterGroup) {
	g.POST("/statement", t.handleTeamStatementPreview)
	g.POST("/statement.pdf", t.handleTeamStatementPDF)
}

func (t *TeamHandler) handleTeamStatementPreview(c *gin.Context) {
	g, ok := t.buildGroupStatement(c, false)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, teamStatementJSON(g))
}

func (t *TeamHandler) handleTeamStatementPDF(c *gin.Context) {
	g, ok := t.buildGroupStatement(c, true)
	if !ok {
		return
	}
	buf, err := statement.RenderGroup(g)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	name := fmt.Sprintf("team-statement-%d-%s_%s.pdf", g.WorkspaceID, g.FromDay, g.ToDay)
	// Both filename forms: the bare one for clients that read only the first,
	// and RFC 5987 for anything non-ASCII a future name could carry.
	c.Header("Content-Disposition", fmt.Sprintf(
		`attachment; filename="%s"; filename*=UTF-8''%s`, name, url.PathEscape(name)))
	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, "application/pdf", buf)
}

// buildGroupStatement is the single assembly point behind both endpoints, so the
// preview and the PDF can never disagree about a team's bill. withLines controls
// whether the itemised rows are materialised at all — the preview needs only the
// counts, and a month of team traffic should not be pulled into memory to render
// three numbers.
//
// It writes its own error response and reports ok=false when it does.
func (t *TeamHandler) buildGroupStatement(c *gin.Context, withLines bool) (*statement.GroupStatement, bool) {
	ws := t.ws(c)
	var body teamStatementBody
	// An empty body is a valid request (default window, summary detail), so a
	// missing or unparseable payload is only an error when bytes were actually
	// sent.
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&body); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return nil, false
		}
	}
	if body.TargetCNY != 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest,
			gin.H{"error": "团队对账单不支持按目标金额生成"})
		return nil, false
	}
	detail := strings.TrimSpace(body.Detail)
	switch detail {
	case "", "summary", "full":
	default:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": `detail must be "summary" or "full"`})
		return nil, false
	}
	// Rows are collected only when the caller asked for detail AND the endpoint
	// can print them. The preview never collects: it reports how many rows a
	// full export would carry, from the range count it already has.
	wantLines := detail == "full"
	collectLines := wantLines && withLines

	if t.LogDir == "" {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "log_dir not configured"})
		return nil, false
	}
	// Unlike /api/team/usage, which degrades to totals-only, a statement is
	// refused outright without the log index. Every table under the headline —
	// the model rollup and the itemised listing both — is a per-member query,
	// and on the scanning fallback that is one full pass over the archive per
	// member. A reimbursement document that took a minute to produce and then
	// arrived with an empty model table is worse than a clear refusal.
	if !t.LogIndexed {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable,
			gin.H{"error": "请求日志索引不可用，暂时无法生成团队对账单"})
		return nil, false
	}

	label := func(string) string { return "" }
	if t.TokenLabel != nil {
		label = t.TokenLabel
	}
	gu, err := BuildGroupUsage(c.Request.Context(), GroupUsageQuery{
		Wallets:     t.DB,
		LogDir:      t.LogDir,
		LogIndexed:  t.LogIndexed,
		WorkspaceID: ws.ID,
		FromDay:     body.From,
		ToDay:       body.To,
		Label:       label,
	})
	if err != nil {
		if errors.Is(err, ErrBadWindow) {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return nil, false
		}
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return nil, false
	}

	rate := t.statementRate()
	loc := requestlog.BucketLocation()
	adminTok := t.adminToken(c)

	g := &statement.GroupStatement{
		WorkspaceID:   ws.ID,
		WorkspaceName: ws.Name,
		AdminMasked:   maskToken(adminTok),
		AdminLabel:    label(adminTok),
		FromDay:       gu.FromDay,
		ToDay:         gu.ToDay,
		TZName:        gu.Timezone,
		GeneratedAt:   time.Now().In(loc),
		CNYPerUSD:     rate,
		LifetimeDays:  t.retentionDays(),
		Partial:       gu.Partial,
		Notes:         gu.Notes,
	}

	members := make([]statement.MemberRow, 0, len(gu.ByMember))
	for _, m := range gu.ByMember {
		members = append(members, statement.MemberRow{
			Masked:   m.Masked,
			Label:    m.Label,
			Role:     m.Role,
			Requests: m.Agg.Count,
			// Both ledger halves are already joined by full token upstream, so
			// two members whose short tokens mask to the same opaque string
			// still carry their own charges here.
			BilledCNY:         m.Agg.BilledUSD * rate,
			PoolLedgerCNY:     m.PoolBilledUSD * rate,
			PersonalLedgerCNY: m.PersonalLedgerUSD * rate,
			Unmeasurable:      m.Unmeasurable,
		})
	}
	models := make([]statement.ModelRow, 0, len(gu.ByModel))
	for _, m := range gu.ByModel {
		models = append(models, statement.ModelRow{
			Model: m.Model, Requests: m.Agg.Count, BilledCNY: m.Agg.BilledUSD * rate,
		})
	}
	// The rollups come from the pre-summed cube and cover the whole range, so
	// they stay correct however the itemised listing below is truncated.
	g.SetGroupTotals(gu.Total.Count, gu.Total.BilledUSD*rate, members, models)

	t.fillGroupLifetime(g, gu, rate)
	t.reconcileGroup(g, gu, rate)

	if collectLines {
		lines, truncated, lerr := groupDetailLines(t.LogDir, gu, loc, rate)
		if lerr != nil {
			// The listing is an enrichment on top of totals that are already
			// correct; losing it must not cost the caller the document.
			log.Warnf("team statement: detail lines failed for workspace %d: %v", ws.ID, lerr)
			g.Partial = true
			g.Notes = append(g.Notes, "请求明细读取失败，本对账单仅含汇总")
		} else {
			g.Lines = lines
			g.LinesTruncated = truncated
		}
	} else if wantLines {
		// The preview reports the row count the PDF will carry without
		// collecting the rows, so the dialog can warn about truncation up front.
		g.LinesTruncated = g.Requests > int64(statement.MaxDetailLines)
	}

	g.Rollup()
	return g, true
}

// fillGroupLifetime adds the roster's running total over the retained log.
//
// It is a second window over the same members, not a wider version of the first:
// the range totals must stay exactly what the caller asked for. Failure is not
// fatal — the figure is context printed beside the range, and a statement
// missing it is still a correct statement.
func (t *TeamHandler) fillGroupLifetime(g *statement.GroupStatement, gu *GroupUsage, rate float64) {
	days := t.retentionDays()
	now := time.Now().In(requestlog.BucketLocation())
	from := now.AddDate(0, 0, -(days - 1)).Format(DayLayout)

	// One fleet-wide cube query, then the roster's own buckets summed out of it
	// — the same shape ComputeGroupUsage uses, and the reason a team of any size
	// costs one round trip here.
	res, err := cachedUsageQuery(t.LogDir, from, now.Format(DayLayout), "")
	if err != nil {
		log.Warnf("team statement: lifetime rollup failed for workspace %d: %v", g.WorkspaceID, err)
		return
	}
	var count int64
	var usd float64
	for _, m := range gu.ByMember {
		if m.Unmeasurable {
			continue
		}
		a := res.ByClient[m.Masked]
		count += a.Count
		usd += a.BilledUSD
	}
	g.LifetimeRequests = count
	g.LifetimeBilledCNY = usd * rate
	g.LifetimeDays = days
}

// reconcileGroup compares the roster's itemised total against what the two
// ledgers actually debited over the same window, and records any shortfall.
//
// The debit is a transaction while the itemised row is a separate append to the
// request log, so the log can lose a line the ledger still holds — a crash, a
// disk problem, a retention prune. A statement built from the log alone then
// under-reports what the team really paid, which is the one direction a spend
// record must never be wrong in. Carrying the difference as its own labelled
// line lets the document total match the money without inventing the requests
// behind it.
//
// Both ledgers are read because a workspace member's spend hits the shared pool
// first and their own wallet only after a cap runs out; reading workspace_tx
// alone reports a team that never funded a pool as having spent nothing, which
// is the exact bug this whole surface exists to fix.
//
// Both halves are summed per member from figures joined by FULL token, never by
// mask. Masks are not identities: every token of ten bytes or fewer masks to the
// same opaque string, so a mask-keyed join hands each such member the sum of all
// of them — inflating both the ledger total and the unitemised gap, in the one
// direction a reimbursement attachment must never be wrong in.
//
// Failure is logged and skipped, never fatal: reconciliation is an enrichment,
// and a statement that refuses to render because SQLite was briefly busy is
// worse than one missing a line.
func (t *TeamHandler) reconcileGroup(g *statement.GroupStatement, gu *GroupUsage, rate float64) {
	if rate <= 0 {
		return
	}
	// Both ledger halves rode in on the usage view, already attached to the
	// member they belong to. Summing the member rows (rather than the ledger
	// maps) also scopes the total to the export-time roster, matching the log
	// side and what the renderer prints about the document's scope.
	var chargedUSD float64
	for _, m := range gu.ByMember {
		chargedUSD += m.PoolBilledUSD + m.PersonalLedgerUSD
	}
	if chargedUSD <= 0 {
		return
	}
	g.ChargedCNY = chargedUSD * rate

	// Only a shortfall is meaningful. The log accounting for more than the
	// ledger means something else — an unbilled attempt row, a refund — and is
	// never presented as a missing charge.
	//
	// The gap also has to survive being written down: the floor is in USD but
	// the figure prints in yuan to two decimals, so a gap of a hundredth of a
	// cent would render as "未能明细化的消费 ¥0.00", an entry asserting that
	// nothing is missing.
	gap := chargedUSD - gu.Total.BilledUSD
	if gap > groupLedgerGapEpsilonUSD && gap*rate >= groupUnitemisedFloorCNY {
		g.UnitemisedCNY = gap * rate
		return
	}
	// Within noise: present the ledger total as exactly the itemised one, so the
	// document does not show a one-fen discrepancy it cannot explain.
	g.ChargedCNY = g.BilledCNY
}

// groupLedgerGapEpsilonUSD / groupUnitemisedFloorCNY mirror the per-token
// statement's thresholds. Charges quantize to 1e-8, so anything under a
// hundredth of a cent is float noise; half a fen is where a discrepancy stops
// rounding to ¥0.00 on the page.
const (
	groupLedgerGapEpsilonUSD = 1e-4
	groupUnitemisedFloorCNY  = 0.005
)

// groupDetailLines materialises the newest MaxDetailLines requests across the
// whole roster, tagged with the member that made each one.
//
// The request-log filter takes one masked token, not a set, so this fans out
// per member and merges. Two things keep that from being the expensive shape it
// looks like:
//
//   - Only members the aggregate pass already proved have rows are queried, and
//     only the maxFanoutMembers biggest spenders of those. A large, mostly-idle
//     team costs almost nothing here, and the proof is free — it came out of the
//     same query that produced the totals.
//   - Each worker asks for no more rows than that member actually has in the
//     window (again, already known), capped at the document's own limit. Asking
//     every member for MaxDetailLines is how a 45-person team pulls 135,000 rows
//     out of SQLite to print 3,000.
//   - Each worker converts its page to Lines and trims to the document cap
//     before merging, so peak memory is a few thousand rows rather than
//     MaxDetailLines per member. Holding every member's full page at once is
//     tens of megabytes on a host that already runs under earlyoom.
//
// Merging per-member newest-N pages yields the true newest-N of the group: the
// global newest N is a subset of the union of the per-member newest N.
//
// An error from any single member fails the whole listing rather than silently
// returning a short one — a listing missing one person's rows would look
// complete and quietly misattribute the range.
func groupDetailLines(dir string, gu *GroupUsage, loc *time.Location, rate float64) ([]statement.Line, bool, error) {
	counts := make(map[string]int64, len(gu.ByMember))
	for _, m := range gu.ByMember {
		counts[m.Masked] = m.Agg.Count
	}
	active, _ := activeMasks(gu.ByMember)
	if len(active) == 0 {
		return nil, false, nil
	}

	var (
		mu       sync.Mutex
		merged   []statement.Line
		firstErr error
		wg       sync.WaitGroup
	)
	sem := make(chan struct{}, usageFanoutConcurrency)
	for _, mask := range active {
		wg.Add(1)
		go func(mask string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			res, err := requestlog.Query(requestlog.Filter{
				Dir: dir,
				// Day labels, never a From/To timestamp pair: the timestamp form
				// forfeits the index that answers this off
				// idx_req_ct(client_token, ts DESC) and scans row by row.
				FromDay:     gu.FromDay,
				ToDay:       gu.ToDay,
				ClientToken: mask,
				Limit:       pageLimitFor(counts[mask]),
				// Only Entries is read; the aggregates for this member already
				// came from the cube pass.
				PageOnly: true,
			})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				return
			}
			for _, r := range res.Entries {
				merged = append(merged, statement.Line{
					TS: r.TS.In(loc), Provider: r.Provider, Model: r.Model,
					Input: r.Input, Output: r.Output, CacheRead: r.CacheRead,
					// BilledOrCost, not BilledUSD: pre-v0.8.61 rows carry the
					// charge in CostUSD alone and would print as free.
					BilledCNY: r.BilledOrCost() * rate,
					Status:    r.Status,
					Member:    mask,
				})
			}
			trimNewest(&merged)
		}(mask)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, false, firstErr
	}
	// No trim here: every worker trims under the same lock immediately after it
	// appends, so the last one to merge has already enforced the cap. A second
	// call would be dead code claiming to be the guarantee.

	truncated := gu.Total.Count > int64(len(merged))
	// Newest-first while merging, so the cap keeps the newest; flipped here into
	// the chronological order the page reads in.
	sort.SliceStable(merged, func(i, j int) bool { return merged[i].TS.Before(merged[j].TS) })
	return merged, truncated, nil
}

// pageLimitFor is how many rows to ask one member for: what they actually have
// in the window, never more than the whole document can print.
func pageLimitFor(have int64) int {
	if have <= 0 || have > int64(statement.MaxDetailLines) {
		return statement.MaxDetailLines
	}
	return int(have)
}

// trimNewest keeps the newest MaxDetailLines of lines, in newest-first order.
// Called after every merge — under the merge lock — so the slice never grows
// past the document cap and the cap needs no second enforcement afterwards.
func trimNewest(lines *[]statement.Line) {
	if len(*lines) <= statement.MaxDetailLines {
		return
	}
	sort.SliceStable(*lines, func(i, j int) bool { return (*lines)[i].TS.After((*lines)[j].TS) })
	*lines = (*lines)[:statement.MaxDetailLines]
}

// retentionDays is the window the "近 N 天" running total covers. It is clamped
// to the usage layer's own maximum: a deployment configured to retain more than
// that cannot be asked for it in one query, and printing a label wider than the
// window actually scanned would overstate the figure beside it.
func (t *TeamHandler) retentionDays() int {
	d := t.RetentionDays
	if d <= 0 {
		d = 90
	}
	if d > maxUsageWindowDays {
		d = maxUsageWindowDays
	}
	return d
}

// statementRate is the single USD→CNY rate the whole document converts at.
//
// It must never return zero: every yuan figure on the page is one multiplication
// by this number, so a zero renders the entire statement as ¥0.00 — wrong in the
// direction a reader is least likely to question. Rate already folds the
// configured fallback in at construction; the compiled-in default covers a
// handler wired without one at all.
func (t *TeamHandler) statementRate() float64 {
	if t.Billing != nil && t.Billing.Rate != nil {
		if v := t.Billing.Rate.CNYPerUSD(); v > 0 {
			return v
		}
	}
	return config.DefaultCNYPerUSD
}

// teamStatementJSON is the preview the export dialog renders before the user
// commits to a download.
//
// Amounts are yuan, matching the PDF exactly — this is the one place in the team
// API that is not USD, because the document it previews is CNY-only: users pay
// and read their balance in yuan, and quoting the preview in dollars would make
// the two disagree on their face.
func teamStatementJSON(g *statement.GroupStatement) gin.H {
	byMember := make([]gin.H, 0, len(g.ByMember))
	for _, m := range g.ByMember {
		byMember = append(byMember, gin.H{
			"masked":       m.Masked,
			"label":        m.Label,
			"role":         m.Role,
			"unmeasurable": m.Unmeasurable,
			"requests":     m.Requests,
			"billed_cny":   m.BilledCNY,
			"share":        share(m.BilledCNY, g.BilledCNY),
			// *_ledger_cny is money that actually moved (workspace_tx /
			// wallet_tx) while billed_cny is the request log's view; the two
			// books can disagree at the edges and the difference is reported
			// once, as unitemised_cny. The "_ledger_" in the name is load
			// bearing: /api/team/usage publishes a personal_billed_usd that is
			// billed minus pool — a derivation from the log, equal to this only
			// where both books are complete. They are here for the console's
			// split view and deliberately absent from the PDF's member table,
			// where a reader would try to add them up.
			"pool_ledger_cny":     m.PoolLedgerCNY,
			"personal_ledger_cny": m.PersonalLedgerCNY,
		})
	}
	byModel := make([]gin.H, 0, len(g.ByModel))
	for _, m := range g.ByModel {
		byModel = append(byModel, gin.H{
			"model": m.Model, "requests": m.Requests, "billed_cny": m.BilledCNY,
		})
	}
	detailLines := int64(len(g.Lines))
	if detailLines == 0 {
		// The preview does not collect rows, so it reports what a full export
		// would print rather than what it happens to hold.
		detailLines = min64(g.Requests, int64(statement.MaxDetailLines))
	}
	notes := g.Notes
	if notes == nil {
		notes = []string{}
	}
	return gin.H{
		"workspace":   gin.H{"id": g.WorkspaceID, "name": g.WorkspaceName},
		"from":        g.FromDay,
		"to":          g.ToDay,
		"timezone":    g.TZName,
		"cny_per_usd": g.CNYPerUSD,

		"requests":       g.Requests,
		"billed_cny":     g.BilledCNY,
		"unitemised_cny": g.UnitemisedCNY,
		"charged_cny":    g.ChargedCNY,

		"lifetime_requests":   g.LifetimeRequests,
		"lifetime_billed_cny": g.LifetimeBilledCNY,
		"lifetime_days":       g.LifetimeDays,

		"member_count": g.MemberCount(),
		"by_member":    byMember,
		"by_model":     byModel,

		"detail_lines": detailLines,
		"truncated":    g.LinesTruncated,
		"partial":      g.Partial,
		"notes":        notes,
	}
}

func share(part, whole float64) float64 {
	if whole <= 0 {
		return 0
	}
	return part / whole
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
