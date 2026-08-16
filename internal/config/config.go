package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wjsoj/cc-core/pricing"
	"gopkg.in/yaml.v3"

	"github.com/wjsoj/cc-core/auth"
)

// CodexWSConfig configures the Codex WebSocket transport. WebSocket carries
// protocol-level ping/pong, so it survives the multi-second silent gaps that
// truncate the legacy HTTP SSE path and surface to clients as "stream
// disconnected before completion". Real codex-tui 0.135.0 uses this transport.
type CodexWSConfig struct {
	// Enabled turns on the WS upgrade route on /v1/responses. Default false:
	// ship dark, enable per-deployment after smoke-testing against a real
	// ChatGPT token. The HTTP POST path is unaffected either way.
	Enabled bool `yaml:"enabled"`

	// ForceHTTP, when true, accepts a client WS upgrade but bridges it over the
	// proven HTTP upstream path instead of dialing an upstream WS. Emergency
	// degrade valve if the WS upstream misbehaves. Default false.
	ForceHTTP bool `yaml:"force_http,omitempty"`

	// BetaVersion selects the responses_websockets beta marker sent upstream:
	// "v2" (default, 2026-02-06) or "v1" (2026-02-04).
	BetaVersion string `yaml:"beta_version,omitempty"`

	// ReadLimitBytes caps a single inbound WS message. 0 => 16 MiB.
	ReadLimitBytes int64 `yaml:"read_limit_bytes,omitempty"`
}

type APIKey struct {
	Key      string `yaml:"key"`
	Provider string `yaml:"provider,omitempty"` // "anthropic" | "openai"; empty = anthropic (legacy)
	ProxyURL string `yaml:"proxy_url,omitempty"`
	Label    string `yaml:"label,omitempty"`
	BaseURL  string `yaml:"base_url,omitempty"`
	Group    string `yaml:"group,omitempty"`
	// ModelMap routes/rewrites client-facing model names to upstream model
	// names. See auth.Auth.ModelMap. Non-empty map turns this key into a
	// model-restricted credential. Empty = wildcard.
	ModelMap map[string]string `yaml:"model_map,omitempty"`
}

// EndpointConfig selects the listening host/port for one provider-scoped
// HTTP endpoint. An endpoint is considered live when Port > 0 and !Disabled
// — this lets users toggle Claude or Codex off without having to remove the
// whole section.
type EndpointConfig struct {
	Host     string `yaml:"host,omitempty"`
	Port     int    `yaml:"port,omitempty"`
	Disabled bool   `yaml:"disabled,omitempty"`
}

// IsEnabled reports whether this endpoint should be bound on startup.
func (e EndpointConfig) IsEnabled() bool { return !e.Disabled && e.Port > 0 }

// EndpointsConfig groups the per-provider endpoint configs. Both endpoints
// share the same upstream credential pool, client token store, usage store,
// and request log. They differ only in the routes they expose and the
// credential subset they route to.
type EndpointsConfig struct {
	Claude EndpointConfig `yaml:"claude"`
	Codex  EndpointConfig `yaml:"codex"`
}

type Config struct {
	LogLevel string `yaml:"log_level"`

	// Endpoints configures per-provider HTTP listeners. See the struct doc
	// on EndpointsConfig. Admin panel + public status page are served on
	// whichever endpoint is designated primary (claude if enabled, else
	// codex, else startup fails).
	Endpoints EndpointsConfig `yaml:"endpoints"`

	// Directory containing OAuth credential JSON files.
	AuthDir string `yaml:"auth_dir"`

	// Persistence file for usage statistics and session state.
	StateFile string `yaml:"state_file"`

	// Off-host disaster-recovery backup to an S3-compatible bucket. Disabled
	// by default. See BackupConfig.
	Backup BackupConfig `yaml:"backup,omitempty"`

	// Minutes of inactivity after which a client session releases its OAuth slot.
	ActiveWindowMinutes int `yaml:"active_window_minutes"`

	// DisplayTimezone is the IANA time zone (e.g. "Asia/Shanghai", "UTC",
	// "Local") used to assign usage/request-log rollups to day and hour
	// buckets in the admin panel. Without this, day boundaries fall on UTC
	// midnight, so on a +08:00 host all traffic before 08:00 local is filed
	// under the previous day and "today" reads empty until 08:00. Empty
	// defaults to "Asia/Shanghai". Display only — on-disk log file names and
	// retention stay UTC, and historical data re-buckets at query time.
	DisplayTimezone string `yaml:"display_timezone,omitempty"`

	// Token required to access the management panel and APIs.
	// Empty = panel disabled. Send as X-Admin-Token header (or Authorization: Bearer).
	AdminToken string `yaml:"admin_token,omitempty"`

	// URL prefix for the management panel. Changing this from the default
	// makes trivial `/admin`-style dictionary scans miss the panel. Must
	// start with "/" and must not end with "/". Default: /mgmt-console.
	AdminPath string `yaml:"admin_path,omitempty"`

	// API-key fallback pool. No concurrency limit.
	APIKeys []APIKey `yaml:"api_keys"`

	// Default upstream proxy URL used when an OAuth file has none specified.
	DefaultProxyURL string `yaml:"default_proxy_url,omitempty"`

	// Anthropic API base URL (override for testing).
	AnthropicBaseURL string `yaml:"anthropic_base_url,omitempty"`

	// OpenAI API base URL (override for testing; used for BYOK Codex API-key
	// routing). Defaults to https://api.openai.com.
	OpenAIBaseURL string `yaml:"openai_base_url,omitempty"`

	// Codex OAuth-authenticated requests hit the ChatGPT backend, not the
	// public OpenAI API. This base URL is here so installations behind
	// vendor relays can override it; normally unchanged.
	ChatGPTBackendBaseURL string `yaml:"chatgpt_backend_base_url,omitempty"`

	// If true, OAuth/API-key refresh+request uses utls Chrome fingerprint.
	UseUTLS bool `yaml:"use_utls"`

	// CodexWS controls the Codex /v1/responses WebSocket ingress (and the
	// WebSocket upstream to chatgpt.com). Disabled by default — the proxy keeps
	// serving Codex over the proven HTTP POST + SSE path until WS is enabled
	// per-deployment. See CodexWSConfig.
	CodexWS CodexWSConfig `yaml:"codex_ws"`

	// Directory for per-request JSONL logs (one file per day:
	// requests-YYYY-MM-DD.jsonl). Empty = disabled.
	LogDir string `yaml:"log_dir,omitempty"`

	// Default maximum concurrent in-flight requests per client token.
	// 0 = unlimited. Per-token overrides take precedence.
	ClientMaxConcurrent int `yaml:"client_max_concurrent"`

	// Maximum pool slots one client token may hold at once for a single
	// provider. 0 = unlimited.
	//
	// This is a fair-share cap, and it is NOT the same thing as
	// ClientMaxConcurrent. A pool slot is what a credential's max_concurrent
	// actually rations, and slots are held for wildly unequal durations: an
	// HTTP request holds one for seconds, but a codex-tui WebSocket session
	// holds one for as long as the socket is open — chatgpt.com keeps those
	// alive for up to an hour. So a couple of WS users can sit on most of a
	// provider's total slot capacity, and every other client then gets
	// "no credentials available" from a completely healthy fleet.
	//
	// Only NEW slots are refused; a session that already holds one keeps
	// working, so this never kills a conversation mid-flight.
	ClientMaxSessions int `yaml:"client_max_sessions"`

	// Multiplier applied on the Codex endpoint only to BOTH the per-token
	// concurrency cap (ClientMaxConcurrent) and the per-token RPM cap
	// (ClientRPM): the effective Codex limit on each gate is this multiple of
	// the per-token / global value. Codex CLI fans out many short, bursty
	// requests that would otherwise trip the shared caps. 0 falls back to a
	// sane default (see Normalize). Claude is unaffected. (Name kept for
	// config back-compat though it now governs RPM too.)
	CodexConcurrencyMultiplier int `yaml:"codex_concurrency_multiplier"`

	// Default sliding-window requests-per-minute cap per client token.
	// 0 = unlimited. Per-token overrides take precedence.
	ClientRPM int `yaml:"client_rpm"`

	// Days to retain rotated request logs. 0 = disable GC (keep forever).
	LogRetentionDays int `yaml:"log_retention_days,omitempty"`

	// Opt out of the SQLite index over the request log (requests.db inside
	// log_dir). The index is derived state that makes the admin panel's
	// aggregates cheap; disabling it falls back to re-scanning the JSONL on
	// every query, which at ~1M records costs tens of seconds per request.
	// Here as an escape hatch, not a tuning knob.
	LogIndexDisabled bool `yaml:"log_index_disabled,omitempty"`

	// Stop writing the daily-rotated requests-*.jsonl files and keep request
	// history only in the index. Halves the disk the log costs and turns a
	// token rename from a rewrite of every archived file into one UPDATE.
	//
	// The trade is real: while the archive exists the index can be deleted and
	// rebuilt from it, and a failed insert is retried from the file on the next
	// pass. With the archive off neither is true — a failed insert is a lost
	// record, and requests.db is the only copy on the box. The daily off-host
	// backup does carry it (buildManifest snapshots it, and refuses to ship an
	// archive without it while this is on), so the exposure is bounded by the
	// backup interval rather than open-ended. Requires the index (mutually
	// exclusive with log_index_disabled), and `cpa-claude export-requests` is
	// the way back out to a .jsonl file.
	LogJSONLDisabled bool `yaml:"log_jsonl_disabled,omitempty"`

	// Pricing overrides (optional). Built-in defaults cover claude-haiku-4-5,
	// claude-opus-4-6, claude-opus-4-7, claude-opus-4-8, and claude-sonnet-4-6.
	Pricing pricing.Config `yaml:"pricing"`

	// SaaS billing — per-token wallet, pricing groups, Z-Pay top-ups. When
	// SaaS.Enabled is false the proxy runs in legacy "no billing" mode:
	// requests bypass the balance check and no wallet rows are touched.
	SaaS SaaSConfig `yaml:"saas"`

	// Monitor — public uptime/availability monitoring shown on /status/.
	Monitor MonitorConfig `yaml:"monitor"`

	// ClientGuard — ingress filter that blocks non-interactive SDK / scripting
	// clients (raw SDKs, LiteLLM, python-requests, curl, …) from the Claude
	// endpoint while letting the interactive client family (Claude Code, Claude
	// Desktop, Cursor) through. Blocklist-based (see cc-core/clientguard). Only
	// applies to the Anthropic (Claude) endpoint; disabled by default.
	ClientGuard ClientGuardConfig `yaml:"client_guard"`
}

// ClientGuardConfig configures the Claude-endpoint ingress client filter.
type ClientGuardConfig struct {
	// Enabled turns the filter on. Off by default (backwards compatible).
	Enabled bool `yaml:"enabled"`

	// ExtraBlockedUserAgents are additional case-insensitive User-Agent
	// substrings to block on top of cc-core's defaults — e.g. add "axios/"
	// or "node-fetch" here if you observe such abuse and accept the risk of
	// catching Electron-based clients.
	ExtraBlockedUserAgents []string `yaml:"extra_blocked_user_agents,omitempty"`

	// AllowEmptyUserAgent, when true, permits requests with no User-Agent.
	// By default an empty UA is blocked — no interactive client omits it.
	AllowEmptyUserAgent bool `yaml:"allow_empty_user_agent,omitempty"`
}

// MonitorConfig configures the public status-page uptime monitor. The monitor
// keeps one logical probe per provider (Claude, OpenAI) — it does not split
// OAuth vs API-key. Two signals are combined:
//
//   - Passive (always on, zero cost): reads the live credential pool to report
//     whether the provider currently has a free slot and how many credentials
//     are healthy.
//   - Active (every IntervalMinutes): sends one minimal request through this
//     server's own local endpoint using ClientToken, confirming a real slot
//     can serve a real model. Recorded as the uptime timeseries.
//
// Active probing is skipped for a provider when its model is empty or no
// ClientToken is configured; the passive signal still drives the status badge.
type MonitorConfig struct {
	// Enabled toggles the whole subsystem. When false, /status/api/monitor
	// still responds but reports passive pool state only with no history.
	Enabled bool `yaml:"enabled"`

	// IntervalMinutes is the active-probe cadence. Default 10.
	IntervalMinutes int `yaml:"interval_minutes,omitempty"`

	// ClientToken is a valid client token used to authenticate the self-probe
	// against the local proxy. Required for active probing. Use a dedicated,
	// low/zero-cost token. When empty, active probing is disabled.
	ClientToken string `yaml:"client_token,omitempty"`

	// ClaudeModel / OpenAIModel are the models the active probe requests on
	// each endpoint. Pick the cheapest model that real traffic uses. Empty
	// disables active probing for that provider. Defaults: claude-haiku-4-5
	// and gpt-5.3-codex.
	ClaudeModel string `yaml:"claude_model,omitempty"`
	OpenAIModel string `yaml:"openai_model,omitempty"`

	// StateFile is where probe history is persisted (90-day daily rollups +
	// recent 24h samples). Defaults to <config-dir>/monitor.json.
	StateFile string `yaml:"state_file,omitempty"`
}

// SaaSConfig configures the per-token wallet + Z-Pay top-up subsystem.
type SaaSConfig struct {
	// Enabled toggles balance-gated billing. When false, the proxy runs as
	// before — no balance check, no wallet debit. Existing tokens keep
	// working with no quota.
	Enabled bool `yaml:"enabled"`

	// DBPath is the SQLite file holding wallets, orders, and pricing
	// groups. Defaults to <config-dir>/saas.db. Created with mode 0600.
	DBPath string `yaml:"db_path,omitempty"`

	// Site is the user-visible site name embedded in the payment "subject"
	// shown to the payer in their Alipay/WeChat app. Defaults to
	// "CPA-Claude".
	Site string `yaml:"site,omitempty"`

	// Payment is the Z-Pay merchant config. When PID/Key are empty the
	// server falls back to MockGateway — useful for offline development
	// since real money never moves.
	Payment PaymentConfig `yaml:"payment"`

	// Exchange controls how the live CNY/USD rate is fetched. Defaults
	// are sane (jsdelivr-hosted free API, 1h refresh, fallback 7.2).
	Exchange ExchangeConfig `yaml:"exchange"`

	// Invoice configures fapiao issuance — directory to drop admin-
	// uploaded PDFs, the Resend transactional email key, and the ops
	// inbox that receives new-request notifications. All fields optional;
	// missing key means invoice-related emails are logged but not sent
	// (the user can still apply, the admin can still upload — only the
	// auto-email step degrades).
	Invoice InvoiceConfig `yaml:"invoice"`

	// Promotions are time-boxed price cuts. Each entry replaces the
	// multiplier its provider's traffic would otherwise be billed at, for as
	// long as its window is open — including the per-key override that
	// upstream API-key capacity carries, so a user never sees the same model
	// billed at two rates depending on which credential happened to serve it.
	// That makes a promotion genuinely expensive on API-key traffic; it is a
	// pricing decision, not an oversight.
	//
	// The window is wall-clock, so a promotion ends on its own with nothing to
	// deploy — the usual way a hand-rolled promotion goes wrong is that
	// somebody forgets to turn it off.
	Promotions []PricingPromotion `yaml:"promotions,omitempty"`
}

// PricingPromotion is one time-boxed price cut for a provider.
type PricingPromotion struct {
	// Name is operator-facing only; it appears in logs and the request-log
	// note so a surprising charge can be traced back to the promotion.
	Name string `yaml:"name,omitempty"`

	// Provider is the cc-core canonical provider ("anthropic" / "openai") or
	// a friendly alias ("claude" / "codex"), normalized on load.
	Provider string `yaml:"provider"`

	// Multiplier is what official cost is charged at while the window is
	// open, replacing the pricing-group and per-key values. Must be > 0;
	// entries with a non-positive multiplier are dropped on load rather than
	// silently billing at zero.
	Multiplier float64 `yaml:"multiplier"`

	// Start and End bound the window. Both are required and are parsed with
	// their offset, so a config written in +08:00 means what it says
	// regardless of the server's zone.
	Start time.Time `yaml:"start"`
	End   time.Time `yaml:"end"`
}

// Covers reports whether this promotion applies to a provider at time now.
// The window is inclusive of Start and exclusive of End, so two back-to-back
// promotions cannot both claim the instant between them.
func (p PricingPromotion) Covers(provider string, now time.Time) bool {
	if p.Provider != provider || p.Multiplier <= 0 {
		return false
	}
	if p.Start.IsZero() || p.End.IsZero() || !p.End.After(p.Start) {
		return false
	}
	return !now.Before(p.Start) && now.Before(p.End)
}

// InvoiceConfig — fapiao + transactional email config. All optional.
type InvoiceConfig struct {
	// PDFDir is where admin-uploaded PDFs land. Defaults to
	// <config-dir>/invoices/. Created lazily with mode 0700.
	PDFDir string `yaml:"pdf_dir,omitempty"`

	// TitleSuggestURL overrides the in-app company-name suggestion
	// upstream. Leave empty to use the bundled default
	// (aiqicha.baidu.com suggest API).
	TitleSuggestURL string `yaml:"title_suggest_url,omitempty"`

	// Resend transactional email. Key empty → invoice emails are logged
	// only; the rest of the flow still works.
	ResendAPIKey string `yaml:"resend_api_key,omitempty"`
	ResendFrom   string `yaml:"resend_from,omitempty"`
	OpsEmail     string `yaml:"ops_email,omitempty"`

	// ResendWebhookSecret is the "whsec_..." string from Resend's webhook
	// detail page. Used to verify inbound-email webhook signatures.
	// Empty → POST /api/webhooks/resend-inbound returns 503 (the route
	// must be configured before inbound mail will land in the admin inbox).
	ResendWebhookSecret string `yaml:"resend_webhook_secret,omitempty"`
}

// PaymentConfig — Z-Pay merchant credentials. Never logged.
type PaymentConfig struct {
	BaseURL   string `yaml:"base_url,omitempty"`   // default https://zpayz.cn
	PID       string `yaml:"pid,omitempty"`        // 商户ID
	Key       string `yaml:"key,omitempty"`        // 商户密钥
	NotifyURL string `yaml:"notify_url,omitempty"` // public webhook
	ReturnURL string `yaml:"return_url,omitempty"` // optional post-pay redirect
}

// DefaultCNYPerUSD is the USD→CNY rate a deployment falls back to when it has
// configured none and no live rate is available.
//
// Exported because it is not only applyDefaults' business: the usage-statement
// export ends its own rate chain here too, and every yuan figure on that
// document is one multiplication by this number. Two copies of the literal
// would let a bumped default quietly bill and invoice at different rates.
const DefaultCNYPerUSD = 7.2

// ExchangeConfig — live USD/CNY rate cache.
type ExchangeConfig struct {
	URL                string  `yaml:"url,omitempty"`
	RefreshIntervalMin int     `yaml:"refresh_interval_min,omitempty"`
	FallbackCNYPerUSD  float64 `yaml:"fallback_cny_per_usd,omitempty"`
}

// DisplayLocation resolves display_timezone to a *time.Location, defaulting to
// Asia/Shanghai and returning nil when the zone can't be loaded (no tzdata),
// which callers treat as "stay in UTC".
//
// Shared rather than inlined because the index materializes day labels in this
// zone: a tool that resolves it differently from the server would read the
// index as if every label were wrong and rebuild it.
func (c *Config) DisplayLocation() *time.Location {
	name := strings.TrimSpace(c.DisplayTimezone)
	if name == "" {
		name = "Asia/Shanghai"
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil
	}
	return loc
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	applyDefaults(cfg, path)
	// Turning off both the archive and the index would leave the request-log
	// writer with nowhere to put a record. Refuse the combination rather than
	// start up and silently discard request history.
	if cfg.LogJSONLDisabled && cfg.LogIndexDisabled {
		return nil, fmt.Errorf("config: log_jsonl_disabled requires the index; unset log_index_disabled")
	}
	if err := normalizePromotions(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// normalizePromotions canonicalizes provider aliases and rejects entries that
// could not price anything correctly.
//
// A malformed promotion is refused at startup rather than skipped at runtime.
// Skipping it would mean an operator announces a discount, the server quietly
// declines to apply it, and the first evidence is a customer comparing their
// bill against the announcement — whereas a server that will not start is
// noticed immediately, while nobody has been overcharged yet.
func normalizePromotions(c *Config) error {
	for i := range c.SaaS.Promotions {
		p := &c.SaaS.Promotions[i]
		label := p.Name
		if label == "" {
			label = fmt.Sprintf("#%d", i+1)
		}
		p.Provider = auth.NormalizeProvider(strings.TrimSpace(p.Provider))
		if p.Provider == "" {
			return fmt.Errorf("config: promotion %s: provider is required", label)
		}
		if p.Multiplier <= 0 {
			return fmt.Errorf("config: promotion %s: multiplier must be > 0 (got %v); a zero multiplier would serve the provider for free", label, p.Multiplier)
		}
		if p.Start.IsZero() || p.End.IsZero() {
			return fmt.Errorf("config: promotion %s: start and end are both required", label)
		}
		if !p.End.After(p.Start) {
			return fmt.Errorf("config: promotion %s: end (%s) must be after start (%s)", label, p.End.Format(time.RFC3339), p.Start.Format(time.RFC3339))
		}
	}
	return nil
}

// DefaultCodexConcurrencyMultiplier is the fallback for
// Config.CodexConcurrencyMultiplier when it is unset (0).
const DefaultCodexConcurrencyMultiplier = 5

func applyDefaults(c *Config, path string) {
	if c.Endpoints.Claude.Port == 0 {
		c.Endpoints.Claude.Port = 8317
	}
	if c.Endpoints.Claude.Host == "" {
		c.Endpoints.Claude.Host = "0.0.0.0"
	}
	if c.Endpoints.Codex.Port == 0 {
		// Codex endpoint defaults to configured-but-disabled so merely
		// upgrading the server binary doesn't flip on an empty listener.
		c.Endpoints.Codex.Port = 8318
		c.Endpoints.Codex.Disabled = true
	}
	if c.Endpoints.Codex.Host == "" {
		c.Endpoints.Codex.Host = "0.0.0.0"
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
	if c.ActiveWindowMinutes == 0 {
		c.ActiveWindowMinutes = 5
	}
	if c.ClientMaxConcurrent == 0 {
		c.ClientMaxConcurrent = 15
	}
	if c.CodexConcurrencyMultiplier == 0 {
		c.CodexConcurrencyMultiplier = DefaultCodexConcurrencyMultiplier
	}
	if c.ClientRPM == 0 {
		c.ClientRPM = 60
	}
	if c.AnthropicBaseURL == "" {
		c.AnthropicBaseURL = "https://api.anthropic.com"
	}
	if c.OpenAIBaseURL == "" {
		c.OpenAIBaseURL = "https://api.openai.com"
	}
	if c.ChatGPTBackendBaseURL == "" {
		c.ChatGPTBackendBaseURL = "https://chatgpt.com/backend-api"
	}
	if c.CodexWS.BetaVersion == "" {
		c.CodexWS.BetaVersion = "v2"
	}
	if c.CodexWS.ReadLimitBytes == 0 {
		c.CodexWS.ReadLimitBytes = 16 << 20
	}
	dir := filepath.Dir(path)
	if c.AuthDir == "" {
		c.AuthDir = filepath.Join(dir, "auths")
	} else if !filepath.IsAbs(c.AuthDir) {
		c.AuthDir = filepath.Join(dir, c.AuthDir)
	}
	if c.StateFile == "" {
		c.StateFile = filepath.Join(dir, "state.json")
	} else if !filepath.IsAbs(c.StateFile) {
		c.StateFile = filepath.Join(dir, c.StateFile)
	}
	if c.LogDir != "" && !filepath.IsAbs(c.LogDir) {
		c.LogDir = filepath.Join(dir, c.LogDir)
	}
	if c.LogRetentionDays == 0 {
		c.LogRetentionDays = 90
	}
	if c.SaaS.DBPath == "" {
		c.SaaS.DBPath = filepath.Join(dir, "saas.db")
	} else if !filepath.IsAbs(c.SaaS.DBPath) {
		c.SaaS.DBPath = filepath.Join(dir, c.SaaS.DBPath)
	}
	c.Backup.applyDefaults()
	if c.SaaS.Site == "" {
		c.SaaS.Site = "CPA-Claude"
	}
	if c.SaaS.Exchange.RefreshIntervalMin == 0 {
		c.SaaS.Exchange.RefreshIntervalMin = 60
	}
	if c.SaaS.Exchange.FallbackCNYPerUSD <= 0 {
		c.SaaS.Exchange.FallbackCNYPerUSD = DefaultCNYPerUSD
	}
	if c.SaaS.Invoice.PDFDir == "" {
		c.SaaS.Invoice.PDFDir = filepath.Join(dir, "invoices")
	} else if !filepath.IsAbs(c.SaaS.Invoice.PDFDir) {
		c.SaaS.Invoice.PDFDir = filepath.Join(dir, c.SaaS.Invoice.PDFDir)
	}
	if c.SaaS.Invoice.OpsEmail == "" {
		c.SaaS.Invoice.OpsEmail = "907401616@qq.com"
	}
	if c.SaaS.Invoice.ResendFrom == "" {
		// Resend's onboarding default. Operators with a verified domain
		// should override this in config.yaml.
		c.SaaS.Invoice.ResendFrom = "CPA-Claude <onboarding@resend.dev>"
	}
	if c.SaaS.Invoice.TitleSuggestURL == "" {
		// 天眼查 web-app's autocomplete endpoint. POST JSON {"keyword": q},
		// returns data[]{comName, taxCode}. Unauthenticated but IP-rate-
		// limited; exceeding it falls back to local-history matches only.
		// v2 (not v3) — v3 locks unauthenticated callers after a handful of
		// queries per IP with errorCode 302004; v2 stays open. Same shape.
		c.SaaS.Invoice.TitleSuggestURL = "https://capi.tianyancha.com/cloud-tempest/search/suggest/v2"
	}
	if c.Monitor.IntervalMinutes == 0 {
		c.Monitor.IntervalMinutes = 10
	}
	if c.Monitor.ClaudeModel == "" {
		c.Monitor.ClaudeModel = "claude-haiku-4-5"
	}
	if c.Monitor.OpenAIModel == "" {
		c.Monitor.OpenAIModel = "gpt-5.3-codex"
	}
	if c.Monitor.StateFile == "" {
		c.Monitor.StateFile = filepath.Join(dir, "monitor.json")
	} else if !filepath.IsAbs(c.Monitor.StateFile) {
		c.Monitor.StateFile = filepath.Join(dir, c.Monitor.StateFile)
	}
	p := strings.TrimSpace(c.AdminPath)
	if p == "" {
		p = "/mgmt-console"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	p = strings.TrimRight(p, "/")
	if p == "" {
		p = "/mgmt-console"
	}
	c.AdminPath = p
}
