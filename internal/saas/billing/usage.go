package billing

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/sync/singleflight"

	"github.com/wjsoj/CPA-Claude/internal/saas/db"
	"github.com/wjsoj/CPA-Claude/internal/tokenmask"
	"github.com/wjsoj/cc-core/requestlog"
)

// Group usage answers "what did this team actually spend", which the pool
// ledger (workspace_tx) cannot: a member whose daily cap is exhausted — or a
// team that never funded a pool at all and only wants one invoice — pays out of
// their personal wallet, and none of that reaches workspace_tx. The request log
// is the only place both halves are recorded, so everything here reads it and
// keeps the pool figure alongside rather than instead.
//
// Three hard rules govern every query below, all from cc-core's request-log
// index:
//
//   - The window is stated as inclusive day labels (FromDay/ToDay), never as a
//     From/To timestamp pair. Only day labels are answerable from the pre-summed
//     agg_cube; a timestamp pair falls back to a row-by-row scan, measured at
//     ~3.4s against ~30ms on a million-row archive. usageFilter is the only
//     place a Filter is built for that reason, and its shape is pinned by test.
//   - Members are identified by tokenmask.Mask of their token, which is the
//     exact form the log stores. A mask that does not match returns zero rows
//     rather than an error, so getting this wrong is silent.
//   - Anything that queries once *per member* runs only when the index is
//     actually open (LogIndexed). requestlog.Query falls back to scanning the
//     JSONL archive without erroring, and a scan is ~7s for a 90-day window on
//     a production archive — bearable once, forty times over is a minute of
//     wall clock and forty full re-decodes of the archive. The single
//     fleet-wide pass stays either way: it is one query no matter how large the
//     team is, and losing it would take the whole feature down on a deployment
//     that legitimately runs log_index_disabled.

// DayLayout is the wire format for every date parameter on the usage
// endpoints: an inclusive whole day in the request log's display zone. RFC3339
// and epoch timestamps are deliberately rejected — see the window rule above.
const DayLayout = "2006-01-02"

// maxUsageWindowDays bounds a usage query. Request logs are retained 90 days;
// the extra slack lets a caller ask for "the whole retained archive" without
// having to compute the exact edge.
const maxUsageWindowDays = 92

// usageCacheTTL is short enough that a panel refresh shows a fresh charge
// within a minute, long enough that a polling dashboard doesn't re-run the
// per-member fan-out on every tick.
const usageCacheTTL = 30 * time.Second

// usageFanoutConcurrency caps the per-member breakdown queries in flight. Each
// is an independent SQLite read on the shared index; a handful in parallel
// hides the round-trip latency without turning a 40-member team into 40
// concurrent readers.
const usageFanoutConcurrency = 4

// maxFanoutMembers bounds how many members a per-member query runs for, the
// same way /api/team/requests has always bounded its own fan-out. Nothing stops
// a workspace from holding hundreds of members, and the cost here is linear in
// that number while the headline totals cost one query regardless. Members are
// taken highest-spend-first, so the ones a bill is actually about are the ones
// detailed; the rest are reported as a truncation rather than dropped silently.
const maxFanoutMembers = 50

// GroupMember is one row of "who counts as this team", decoupled from the
// saas.DB tables so ComputeGroupUsage can be exercised without a database.
type GroupMember struct {
	Token string // full client token; masked here, never returned
	Label string
	Role  string
}

// MemberUsage is one member's real spend over the window, with the pool-funded
// part broken out. Personal spend is Agg.BilledUSD - PoolBilledUSD.
type MemberUsage struct {
	Masked string
	Label  string
	Role   string
	Agg    requestlog.Aggregate
	// PoolBilledUSD is what workspace_tx says the shared pool covered, and
	// PersonalLedgerUSD what wallet_tx says the member's own wallet was debited.
	// Both come from a different book than Agg (a transaction ledger vs an
	// appended log), so they are reported side by side and never reconciled
	// here. Both are keyed by full token upstream, so an unmeasurable member
	// still carries their real ledger figures.
	PoolBilledUSD     float64
	PersonalLedgerUSD float64
	// Unmeasurable marks a member whose token is too short to mask
	// distinguishably (tokenmask.Opaque). Their rows are indistinguishable from
	// every other such token's, so Agg is left zero rather than reporting a
	// figure that includes someone else's spend.
	Unmeasurable bool
}

// ModelUsage / DayUsage are the group-scoped breakdowns. They exist only when
// the caller asks for them, because a group-scoped cross-tab is not something
// one fleet-wide query can produce (see ComputeGroupUsage).
type ModelUsage struct {
	Model string
	Agg   requestlog.Aggregate
}

type DayUsage struct {
	Day string
	Agg requestlog.Aggregate
}

// GroupUsage is the shared shape behind both the team console and the operator
// panel — one producer so the two can never disagree about a team's bill.
type GroupUsage struct {
	FromDay  string
	ToDay    string
	Timezone string
	// Total is the sum over ByMember, not the log's fleet-wide summary.
	Total requestlog.Aggregate
	// PoolBilledUSD and PersonalLedgerUSD are the roster's ledger halves. They
	// exclude members whose spend could not be measured in the log, so that
	// "pool + personal" and Total describe the same set of people — see the note
	// in ComputeGroupUsage. The excluded figures survive on the member rows.
	PoolBilledUSD     float64
	PersonalLedgerUSD float64
	ByMember          []MemberUsage
	ByModel           []ModelUsage
	ByDay             []DayUsage
	// Partial reports that the numbers below are known to be incomplete —
	// the log query failed, or some members are unmeasurable. Usage is an
	// enrichment on a management screen; degrading is better than a 500.
	Partial bool
	Notes   []string
}

// GroupUsageInput separates "who is in the group" and "what did the pool cover"
// (both the caller's SQL) from "what did they spend" (this file's request-log
// queries).
type GroupUsageInput struct {
	LogDir string
	// LogIndexed reports that the request-log SQL index is open for LogDir.
	// False turns off every per-member query (see the file header); the
	// fleet-wide pass runs regardless.
	LogIndexed bool
	FromDay    string
	ToDay      string
	Members    []GroupMember
	// PoolByToken / PersonalByToken are the window's ledger halves keyed by full
	// token, positive USD. Callers fetch each in one grouped query; a missing
	// key means zero. Keyed by full token and not by mask on purpose: masks
	// collide for short tokens, and a collision here would credit one member
	// with another's charges.
	PoolByToken     map[string]float64
	PersonalByToken map[string]float64
	// WantBreakdown adds ByModel/ByDay. It costs one extra query per member
	// with traffic, so the member list (which needs totals only) leaves it off.
	WantBreakdown bool
}

// ComputeGroupUsage returns the group's spend over the window.
//
// The first query is deliberately unfiltered: the cube answers every
// client_token bucket at once for the same cost as answering one, so a group of
// any size costs a single round trip and the members with no traffic identify
// themselves for free. Only ByClient is group-scoped in that result — Summary,
// ByModel and ByDay are fleet-wide — so the total is summed from the members'
// buckets, and the breakdowns (when asked for) need a second pass restricted to
// each member that actually has rows.
func ComputeGroupUsage(in GroupUsageInput) (*GroupUsage, error) {
	from, to, err := normalizeWindow(in.FromDay, in.ToDay)
	if err != nil {
		return nil, err
	}
	out := &GroupUsage{
		FromDay:  from,
		ToDay:    to,
		Timezone: requestlog.BucketLocation().String(),
		ByMember: make([]MemberUsage, 0, len(in.Members)),
		ByModel:  []ModelUsage{},
		ByDay:    []DayUsage{},
	}
	if in.LogDir == "" {
		out.Partial = true
		out.Notes = append(out.Notes, "请求日志未启用，无法统计实际消费")
	}

	byClient := map[string]requestlog.Aggregate{}
	if in.LogDir != "" {
		res, qerr := cachedUsageQuery(in.LogDir, from, to, "")
		if qerr != nil {
			out.Partial = true
			out.Notes = append(out.Notes, "读取请求日志失败，用量可能不完整")
		} else {
			byClient = res.ByClient
		}
	}

	unmeasurable := 0
	// A mask can repeat only when two members share the opaque form; a real
	// mask carries 7 random characters, so double-counting a genuine bucket
	// isn't a case that arises. The ledger halves are read by full token, which
	// has no such caveat at all.
	for _, m := range in.Members {
		mask := tokenmask.Mask(m.Token)
		mu := MemberUsage{
			Masked:            mask,
			Label:             m.Label,
			Role:              m.Role,
			PoolBilledUSD:     in.PoolByToken[m.Token],
			PersonalLedgerUSD: in.PersonalByToken[m.Token],
		}
		if mask == tokenmask.Opaque {
			mu.Unmeasurable = true
			unmeasurable++
		} else {
			mu.Agg = byClient[mask]
			// Only measurable members contribute to the group figures. Counting
			// an unmeasurable member's pool draw while their log total is
			// necessarily zero would make the group's pool exceed its own total
			// spend, and the derived "personal" half — total minus pool — go
			// negative and clamp to zero, erasing real spend on a screen whose
			// entire purpose is to stop the panel lying.
			out.Total = addAggregate(out.Total, mu.Agg)
			out.PoolBilledUSD += mu.PoolBilledUSD
			out.PersonalLedgerUSD += mu.PersonalLedgerUSD
		}
		out.ByMember = append(out.ByMember, mu)
	}
	if unmeasurable > 0 {
		out.Partial = true
		out.Notes = append(out.Notes, fmt.Sprintf("%d 个成员的令牌过短，脱敏后无法在请求日志中区分，其用量与账单流水均未计入合计（明细行仍保留）", unmeasurable))
	}
	// Even with every member measurable the two books can disagree — the ledger
	// settles a request the log lost, or a charge lands either side of the range
	// edge. Say so rather than let the derived personal half clamp to zero and
	// present a pool larger than the total it is supposedly part of.
	if out.PoolBilledUSD > out.Total.BilledUSD+ledgerVsLogEpsilonUSD {
		out.Partial = true
		out.Notes = append(out.Notes, "组池流水大于请求日志统计到的总消费，个人钱包部分无法推算（账本与日志不一致）")
	}
	sortMemberUsage(out.ByMember)

	if in.WantBreakdown {
		switch {
		case in.LogDir == "":
			out.ByDay = zeroFillDays(from, to, nil)
		case !in.LogIndexed:
			// One scan per member, and no early exit to soften it — see the
			// file header. The totals above are already correct and cost one
			// query; only the cross-tabs are given up.
			out.ByDay = zeroFillDays(from, to, nil)
			out.Partial = true
			out.Notes = append(out.Notes, "请求日志索引不可用，模型/日期分布暂无法统计（合计与成员用量不受影响）")
		default:
			bd, failed, capped := groupBreakdown(in.LogDir, from, to, out.ByMember)
			out.ByModel = bd.models()
			out.ByDay = bd.days(from, to)
			if failed {
				out.Partial = true
				out.Notes = append(out.Notes, "部分成员的明细统计失败，模型/日期分布可能不完整")
			}
			if capped > 0 {
				out.Partial = true
				out.Notes = append(out.Notes, fmt.Sprintf("成员过多，模型/日期分布仅统计消费最高的 %d 人，另有 %d 人未计入（合计与成员用量不受影响）", maxFanoutMembers, capped))
			}
		}
	}
	return out, nil
}

// ledgerVsLogEpsilonUSD is the float noise floor for comparing a ledger sum
// against a log sum. Charges quantize to 1e-8; anything under a hundredth of a
// cent is arithmetic, not a discrepancy.
const ledgerVsLogEpsilonUSD = 1e-4

// breakdown accumulates the group-scoped cross-tabs the fleet-wide query can't
// express.
type breakdown struct {
	model map[string]requestlog.Aggregate
	day   map[string]requestlog.Aggregate
}

func (b breakdown) models() []ModelUsage {
	out := make([]ModelUsage, 0, len(b.model))
	for m, a := range b.model {
		out = append(out, ModelUsage{Model: m, Agg: a})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Agg.BilledUSD != out[j].Agg.BilledUSD {
			return out[i].Agg.BilledUSD > out[j].Agg.BilledUSD
		}
		return out[i].Model < out[j].Model
	})
	return out
}

func (b breakdown) days(from, to string) []DayUsage { return zeroFillDays(from, to, b.day) }

// groupBreakdown queries each member that the fleet-wide pass already proved
// has rows, and merges the per-member ByModel/ByDay. Members with no traffic
// are skipped: the first query is proof there is nothing to find, so a large
// mostly-idle team costs almost nothing here. It returns how many members the
// fan-out cap left out, so the caller can say so.
func groupBreakdown(dir, from, to string, members []MemberUsage) (breakdown, bool, int) {
	b := breakdown{model: map[string]requestlog.Aggregate{}, day: map[string]requestlog.Aggregate{}}
	active, capped := activeMasks(members)
	if len(active) == 0 {
		return b, false, capped
	}
	var (
		mu     sync.Mutex
		failed bool
		wg     sync.WaitGroup
	)
	sem := make(chan struct{}, usageFanoutConcurrency)
	for _, mask := range active {
		wg.Add(1)
		go func(mask string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			res, err := cachedUsageQuery(dir, from, to, mask)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failed = true
				return
			}
			for k, v := range res.ByModel {
				b.model[k] = addAggregate(b.model[k], v)
			}
			for k, v := range res.ByDay {
				b.day[k] = addAggregate(b.day[k], v)
			}
		}(mask)
	}
	wg.Wait()
	return b, failed, capped
}

// activeMasks picks the members a per-member query is worth running for, in the
// spend-descending order ComputeGroupUsage already sorted them into, and reports
// how many were left out by maxFanoutMembers. Members the fleet-wide pass proved
// have no rows are free to skip; unmeasurable ones have no mask to query with.
func activeMasks(members []MemberUsage) ([]string, int) {
	active := make([]string, 0, len(members))
	capped := 0
	for _, m := range members {
		if m.Unmeasurable || m.Agg.Count == 0 {
			continue
		}
		if len(active) >= maxFanoutMembers {
			capped++
			continue
		}
		active = append(active, m.Masked)
	}
	return active, capped
}

// MemberSpend is the per-member total the member list needs: today's and this
// month's real spend, keyed by masked token.
type MemberSpend struct {
	Day   requestlog.Aggregate
	Month requestlog.Aggregate
	// Measurable is false for tokens that mask to tokenmask.Opaque.
	Measurable bool
}

// MemberSpendToDate returns each member's real spend for the current day and
// the current month-to-date, in the request log's display zone.
//
// Two fleet-wide queries answer the whole team regardless of its size — the
// window can't be sliced out of one month-long query, because only the
// per-client bucket is group-scoped and ByDay is fleet-wide.
//
// ok=false means the log could not be read; callers should show the pool
// figures alone rather than fail the member list.
func MemberSpendToDate(logDir string, members []GroupMember) (map[string]MemberSpend, bool) {
	out := make(map[string]MemberSpend, len(members))
	for _, m := range members {
		mask := tokenmask.Mask(m.Token)
		out[mask] = MemberSpend{Measurable: mask != tokenmask.Opaque}
	}
	if logDir == "" {
		return out, false
	}
	now := time.Now().In(requestlog.BucketLocation())
	today := now.Format(DayLayout)
	monthStart := now.Format("2006-01") + "-01"

	// A one-member call — the row a member add/edit renders — filters on that
	// member instead of asking for the whole fleet's buckets. Both are one query
	// each, but the filtered pair is answered off the member's own slice of the
	// cube, and this path also runs on the un-indexed scan where "the whole
	// fleet" means every row in the window.
	only := ""
	if len(out) == 1 {
		for mask, s := range out {
			if s.Measurable {
				only = mask
			}
		}
	}
	dayRes, err := cachedUsageQuery(logDir, today, today, only)
	if err != nil {
		return out, false
	}
	monthRes, err := cachedUsageQuery(logDir, monthStart, today, only)
	if err != nil {
		return out, false
	}
	for mask, s := range out {
		if !s.Measurable {
			continue
		}
		s.Day = dayRes.ByClient[mask]
		s.Month = monthRes.ByClient[mask]
		out[mask] = s
	}
	return out, true
}

// ---- window parsing ----

// ErrBadWindow marks a caller mistake in the from/to parameters, so the HTTP
// layer can answer 400 rather than 500.
var ErrBadWindow = errors.New("bad usage window")

var errBadDay = fmt.Errorf("%w: date must be YYYY-MM-DD in the display timezone", ErrBadWindow)

// ParseDay validates a wire date. It refuses anything that isn't a bare day
// label, because a timestamp silently costs a full row scan (see the file
// header) — better a 400 than a query that is 100x slower.
func ParseDay(s string) (string, error) {
	t, err := time.ParseInLocation(DayLayout, s, requestlog.BucketLocation())
	if err != nil {
		return "", errBadDay
	}
	return t.Format(DayLayout), nil
}

// DefaultUsageWindow is the window the usage endpoints assume when the caller
// gives none: the trailing 30 days ending today.
func DefaultUsageWindow() (from, to string) {
	now := time.Now().In(requestlog.BucketLocation())
	return now.AddDate(0, 0, -29).Format(DayLayout), now.Format(DayLayout)
}

func normalizeWindow(from, to string) (string, string, error) {
	if from == "" || to == "" {
		df, dt := DefaultUsageWindow()
		if from == "" {
			from = df
		}
		if to == "" {
			to = dt
		}
	}
	f, err := ParseDay(from)
	if err != nil {
		return "", "", err
	}
	t, err := ParseDay(to)
	if err != nil {
		return "", "", err
	}
	if f > t {
		return "", "", fmt.Errorf("%w: from must not be after to", ErrBadWindow)
	}
	if days := countDays(f, t); days > maxUsageWindowDays {
		return "", "", fmt.Errorf("%w: %d days requested, max %d", ErrBadWindow, days, maxUsageWindowDays)
	}
	return f, t, nil
}

func countDays(from, to string) int {
	f, err1 := time.ParseInLocation(DayLayout, from, time.UTC)
	t, err2 := time.ParseInLocation(DayLayout, to, time.UTC)
	if err1 != nil || err2 != nil {
		return 0
	}
	return int(t.Sub(f).Hours()/24) + 1
}

// WindowBounds converts an inclusive day window into the half-open instant pair
// [start, end) in the request log's display zone. Used to line the pool ledger
// up with the same window the log query uses — note the pool's own daily/monthly
// caps are fixed at UTC+8 regardless of display_timezone, so the two agree only
// while display_timezone is Asia/Shanghai (the default). That's a known,
// deliberate gap: moving the cap boundaries would move billing boundaries.
func WindowBounds(from, to string) (time.Time, time.Time, error) {
	loc := requestlog.BucketLocation()
	f, err := time.ParseInLocation(DayLayout, from, loc)
	if err != nil {
		return time.Time{}, time.Time{}, errBadDay
	}
	t, err := time.ParseInLocation(DayLayout, to, loc)
	if err != nil {
		return time.Time{}, time.Time{}, errBadDay
	}
	return f, t.AddDate(0, 0, 1), nil
}

func zeroFillDays(from, to string, have map[string]requestlog.Aggregate) []DayUsage {
	loc := requestlog.BucketLocation()
	f, err := time.ParseInLocation(DayLayout, from, loc)
	if err != nil {
		return []DayUsage{}
	}
	t, err := time.ParseInLocation(DayLayout, to, loc)
	if err != nil {
		return []DayUsage{}
	}
	out := []DayUsage{}
	for d := f; !d.After(t); d = d.AddDate(0, 0, 1) {
		label := d.Format(DayLayout)
		out = append(out, DayUsage{Day: label, Agg: have[label]})
	}
	return out
}

func sortMemberUsage(ms []MemberUsage) {
	sort.Slice(ms, func(i, j int) bool {
		if ms[i].Agg.BilledUSD != ms[j].Agg.BilledUSD {
			return ms[i].Agg.BilledUSD > ms[j].Agg.BilledUSD
		}
		return ms[i].Masked < ms[j].Masked
	})
}

func addAggregate(a, b requestlog.Aggregate) requestlog.Aggregate {
	a.Count += b.Count
	a.InputTokens += b.InputTokens
	a.OutputTokens += b.OutputTokens
	a.CacheReadTokens += b.CacheReadTokens
	a.CacheCreateTokens += b.CacheCreateTokens
	a.CacheCreate1hTokens += b.CacheCreate1hTokens
	a.CostUSD += b.CostUSD
	a.BilledUSD += b.BilledUSD
	a.Errors += b.Errors
	a.TotalDurationMs += b.TotalDurationMs
	return a
}

// ---- query cache ----

// usageResult holds only the aggregate maps; entries are never requested here
// (Limit is 1 precisely so the query returns aggregates and no page). The maps
// are shared between concurrent readers and must be treated as read-only —
// Aggregate is a value type, so reading a bucket copies it.
type usageResult struct {
	// Summary is the filter's own total. It equals ByClient[mask] on a
	// single-member query and the whole fleet's spend on an unfiltered one, so
	// only the filtered callers may read it.
	Summary  requestlog.Aggregate
	ByClient map[string]requestlog.Aggregate
	ByModel  map[string]requestlog.Aggregate
	ByDay    map[string]requestlog.Aggregate
}

// usageFilter builds every request-log query this package makes. It is a
// separate function so the one rule that costs two orders of magnitude when
// broken — day labels, never a From/To timestamp pair — is stated once and can
// be asserted by test.
//
// Limit 1, never PageOnly: PageOnly zeroes exactly the aggregates this whole
// file reads. One entry row is answered off an index and costs nothing.
func usageFilter(dir, fromDay, toDay, clientToken string) requestlog.Filter {
	return requestlog.Filter{
		Dir:         dir,
		FromDay:     fromDay,
		ToDay:       toDay,
		ClientToken: clientToken,
		Limit:       1,
	}
}

type usageCacheEntry struct {
	res *usageResult
	at  time.Time
}

var usageQueryCache = struct {
	mu sync.Mutex
	m  map[string]usageCacheEntry
	sf singleflight.Group
}{m: map[string]usageCacheEntry{}}

// cachedUsageQuery runs one aggregate query, deduplicating concurrent callers
// and reusing the answer briefly. Both the team console and the operator panel
// poll, and the fleet-wide query is identical for every group — so the cache is
// keyed on the query, not on the workspace.
func cachedUsageQuery(dir, fromDay, toDay, clientToken string) (*usageResult, error) {
	key := dir + "\x00" + fromDay + "\x00" + toDay + "\x00" + clientToken
	usageQueryCache.mu.Lock()
	if e, ok := usageQueryCache.m[key]; ok && time.Since(e.at) < usageCacheTTL {
		usageQueryCache.mu.Unlock()
		return e.res, nil
	}
	usageQueryCache.mu.Unlock()

	v, err, _ := usageQueryCache.sf.Do(key, func() (any, error) {
		res, err := requestlog.Query(usageFilter(dir, fromDay, toDay, clientToken))
		if err != nil {
			return nil, err
		}
		out := &usageResult{Summary: res.Summary, ByClient: res.ByClient, ByModel: res.ByModel, ByDay: res.ByDay}
		usageQueryCache.mu.Lock()
		usageQueryCache.m[key] = usageCacheEntry{res: out, at: time.Now()}
		evictExpiredUsageEntries()
		usageQueryCache.mu.Unlock()
		return out, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(*usageResult), nil
}

// usageCacheMaxEntries bounds the cache. The key space is (window x member), so
// one console session that clicks through a handful of date presets on a
// forty-member team fills hundreds of keys in seconds — the limit has to be
// well clear of that or the eviction runs constantly.
const usageCacheMaxEntries = 2048

// evictExpiredUsageEntries drops entries past their TTL. Callers must hold the
// cache mutex.
//
// It sweeps by age rather than clearing the map, which is what an earlier
// version did: a fan-out inserts one key per member at once, so a wholesale
// clear triggered by the last member of a team also threw away the fleet-wide
// entry every group query starts from — the cache went cold exactly when the
// concurrency it exists for showed up. With a 30s TTL a sweep almost always
// frees the whole map; if it somehow doesn't, going a little over the bound
// beats evicting entries that are still being read this second.
func evictExpiredUsageEntries() {
	if len(usageQueryCache.m) <= usageCacheMaxEntries {
		return
	}
	for k, e := range usageQueryCache.m {
		if time.Since(e.at) >= usageCacheTTL {
			delete(usageQueryCache.m, k)
		}
	}
}

// BuildGroupUsage assembles one workspace's usage from both books: the request
// log (real spend, via ComputeGroupUsage) and workspace_tx (the pool's share).
// Both the team console and the operator panel call this, so the two views of a
// team's bill are the same numbers by construction.
// GroupUsageQuery is what BuildGroupUsage needs from its caller. It is a struct
// rather than a parameter list because the two flags in it (which log directory,
// and whether that directory has a working index) decide how expensive the call
// is, and a bare bool in a seven-argument call is exactly the thing a later
// caller copies wrong.
type GroupUsageQuery struct {
	Wallets     *db.DB
	LogDir      string
	LogIndexed  bool
	WorkspaceID int64
	FromDay     string
	ToDay       string
	// Label resolves a client token to its display name; may be nil.
	Label func(string) string
}

func BuildGroupUsage(ctx context.Context, q GroupUsageQuery) (*GroupUsage, error) {
	from, to, err := normalizeWindow(q.FromDay, q.ToDay)
	if err != nil {
		return nil, err
	}
	ms, err := q.Wallets.ListMembers(ctx, q.WorkspaceID)
	if err != nil {
		return nil, err
	}
	members := make([]GroupMember, 0, len(ms))
	for _, m := range ms {
		l := ""
		if q.Label != nil {
			l = q.Label(m.Token)
		}
		members = append(members, GroupMember{Token: m.Token, Label: l, Role: m.Role})
	}
	in := GroupUsageInput{
		LogDir:        q.LogDir,
		LogIndexed:    q.LogIndexed,
		FromDay:       from,
		ToDay:         to,
		Members:       members,
		WantBreakdown: true,
	}
	// The ledger halves are an enrichment on top of the real-spend figures; if
	// they can't be read the usage view still stands, minus the pool split.
	var ledgerErr error
	if start, end, berr := WindowBounds(from, to); berr == nil {
		in.PoolByToken, ledgerErr = q.Wallets.MemberPoolSpendBetween(ctx, q.WorkspaceID, start, end)
		if ledgerErr == nil {
			in.PersonalByToken, ledgerErr = q.Wallets.MemberPersonalSpendBetween(ctx, q.WorkspaceID, start, end)
		}
	}
	gu, err := ComputeGroupUsage(in)
	if err != nil {
		return nil, err
	}
	if ledgerErr != nil {
		gu.Partial = true
		gu.Notes = append(gu.Notes, "读取账单流水失败，池消费/个人消费拆分不可用")
	}
	return gu, nil
}

// GroupUsageJSON renders the wire body. Arrays are pre-sorted and by_day is
// zero-filled server-side so no client has to reproduce the ordering — two
// clients rendering the same team must not draw different charts.
func GroupUsageJSON(gu *GroupUsage) gin.H {
	byMember := make([]gin.H, 0, len(gu.ByMember))
	for _, m := range gu.ByMember {
		row := gin.H{
			"masked":          m.Masked,
			"label":           m.Label,
			"role":            m.Role,
			"unmeasurable":    m.Unmeasurable,
			"pool_billed_usd": m.PoolBilledUSD,
			// personal_billed_usd is derived — this member's log total minus
			// what the pool ledger covered — and is NOT the same quantity as the
			// team statement's personal_ledger_cny, which is what wallet_tx
			// actually debited (see teamStatementJSON). The two agree only when
			// both books are complete; they differ outright wherever the log
			// records spend no wallet row exists for.
			//
			// Clamped at zero rather than reconciled: a real reconciliation
			// belongs in a statement, and ComputeGroupUsage has already flagged
			// the result partial with a note when a clamp is in play.
			"personal_billed_usd": nonNegative(m.Agg.BilledUSD - m.PoolBilledUSD),
			// The ledger's own figure for this member, carried alongside so the
			// console can show who a charge belonged to without deriving it.
			"personal_ledger_usd": m.PersonalLedgerUSD,
		}
		mergeAggregate(row, m.Agg)
		byMember = append(byMember, row)
	}
	byModel := make([]gin.H, 0, len(gu.ByModel))
	for _, m := range gu.ByModel {
		row := gin.H{"model": m.Model}
		mergeAggregate(row, m.Agg)
		byModel = append(byModel, row)
	}
	byDay := make([]gin.H, 0, len(gu.ByDay))
	for _, d := range gu.ByDay {
		row := gin.H{"day": d.Day}
		mergeAggregate(row, d.Agg)
		byDay = append(byDay, row)
	}
	total := gin.H{}
	mergeAggregate(total, gu.Total)
	notes := gu.Notes
	if notes == nil {
		notes = []string{}
	}
	return gin.H{
		"from":                gu.FromDay,
		"to":                  gu.ToDay,
		"timezone":            gu.Timezone,
		"currency":            "USD",
		"partial":             gu.Partial,
		"notes":               notes,
		"total":               total,
		"pool_billed_usd":     gu.PoolBilledUSD,
		"personal_billed_usd": nonNegative(gu.Total.BilledUSD - gu.PoolBilledUSD),
		"personal_ledger_usd": gu.PersonalLedgerUSD,
		"by_member":           byMember,
		"by_model":            byModel,
		"by_day":              byDay,
	}
}

// mergeAggregate writes the counter fields onto a row. Amounts stay raw
// float64: rounding is the display layer's job, and rounding per row would make
// the rows stop summing to the total.
func mergeAggregate(row gin.H, a requestlog.Aggregate) {
	row["requests"] = a.Count
	row["billed_usd"] = a.BilledUSD
	row["cost_usd"] = a.CostUSD
	row["input_tokens"] = a.InputTokens
	row["output_tokens"] = a.OutputTokens
	row["cache_read_tokens"] = a.CacheReadTokens
	row["cache_create_tokens"] = a.CacheCreateTokens
	row["errors"] = a.Errors
}

func nonNegative(v float64) float64 {
	if v < 0 {
		return 0
	}
	return v
}
