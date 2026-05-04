package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/wjsoj/CPA-Claude/internal/auth"
)

// Sidecar emulates the auxiliary traffic real Claude Code 2.1.126 fires
// alongside /v1/messages. Three phases:
//
//   - Phase A (always): quota probe (Haiku "quota") at session start.
//     Hides the strongest stealth-detection signal — a healthy OAuth
//     account whose request stream contains zero Haiku quota probes.
//
//   - Phase B (bootstrap burst): the 8 other GET/POST sidecars CC fires
//     at process start, each with the *exact* User-Agent / anthropic-beta /
//     Connection header captured for that endpoint in
//     crack/oauth/rows/01..10. Real CC mixes Bun fetch, axios 1.13.6,
//     claude-code/<ver>, and claude-cli/<ver> across these endpoints —
//     getting any of them wrong is itself a fingerprint, so each sidecar
//     step pins its own client identity.
//
//   - Phase C (heartbeat): a goroutine that POSTs
//     /api/event_logging/v2/batch every ~18s ±40% with a realistic
//     ClaudeCodeInternalEvent payload (env block matches our pinned
//     2.1.126 / Linux / x64 / Node v24.3.0 fingerprint). Stops 5 min
//     after the session goes idle — mirrors a real CLI process exit.
//
// A virtual session is identified by (accountKey, clientToken). All three
// phases share one bootstrapSessionID per session, matching what real CC
// does (rows 01/06/14 all carried the same session UUID).

const (
	// sidecarSessionIdleTTL controls when an idle virtual session is
	// considered closed. The next request from the same (account,
	// clientToken) re-fires the bootstrap burst and restarts heartbeat.
	sidecarSessionIdleTTL = 30 * time.Minute

	// sidecarGCInterval is how often the background sweeper visits the
	// session map to evict idle entries.
	sidecarGCInterval = 5 * time.Minute

	// sidecarRequestTimeout caps how long any single sidecar HTTP call
	// may take. Each step runs in its own goroutine (or in the bootstrap
	// dispatcher goroutine) and never blocks the user request.
	sidecarRequestTimeout = 30 * time.Second

	// bootstrapWaitCap caps how long the first business /v1/messages from
	// a fresh (account, clientToken) pair will wait for sidecar bootstrap
	// to reach the quota_probe step (real CC's last pre-business call,
	// captured at T+1.27s). 5s comfortably accommodates slow proxy lanes
	// while ensuring a wedged upstream can't hang user traffic.
	bootstrapWaitCap = 5 * time.Second

	// heartbeatBaseInterval is the median spacing between event_logging
	// heartbeats. Real captures show 10-25s between batches; we centre
	// at 18s and apply ±40% jitter so two co-running sessions don't
	// emit synchronously (the synchrony itself is a fingerprint).
	heartbeatBaseInterval = 18 * time.Second
	heartbeatJitter       = 0.4

	// heartbeatActiveWindow: if no Notify in this window, stop emitting
	// heartbeats — the user has effectively closed the CLI. Triggered
	// from inside the heartbeat ticker, so the cap on lingering goroutines
	// is min(idleTTL, activeWindow + heartbeatBaseInterval).
	heartbeatActiveWindow = 5 * time.Minute

	// Datadog logs intake — Phase D. Real CC ships its own telemetry to
	// Anthropic's Datadog org alongside event_logging. The intake key is
	// global (verified across two completely independent capture sessions
	// with different auth modes), so hardcoding is safe; it must be
	// re-checked on each major CC release in case Anthropic rotates.
	datadogIntakeURL = "https://http-intake.logs.us5.datadoghq.com/api/v2/logs"
	datadogIntakeKey = "pubea5604404508cdd34afb69e6f42a05bc"

	// datadogBaseInterval centres the Datadog heartbeat. Real captures
	// show an irregular 15-30s spacing — we centre at 25s so the two
	// telemetry streams (event_logging + datadog) don't beat together.
	datadogBaseInterval = 25 * time.Second
	datadogJitter       = 0.4
)

// quotaProbeBeta and quotaProbeModel come from crack/oauth/rows/06.
const (
	quotaProbeBeta  = "oauth-2025-04-20,interleaved-thinking-2025-05-14,redact-thinking-2026-02-12,context-management-2025-06-27,prompt-caching-scope-2026-01-05"
	quotaProbeModel = "claude-haiku-4-5-20251001"
)

// User-Agent strings used across sidecar endpoints. Real CC 2.1.126 uses
// FOUR distinct HTTP clients: Bun fetch (GrowthBook only), axios 1.13.6
// (penguin / mcp-registry / mcp_servers / downloads), claude-code/<ver>
// (oauth/account/settings, bootstrap, event_logging), and the main
// claude-cli UA (grove + chat). Mismatching is detectable.
const (
	uaBun        = "Bun/1.3.14"
	uaAxios      = "axios/1.13.6"
	uaClaudeCode = "claude-code/" + CLICurrentVersion
	uaClaudeCLI  = claudeCLIUserAgent // shared with the chat path
)

// sidecarMgr tracks the lifecycle of every virtual session and dispatches
// the appropriate auxiliary traffic. Safe for concurrent use.
type sidecarMgr struct {
	enabled    bool
	useUTLS    bool
	baseURL    string // typically https://api.anthropic.com
	httpClient *http.Client

	sessions sync.Map // sidecarSessionKey → *sidecarSession

	stopOnce sync.Once
	stopCh   chan struct{}
}

type sidecarConfig struct {
	enabled bool
	useUTLS bool
	baseURL string
}

type sidecarSessionKey struct {
	accountKey  string
	clientToken string
}

type sidecarSession struct {
	// lastSeen is unix-nano of the most recent Notify, atomic so the GC
	// and the heartbeat ticker can read without contention.
	lastSeen atomic.Int64
	// bootstrapFired is the latch ensuring exactly one bootstrap+probe
	// dispatch per (session lifetime).
	bootstrapFired atomic.Bool
	// bootstrapSessionID is the UUID shared by the bootstrap burst,
	// the quota probe, and the event_logging heartbeats. Computed
	// once when the session is born.
	bootstrapSessionID string
	// bootstrapReady is closed once the quota_probe step has been
	// dispatched (or once bootstrap aborts), letting the first business
	// /v1/messages from this session wait until real CC's
	// bootstrap-then-business sequence is observable upstream. Allocated
	// at session creation; never reused — a long-idle session gets a
	// brand-new sidecarSession with a fresh channel.
	bootstrapReady chan struct{}
	// cancel stops the heartbeat goroutine; called when the session is
	// evicted or when the heartbeat itself decides the user is gone.
	cancel context.CancelFunc
}

func newSidecarMgr(cfg sidecarConfig) *sidecarMgr {
	m := &sidecarMgr{
		enabled: cfg.enabled,
		useUTLS: cfg.useUTLS,
		baseURL: strings.TrimRight(cfg.baseURL, "/"),
		stopCh:  make(chan struct{}),
	}
	if !m.enabled {
		return m
	}
	go m.gcLoop()
	return m
}

// Notify registers a request from (a, clientToken). Returns a channel
// that's closed once bootstrap has reached the quota-probe step (or once
// bootstrap aborted) — caller may select on it to delay the FIRST business
// /v1/messages from this session, so upstream sees the canonical real-CC
// "GrowthBook → settings → bootstrap → quota probe → business" ordering
// instead of business-first. Returns nil when no waiting is appropriate
// (sidecar disabled, non-OAuth credential, etc.). Already-closed channels
// (subsequent calls within an active session) make the wait a no-op.
//
// Always returns "fast" in the sense that the channel itself is allocated
// synchronously; every actual HTTP call still runs in its own goroutine.
func (m *sidecarMgr) Notify(a *auth.Auth, clientToken string) <-chan struct{} {
	if m == nil || !m.enabled || a == nil || a.Kind != auth.KindOAuth {
		return nil
	}
	now := time.Now().UnixNano()
	key := sidecarSessionKey{accountKey: a.AccountKey(), clientToken: clientToken}

	fresh := &sidecarSession{bootstrapReady: make(chan struct{})}
	v, loaded := m.sessions.LoadOrStore(key, fresh)
	sess := v.(*sidecarSession)
	prevSeen := sess.lastSeen.Swap(now)

	// "New session" if first ever or returning from idle past the TTL.
	// On idle wake, replace the session wholesale (new bootstrapReady,
	// new fired latch) — mutating in place would race with concurrent
	// readers of bootstrapReady.
	isNew := !loaded
	if !isNew && prevSeen > 0 && time.Duration(now-prevSeen) >= sidecarSessionIdleTTL {
		if sess.cancel != nil {
			sess.cancel()
		}
		sess = &sidecarSession{bootstrapReady: make(chan struct{})}
		sess.lastSeen.Store(now)
		m.sessions.Store(key, sess)
		isNew = true
	}
	if !isNew {
		return sess.bootstrapReady
	}
	if !sess.bootstrapFired.CompareAndSwap(false, true) {
		// Another concurrent first-request already kicked off bootstrap;
		// share its readiness channel.
		return sess.bootstrapReady
	}

	sess.bootstrapSessionID = bootstrapSessionIDFor(key.accountKey, key.clientToken)
	ctx, cancel := context.WithCancel(context.Background())
	sess.cancel = cancel

	go m.runBootstrap(ctx, a, key, sess.bootstrapSessionID, sess.bootstrapReady)
	go m.runHeartbeat(ctx, a, sess)
	go m.runDatadogHeartbeat(ctx, a, sess)
	return sess.bootstrapReady
}

// bootstrapSessionIDFor derives a UUIDv4-shaped session id stable for the
// lifetime of one virtual session. Distinct from the per-conversation
// chat session_id (which rotates as the user starts new conversations).
func bootstrapSessionIDFor(accountKey, clientToken string) string {
	sum := sha256.Sum256([]byte("cpa-claude-bootstrap/" + accountKey + "|" + clientToken))
	return uuidFromBytes(sum[:16])
}

// =============================================================================
// Bootstrap burst — Phase B
// =============================================================================

// bootstrapStep describes one auxiliary HTTP call CC fires at startup.
// Order, URLs, methods, UAs, and beta values are all from
// crack/oauth/rows/01..10. delayFromStart is the timestamp captured in
// that row relative to row 1's startTime.
type bootstrapStep struct {
	name           string
	method         string
	url            string // absolute; templates expand from sessionID/etc inside builder
	delayFromStart time.Duration
	userAgent      string
	beta           string // "" = omit Anthropic-Beta header
	anthropicVer   string // "" = omit Anthropic-Version header
	contentType    string // "" = no body / no header
	connection     string // "keep-alive" or "close"
	noAuth         bool   // true → don't set Authorization (downloads.claude.ai etc.)
	bodyBuilder    func(a *auth.Auth, sessionID string) ([]byte, error)
	// extraHeaders sets endpoint-specific headers (e.g. x-service-name).
	extraHeaders map[string]string
}

// realBootstrapSteps returns the 9-step sequence fired at session start.
// Step 6 is the quota probe. Steps' delays are the relative timestamps
// captured in crack/oauth/rows/01..10 (rounded to ms). They are NOT
// jittered — real CC fires them deterministically because each step
// depends on a different bootstrap subsystem coming online.
func realBootstrapSteps(baseURL string) []bootstrapStep {
	return []bootstrapStep{
		{
			name:           "growthbook_eval",
			method:         "POST",
			url:            baseURL + "/api/eval/sdk-zAZezfDKGoZuXXKe",
			delayFromStart: 0,
			userAgent:      uaBun,
			beta:           "oauth-2025-04-20",
			contentType:    "application/json",
			connection:     "keep-alive",
			bodyBuilder:    buildGrowthBookBody,
		},
		{
			name:           "oauth_account_settings",
			method:         "GET",
			url:            baseURL + "/api/oauth/account/settings",
			delayFromStart: 160 * time.Millisecond,
			userAgent:      uaClaudeCode,
			beta:           "oauth-2025-04-20",
			connection:     "close",
		},
		{
			name:           "claude_code_grove",
			method:         "GET",
			url:            baseURL + "/api/claude_code_grove",
			delayFromStart: 160 * time.Millisecond,
			userAgent:      uaClaudeCLI,
			beta:           "oauth-2025-04-20",
			connection:     "close",
		},
		{
			name:           "claude_cli_bootstrap",
			method:         "GET",
			url:            baseURL + "/api/claude_cli/bootstrap",
			delayFromStart: 1250 * time.Millisecond,
			userAgent:      uaClaudeCode,
			beta:           "oauth-2025-04-20",
			contentType:    "application/json",
			connection:     "close",
		},
		{
			name:           "claude_code_penguin_mode",
			method:         "GET",
			url:            baseURL + "/api/claude_code_penguin_mode",
			delayFromStart: 1250 * time.Millisecond,
			userAgent:      uaAxios,
			beta:           "oauth-2025-04-20",
			connection:     "close",
		},
		{
			name:           "quota_probe",
			method:         "POST",
			url:            baseURL + "/v1/messages",
			delayFromStart: 1270 * time.Millisecond,
			userAgent:      uaClaudeCLI,
			beta:           quotaProbeBeta,
			anthropicVer:   claudeAnthropicVersion,
			contentType:    "application/json",
			connection:     "keep-alive",
			bodyBuilder:    buildQuotaProbeBody,
			extraHeaders: map[string]string{
				"X-App":                       "cli",
				"X-Stainless-Lang":            claudeStainlessLang,
				"X-Stainless-Runtime":         claudeStainlessRuntime,
				"X-Stainless-Runtime-Version": claudeStainlessRuntimeV,
				"X-Stainless-Package-Version": claudeStainlessPackageV,
				"X-Stainless-Os":              claudeStainlessOS,
				"X-Stainless-Arch":            claudeStainlessArch,
				"X-Stainless-Timeout":         claudeStainlessTimeout,
				"X-Stainless-Retry-Count":     claudeStainlessRetryCnt,
				"Anthropic-Dangerous-Direct-Browser-Access": "true",
			},
		},
		{
			name:           "mcp_registry",
			method:         "GET",
			url:            baseURL + "/mcp-registry/v0/servers?version=latest&limit=100&visibility=commercial%2Cgsuite%2Centerprise%2Chealth",
			delayFromStart: 1950 * time.Millisecond,
			userAgent:      uaAxios,
			connection:     "close",
		},
		{
			name:           "v1_mcp_servers",
			method:         "GET",
			url:            baseURL + "/v1/mcp_servers?limit=1000",
			delayFromStart: 1950 * time.Millisecond,
			userAgent:      uaAxios,
			beta:           "mcp-servers-2025-12-04",
			anthropicVer:   claudeAnthropicVersion,
			contentType:    "application/json",
			connection:     "close",
		},
		{
			name:           "claude_code_releases",
			method:         "GET",
			url:            "https://downloads.claude.ai/claude-code-releases/latest",
			delayFromStart: 2380 * time.Millisecond,
			userAgent:      uaAxios,
			connection:     "close",
			noAuth:         true, // public CDN — sending a Bearer here is itself a tell
		},
	}
}

// runBootstrap dispatches the 9-step burst with the exact relative timing
// real CC produces. Each step is best-effort; failures are logged at
// debug level and never propagate. Cancellation: if ctx is cancelled mid-
// burst (session evicted), abort.
//
// `ready` is closed once the quota_probe step has been dispatched (success
// or failure) so the first business /v1/messages from this session can
// proceed. Defer-close also fires on early ctx-cancel so a stuck shutdown
// can't hang client requests waiting on a session that will never finish.
func (m *sidecarMgr) runBootstrap(ctx context.Context, a *auth.Auth, key sidecarSessionKey, sessionID string, ready chan struct{}) {
	closed := false
	closeReady := func() {
		if !closed {
			closed = true
			close(ready)
		}
	}
	defer closeReady()

	steps := realBootstrapSteps(m.baseURL)
	start := time.Now()
	for _, step := range steps {
		// Sleep until the step's relative time.
		due := start.Add(step.delayFromStart)
		if w := time.Until(due); w > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(w):
			}
		}
		if err := m.sendBootstrapStep(ctx, a, sessionID, step); err != nil {
			log.Debugf("sidecar: %s via %s failed: %v", step.name, a.ID, err)
		}
		if step.name == "quota_probe" {
			// quota_probe is the last pre-business step real CC fires.
			// Unblock waiting business request now; the remaining
			// mcp/release steps continue running in this goroutine but
			// no longer gate user traffic.
			closeReady()
		}
	}
	log.Debugf("sidecar: bootstrap complete via %s (clientToken=%s, sessionID=%s)",
		a.ID, maskClientToken(key.clientToken), sessionID)
}

// sendBootstrapStep builds and dispatches one step. Bodies are only sent
// for POST steps with a non-nil builder.
func (m *sidecarMgr) sendBootstrapStep(parent context.Context, a *auth.Auth, sessionID string, step bootstrapStep) error {
	ctx, cancel := context.WithTimeout(parent, sidecarRequestTimeout)
	defer cancel()

	var body []byte
	if step.bodyBuilder != nil {
		b, err := step.bodyBuilder(a, sessionID)
		if err != nil {
			return fmt.Errorf("build body: %w", err)
		}
		body = b
	}
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, step.method, step.url, bodyReader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if !step.noAuth {
		token, _ := a.Credentials()
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("User-Agent", step.userAgent)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	if step.userAgent == uaBun {
		// Bun fetch sends a slightly different Accept default.
		req.Header.Set("Accept", "*/*")
	}
	req.Header.Set("Accept-Encoding", "gzip, br")
	if step.beta != "" {
		req.Header.Set("Anthropic-Beta", step.beta)
	}
	if step.anthropicVer != "" {
		req.Header.Set("Anthropic-Version", step.anthropicVer)
	}
	if step.contentType != "" {
		req.Header.Set("Content-Type", step.contentType)
	}
	if step.connection != "" {
		req.Header.Set("Connection", step.connection)
	}
	for k, v := range step.extraHeaders {
		req.Header.Set(k, v)
	}
	// Quota probe gets the X-Claude-Code-Session-Id header to match real CC.
	if step.name == "quota_probe" {
		req.Header.Set("X-Claude-Code-Session-Id", sessionID)
		req.Header.Set("X-Client-Request-Id", newRequestUUID())
	}

	client := m.httpClient
	if client == nil {
		client = auth.ClientFor(a.ProxyURL, m.useUTLS)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("transport: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("upstream %d", resp.StatusCode)
	}
	return nil
}

// =============================================================================
// Body builders for POST sidecars
// =============================================================================

// buildGrowthBookBody mirrors the row-1 capture: an attributes object
// listing all the per-account properties Anthropic uses to bucket
// experiments. Most fields are stable per account; firstTokenTime is a
// process-start-ish timestamp.
func buildGrowthBookBody(a *auth.Auth, sessionID string) ([]byte, error) {
	deviceID := DeviceIDFor(a.AccountKey())
	body := map[string]any{
		"attributes": map[string]any{
			"id":               deviceID,
			"sessionId":        sessionID,
			"deviceID":         deviceID,
			"platform":         "linux",
			"organizationUUID": a.OrganizationUUID,
			"accountUUID":      a.AccountUUIDValue(),
			"userType":         "external",
			"subscriptionType": "max",
			"rateLimitTier":    "default_claude_max_20x",
			"firstTokenTime":   time.Now().UnixMilli(),
			"email":            strings.TrimSpace(a.Email),
			"appVersion":       CLICurrentVersion,
			"entrypoint":       "cli",
		},
		"forcedVariations": map[string]any{},
		"forcedFeatures":   []any{},
		"url":              "",
	}
	return json.Marshal(body)
}

// buildQuotaProbeBody returns the byte-for-byte shape of row 6:
// model=Haiku, max_tokens=1, single-word "quota", with metadata.user_id
// carrying the same identity (device, account, session) the rest of the
// bootstrap traffic uses.
func buildQuotaProbeBody(a *auth.Auth, sessionID string) ([]byte, error) {
	deviceID := DeviceIDFor(a.AccountKey())
	uid := buildJSONUserID(deviceID, a.AccountUUIDValue(), sessionID)
	body := map[string]any{
		"model":      quotaProbeModel,
		"max_tokens": 1,
		"messages": []map[string]any{
			{"role": "user", "content": "quota"},
		},
		"metadata": map[string]any{"user_id": uid},
	}
	return json.Marshal(body)
}

// =============================================================================
// Event logging heartbeat — Phase C
// =============================================================================

// runHeartbeat emits one ClaudeCodeInternalEvent batch per tick to
// /api/event_logging/v2/batch. Stops when:
//   - parent ctx is cancelled (session evicted, server shutdown), OR
//   - the session has been idle past heartbeatActiveWindow.
func (m *sidecarMgr) runHeartbeat(ctx context.Context, a *auth.Auth, sess *sidecarSession) {
	// Wait for the bootstrap burst to finish before our first heartbeat —
	// real CC's first event_logging batch lands at T+10s, well after
	// bootstrap. We use 8s here so the heartbeat starts after the last
	// bootstrap step (T+2.4s) but well before T+15s.
	select {
	case <-ctx.Done():
		return
	case <-time.After(8 * time.Second):
	}

	for {
		if isHeartbeatIdle(sess) {
			return
		}
		if err := m.sendHeartbeat(ctx, a, sess.bootstrapSessionID); err != nil {
			log.Debugf("sidecar: heartbeat via %s failed: %v", a.ID, err)
		}
		wait := jitteredHeartbeatInterval()
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

func isHeartbeatIdle(sess *sidecarSession) bool {
	last := sess.lastSeen.Load()
	if last == 0 {
		return true
	}
	return time.Since(time.Unix(0, last)) > heartbeatActiveWindow
}

func jitteredHeartbeatInterval() time.Duration {
	d := float64(heartbeatBaseInterval)
	delta := (rand.Float64()*2 - 1) * heartbeatJitter * d
	return time.Duration(d + delta)
}

// sendHeartbeat POSTs one ClaudeCodeInternalEvent batch. Body and headers
// match crack/oauth/rows/14 (event_logging/v2/batch with
// User-Agent: claude-code/<ver>, beta: oauth-2025-04-20,
// x-service-name: claude-code).
func (m *sidecarMgr) sendHeartbeat(parent context.Context, a *auth.Auth, sessionID string) error {
	ctx, cancel := context.WithTimeout(parent, sidecarRequestTimeout)
	defer cancel()

	body, err := buildHeartbeatBody(a, sessionID)
	if err != nil {
		return fmt.Errorf("build body: %w", err)
	}
	url := m.baseURL + "/api/event_logging/v2/batch"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	token, _ := a.Credentials()
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Encoding", "gzip, br")
	req.Header.Set("Anthropic-Beta", "oauth-2025-04-20")
	req.Header.Set("User-Agent", uaClaudeCode)
	req.Header.Set("X-Service-Name", "claude-code")
	req.Header.Set("Connection", "close")

	client := m.httpClient
	if client == nil {
		client = auth.ClientFor(a.ProxyURL, m.useUTLS)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("transport: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("upstream %d", resp.StatusCode)
	}
	return nil
}

// buildHeartbeatBody constructs a single-event batch shaped like row 14.
// Volatile fields (timestamps, event_id, process metric) are refreshed
// each tick; the env block stays fixed at our pinned 2.1.126 / Linux /
// x64 / Node v24.3.0 fingerprint so it matches the X-Stainless headers.
//
// Event name `tengu_dir_search` is what real CC emits most frequently
// during normal use (file-completion lookups), so it blends in with the
// rest of an active session's telemetry.
func buildHeartbeatBody(a *auth.Auth, sessionID string) ([]byte, error) {
	now := time.Now().UTC()
	deviceID := DeviceIDFor(a.AccountKey())

	processMetrics := map[string]any{
		"uptime":      time.Since(processStart).Seconds(),
		"rss":         320_000_000,
		"heapTotal":   40_000_000,
		"heapUsed":    34_000_000,
		"external":    13_000_000,
		"arrayBuffers": 521,
		"constrainedMemory": 1_590_133_555_2,
		"cpuUsage": map[string]any{
			"user":   500_000,
			"system": 160_000,
		},
	}
	processB64, err := json.Marshal(processMetrics)
	if err != nil {
		return nil, err
	}

	additionalMeta := map[string]any{
		"rh":                  randomHex16(),
		"durationMs":          rand.Intn(20) + 1,
		"managedFilesFound":   0,
		"userFilesFound":      0,
		"projectFilesFound":   0,
		"projectDirsSearched": 0,
		"subdir":              pickSubdir(),
	}
	additionalB64, err := json.Marshal(additionalMeta)
	if err != nil {
		return nil, err
	}

	envBlock := map[string]any{
		"platform":               "linux",
		"node_version":           claudeStainlessRuntimeV,
		"terminal":               "konsole",
		"package_managers":       "npm,yarn,pnpm",
		"runtimes":               "bun,deno,node",
		"is_running_with_bun":    true,
		"is_ci":                  false,
		"is_claubbit":            false,
		"is_github_action":       false,
		"is_claude_code_action":  false,
		"is_claude_ai_auth":      true,
		"version":                CLICurrentVersion,
		"arch":                   claudeStainlessArch,
		"is_claude_code_remote":  false,
		"deployment_environment": "unknown-linux",
		"is_conductor":           false,
		"version_base":           CLICurrentVersion,
		"build_time":             "2026-04-30T16:01:00Z",
		"is_local_agent_mode":    false,
		"vcs":                    "git",
		"platform_raw":           "linux",
		"shell":                  "zsh",
	}

	event := map[string]any{
		"event_type": "ClaudeCodeInternalEvent",
		"event_data": map[string]any{
			"event_name":          "tengu_dir_search",
			"client_timestamp":    now.Format("2006-01-02T15:04:05.000Z"),
			"model":               "claude-opus-4-7[1m]",
			"session_id":          sessionID,
			"user_type":           "external",
			"betas":               claudeAnthropicBetaFull,
			"env":                 envBlock,
			"entrypoint":          "cli",
			"is_interactive":      true,
			"client_type":         "cli",
			"process":             base64.StdEncoding.EncodeToString(processB64),
			"additional_metadata": base64.StdEncoding.EncodeToString(additionalB64),
			"auth": map[string]any{
				"organization_uuid": a.OrganizationUUID,
				"account_uuid":      a.AccountUUIDValue(),
			},
			"event_id":  newRequestUUID(),
			"device_id": deviceID,
			"email":     strings.TrimSpace(a.Email),
		},
	}
	body := map[string]any{"events": []any{event}}
	return json.Marshal(body)
}

// processStart snapshots when the proxy itself was started, so the
// `uptime` we report in heartbeat process metrics grows monotonically
// like a real long-running CLI process would.
var processStart = time.Now()

// randomHex16 returns a 16-char lowercase hex string (used for the rh
// field in the additional_metadata blob — real CC uses a request hash
// that we have no equivalent for, so a random one suffices).
func randomHex16() string {
	const hex = "0123456789abcdef"
	b := make([]byte, 16)
	for i := range b {
		b[i] = hex[rand.Intn(len(hex))]
	}
	return string(b)
}

// pickSubdir rotates through the subdirectory names real CC searches
// during a normal session — keeps heartbeats from looking identical.
func pickSubdir() string {
	dirs := []string{"commands", "output-styles", "agents", "tools", "skills"}
	return dirs[rand.Intn(len(dirs))]
}

// =============================================================================
// Datadog logs heartbeat — Phase D
// =============================================================================

// runDatadogHeartbeat emits one Datadog log batch per tick. Anthropic
// ingests these into the same Datadog org as their global telemetry,
// using the public client token; user identity is in the body
// (subscription_type / user_bucket / device tags) not in the key.
//
// Same lifecycle rules as runHeartbeat: starts after bootstrap settles,
// stops on context cancel or 5-min idle.
func (m *sidecarMgr) runDatadogHeartbeat(ctx context.Context, a *auth.Auth, sess *sidecarSession) {
	// Stagger the first datadog tick relative to event_logging so the two
	// streams don't synchronize. event_logging starts at +8s; we start
	// at +14s.
	select {
	case <-ctx.Done():
		return
	case <-time.After(14 * time.Second):
	}

	for {
		if isHeartbeatIdle(sess) {
			return
		}
		if err := m.sendDatadogHeartbeat(ctx, a, sess.bootstrapSessionID); err != nil {
			log.Debugf("sidecar: datadog heartbeat via %s failed: %v", a.ID, err)
		}
		wait := jitteredDatadogInterval()
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

func jitteredDatadogInterval() time.Duration {
	d := float64(datadogBaseInterval)
	delta := (rand.Float64()*2 - 1) * datadogJitter * d
	return time.Duration(d + delta)
}

// sendDatadogHeartbeat POSTs one tengu_dir_search event to the Datadog
// intake. Headers and body match crack/oauth/rows/16/21 — note that the
// Authorization header is NOT set (the dd-api-key header carries auth)
// and User-Agent is axios/1.13.6 (the Datadog client lib in CC).
func (m *sidecarMgr) sendDatadogHeartbeat(parent context.Context, a *auth.Auth, sessionID string) error {
	ctx, cancel := context.WithTimeout(parent, sidecarRequestTimeout)
	defer cancel()

	body, err := buildDatadogHeartbeatBody(a, sessionID)
	if err != nil {
		return fmt.Errorf("build body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, datadogIntakeURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	// Datadog intake does NOT take an Anthropic Bearer — auth is the
	// dd-api-key header. Sending Authorization here is a tell.
	req.Header.Set("DD-API-KEY", datadogIntakeKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Encoding", "gzip, br")
	req.Header.Set("User-Agent", uaAxios)
	req.Header.Set("Connection", "close")

	client := m.httpClient
	if client == nil {
		// Datadog has no proxy/uTLS coupling to the Anthropic credential —
		// use a plain default client. (auth.ClientFor would still work but
		// would route through the credential's proxy unnecessarily.)
		client = auth.ClientFor("", false)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("transport: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("upstream %d", resp.StatusCode)
	}
	return nil
}

// userBucketFor maps an account anchor to a stable bucket in [0,99],
// matching the per-account experiment bucket the real CC reports as
// `user_bucket`. Stable forever per account so all heartbeats from the
// same account land in one Datadog slice (different bucket per
// heartbeat would itself be a fingerprint).
func userBucketFor(accountKey string) int {
	sum := sha256.Sum256([]byte("cpa-claude-bucket/" + accountKey))
	return int(sum[0]) % 100
}

// buildDatadogHeartbeatBody returns a JSON array of one event matching
// the row 16/21 shape — all the per-event "env" fields are flattened
// into the top level (Datadog's preferred indexing layout), and ddtags
// is a comma-joined string of indexed dimensions.
func buildDatadogHeartbeatBody(a *auth.Auth, sessionID string) ([]byte, error) {
	bucket := userBucketFor(a.AccountKey())
	tags := []string{
		"event:tengu_dir_search",
		"arch:" + claudeStainlessArch,
		"client_type:cli",
		"entrypoint:cli",
		"model:claude-opus-4-7",
		"platform:linux",
		"subscription_type:max",
		fmt.Sprintf("user_bucket:%d", bucket),
		"user_type:external",
		"version:" + CLICurrentVersion,
		"version_base:" + CLICurrentVersion,
	}
	processMetrics := map[string]any{
		"uptime":            time.Since(processStart).Seconds(),
		"rss":               320_000_000,
		"heapTotal":         40_000_000,
		"heapUsed":          34_000_000,
		"external":          13_000_000,
		"arrayBuffers":      938,
		"constrainedMemory": 1_590_133_555_2,
		"cpuUsage": map[string]any{
			"user":   500_000,
			"system": 160_000,
		},
	}
	event := map[string]any{
		"ddsource":               "nodejs",
		"ddtags":                 strings.Join(tags, ","),
		"message":                "tengu_dir_search",
		"service":                "claude-code",
		"hostname":               "claude-code",
		"env":                    "external",
		"model":                  "claude-opus-4-7",
		"session_id":             sessionID,
		"user_type":              "external",
		"betas":                  claudeAnthropicBetaFull,
		"entrypoint":             "cli",
		"is_interactive":         "true",
		"client_type":            "cli",
		"process_metrics":        processMetrics,
		"swe_bench_run_id":       "",
		"swe_bench_instance_id":  "",
		"swe_bench_task_id":      "",
		"subscription_type":      "max",
		"rh":                     randomHex16(),
		"platform":               "linux",
		"platform_raw":           "linux",
		"arch":                   claudeStainlessArch,
		"node_version":           claudeStainlessRuntimeV,
		"terminal":               "konsole",
		"shell":                  "zsh",
		"package_managers":       "npm,yarn,pnpm",
		"runtimes":               "bun,deno,node",
		"is_running_with_bun":    true,
		"is_ci":                  false,
		"is_claubbit":            false,
		"is_claude_code_remote":  false,
		"is_local_agent_mode":    false,
		"is_conductor":           false,
		"is_github_action":       false,
		"is_claude_code_action":  false,
		"is_claude_ai_auth":      true,
		"version":                CLICurrentVersion,
		"version_base":           CLICurrentVersion,
		"build_time":             "2026-04-30T16:01:00Z",
		"deployment_environment": "unknown-linux",
		"vcs":                    "git",
		"user_bucket":            bucket,
	}
	return json.Marshal([]any{event})
}

// =============================================================================
// Lifecycle
// =============================================================================

func (m *sidecarMgr) Stop() {
	if m == nil {
		return
	}
	m.stopOnce.Do(func() { close(m.stopCh) })
	// Cancel every live session so heartbeat goroutines exit promptly.
	m.sessions.Range(func(_, v any) bool {
		if sess, ok := v.(*sidecarSession); ok && sess.cancel != nil {
			sess.cancel()
		}
		return true
	})
}

// gcLoop evicts virtual sessions whose lastSeen is older than the idle
// TTL, cancelling their heartbeat goroutine on the way out.
func (m *sidecarMgr) gcLoop() {
	t := time.NewTicker(sidecarGCInterval)
	defer t.Stop()
	for {
		select {
		case <-m.stopCh:
			return
		case <-t.C:
			cutoff := time.Now().Add(-sidecarSessionIdleTTL).UnixNano()
			m.sessions.Range(func(k, v any) bool {
				sess := v.(*sidecarSession)
				if sess.lastSeen.Load() < cutoff {
					if sess.cancel != nil {
						sess.cancel()
					}
					m.sessions.Delete(k)
				}
				return true
			})
		}
	}
}
