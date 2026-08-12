// Package monitor implements the public /status/ uptime monitor. It keeps one
// logical health record per provider (Claude, OpenAI) and combines two signals:
//
//   - Passive (zero cost, always live): reads auth.Pool for the full health
//     partition (healthy / half-open / degraded / quota / cooling / hard-failed
//     / disabled) plus whether a serving credential has a free slot. This is
//     the "is there capacity right now" signal. "Serving" and "healthy" are
//     different questions and both are published.
//   - Active (every IntervalMinutes): sends one minimal request DIRECTLY to a
//     healthy API-key credential's upstream, confirming the model actually
//     serves. **OAuth (subscription) credentials are never actively probed** —
//     probing them burns quota / risks the account — so a provider served only
//     by OAuth records a passive sample instead (healthy when the pool has
//     healthy credentials). Recorded as the uptime timeseries (24h samples +
//     90-day daily rollups), persisted to disk.
package monitor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/wjsoj/cc-core/auth"
)

const (
	// recentRetention bounds how far back the fine-grained 24h timeline keeps
	// samples. We hold 48h so the UI's 24h window is always fully populated
	// even right after a restart-then-prune cycle.
	recentRetention = 48 * time.Hour
	// dayRetention bounds the daily rollup history (statuspage-style 90 bars).
	dayRetention = 90
	// dateLayout is the local-date key for daily rollups.
	dateLayout = "2006-01-02"
	// probeTimeout caps a single active probe round-trip.
	probeTimeout = 30 * time.Second
)

// EndpointTarget describes how to reach one provider's local endpoint for the
// active self-probe. Addr is host:port the proxy listens on; the monitor dials
// loopback on that port regardless of the bind host.
type EndpointTarget struct {
	Provider string // auth.ProviderAnthropic | auth.ProviderOpenAI
	Port     int
	Model    string // probe model; empty disables active probing for this provider
}

// Config is the runtime config the monitor needs. It's assembled by the server
// from config.MonitorConfig + the live endpoint set.
type Config struct {
	Enabled     bool
	Interval    time.Duration
	ClientToken string
	StateFile   string
	Targets     []EndpointTarget
}

// Sample is one active-probe observation.
type Sample struct {
	TS        time.Time `json:"ts"`
	OK        bool      `json:"ok"`
	Status    int       `json:"status"`
	LatencyMs int64     `json:"latency_ms"`
	Err       string    `json:"err,omitempty"`
	// PoolHealthy is DEPRECATED and no longer written or read.
	//
	// It used to record whether the passive pool had a healthy credential at
	// sample time, and healthySignal() then painted the sample green whenever
	// it was set. That made the uptime strip permanently green: every probe
	// failure was excused by a pool signal that was itself computed from a
	// polluted "healthy" count. The field is retained only so previously
	// persisted state files still unmarshal.
	PoolHealthy bool `json:"pool_healthy,omitempty"`
}

// realFailure reports whether a probe sample reflects a genuine provider-side
// outage, as opposed to a probe artifact that says nothing about whether we can
// actually serve traffic.
//
// The active probe is a DIRECT API-key call and never goes through OAuth — the
// path real (subscription) traffic takes. So a request- or auth-shaped rejection
// (any 4xx: a probe body the upstream/relay won't accept, a revoked or
// rate-limited key, a model the relay doesn't expose) tells us about our probe
// or that one API key, NOT whether the provider is serving OAuth traffic. A
// transport error / timeout (Status == 0) is likewise "no signal". Only a 5xx —
// the upstream server itself erroring — is treated as a real provider-health
// failure. Everything else defers to the passive pool-capacity signal (healthy
// credentials / free slot), which is the source of truth for "can we serve".
//
// Cloudflare's edge-connectivity codes (520–527: "connection timed out",
// "origin unreachable", "SSL handshake failed", …) carry a 5xx status but are
// CF↔origin transport failures, NOT the origin application erroring — they're
// indistinguishable from a Status==0 transport timeout from our side, and the
// real forward path retries them. So they fall under "no signal" too, despite
// being ≥ 500. A genuine origin 5xx (500/502/503/529) still counts.
func (s Sample) realFailure() bool {
	if s.Status >= 520 && s.Status <= 527 {
		return false
	}
	return s.Status >= 500
}

// healthySignal reports whether the sample should count as healthy (green) in
// the uptime timeline and toward the uptime percentage.
//
// A sample is green if and only if the request actually succeeded. The previous
// rule (`s.OK || s.PoolHealthy || !s.realFailure()`) excused every non-5xx
// outcome — timeouts, transport failures, Cloudflare 520–527, 4xx — and then
// excused the 5xx too whenever the pool reported a healthy credential. The net
// effect was a strip that could not go red, which is worse than no strip. A
// timeout is a failed request from the caller's point of view; record it as one.
// realFailure() survives, but only to classify the log line.
func (s Sample) healthySignal() bool { return s.OK }

// DayStat is a per-local-day rollup of probe outcomes.
type DayStat struct {
	Date  string `json:"date"`
	Total int    `json:"total"`
	OK    int    `json:"ok"`
}

// provStore is the persisted per-provider history.
type provStore struct {
	Recent []Sample            `json:"recent"`
	Days   map[string]*DayStat `json:"days"`
	Last   *Sample             `json:"last,omitempty"`
}

func newProvStore() *provStore {
	return &provStore{Days: map[string]*DayStat{}}
}

type persistState struct {
	Providers map[string]*provStore `json:"providers"`
}

// Monitor owns the probe loop and history. Safe for concurrent reads via
// Snapshot while the loop writes.
type Monitor struct {
	cfg  Config
	pool *auth.Pool

	mu     sync.Mutex
	stores map[string]*provStore // provider -> history

	client *http.Client
}

// New builds a Monitor. pool is required (for the passive signal); cfg may have
// Enabled=false, in which case Start is a no-op and only the passive signal is
// served.
func New(cfg Config, pool *auth.Pool) *Monitor {
	m := &Monitor{
		cfg:    cfg,
		pool:   pool,
		stores: map[string]*provStore{},
		client: &http.Client{Timeout: probeTimeout},
	}
	for _, t := range cfg.Targets {
		m.stores[auth.NormalizeProvider(t.Provider)] = newProvStore()
	}
	m.load()
	return m
}

// Start runs the active probe loop until ctx is cancelled. Returns immediately
// when monitoring is disabled or no client token is set (passive-only mode).
func (m *Monitor) Start(ctx context.Context) {
	if !m.cfg.Enabled {
		log.Info("monitor: disabled (passive pool status only)")
		return
	}
	interval := m.cfg.Interval
	if interval <= 0 {
		interval = 10 * time.Minute
	}
	log.Infof("monitor: active probing every %s across %d provider(s)", interval, len(m.cfg.Targets))
	go func() {
		// Probe once shortly after boot so the page isn't empty, then settle
		// into the configured cadence.
		first := time.NewTimer(15 * time.Second)
		defer first.Stop()
		select {
		case <-ctx.Done():
			return
		case <-first.C:
			m.probeAll(ctx)
		}
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				m.probeAll(ctx)
			}
		}
	}()
}

func (m *Monitor) probeAll(ctx context.Context) {
	for _, t := range m.cfg.Targets {
		if t.Model == "" {
			continue
		}
		provider := auth.NormalizeProvider(t.Provider)
		// Active end-to-end probe ONLY against a healthy API-key credential.
		// OAuth (subscription) credentials are never actively probed. For a
		// provider with no API-key credential (OAuth-only), record a passive
		// sample from the pool signal so the timeline stays populated (healthy
		// when the pool has healthy credentials) instead of going blank.
		var s Sample
		if cred := m.pickAPIKeyCred(provider); cred != nil {
			s = m.probe(ctx, provider, cred, t.Model)
		} else {
			s = m.passiveSample(provider)
		}
		// The sample is recorded exactly as observed. It is deliberately NOT
		// overwritten with the passive pool signal any more: doing so meant a
		// probe could never fail, because the pool count that excused it was
		// itself derived from a "healthy" number that counted just-expired
		// circuit breakers as healthy.
		m.record(t.Provider, s)
	}
	m.save()
}

// pickAPIKeyCred returns a healthy API-key credential for the provider, or nil
// when none exists. Confines active probing to API-key credentials so OAuth
// credentials are never hit by the probe.
func (m *Monitor) pickAPIKeyCred(provider string) *auth.Auth {
	provider = auth.NormalizeProvider(provider)
	for _, st := range m.pool.Status() {
		if st.Auth.Kind != auth.KindAPIKey {
			continue
		}
		if auth.NormalizeProvider(st.Auth.Provider) != provider {
			continue
		}
		live := m.pool.FindByID(st.Auth.ID)
		if live == nil {
			continue
		}
		// Serving, not "healthy": a half-open or degraded key is routable and is
		// exactly the one we want to probe — probing it is how it gets verified.
		// A cooling / quota / hard-failed key must not be woken up by a probe.
		if !live.HealthState().Serving {
			continue
		}
		return live
	}
	return nil
}

// passiveSample records the pool's passive health for a provider we don't
// actively probe (OAuth-only). OK only when a credential is fully healthy —
// half-open and degraded credentials are routable but unverified, so they must
// not paint the strip green; a synthetic 503 marks "nothing can serve".
func (m *Monitor) passiveSample(provider string) Sample {
	ph, _ := PoolHealthFor(m.pool, provider)
	s := Sample{TS: time.Now()}
	switch {
	case ph.ByState[auth.HealthHealthy] > 0:
		s.OK = true
		s.Status = 200
	case ph.Serving > 0:
		// Routable but nothing has proven it works since the last failure.
		s.Status = 503
		s.Err = "no verified credentials (" + string(ph.Worst) + ")"
	default:
		s.Status = 503
		s.Err = "no serving credentials"
	}
	return s
}

// probe sends one minimal request DIRECTLY to the API-key credential's upstream
// (never through the OAuth-preferring pool), and returns the outcome. A 2xx is
// success; a transport error (no HTTP response, status 0) is "nodata" and is
// treated as healthy by the recorder.
func (m *Monitor) probe(ctx context.Context, provider string, cred *auth.Auth, model string) Sample {
	token, _ := cred.Credentials()
	info := cred.Snapshot()
	upstreamModel := model
	if um, ok := cred.ResolveUpstreamModel(model); ok && um != "" {
		upstreamModel = um
	}
	url, body, headers := directProbeRequest(provider, info.BaseURL, token, upstreamModel)
	start := time.Now()
	s := Sample{TS: start}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		s.Err = err.Error()
		return s
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := m.client
	if info.ProxyURL != "" {
		client = auth.ClientFor(info.ProxyURL, false)
	}
	resp, err := client.Do(req)
	s.LatencyMs = time.Since(start).Milliseconds()
	if err != nil {
		s.Err = err.Error()
		return s
	}
	defer resp.Body.Close()
	// Drain a little so the connection can be reused.
	buf := make([]byte, 2048)
	_, _ = resp.Body.Read(buf)
	s.Status = resp.StatusCode
	s.OK = resp.StatusCode >= 200 && resp.StatusCode < 300
	if !s.OK {
		s.Err = fmt.Sprintf("http %d", resp.StatusCode)
	}
	return s
}

// directProbeRequest builds a minimal upstream request for an API-key
// credential (its BaseURL override, else the provider default).
func directProbeRequest(provider, baseURL, token, model string) (url string, body []byte, headers map[string]string) {
	ping := map[string]any{
		"model":      model,
		"max_tokens": 1,
		"messages":   []map[string]string{{"role": "user", "content": "ping"}},
	}
	if provider == auth.ProviderOpenAI {
		base := strings.TrimRight(baseURL, "/")
		if base == "" {
			base = "https://api.openai.com"
		}
		url = base + "/v1/chat/completions"
		body, _ = json.Marshal(ping)
		headers = map[string]string{
			"Content-Type":  "application/json",
			"Authorization": "Bearer " + token,
		}
		return
	}
	base := strings.TrimRight(baseURL, "/")
	if base == "" {
		base = "https://api.anthropic.com"
	}
	url = base + "/v1/messages"
	body, _ = json.Marshal(ping)
	headers = map[string]string{
		"Content-Type":      "application/json",
		"anthropic-version": "2023-06-01",
		"x-api-key":         token,
	}
	return
}

// record appends a probe sample to the provider's history, prunes old data,
// and updates the daily rollup. Today is the sample's local date.
func (m *Monitor) record(provider string, s Sample) {
	provider = auth.NormalizeProvider(provider)
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.stores[provider]
	if st == nil {
		st = newProvStore()
		m.stores[provider] = st
	}
	last := s
	st.Last = &last

	st.Recent = append(st.Recent, s)
	// Anchor the retention window to the newest sample we've seen, not the one
	// just inserted — robust if a probe ever lands out of order.
	latest := s.TS
	for _, x := range st.Recent {
		if x.TS.After(latest) {
			latest = x.TS
		}
	}
	cutoff := latest.Add(-recentRetention)
	kept := st.Recent[:0]
	for _, x := range st.Recent {
		if x.TS.After(cutoff) {
			kept = append(kept, x)
		}
	}
	st.Recent = kept

	key := s.TS.Format(dateLayout)
	d := st.Days[key]
	if d == nil {
		d = &DayStat{Date: key}
		st.Days[key] = d
	}
	// Anything that was not a successful request counts against the day —
	// timeouts, transport errors, CF 520–527 and 4xx included. See
	// healthySignal: excusing those is what kept the bars permanently green.
	d.Total++
	if s.healthySignal() {
		d.OK++
	}
	m.pruneDaysLocked(st)

	switch {
	case s.OK:
		log.Debugf("monitor: %s probe ok (%dms)", provider, s.LatencyMs)
	case s.realFailure():
		log.Infof("monitor: %s probe FAILED status=%d err=%q", provider, s.Status, s.Err)
	default:
		// Probe-side rejection (4xx) or transport error — no provider-health
		// signal, doesn't dock uptime. Logged quietly for diagnostics.
		log.Debugf("monitor: %s probe no-signal status=%d err=%q", provider, s.Status, s.Err)
	}
}

func (m *Monitor) pruneDaysLocked(st *provStore) {
	if len(st.Days) <= dayRetention {
		return
	}
	// Cutoff is relative to the newest day key present (lexical max works for
	// the YYYY-MM-DD layout), so backfilled or out-of-order samples don't move
	// the window backwards and defeat pruning.
	var newest string
	for k := range st.Days {
		if k > newest {
			newest = k
		}
	}
	t, err := time.Parse(dateLayout, newest)
	if err != nil {
		return
	}
	cutoff := t.AddDate(0, 0, -(dayRetention - 1)).Format(dateLayout)
	for k := range st.Days {
		if k < cutoff {
			delete(st.Days, k)
		}
	}
}

// ---- snapshot (public API shape) ----

// ProviderSnapshot is the per-provider public payload.
type ProviderSnapshot struct {
	Name          string `json:"name"` // "claude" | "openai"
	Provider      string `json:"provider"`
	Operational   string `json:"operational"` // operational | degraded | down | unknown
	SlotAvailable bool   `json:"slot_available"`
	// HealthyCreds counts only auth.HealthHealthy — verified-good credentials.
	// It is NOT the count that decides whether we can serve; ServingCreds is.
	HealthyCreds int `json:"healthy_creds"`
	// ServingCreds counts credentials the router will actually offer traffic to
	// (healthy + half-open + degraded). This is the "are we up" number.
	ServingCreds int `json:"serving_creds"`
	// CoolingCreds counts credentials paused by the API-key circuit breaker.
	CoolingCreds int `json:"cooling_creds"`
	// WorstState is the highest-severity credential state present.
	WorstState string `json:"worst_state"`
	// ByState is the full partition; the values sum to TotalCreds.
	ByState      map[string]int `json:"by_state"`
	TotalCreds   int            `json:"total_creds"`
	ProbeEnabled bool           `json:"probe_enabled"`
	LastProbe    *Sample        `json:"last_probe,omitempty"`
	Uptime90d    []DayStat      `json:"uptime_90d"`
	Uptime90dPct float64        `json:"uptime_90d_pct"`
	Timeline24h  []Sample       `json:"timeline_24h"`
}

// Snapshot is the full /status/api/monitor payload.
type Snapshot struct {
	GeneratedAt time.Time          `json:"generated_at"`
	Interval    int                `json:"interval_minutes"`
	Providers   []ProviderSnapshot `json:"providers"`
}

// AllStates is every health state, in triage order. Exported so serializers can
// emit a complete by_state map (zeros included) instead of a sparse one that
// makes a missing key ambiguous with a zero count.
var AllStates = []auth.HealthState{
	auth.HealthHealthy,
	auth.HealthHalfOpen,
	auth.HealthDegraded,
	auth.HealthQuota,
	auth.HealthCooling,
	auth.HealthHardFailed,
	auth.HealthDisabled,
}

// HealthReportFor resolves the live health report for one credential. Prefers
// the live *auth.Auth (whose HealthState() also expires stale deadlines); falls
// back to reconstructing the report from the AuthInfo snapshot when the
// credential has been removed from the pool between Status() and here.
func HealthReportFor(pool *auth.Pool, info auth.AuthInfo) auth.HealthReport {
	if pool != nil {
		if live := pool.FindByID(info.ID); live != nil {
			return live.HealthState()
		}
	}
	return reportFromInfo(info)
}

// reportFromInfo rebuilds a HealthReport from an AuthInfo snapshot. AuthInfo
// already carries the classified State (Snapshot computes it under the lock);
// this only re-derives the fields AuthInfo does not carry.
func reportFromInfo(info auth.AuthInfo) auth.HealthReport {
	r := auth.HealthReport{
		State:               info.State,
		ConsecutiveFailures: info.ConsecutiveFailures,
		Consecutive429s:     info.Consecutive429s,
		Consecutive401s:     info.Consecutive401s,
		LastSuccess:         info.LastSuccess,
		LastFailure:         info.LastFailure,
		LastFailureReason:   info.LastFailureReason,
		HardFailureAt:       info.HardFailureAt,
		QuotaResetAt:        info.QuotaResetAt,
		QuarantineUntil:     info.QuarantineUntil,
		QuarantineStrikes:   info.QuarantineStrikes,
	}
	if r.State == "" {
		r.State = auth.HealthHealthy
	}
	r.Reason = info.LastFailureReason
	switch r.State {
	case auth.HealthDisabled:
		r.Reason = "disabled by operator"
	case auth.HealthQuota:
		if d := time.Until(info.QuotaResetAt); d > 0 {
			r.RetryAfter = d
		}
	case auth.HealthCooling:
		if d := time.Until(info.QuarantineUntil); d > 0 {
			r.RetryAfter = d
		}
	}
	switch r.State {
	case auth.HealthDisabled, auth.HealthHardFailed, auth.HealthQuota, auth.HealthCooling:
		r.Serving = false
	default:
		r.Serving = true
	}
	return r
}

// PoolHealthFor aggregates one provider's credentials and reports whether any
// serving credential has a free concurrency slot right now.
//
// slot is computed over SERVING credentials, not "healthy" ones: a half-open
// key still takes traffic, and pretending otherwise is what made the panel show
// "down" while requests were flowing (and, in the other direction, "healthy"
// the instant a circuit-breaker deadline elapsed).
func PoolHealthFor(pool *auth.Pool, provider string) (ph auth.PoolHealth, slot bool) {
	provider = auth.NormalizeProvider(provider)
	if pool == nil {
		return auth.NewPoolHealth(provider, nil), false
	}
	reports := make([]auth.HealthReport, 0, 8)
	for _, st := range pool.Status() {
		info := st.Auth
		if auth.NormalizeProvider(info.Provider) != provider {
			continue
		}
		r := HealthReportFor(pool, info)
		reports = append(reports, r)
		if !r.Serving {
			continue
		}
		// cap 0 = unlimited (API keys, uncapped OAuth). A free slot exists
		// when uncapped or active sessions are below the cap.
		if info.MaxConcurrent == 0 || st.ActiveClients < info.MaxConcurrent {
			slot = true
		}
	}
	return auth.NewPoolHealth(provider, reports), slot
}

// PoolHealthView is the wire shape of an aggregated pool, shared by every
// endpoint that publishes one (`pool` in the admin summary, the public
// overview/dashboard, and /healthz) so the frontend parses one structure.
type PoolHealthView struct {
	Available  bool           `json:"available"`
	Total      int            `json:"total"`
	Serving    int            `json:"serving"`
	WorstState string         `json:"worst_state"`
	ByState    map[string]int `json:"by_state"`
}

// NewPoolHealthView serializes an auth.PoolHealth, zero-filling every state key.
func NewPoolHealthView(p auth.PoolHealth) PoolHealthView {
	by := make(map[string]int, len(AllStates))
	for _, s := range AllStates {
		by[string(s)] = p.ByState[s]
	}
	worst := p.Worst
	if worst == "" {
		worst = auth.HealthHealthy
	}
	return PoolHealthView{
		Available:  p.Available(),
		Total:      p.Total,
		Serving:    p.Serving,
		WorstState: string(worst),
		ByState:    by,
	}
}

// GetSnapshot builds the public payload: passive live counts for every
// provider, merged with persisted probe history.
func (m *Monitor) GetSnapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := Snapshot{
		GeneratedAt: time.Now(),
		Interval:    int(m.cfg.Interval / time.Minute),
	}
	if out.Interval == 0 {
		out.Interval = 10
	}
	for _, t := range m.cfg.Targets {
		provider := auth.NormalizeProvider(t.Provider)
		ph, slot := PoolHealthFor(m.pool, provider)
		st := m.stores[provider]

		view := NewPoolHealthView(ph)
		ps := ProviderSnapshot{
			Name:          displayName(provider),
			Provider:      provider,
			SlotAvailable: slot,
			HealthyCreds:  ph.ByState[auth.HealthHealthy],
			ServingCreds:  ph.Serving,
			CoolingCreds:  ph.ByState[auth.HealthCooling],
			WorstState:    view.WorstState,
			ByState:       view.ByState,
			TotalCreds:    ph.Total,
			ProbeEnabled:  m.cfg.Enabled && t.Model != "" && m.pickAPIKeyCred(provider) != nil,
			Uptime90d:     []DayStat{},
			Timeline24h:   []Sample{},
		}
		if st != nil {
			ps.LastProbe = st.Last
			ps.Uptime90d = lastDays(st.Days, dayRetention, out.GeneratedAt)
			ps.Uptime90dPct = uptimePct(ps.Uptime90d)
			ps.Timeline24h = recentWindow(st.Recent, 24*time.Hour, out.GeneratedAt)
		}
		ps.Operational = deriveStatus(ps)
		out.Providers = append(out.Providers, ps)
	}
	return out
}

func displayName(provider string) string {
	if auth.NormalizeProvider(provider) == auth.ProviderOpenAI {
		return "OpenAI"
	}
	return "Claude"
}

// deriveStatus is the global health badge, derived from the passive pool
// partition. Three branches, in order:
//
//   - nothing can take a request (ServingCreds == 0) → down. Note this is
//     ServingCreds, not HealthyCreds: a pool of nothing but half-open keys is
//     still up, and a pool whose "healthy" count was inflated by an expired
//     circuit breaker was never up in the first place.
//   - something can take a request but the pool is carrying half-open,
//     degraded or cooling credentials → degraded. The deterioration is visible
//     before it becomes an outage, which is the entire point.
//   - everything verified-good and a free slot → operational. Verified-good but
//     every slot busy stays degraded, as before.
func deriveStatus(ps ProviderSnapshot) string {
	if ps.TotalCreds == 0 || ps.ServingCreds == 0 {
		return "down"
	}
	if ps.ByState[string(auth.HealthHalfOpen)] > 0 ||
		ps.ByState[string(auth.HealthDegraded)] > 0 ||
		ps.CoolingCreds > 0 {
		return "degraded"
	}
	if ps.SlotAvailable {
		return "operational"
	}
	// Everything verified-good but every slot is busy — partially available.
	return "degraded"
}

// lastDays returns exactly n daily rollups ending today (oldest first),
// filling gaps with zero-total entries so the UI can render a fixed-width bar
// strip.
func lastDays(days map[string]*DayStat, n int, now time.Time) []DayStat {
	out := make([]DayStat, 0, n)
	for i := n - 1; i >= 0; i-- {
		key := now.AddDate(0, 0, -i).Format(dateLayout)
		if d, ok := days[key]; ok && d != nil {
			out = append(out, *d)
		} else {
			out = append(out, DayStat{Date: key})
		}
	}
	return out
}

func uptimePct(days []DayStat) float64 {
	var total, ok int
	for _, d := range days {
		total += d.Total
		ok += d.OK
	}
	if total == 0 {
		return 0
	}
	return float64(ok) / float64(total) * 100
}

func recentWindow(samples []Sample, window time.Duration, now time.Time) []Sample {
	cutoff := now.Add(-window)
	out := make([]Sample, 0, len(samples))
	for _, s := range samples {
		if s.TS.After(cutoff) {
			out = append(out, s)
		}
	}
	return out
}

// ---- persistence ----

func (m *Monitor) load() {
	if m.cfg.StateFile == "" {
		return
	}
	b, err := os.ReadFile(m.cfg.StateFile)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Warnf("monitor: failed to read %s: %v", m.cfg.StateFile, err)
		}
		return
	}
	var ps persistState
	if err := json.Unmarshal(b, &ps); err != nil {
		log.Warnf("monitor: corrupt state %s: %v", m.cfg.StateFile, err)
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for prov, st := range ps.Providers {
		if st == nil {
			continue
		}
		if st.Days == nil {
			st.Days = map[string]*DayStat{}
		}
		m.stores[auth.NormalizeProvider(prov)] = st
	}
}

func (m *Monitor) save() {
	if m.cfg.StateFile == "" {
		return
	}
	m.mu.Lock()
	ps := persistState{Providers: map[string]*provStore{}}
	for k, v := range m.stores {
		ps.Providers[k] = v
	}
	b, err := json.MarshalIndent(ps, "", "  ")
	m.mu.Unlock()
	if err != nil {
		log.Warnf("monitor: marshal state: %v", err)
		return
	}
	tmp := m.cfg.StateFile + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		log.Warnf("monitor: write state: %v", err)
		return
	}
	if err := os.Rename(tmp, m.cfg.StateFile); err != nil {
		log.Warnf("monitor: rename state: %v", err)
	}
}
