// Package monitor implements the public /status/ uptime monitor. It keeps one
// logical health record per provider (Claude, OpenAI) and combines two signals:
//
//   - Passive (zero cost, always live): reads auth.Pool to report whether the
//     provider currently has a free credential slot and how many credentials
//     are healthy. This is the primary "is there capacity right now" signal.
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
func (s Sample) realFailure() bool { return s.Status >= 500 }

// healthySignal reports whether the sample should count as healthy (green) in
// the uptime timeline and toward the uptime percentage.
func (s Sample) healthySignal() bool { return s.OK || !s.realFailure() }

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
		if cred := m.pickAPIKeyCred(provider); cred != nil {
			m.record(t.Provider, m.probe(ctx, provider, cred, t.Model))
		} else {
			m.record(t.Provider, m.passiveSample(provider))
		}
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
		if h, _, _, _ := live.HealthSnapshot(); !h {
			continue
		}
		return live
	}
	return nil
}

// passiveSample records the pool's passive health for a provider we don't
// actively probe (OAuth-only). OK when at least one credential is healthy, so
// the strip reads green; a synthetic 503 marks "no healthy credentials".
func (m *Monitor) passiveSample(provider string) Sample {
	healthy, _, _ := m.liveCounts(provider)
	s := Sample{TS: time.Now()}
	if healthy > 0 {
		s.OK = true
		s.Status = 200
	} else {
		s.Status = 503
		s.Err = "no healthy credentials"
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
	// Only a genuine provider-side failure (5xx) counts against the day. A
	// transport error/timeout (no HTTP response) or a probe-side rejection (4xx
	// — bad probe body, revoked/rate-limited key, unknown model) is "no signal":
	// the probe never went through OAuth, so it can't tell us the provider is
	// down. Defer to the passive pool signal instead of painting the bar red.
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
	Name          string    `json:"name"` // "claude" | "openai"
	Provider      string    `json:"provider"`
	Operational   string    `json:"operational"` // operational | degraded | down | unknown
	SlotAvailable bool      `json:"slot_available"`
	HealthyCreds  int       `json:"healthy_creds"`
	TotalCreds    int       `json:"total_creds"`
	ProbeEnabled  bool      `json:"probe_enabled"`
	LastProbe     *Sample   `json:"last_probe,omitempty"`
	Uptime90d     []DayStat `json:"uptime_90d"`
	Uptime90dPct  float64   `json:"uptime_90d_pct"`
	Timeline24h   []Sample  `json:"timeline_24h"`
}

// Snapshot is the full /status/api/monitor payload.
type Snapshot struct {
	GeneratedAt time.Time          `json:"generated_at"`
	Interval    int                `json:"interval_minutes"`
	Providers   []ProviderSnapshot `json:"providers"`
}

// liveCounts reports the passive pool signal for one provider: how many
// credentials are healthy, the total for the provider, and whether at least one
// healthy credential has a free concurrency slot right now.
func (m *Monitor) liveCounts(provider string) (healthy, total int, slot bool) {
	provider = auth.NormalizeProvider(provider)
	for _, st := range m.pool.Status() {
		info := st.Auth
		if auth.NormalizeProvider(info.Provider) != provider {
			continue
		}
		total++
		isHealthy := false
		if live := m.pool.FindByID(info.ID); live != nil {
			h, _, _, _ := live.HealthSnapshot()
			isHealthy = h
		} else {
			// Fall back to the snapshot fields if the live auth is gone.
			isHealthy = !info.Disabled && info.QuotaExceededAt.IsZero()
		}
		if !isHealthy {
			continue
		}
		healthy++
		// cap 0 = unlimited (API keys, uncapped OAuth). A free slot exists
		// when uncapped or active sessions are below the cap.
		if info.MaxConcurrent == 0 || st.ActiveClients < info.MaxConcurrent {
			slot = true
		}
	}
	return healthy, total, slot
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
		healthy, total, slot := m.liveCounts(provider)
		st := m.stores[provider]

		ps := ProviderSnapshot{
			Name:          displayName(provider),
			Provider:      provider,
			SlotAvailable: slot,
			HealthyCreds:  healthy,
			TotalCreds:    total,
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

// deriveStatus combines the passive capacity signal with the most recent active
// probe outcome into a single badge.
func deriveStatus(ps ProviderSnapshot) string {
	if ps.TotalCreds == 0 || ps.HealthyCreds == 0 {
		return "down"
	}
	// Only a genuine provider-side failure (5xx) degrades the badge. A nodata
	// probe (transport error / timeout) or a probe-side 4xx rejection is "no
	// signal" — the probe bypasses OAuth, so it can't prove the provider is
	// down — and we defer to the passive pool capacity, the source of truth.
	probeBad := ps.ProbeEnabled && ps.LastProbe != nil && ps.LastProbe.realFailure()
	if ps.SlotAvailable && !probeBad {
		return "operational"
	}
	// Healthy credentials exist, but either every slot is busy or the last
	// end-to-end probe failed — partially available.
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
