package admin

import (
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"github.com/wjsoj/CPA-Claude/internal/monitor"
	saasdb "github.com/wjsoj/CPA-Claude/internal/saas/db"
	"github.com/wjsoj/cc-core/auth"
	"github.com/wjsoj/cc-core/requestlog"
	"github.com/wjsoj/cc-core/usage"
)

// RegisterStatus mounts the public /status/ SPA + API. Unlike Register(),
// this does not require admin_token — the page is intentionally anonymous.
// Per-token lookups require the full client token as proof of ownership;
// aggregate info is redacted (no emails, no full tokens, no file paths).
func (h *Handler) RegisterStatus(r *gin.Engine) {
	sub, err := fs.Sub(webFS, "web/dist")
	if err != nil {
		log.Errorf("status: failed to scope embed FS: %v", err)
		return
	}
	r.GET("/status", func(c *gin.Context) {
		c.Redirect(http.StatusFound, "/status/")
	})
	r.GET("/status/", func(c *gin.Context) {
		serveAsset(c, sub, "index.html")
	})
	r.GET("/status/assets/*filepath", func(c *gin.Context) {
		p := strings.TrimPrefix(c.Param("filepath"), "/")
		if p == "" {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		serveAsset(c, sub, "assets/"+p)
	})
	r.GET("/status/api/overview", h.handleStatusOverview)
	r.GET("/status/api/monitor", h.handleStatusMonitor)
	r.GET("/status/api/dashboard", h.handleStatusDashboard)
	r.POST("/status/api/query", h.handleStatusQuery)
	r.POST("/status/api/history", h.handleStatusHistory)
	r.POST("/status/api/statement", h.handleStatementPreview)
	r.POST("/status/api/statement.pdf", h.handleStatementPDF)
	log.Info("admin: public /status/ page enabled")
}

// handleStatusMonitor serves the public uptime widget: per-provider live
// capacity (free slot? healthy creds?) merged with the persisted end-to-end
// probe history (90-day daily rollups + 24h timeline). No secrets are exposed
// — only provider name, aggregate health counts, and probe latency/status.
func (h *Handler) handleStatusMonitor(c *gin.Context) {
	if h.mon == nil {
		c.JSON(http.StatusOK, gin.H{"providers": []any{}})
		return
	}
	c.JSON(http.StatusOK, h.mon.GetSnapshot())
}

// Pseudonyms used to anonymize client-token identities on the public
// dashboard. The pool is intentionally wider than the 26 cryptographic
// standbys to reduce collision rate for small- to mid-scale deployments.
var statusPseudonyms = []string{
	"Alice", "Bob", "Carol", "Dave", "Eve", "Frank", "Grace", "Heidi",
	"Ivan", "Judy", "Mallory", "Niaj", "Olivia", "Peggy", "Quentin", "Rupert",
	"Sybil", "Trent", "Uma", "Victor", "Walter", "Xena", "Yvonne", "Zach",
	"Aria", "Blake", "Cleo", "Dax", "Enzo", "Faye", "Gus", "Hana",
	"Iris", "Jace", "Kai", "Luna", "Milo", "Nova", "Otto", "Pia",
	"Quill", "Remy", "Sage", "Tess", "Ulli", "Vera", "Wren", "Yuri",
}

// pseudonymFor maps a stable identifier (masked client token, or a
// display name when the backend has already remapped) to a deterministic
// pseudonym. Collisions are possible for > ~50 distinct identities; we
// accept them — the goal is public obfuscation, not perfect pseudonymity.
// Implementation is FNV-1a 32-bit, inlined to avoid an import just for
// this one call site.
func pseudonymFor(key string) string {
	if key == "" {
		return "Anon"
	}
	var h uint32 = 2166136261
	for i := 0; i < len(key); i++ {
		h ^= uint32(key[i])
		h *= 16777619
	}
	return statusPseudonyms[int(h)%len(statusPseudonyms)]
}

// anonymizeByClient rebuilds the ByClient aggregate map with pseudonymous
// keys. Real display names/masks are never sent over the public wire.
// Collisions merge buckets (Count/Tokens/Cost summed) — acceptable because
// the dashboard shows totals, not identity.
func anonymizeByClient(in map[string]requestlog.Aggregate) map[string]requestlog.Aggregate {
	if len(in) == 0 {
		return in
	}
	out := make(map[string]requestlog.Aggregate, len(in))
	for k, v := range in {
		name := pseudonymFor(k)
		if existing, ok := out[name]; ok {
			existing.Count += v.Count
			existing.InputTokens += v.InputTokens
			existing.OutputTokens += v.OutputTokens
			existing.CacheReadTokens += v.CacheReadTokens
			existing.CacheCreateTokens += v.CacheCreateTokens
			existing.CostUSD += v.CostUSD
			existing.Errors += v.Errors
			existing.TotalDurationMs += v.TotalDurationMs
			out[name] = existing
		} else {
			out[name] = v
		}
	}
	return out
}

// ---- shared credential-health serialization ----

// authHealth is the per-credential health block. It is embedded verbatim into
// every credential object we publish (the admin summary's rows and the public
// overview's), so the panel and the status page parse one shape.
//
// The legacy booleans (healthy / disabled / quota_exceeded / hard_failure) are
// kept alongside it on those structs for backwards compatibility, but `state`
// is the field to read: seven situations do not fit in one bool, and collapsing
// them is what let a channel be painted green the instant its circuit-breaker
// deadline elapsed — before anything had confirmed it recovered.
type authHealth struct {
	// State is one of healthy | half_open | degraded | quota | cooling |
	// hard_failed | disabled.
	State string `json:"state"`
	// Serving is whether the router will offer this credential traffic right
	// now. It is NOT State == healthy: degraded and half-open both serve.
	Serving bool `json:"serving"`
	// Reason is human-facing, non-empty whenever State is not healthy.
	Reason string `json:"reason,omitempty"`
	// RetryAfterSeconds is when this credential is expected back, when that is
	// knowable (cooling / quota). Absent or 0 = unknown.
	RetryAfterSeconds int `json:"retry_after_seconds,omitempty"`
	// ConsecutiveFailures is the raw counter feeding the degraded/hard-fail
	// ladder. Published so the panel can show deterioration as a number rather
	// than only as a colour.
	ConsecutiveFailures int `json:"consecutive_failures"`
	// QuarantineStrikes is the API-key circuit-breaker backoff step. It
	// survives an expired deadline (that is exactly what makes a credential
	// half-open), so a non-zero value with no quarantined_until is meaningful.
	QuarantineStrikes int        `json:"quarantine_strikes"`
	QuarantinedUntil  *time.Time `json:"quarantined_until,omitempty"`
	LastSuccessAt     *time.Time `json:"last_success_at,omitempty"`
}

func newAuthHealth(r auth.HealthReport) authHealth {
	v := authHealth{
		State:               string(r.State),
		Serving:             r.Serving,
		Reason:              r.Reason,
		ConsecutiveFailures: r.ConsecutiveFailures,
		QuarantineStrikes:   r.QuarantineStrikes,
	}
	if v.State == "" {
		v.State = string(auth.HealthHealthy)
	}
	if r.RetryAfter > 0 {
		// Round up: a client that retries at the truncated second is early.
		secs := int((r.RetryAfter + time.Second - 1) / time.Second)
		v.RetryAfterSeconds = secs
	}
	if !r.QuarantineUntil.IsZero() {
		t := r.QuarantineUntil
		v.QuarantinedUntil = &t
	}
	if !r.LastSuccess.IsZero() {
		t := r.LastSuccess
		v.LastSuccessAt = &t
	}
	return v
}

// statusCounts is the pool-wide credential census, published on both the public
// overview and the public dashboard.
//
// The seven state buckets partition the pool exactly: healthy + half_open +
// degraded + quota + cooling + unhealthy + disabled == total. `unhealthy` is
// the hard-failed bucket under its historical name — the old ladder put quota
// and cooling credentials in it too (and, because hard_failure is always false
// for an API key, a cooling key landed in NO bucket, so the buckets summed to
// less than total). `serving` is orthogonal to the partition and overlaps
// healthy/half_open/degraded; it is the count that answers "are we up".
type statusCounts struct {
	Total     int `json:"total"`
	Healthy   int `json:"healthy"`
	HalfOpen  int `json:"half_open"`
	Degraded  int `json:"degraded"`
	Quota     int `json:"quota"`
	Cooling   int `json:"cooling"`
	Unhealthy int `json:"unhealthy"`
	Disabled  int `json:"disabled"`
	Serving   int `json:"serving"`
	OAuth     int `json:"oauth"`
	APIKey    int `json:"apikey"`
	Models    int `json:"models"`
}

// poolCensus is one walk of the credential pool, shared by the overview and the
// dashboard so the two endpoints can never disagree about the same instant.
type poolCensus struct {
	Counts statusCounts
	// Pools is keyed by normalized provider ("anthropic" | "openai").
	Pools map[string]monitor.PoolHealthView
	Auths []statusOverviewAuth
}

func (h *Handler) poolCensus() poolCensus {
	out := poolCensus{
		Pools: map[string]monitor.PoolHealthView{},
		Auths: []statusOverviewAuth{},
	}
	byProvider := map[string][]auth.HealthReport{}
	for _, st := range h.pool.Status() {
		kind := "oauth"
		if st.Auth.Kind == auth.KindAPIKey {
			kind = "apikey"
			out.Counts.APIKey++
		} else {
			out.Counts.OAuth++
		}
		out.Counts.Total++

		rep := monitor.HealthReportFor(h.pool, st.Auth)
		provider := auth.NormalizeProvider(st.Auth.Provider)
		byProvider[provider] = append(byProvider[provider], rep)

		if rep.Serving {
			out.Counts.Serving++
		}
		switch rep.State {
		case auth.HealthHealthy:
			out.Counts.Healthy++
		case auth.HealthHalfOpen:
			out.Counts.HalfOpen++
		case auth.HealthDegraded:
			out.Counts.Degraded++
		case auth.HealthQuota:
			out.Counts.Quota++
		case auth.HealthCooling:
			out.Counts.Cooling++
		case auth.HealthHardFailed:
			out.Counts.Unhealthy++
		case auth.HealthDisabled:
			out.Counts.Disabled++
		}

		// Truncate label to 48 chars to keep the page defensive against
		// operators who stuff private info (e.g. email) into the label.
		label := st.Auth.Label
		if len(label) > 48 {
			label = label[:48] + "…"
		}
		out.Auths = append(out.Auths, statusOverviewAuth{
			Kind:  kind,
			Label: label,
			Group: st.Auth.Group,
			// Legacy booleans, unchanged in meaning for existing consumers.
			Healthy:       rep.Healthy(),
			Disabled:      rep.State == auth.HealthDisabled,
			QuotaExceeded: rep.State == auth.HealthQuota,
			HardFailure:   rep.State == auth.HealthHardFailed,
			authHealth:    newAuthHealth(rep),
		})
	}
	for provider, reports := range byProvider {
		out.Pools[provider] = monitor.NewPoolHealthView(auth.NewPoolHealth(provider, reports))
	}
	out.Counts.Models = len(h.pricing.Models())
	return out
}

// ---- /status/api/dashboard ----

type statusDashboardRequests struct {
	Summary  requestlog.Aggregate            `json:"summary"`
	ByClient map[string]requestlog.Aggregate `json:"by_client"`
	ByModel  map[string]requestlog.Aggregate `json:"by_model"`
	ByDay    map[string]requestlog.Aggregate `json:"by_day"`
}

type statusDashboard struct {
	// Counts carries what `pool` used to hold (total/healthy/quota/unhealthy/
	// disabled/oauth/apikey, same key names) plus the new state buckets. The
	// `pool` key now holds the per-provider health aggregate, matching
	// /status/api/overview — one name, one meaning, on every endpoint.
	Counts      statusCounts                      `json:"counts"`
	Pool        map[string]monitor.PoolHealthView `json:"pool"`
	Pricing     any                               `json:"pricing,omitempty"`
	Requests14d statusDashboardRequests           `json:"requests_14d"`
	RequestsAll statusDashboardRequests           `json:"requests_all"`
	Hourly24h   []requestlog.HourBucket           `json:"hourly_24h"`
}

func (h *Handler) handleStatusDashboard(c *gin.Context) {
	var out statusDashboard

	// Pool health — the same walk the /overview endpoint does, inlined here so
	// the SPA only needs one round trip for the dashboard tab.
	census := h.poolCensus()
	out.Counts = census.Counts
	out.Pool = census.Pools

	// Pricing (public — same shape admin /summary exposes).
	if h.pricing != nil {
		out.Pricing = gin.H{
			"default": h.pricing.Default(),
			"models":  h.pricing.Models(),
		}
	}

	if h.cfg.LogDir == "" {
		c.JSON(http.StatusOK, out)
		return
	}

	// 14-day window, stated as day labels rather than instants. Two reasons,
	// and the second is worth more than it looks:
	//
	//  - agg_cube is keyed on bday, so a label window is answered by grouping
	//    ~6.5k pre-summed rows instead of scanning the archive row by row.
	//    Measured on production: 1.69s → 2ms.
	//  - the labels are in the display zone, which is the zone ByDay's keys
	//    are already bucketed in. The old bounds were UTC-truncated while the
	//    buckets they selected were Shanghai days, so the two edges of the
	//    window disagreed with the series it returned by the zone offset.
	//
	// Day granularity also keeps the cache key stable, which is what the
	// previous truncation was for — reqCacheKey serializes From/To at second
	// precision, so a wall-clock bound would miss the cache on every poll.
	loc := requestlog.BucketLocation()
	todayLoc := time.Now().In(loc)
	if res, err := h.cachedQueryShared(requestlog.Filter{
		Dir:     h.cfg.LogDir,
		FromDay: todayLoc.AddDate(0, 0, -13).Format("2006-01-02"),
		ToDay:   todayLoc.Format("2006-01-02"),
		Limit:   1,
	}, statusCacheTTL); err == nil {
		out.Requests14d = statusDashboardRequests{
			Summary:  res.Summary,
			ByClient: anonymizeByClient(res.ByClient),
			ByModel:  res.ByModel,
			ByDay:    res.ByDay,
		}
	}

	// All-time — needed for cache stats, tokens/$ and weekly/monthly charts.
	if res, err := h.cachedQueryShared(requestlog.Filter{
		Dir: h.cfg.LogDir, Limit: 1,
	}, statusCacheTTL); err == nil {
		out.RequestsAll = statusDashboardRequests{
			Summary:  res.Summary,
			ByClient: anonymizeByClient(res.ByClient),
			ByModel:  res.ByModel,
			ByDay:    res.ByDay,
		}
	}

	// 24h hourly.
	out.Hourly24h = h.statusHourly24()

	c.JSON(http.StatusOK, out)
}

// statusHourly24 returns the 24h hourly buckets for the public dashboard,
// cached for statusCacheTTL with concurrent misses collapsed. Each cold
// call re-reads up to two rotated log files — cheap once, but the page is
// anonymous and polled, so misses multiply with viewers.
func (h *Handler) statusHourly24() []requestlog.HourBucket {
	h.statusAggMu.Lock()
	if h.hourlyCache != nil && time.Since(h.hourlyAt) < statusCacheTTL {
		buckets := h.hourlyCache
		h.statusAggMu.Unlock()
		return buckets
	}
	h.statusAggMu.Unlock()

	v, err, _ := h.reqSF.Do("status-hourly24", func() (any, error) {
		h.statusAggMu.Lock()
		if h.hourlyCache != nil && time.Since(h.hourlyAt) < statusCacheTTL {
			buckets := h.hourlyCache
			h.statusAggMu.Unlock()
			return buckets, nil
		}
		h.statusAggMu.Unlock()
		buckets, err := requestlog.AggregateHourly(h.cfg.LogDir, 24)
		if err != nil {
			return nil, err
		}
		h.statusAggMu.Lock()
		h.hourlyCache, h.hourlyAt = buckets, time.Now()
		h.statusAggMu.Unlock()
		return buckets, nil
	})
	if err != nil {
		return nil
	}
	return v.([]requestlog.HourBucket)
}

// statusWindow24h returns the pool-wide 24h request/cost/error rollup for
// the public overview, cached for statusCacheTTL. The underlying scan
// covers ~2 rotated files per call.
func (h *Handler) statusWindow24h() statusWindow24 {
	h.statusAggMu.Lock()
	if !h.window24At.IsZero() && time.Since(h.window24At) < statusCacheTTL {
		w := h.window24Cache
		h.statusAggMu.Unlock()
		return w
	}
	h.statusAggMu.Unlock()

	v, err, _ := h.reqSF.Do("status-window24h", func() (any, error) {
		h.statusAggMu.Lock()
		if !h.window24At.IsZero() && time.Since(h.window24At) < statusCacheTTL {
			w := h.window24Cache
			h.statusAggMu.Unlock()
			return w, nil
		}
		h.statusAggMu.Unlock()
		agg, err := requestlog.AggregateByAuth(h.cfg.LogDir, time.Now().Add(-24*time.Hour), time.Time{})
		if err != nil {
			return nil, err
		}
		var w statusWindow24
		for _, a := range agg {
			w.Requests += a.Count
			w.CostUSD += a.CostUSD
			w.Errors += a.Errors
		}
		h.statusAggMu.Lock()
		h.window24Cache, h.window24At = w, time.Now()
		h.statusAggMu.Unlock()
		return w, nil
	})
	if err != nil {
		return statusWindow24{}
	}
	return v.(statusWindow24)
}

// ---- /status/api/overview ----

type statusOverviewAuth struct {
	Kind  string `json:"kind"`
	Label string `json:"label,omitempty"`
	Group string `json:"group,omitempty"`
	// Legacy booleans. Retained for backwards compatibility; `state` (from the
	// embedded authHealth) is the authoritative field. Healthy here means
	// strictly state == "healthy" — it is no longer the four-way AND that used
	// to report a cooling API key as healthy.
	Healthy       bool `json:"healthy"`
	Disabled      bool `json:"disabled,omitempty"`
	QuotaExceeded bool `json:"quota_exceeded,omitempty"`
	HardFailure   bool `json:"hard_failure,omitempty"`
	authHealth
}

type statusOverview struct {
	Counts statusCounts `json:"counts"`
	// Pool is the per-provider aggregate, keyed by normalized provider name.
	Pool   map[string]monitor.PoolHealthView `json:"pool"`
	Window struct {
		Requests int64   `json:"requests"`
		CostUSD  float64 `json:"cost_usd"`
		Errors   int64   `json:"errors"`
	} `json:"window_24h"`
	Auths []statusOverviewAuth `json:"auths"`
}

func (h *Handler) handleStatusOverview(c *gin.Context) {
	var out statusOverview
	census := h.poolCensus()
	out.Counts = census.Counts
	out.Pool = census.Pools
	out.Auths = census.Auths

	// 24h aggregate across the whole pool (cached; see statusWindow24h).
	if h.cfg.LogDir != "" {
		w := h.statusWindow24h()
		out.Window.Requests = w.Requests
		out.Window.CostUSD = w.CostUSD
		out.Window.Errors = w.Errors
	}
	c.JSON(http.StatusOK, out)
}

// ---- /status/api/query ----

type statusQueryBody struct {
	Tokens []string `json:"tokens"`
}

type statusTokenResult struct {
	Masked string `json:"masked"`
	Found  bool   `json:"found"`
	Name   string `json:"name,omitempty"`
	Group  string `json:"group,omitempty"`
	// Blocked is true when the wallet is at or below zero, which is what
	// the proxy hot-path uses to refuse new requests.
	BalanceUSD float64 `json:"balance_usd"`
	Blocked    bool    `json:"blocked"`
	// Workspace (group shared pool) fields — set only when the token is a
	// member of a workspace. PoolAvailUSD is what this member may still draw
	// from the pool right now (after caps); IsTeamAdmin unlocks the team
	// console in the status SPA.
	Workspace    string  `json:"workspace,omitempty"`
	PoolAvailUSD float64 `json:"pool_avail_usd,omitempty"`
	IsTeamAdmin  bool    `json:"is_team_admin,omitempty"`
	IsTeamMember bool    `json:"is_team_member,omitempty"`
	// WeeklyUsedUSD is informational — current ISO-week spend from the
	// usage ledger. Not a limit any more.
	WeeklyUsedUSD float64               `json:"weekly_used_usd"`
	PricingGroup  string                `json:"pricing_group,omitempty"`
	GroupID       int64                 `json:"group_id,omitempty"`
	Total         usage.ClientCost      `json:"total"`
	Weekly        []usage.WeekEntry     `json:"weekly,omitempty"`
	LastUsed      *time.Time            `json:"last_used,omitempty"`
	Recent        []statusRecentEntry   `json:"recent,omitempty"`
	RecentTotal   int                   `json:"recent_total,omitempty"`
	Daily         []statusDailyEntry    `json:"daily,omitempty"`
	Window24h     *requestlog.Aggregate `json:"window_24h,omitempty"`
}

type statusDailyEntry struct {
	Date     string  `json:"date"`
	CostUSD  float64 `json:"cost_usd"`
	Requests int64   `json:"requests"`
}

type statusRecentEntry struct {
	TS         time.Time `json:"ts"`
	Provider   string    `json:"provider,omitempty"`
	Model      string    `json:"model,omitempty"`
	Input      int64     `json:"input_tokens"`
	Output     int64     `json:"output_tokens"`
	CacheRead  int64     `json:"cache_read_tokens"`
	CacheWrite int64     `json:"cache_create_tokens"`
	CostUSD    float64   `json:"cost_usd"`
	// BilledUSD is the post-multiplier amount the user's wallet was
	// debited. Zero on rows pre-dating SaaS billing or on error
	// statuses where nothing settled. The status page's Usage Lookup
	// renders this as the primary number; cost_usd is shown in the
	// hover popup as the "official" upstream cost.
	BilledUSD  float64 `json:"billed_usd,omitempty"`
	Multiplier float64 `json:"multiplier,omitempty"`
	Status     int     `json:"status"`
	DurationMs int64   `json:"duration_ms"`
	Stream     bool    `json:"stream,omitempty"`
	AuthLabel  string  `json:"auth_label,omitempty"`
	AuthKind   string  `json:"auth_kind,omitempty"`
}

const statusRecentLimit = 60
const statusTokensPerRequest = 20
const statusDailyWindowDays = 14

func (h *Handler) handleStatusQuery(c *gin.Context) {
	var body statusQueryBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// Dedupe and cap to prevent abuse of the log scan.
	seen := make(map[string]bool)
	tokens := make([]string, 0, len(body.Tokens))
	for _, t := range body.Tokens {
		t = strings.TrimSpace(t)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		tokens = append(tokens, t)
		if len(tokens) >= statusTokensPerRequest {
			break
		}
	}

	// Pre-resolve each token: validate against the store, snapshot usage.
	// A token that isn't registered returns found=false with no data — we
	// don't reveal whether it was deleted vs never existed.
	clients := h.usage.SnapshotClients()
	currentWeek := h.usage.CurrentWeekKey()
	maskedIdx := make(map[string]int, len(tokens)) // masked → result index
	results := make([]statusTokenResult, 0, len(tokens))
	for _, tok := range tokens {
		masked := maskToken(tok)
		r := statusTokenResult{Masked: masked}
		entry, ok := h.tokens.Lookup(tok)
		if !ok {
			results = append(results, r)
			continue
		}
		r.Found = true
		r.Name = entry.Name
		r.Group = entry.Group
		if pc, hasData := clients[tok]; hasData {
			r.Total = pc.Total
			r.Weekly = pc.WeeklyOrdered(8)
			if !pc.LastUsed.IsZero() {
				lu := pc.LastUsed
				r.LastUsed = &lu
			}
			if w, ok := pc.Weekly[currentWeek]; ok {
				r.WeeklyUsedUSD = w.CostUSD
			}
		}
		// SaaS wallet — balance gates new requests. The proxy refuses
		// further calls when balance <= 0, so surface that as `blocked`
		// here for the status panel's "this token is paused" banner.
		if h.wallets != nil {
			if w, err := h.wallets.GetWallet(c.Request.Context(), tok); err == nil {
				r.BalanceUSD = w.BalanceUSD
				r.GroupID = w.GroupID
				if g, err := h.wallets.GetGroup(c.Request.Context(), w.GroupID); err == nil {
					r.PricingGroup = g.Name
				}
				// Workspace membership: a member can spend the shared pool
				// before their personal balance, so the "blocked" decision
				// must include the available pool, mirroring the proxy
				// hot-path's PrecheckBalance.
				poolAvail := 0.0
				if m, err := h.wallets.MemberFor(c.Request.Context(), tok); err == nil {
					r.IsTeamMember = true
					r.IsTeamAdmin = m.Role == saasdb.WSRoleAdmin
					if ws, err := h.wallets.GetWorkspace(c.Request.Context(), m.WorkspaceID); err == nil {
						r.Workspace = ws.Name
					}
					poolAvail = h.wallets.MemberPoolAvail(c.Request.Context(), tok)
					r.PoolAvailUSD = poolAvail
				}
				if w.BalanceUSD <= 0 && poolAvail <= 0 {
					r.Blocked = true
				}
			}
		}
		// Use the masked form as the correlation key for the log scan — the
		// request log itself stores only masked tokens, so this comparison
		// is already what the writer will have emitted.
		maskedIdx[masked] = len(results)
		results = append(results, r)
	}

	// One pair of narrow queries per token, rather than one scan of everybody
	// and a bucketing loop.
	//
	// This handler used to pull a fleet-wide slab of 200k rows and sort them
	// into per-token buckets in Go. On production that measured a p95 of 44.6s
	// — of which only ~5s was SQL. The rest was deserialising 200k Records
	// into the heap and collecting them again, which is why the process sat at
	// 816MB and why no amount of indexing would have helped: the fix is not to
	// read the rows at all. The cap also meant the buckets only ever saw the
	// most recent ~6 days of a busy relay, so per-token totals were wrong on
	// top of being expensive.
	//
	// Per token the panel needs four things, and two queries cover them:
	//
	//   1. day-label window → Summary.Count (recent_total), ByDay (the daily
	//      series) and the newest statusRecentLimit rows, all at once. Day
	//      labels put the aggregates on agg_cube (measured 4.75s → 6ms) while
	//      Entries ride idx_req_ct.
	//   2. rolling 24h → its own query, because a 24h window is not day
	//      aligned and therefore cannot come off the cube. It still narrows to
	//      one token on the index, so it reads one day of one account.
	if h.cfg.LogDir != "" && len(maskedIdx) > 0 {
		seedDays := make([]string, 0, statusDailyWindowDays)
		// Day labels follow the configured display zone so this rollup lines
		// up with the admin overview's ByDay buckets (default UTC).
		today := time.Now().In(requestlog.BucketLocation())
		for i := statusDailyWindowDays - 1; i >= 0; i-- {
			d := today.AddDate(0, 0, -i)
			seedDays = append(seedDays, d.Format("2006-01-02"))
		}
		// Truncated to the minute because reqCacheKey serializes From at second
		// precision: a wall-clock bound would mint a new key on every call and
		// the 24h query would never hit the cache.
		cutoff24h := time.Now().Add(-24 * time.Hour).Truncate(time.Minute)
		// Display-time remap: the log stores a snapshot of the auth label at
		// request time. When an auth is renamed, callers expect the UI to show
		// the current label (the audit trail is keyed by AuthID). Resolve once
		// per call and rewrite on emit; stale entries whose AuthID has been
		// deleted fall back to the snapshot value.
		labelIdx := h.pool.LabelIndex()
		// Bound the window to the retention period (older files are pruned
		// anyway; the bound is defensive against prune failures and short
		// retention configs).
		retention := h.cfg.LogRetentionDays
		if retention <= 0 {
			retention = 90
		}
		fromDay := today.AddDate(0, 0, -(retention - 1)).Format("2006-01-02")
		toDay := today.Format("2006-01-02")

		for masked, i := range maskedIdx {
			// Seeded before the query, not inside the success branch: the chart
			// expects a point per day, and a transient query failure should
			// render as a flat series rather than as no series at all.
			daily := make([]statusDailyEntry, 0, len(seedDays))
			for _, day := range seedDays {
				daily = append(daily, statusDailyEntry{Date: day})
			}
			results[i].Daily = daily

			// One query answers three of the four fields: the day-label window
			// puts Summary and ByDay on agg_cube, and Entries come off
			// idx_req_ct already ordered newest-first, so LIMIT is the page.
			if res, err := h.cachedQueryShared(requestlog.Filter{
				Dir:         h.cfg.LogDir,
				ClientToken: masked,
				FromDay:     fromDay,
				ToDay:       toDay,
				Limit:       statusRecentLimit,
			}, statusCacheTTL); err == nil {
				results[i].RecentTotal = int(res.Summary.Count)

				for j, day := range seedDays {
					// ByDay is keyed on bday — the display-zone label seedDays
					// is built from — so the two line up without re-bucketing.
					if a, ok := res.ByDay[day]; ok {
						daily[j].CostUSD, daily[j].Requests = a.CostUSD, a.Count
					}
				}

				if len(res.Entries) > 0 {
					recent := make([]statusRecentEntry, 0, len(res.Entries))
					for _, rec := range res.Entries {
						label, kind := rec.AuthLabel, rec.AuthKind
						if cur, ok := labelIdx[rec.AuthID]; ok {
							label, kind = cur.Label, authKindString(cur.Kind)
						}
						recent = append(recent, statusRecentEntry{
							TS:         rec.TS,
							Provider:   rec.Provider,
							Model:      rec.Model,
							Input:      rec.Input,
							Output:     rec.Output,
							CacheRead:  rec.CacheRead,
							CacheWrite: rec.CacheCreate,
							CostUSD:    rec.CostUSD,
							BilledUSD:  rec.BilledUSD,
							Multiplier: rec.Multiplier,
							Status:     rec.Status,
							DurationMs: rec.DurationMs,
							Stream:     rec.Stream,
							AuthLabel:  label,
							AuthKind:   kind,
						})
					}
					results[i].Recent = recent
				}
			}

			// The rolling 24h window is not day aligned, so it is the one
			// figure the cube cannot answer. It still narrows to a single
			// token on the index, reading one account's last day instead of
			// the whole fleet's retention period.
			if res, err := h.cachedQueryShared(requestlog.Filter{
				Dir:         h.cfg.LogDir,
				ClientToken: masked,
				From:        cutoff24h,
				Limit:       1,
			}, statusCacheTTL); err == nil && res.Summary.Count > 0 {
				a := res.Summary
				results[i].Window24h = &a
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"results": results})
}

// ---- /status/api/history ----
//
// Paged ledger for a single client token over an arbitrary time range.
// Kept separate from /query so batch overview requests stay lean. Both the
// page and its row count are answered by the index — this used to read the
// archive once per invocation and paginate the result in memory.

type statusHistoryBody struct {
	Token  string `json:"token"`
	From   string `json:"from,omitempty"`   // YYYY-MM-DD or RFC3339
	To     string `json:"to,omitempty"`     // YYYY-MM-DD or RFC3339
	Offset int    `json:"offset,omitempty"` // newest-first index
	Limit  int    `json:"limit,omitempty"`  // default 50, max 200
}

type statusHistoryResp struct {
	Entries []statusRecentEntry `json:"entries"`
	Total   int                 `json:"total"`
	Offset  int                 `json:"offset"`
	Limit   int                 `json:"limit"`
}

func (h *Handler) handleStatusHistory(c *gin.Context) {
	if h.cfg.LogDir == "" {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "log_dir not configured"})
		return
	}
	var body statusHistoryBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	tok := strings.TrimSpace(body.Token)
	if tok == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "token required"})
		return
	}
	// Same ownership check as /query: the caller must present a token the
	// store knows about. We don't reveal whether an unknown token was once
	// valid or never existed.
	if _, ok := h.tokens.Lookup(tok); !ok {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "token not found"})
		return
	}
	masked := maskToken(tok)

	limit := body.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	offset := body.Offset
	if offset < 0 {
		offset = 0
	}

	// The token goes into the filter, not into a loop over somebody else's
	// rows: req carries idx_req_ct(client_token, ts DESC, id DESC) WHERE
	// attempt_only = 0, whose order is exactly this endpoint's, so one page
	// is an index seek rather than a scan.
	//
	// This used to pull a fleet-wide Limit: 100000 slab and drop every row
	// belonging to anyone else, on the assumption that "real deployments stay
	// well under it". A relay doing ~33k requests a day reaches 100k in three
	// days, so the cap silently became a three-day horizon for every token,
	// and the page cost 2.9s to build out of rows it then threw away.
	base := requestlog.Filter{Dir: h.cfg.LogDir, ClientToken: masked}
	applyDateBounds(&base, body.From, body.To)

	// Two queries, because one cannot answer both: PageOnly skips the
	// aggregate pass (that is the point of it), and the pager needs a total.
	// The counting one asks for a single row and reads only Summary.Count,
	// so it rides the cube whenever the bounds are day labels.
	pageF := base
	pageF.Limit, pageF.Offset, pageF.PageOnly = limit, offset, true

	countF := base
	countF.Limit = 1

	// Shared read-only cache: the pager re-issues these filters on every page
	// flip. Keys carry ClientToken/Limit/Offset/PageOnly already, so the two
	// shapes never collide and one token's page can never be served to another.
	res, err := h.cachedQueryShared(pageF, statusCacheTTL)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// Fallback if the count query fails. It leans towards "there is more",
	// because the opposite guess strands the reader: a full page plus the
	// offset reads as exactly one page left, and the pager stops offering the
	// next one — silently truncating a history that is really still going.
	total := len(res.Entries) + offset
	if len(res.Entries) == limit {
		total++
	}
	if cnt, cerr := h.cachedQueryShared(countF, statusCacheTTL); cerr == nil {
		total = int(cnt.Summary.Count)
	} else {
		log.Warnf("status history: count query failed for %s: %v", masked, cerr)
	}

	// Auth label/kind are remapped from current pool state so renames show
	// up in the ledger; snapshots survive as fallback for deleted auths.
	labelIdx := h.pool.LabelIndex()
	all := make([]statusRecentEntry, 0, len(res.Entries))
	for _, rec := range res.Entries {
		label, kind := rec.AuthLabel, rec.AuthKind
		if cur, ok := labelIdx[rec.AuthID]; ok {
			label = cur.Label
			kind = authKindString(cur.Kind)
		}
		all = append(all, statusRecentEntry{
			TS:         rec.TS,
			Provider:   rec.Provider,
			Model:      rec.Model,
			Input:      rec.Input,
			Output:     rec.Output,
			CacheRead:  rec.CacheRead,
			CacheWrite: rec.CacheCreate,
			CostUSD:    rec.CostUSD,
			BilledUSD:  rec.BilledUSD,
			Multiplier: rec.Multiplier,
			Status:     rec.Status,
			DurationMs: rec.DurationMs,
			Stream:     rec.Stream,
			AuthLabel:  label,
			AuthKind:   kind,
		})
	}
	// No slicing: SQL already applied LIMIT/OFFSET, so `all` is the page.
	c.JSON(http.StatusOK, statusHistoryResp{
		Entries: all,
		Total:   total,
		Offset:  offset,
		Limit:   limit,
	})
}
