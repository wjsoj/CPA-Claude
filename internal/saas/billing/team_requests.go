package billing

import (
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/wjsoj/CPA-Claude/internal/saas/db"
	"github.com/wjsoj/CPA-Claude/internal/tokenmask"
	"github.com/wjsoj/cc-core/requestlog"
)

// /api/team/requests is the drill-down under /api/team/usage: the individual
// request rows behind a member's or a team's number, newest first.
//
// It is deliberately NOT a second source of truth for the totals. The rows are
// capped and PageOnly skips the aggregates entirely, so summing what comes back
// undercounts by construction — /usage and /statement own the arithmetic, this
// endpoint owns "show me what that spend was made of".
//
// The three rules in usage.go's header govern the queries here too — day-label
// windows, masked-token identity, and per-member fan-out only when the index is
// open. This surface adds one of its own: a member whose mask is
// tokenmask.Opaque is never queried, because that mask is shared by every short
// token on the relay and would read another tenant's rows (see
// resolveRosterMember).

const (
	// teamRequestsDefaultLimit is what the console asks for when it says nothing.
	teamRequestsDefaultLimit = 200
	// teamRequestsMaxLimit bounds one page. The merge below keeps at most
	// ~2×limit rows alive at any moment regardless of how many members are
	// fanned out, so this is the memory bound rather than limit×members.
	teamRequestsMaxLimit = 500
)

// requests answers the drill-down for this workspace's roster.
//
// Query parameters:
//
//   - from/to — inclusive YYYY-MM-DD day labels in the display timezone, the
//     same contract as /usage and for the same reason (see usage.go's header:
//     only a day label is answerable from the pre-summed cube). Absent means the
//     whole retained log, which is what this endpoint has always returned.
//   - member — one masked token, checked against the caller's own roster before
//     it reaches a query. See resolveRosterMember.
//   - limit — page size, defaulting to teamRequestsDefaultLimit and capped at
//     teamRequestsMaxLimit.
func (t *TeamHandler) requests(c *gin.Context) {
	tz := requestlog.BucketLocation().String()
	if t.LogDir == "" {
		c.JSON(http.StatusOK, gin.H{"requests": []any{}, "truncated": false, "timezone": tz})
		return
	}
	fromDay, toDay, ok := t.requestsWindow(c)
	if !ok {
		return
	}
	limit, ok := teamRequestsLimit(c)
	if !ok {
		return
	}
	member := c.Query("member")
	// Without the SQL index requestlog.Query silently scans the JSONL archive,
	// and PageOnly's early exit only helps a member who *has* rows in the
	// window — a quiet member costs a full re-decode of every file in it
	// (measured ~2s for 30 days, ~6.4s unbounded, on a million-row archive).
	// One of those is the same price /usage's fleet-wide pass already pays, so
	// a single-member drill-down still runs; fanning it out across the roster
	// is up to fifty of them and is refused the way /statement refuses, rather
	// than parked on a browser for a minute.
	if !t.LogIndexed && member == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": "请求日志索引不可用，无法按成员汇总明细；请指定单个成员查看",
		})
		return
	}

	ws := t.ws(c)
	ms, err := t.DB.ListMembers(c.Request.Context(), ws.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ms, cappedMembers, opaque := t.resolveRosterMember(ms, member, fromDay, toDay)

	rows, truncated := t.mergeMemberRequests(ms, fromDay, toDay, limit)
	c.JSON(http.StatusOK, gin.H{
		"requests": rows,
		// truncated says "there is more behind this page", so the console can
		// offer a narrower window instead of letting a user believe a busy team
		// made exactly `limit` requests this month.
		"truncated": truncated || cappedMembers,
		// How many roster members were left out because their token masks to
		// tokenmask.Opaque. Without it an empty page is ambiguous between "no
		// requests" and "we refused to look" — the silent zero this whole
		// surface exists to remove. /usage carries the same fact as a note.
		"unmeasurable_members": opaque,
		// The zone the from/to labels — and every ts rendered against them — are
		// cut in, so the console never has to guess whose midnight it is.
		"timezone": tz,
	})
}

// requestsWindow parses from/to. Both are optional; either one alone is honoured
// as an open-ended bound, matching the log query's own semantics.
func (t *TeamHandler) requestsWindow(c *gin.Context) (string, string, bool) {
	from, to := "", ""
	for _, p := range []struct {
		name string
		dst  *string
	}{{"from", &from}, {"to", &to}} {
		v := c.Query(p.name)
		if v == "" {
			continue
		}
		d, err := ParseDay(v)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return "", "", false
		}
		*p.dst = d
	}
	if from != "" && to != "" && from > to {
		c.JSON(http.StatusBadRequest, gin.H{"error": "from must not be after to"})
		return "", "", false
	}
	return from, to, true
}

func teamRequestsLimit(c *gin.Context) (int, bool) {
	v := c.Query("limit")
	if v == "" {
		return teamRequestsDefaultLimit, true
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "limit must be a positive integer"})
		return 0, false
	}
	if n > teamRequestsMaxLimit {
		n = teamRequestsMaxLimit
	}
	return n, true
}

// resolveRosterMember decides which members are actually queried: the one named
// by ?member= when it is given, otherwise the highest-spending maxFanoutMembers
// of the roster. It reports whether the cap dropped anyone, and how many members
// were excluded for having no usable mask.
//
// Two rules make up the boundary, and both are needed:
//
//   - Ownership. The masked token is the only identifier on the team API that a
//     caller constructs and that ends up as a request-log query key, and masks
//     are visible to anyone who can read a member list — so passing it straight
//     through would turn this endpoint into a cross-tenant read of any team
//     whose masks you have seen. It is resolved against the caller's own roster;
//     an unknown mask matches nobody and the answer is empty.
//   - Opacity. tokenmask.Opaque ("***") is what every token too short to mask
//     collapses to, so it is not an identity: querying it returns the rows of
//     every short token on the relay, whoever they belong to. Ownership alone
//     does not catch that — one short token anywhere on the caller's own roster
//     would "own" the mask and let the whole fleet's short-token traffic through
//     it. So such members are dropped from the fan-out and ?member=*** resolves
//     to nobody, which is the same invariant /usage states as Unmeasurable.
func (t *TeamHandler) resolveRosterMember(ms []*db.WorkspaceMember, masked, fromDay, toDay string) ([]*db.WorkspaceMember, bool, int) {
	measurable := ms[:0:0]
	opaque := 0
	for _, m := range ms {
		if maskToken(m.Token) == tokenmask.Opaque {
			opaque++
			continue
		}
		measurable = append(measurable, m)
	}
	if masked != "" {
		// Matched against the measurable roster, not the whole one, which is
		// what makes ?member=*** resolve to nobody: the opaque members were
		// already dropped above, so there is nothing left for that mask to name.
		only := ms[:0:0]
		for _, m := range measurable {
			if maskToken(m.Token) == masked {
				only = append(only, m)
			}
		}
		// One member is one query: no fan-out to cap, and no reason to read the
		// other 49 members' pages only to throw them away.
		return only, false, opaque
	}
	if len(measurable) > maxFanoutMembers {
		return t.topSpenders(measurable, fromDay, toDay), true, opaque
	}
	return measurable, false, opaque
}

// topSpenders orders the roster by what it spent over the window and keeps the
// first maxFanoutMembers, matching activeMasks' rule on the /usage side: the
// members a bill is actually about are the ones detailed. Taking ListMembers'
// order instead would keep the earliest-joined fifty, so the current period's
// biggest spender — the row on top of the member table the user just clicked
// through from — could be absent from their own team's page.
//
// The ranking costs one fleet-wide query, shared with /usage through the same
// short-lived cache. If it fails the roster order stands: a page of the wrong
// fifty members is a worse answer than an unranked one, but it is a far better
// answer than an error, and truncation is reported either way.
func (t *TeamHandler) topSpenders(ms []*db.WorkspaceMember, fromDay, toDay string) []*db.WorkspaceMember {
	res, err := cachedUsageQuery(t.LogDir, fromDay, toDay, "")
	if err == nil {
		spend := make(map[string]float64, len(ms))
		for _, m := range ms {
			spend[m.Token] = res.ByClient[maskToken(m.Token)].BilledUSD
		}
		// append(s[:0:0], s...) is the clone idiom: a zero-capacity reslice
		// forces a fresh backing array so the sort below cannot reorder the
		// caller's slice. gocritic reads it as a misplaced append.
		ranked := append(ms[:0:0], ms...) //nolint:gocritic // appendAssign: deliberate slice clone
		sort.SliceStable(ranked, func(i, j int) bool {
			return spend[ranked[i].Token] > spend[ranked[j].Token]
		})
		ms = ranked
	}
	return ms[:maxFanoutMembers]
}

// mergeMemberRequests queries each member's newest `limit` rows and merges them
// into the group's newest `limit`.
//
// Each member is asked for the full limit rather than a share of it: the global
// newest N is a subset of the union of the per-member newest N, and only that
// makes the merge exact. Asking for limit/len(members) each would silently drop
// a heavy user's rows whenever they dominate the window — the exact case a
// drill-down is opened for.
//
// The merge trims to `limit` as each member lands rather than after the fan-out,
// so the working set is bounded by the page and not by the roster: 500 rows ×
// 50 members was ~20MB of throwaway JSON objects on a host that runs under
// earlyoom. Correctness is unchanged — the same subset property that justifies
// asking every member for the full limit is what makes an incremental trim
// exact.
//
// A member whose query fails is skipped rather than failing the page: this is a
// browsing surface, not a document, and one unreadable slice of the archive
// should not blank the screen. The reported truncation is a floor, so a skipped
// member can only understate what is behind the page, never claim rows exist
// that don't.
func (t *TeamHandler) mergeMemberRequests(ms []*db.WorkspaceMember, fromDay, toDay string, limit int) ([]gin.H, bool) {
	rate := t.statementRate()
	type entry struct {
		ts  time.Time
		row gin.H
	}
	var (
		mu        sync.Mutex
		all       = make([]entry, 0, limit)
		truncated bool
		wg        sync.WaitGroup
	)
	sem := make(chan struct{}, usageFanoutConcurrency)
	for _, m := range ms {
		masked := maskToken(m.Token)
		label := ""
		if t.TokenLabel != nil {
			label = t.TokenLabel(m.Token)
		}
		wg.Add(1)
		go func(masked, label string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			res, qerr := requestlog.Query(requestlog.Filter{
				Dir:         t.LogDir,
				ClientToken: masked,
				// Day labels, never a From/To timestamp pair — the timestamp form
				// forfeits the pre-summed index and scans row by row (~3.4s against
				// ~30ms on a million-row archive).
				FromDay: fromDay,
				ToDay:   toDay,
				// One row past the page, as a sentinel: a query that returns
				// exactly `limit` cannot say whether the window held more, and
				// reporting truncation on the nose tells a user that rows are
				// missing at the very moment the page is complete.
				Limit: limit + 1,
				// Only res.Entries is read below — skip the per-member aggregates
				// and stop scanning at the newest hits rather than walking the
				// whole window.
				PageOnly: true,
			})
			if qerr != nil {
				return
			}
			rows := res.Entries
			mu.Lock()
			defer mu.Unlock()
			if len(rows) > limit {
				// The sentinel row proves this member alone has more than a page
				// in the window, before the merge trims anything.
				rows = rows[:limit]
				truncated = true
			}
			for _, r := range rows {
				billed := r.BilledOrCost()
				all = append(all, entry{ts: r.TS, row: gin.H{
					"member":        masked,
					"label":         label,
					"ts":            r.TS.Unix(),
					"provider":      r.Provider,
					"model":         r.Model,
					"status":        r.Status,
					"input_tokens":  r.Input,
					"output_tokens": r.Output,
					// BilledOrCost, not the raw BilledUSD field: rows written
					// before the cost/billed split carry the charge in cost_usd
					// alone and would read as free. Retention is 90 days, so both
					// conventions are live at once — and this column is what a
					// member compares against their invoice.
					"billed_usd": billed,
					// Converted at the same rate the team statement uses, so the
					// drill-down and the document a user reconciles it against
					// cannot quote different yuan for the same request.
					"billed_cny": billed * rate,
				}})
			}
			sort.SliceStable(all, func(i, j int) bool { return all[i].ts.After(all[j].ts) })
			if len(all) > limit {
				all = all[:limit]
				truncated = true
			}
		}(masked, label)
	}
	wg.Wait()

	out := make([]gin.H, 0, len(all))
	for _, e := range all {
		out = append(out, e.row)
	}
	return out, truncated
}
