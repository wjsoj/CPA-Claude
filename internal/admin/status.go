package admin

import (
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"github.com/wjsoj/CPA-Claude/internal/auth"
	"github.com/wjsoj/CPA-Claude/internal/requestlog"
	"github.com/wjsoj/CPA-Claude/internal/usage"
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
	r.POST("/status/api/query", h.handleStatusQuery)
	log.Info("admin: public /status/ page enabled")
}

// ---- /status/api/overview ----

type statusOverviewAuth struct {
	Kind          string `json:"kind"`
	Label         string `json:"label,omitempty"`
	Group         string `json:"group,omitempty"`
	Healthy       bool   `json:"healthy"`
	Disabled      bool   `json:"disabled,omitempty"`
	QuotaExceeded bool   `json:"quota_exceeded,omitempty"`
	HardFailure   bool   `json:"hard_failure,omitempty"`
}

type statusOverview struct {
	Counts struct {
		Total     int `json:"total"`
		Healthy   int `json:"healthy"`
		Quota     int `json:"quota"`
		Unhealthy int `json:"unhealthy"`
		Disabled  int `json:"disabled"`
		OAuth     int `json:"oauth"`
		APIKey    int `json:"apikey"`
		Models    int `json:"models"`
	} `json:"counts"`
	Window struct {
		Requests int64   `json:"requests"`
		CostUSD  float64 `json:"cost_usd"`
		Errors   int64   `json:"errors"`
	} `json:"window_24h"`
	Auths []statusOverviewAuth `json:"auths"`
}

func (h *Handler) handleStatusOverview(c *gin.Context) {
	var out statusOverview
	out.Auths = []statusOverviewAuth{}
	for _, st := range h.pool.Status() {
		kind := "oauth"
		if st.Auth.Kind == auth.KindAPIKey {
			kind = "apikey"
			out.Counts.APIKey++
		} else {
			out.Counts.OAuth++
		}
		out.Counts.Total++

		live := h.pool.FindByID(st.Auth.ID)
		var healthy, hardFail bool
		if live != nil {
			healthy, hardFail, _, _ = live.HealthSnapshot()
		}
		quota := !st.Auth.QuotaExceededAt.IsZero()
		switch {
		case st.Auth.Disabled:
			out.Counts.Disabled++
		case quota:
			out.Counts.Quota++
		case hardFail || !healthy:
			out.Counts.Unhealthy++
		default:
			out.Counts.Healthy++
		}
		// Truncate label to 48 chars to keep the page defensive against
		// operators who stuff private info (e.g. email) into the label.
		label := st.Auth.Label
		if len(label) > 48 {
			label = label[:48] + "…"
		}
		out.Auths = append(out.Auths, statusOverviewAuth{
			Kind:          kind,
			Label:         label,
			Group:         st.Auth.Group,
			Healthy:       healthy && !quota && !hardFail && !st.Auth.Disabled,
			Disabled:      st.Auth.Disabled,
			QuotaExceeded: quota,
			HardFailure:   hardFail,
		})
	}
	out.Counts.Models = len(h.pricing.Models())

	// 24h aggregate across the whole pool.
	if h.cfg.LogDir != "" {
		agg, err := requestlog.AggregateByAuth(h.cfg.LogDir, time.Now().Add(-24*time.Hour), time.Time{})
		if err == nil {
			for _, a := range agg {
				out.Window.Requests += a.Count
				out.Window.CostUSD += a.CostUSD
				out.Window.Errors += a.Errors
			}
		}
	}
	c.JSON(http.StatusOK, out)
}

// ---- /status/api/query ----

type statusQueryBody struct {
	Tokens []string `json:"tokens"`
}

type statusTokenResult struct {
	Masked        string                   `json:"masked"`
	Found         bool                     `json:"found"`
	Name          string                   `json:"name,omitempty"`
	Group         string                   `json:"group,omitempty"`
	WeeklyLimit   float64                  `json:"weekly_limit"`
	WeeklyUsedUSD float64                  `json:"weekly_used_usd"`
	Blocked       bool                     `json:"blocked"`
	Total         usage.ClientCost         `json:"total"`
	Weekly        []usage.WeekEntry        `json:"weekly,omitempty"`
	LastUsed      *time.Time               `json:"last_used,omitempty"`
	Recent        []statusRecentEntry      `json:"recent,omitempty"`
	Window24h     *requestlog.Aggregate    `json:"window_24h,omitempty"`
}

type statusRecentEntry struct {
	TS         time.Time `json:"ts"`
	Model      string    `json:"model,omitempty"`
	Input      int64     `json:"input_tokens"`
	Output     int64     `json:"output_tokens"`
	CacheRead  int64     `json:"cache_read_tokens"`
	CacheWrite int64     `json:"cache_create_tokens"`
	CostUSD    float64   `json:"cost_usd"`
	Status     int       `json:"status"`
	DurationMs int64     `json:"duration_ms"`
	Stream     bool      `json:"stream,omitempty"`
}

const statusRecentLimit = 40
const statusTokensPerRequest = 20

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
		name, weekly, _, group, ok := h.tokens.Lookup(tok)
		if !ok {
			results = append(results, r)
			continue
		}
		r.Found = true
		r.Name = name
		r.Group = group
		r.WeeklyLimit = weekly
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
		if r.WeeklyLimit > 0 && r.WeeklyUsedUSD >= r.WeeklyLimit {
			r.Blocked = true
		}
		// Use the masked form as the correlation key for the log scan — the
		// request log itself stores only masked tokens, so this comparison
		// is already what the writer will have emitted.
		maskedIdx[masked] = len(results)
		results = append(results, r)
	}

	// Log scan: walk the last two days of request logs and bucket per token.
	if h.cfg.LogDir != "" && len(maskedIdx) > 0 {
		type bucket struct {
			agg    requestlog.Aggregate
			recent []statusRecentEntry
		}
		buckets := make(map[string]*bucket, len(maskedIdx))
		for k := range maskedIdx {
			buckets[k] = &bucket{}
		}
		cutoff := time.Now().Add(-24 * time.Hour)
		// Pull a wider window for Recent, but only keep 40 newest per token.
		recentCutoff := time.Now().Add(-7 * 24 * time.Hour)
		f := requestlog.Filter{
			Dir:   h.cfg.LogDir,
			From:  recentCutoff,
			Limit: 1, // we don't use Entries from Query; walk logs ourselves
		}
		// Use low-level scan via the public Query helper: we only care about
		// Entries for a small window, so call Query with a huge Limit.
		f.Limit = 5000
		res, err := requestlog.Query(f)
		if err == nil {
			for _, rec := range res.Entries {
				b, ok := buckets[rec.ClientToken]
				if !ok {
					continue
				}
				if !rec.TS.Before(cutoff) {
					b.agg.Count++
					b.agg.InputTokens += rec.Input
					b.agg.OutputTokens += rec.Output
					b.agg.CacheReadTokens += rec.CacheRead
					b.agg.CacheCreateTokens += rec.CacheCreate
					b.agg.CostUSD += rec.CostUSD
					b.agg.TotalDurationMs += rec.DurationMs
					if rec.Status >= 400 || rec.Error != "" {
						b.agg.Errors++
					}
				}
				if len(b.recent) < statusRecentLimit {
					b.recent = append(b.recent, statusRecentEntry{
						TS:         rec.TS,
						Model:      rec.Model,
						Input:      rec.Input,
						Output:     rec.Output,
						CacheRead:  rec.CacheRead,
						CacheWrite: rec.CacheCreate,
						CostUSD:    rec.CostUSD,
						Status:     rec.Status,
						DurationMs: rec.DurationMs,
						Stream:     rec.Stream,
					})
				}
			}
		}
		for m, b := range buckets {
			i := maskedIdx[m]
			if b.agg.Count > 0 {
				a := b.agg
				results[i].Window24h = &a
			}
			if len(b.recent) > 0 {
				results[i].Recent = b.recent
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"results": results})
}
