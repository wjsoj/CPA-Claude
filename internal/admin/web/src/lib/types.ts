// API response types. Hand-derived from internal/admin/admin.go — keep in
// sync when the Go structs change.

export interface Counts {
  input_tokens?: number;
  output_tokens?: number;
  cache_create_tokens?: number;
  cache_read_tokens?: number;
  requests?: number;
  errors?: number;
}

export interface DayEntry {
  date: string;
  counts: Counts;
}

export interface UsageSummary {
  total: Counts;
  sum_24h: Counts;
  sum_5h: Counts;
  last_used?: string;
  daily?: DayEntry[];
  total_cost_usd?: number;
}

export type Provider = "anthropic" | "openai";

export interface AuthRow {
  id: string;
  kind: "oauth" | "apikey";
  provider: Provider;
  plan_type?: string;
  label: string;
  email?: string;
  proxy_url: string;
  base_url?: string;
  group?: string;
  max_concurrent: number;
  // API-key selection priority (lower = used first). 0 for OAuth / unranked.
  order: number;
  // API-key billing override: official × this. 0/absent = use group multiplier.
  price_multiplier?: number;
  active_clients: number;
  client_tokens: string[];
  disabled: boolean;
  quota_exceeded: boolean;
  quota_reset_at?: string;
  expires_at?: string;
  last_failure?: string;
  file_backed: boolean;
  healthy: boolean;
  hard_failure: boolean;
  failure_reason?: string;
  last_client_cancel?: string;
  client_cancel_reason?: string;
  model_map?: Record<string, string>;
  usage?: UsageSummary;
  codex_rate_limits?: Record<string, string>;
  codex_rate_limits_at?: string;
  // Live snapshot from chatgpt.com/backend-api/wham/usage (active probe via
  // cc-core FetchCodexUsage). Carries the official portal view of the
  // primary/secondary rate-limit windows, credit balance, and plan type.
  codex_usage?: CodexUsage;
  codex_usage_at?: string;
}

export interface CodexUsageRateWindow {
  used_percent?: number;
  limit_window_seconds?: number;
  reset_after_seconds?: number;
  reset_at?: number; // unix seconds
}

export interface CodexUsage {
  user_id?: string;
  account_id?: string;
  email?: string;
  plan_type?: string;
  updated?: string;
  rate_limit?: {
    allowed?: boolean;
    limit_reached?: boolean;
    primary_window?: CodexUsageRateWindow;
    secondary_window?: CodexUsageRateWindow;
  };
  credits?: {
    has_credits?: boolean;
    unlimited?: boolean;
    overage_limit_reached?: boolean;
    balance?: string;
    approx_local_messages?: number[];
    approx_cloud_messages?: number[];
  };
  spend_control?: {
    reached?: boolean;
    individual_limit?: number | null;
  };
  // Historically a bare string ("primary"/"secondary"), but the wham/usage
  // backend now sometimes returns an object ({type, resets_at, ...}). Accept
  // any shape; render via reachedLabel() so it never prints "[object Object]".
  rate_limit_reached_type?: string | { type?: string } | null;
}

export interface CodexUsageResponse {
  usage?: CodexUsage;
}

export interface ClientRow {
  token: string;
  full_token?: string;
  label?: string;
  group?: string;
  // Provider allow-list: which upstream providers this token may use.
  // Empty / absent = both (unrestricted). Values are canonical provider
  // ids ("anthropic" | "openai").
  providers?: string[];
  // Current rolling-week spend (informational). Access is gated on the
  // wallet balance below, not on this.
  weekly_usd: number;
  // SaaS wallet — balance + pricing-group assignment. Zero when SaaS
  // billing is disabled or the wallet row hasn't been created yet.
  balance_usd: number;
  // True when the wallet is at/below zero — the proxy refuses new
  // requests in that state. Derived client-side from balance_usd <= 0
  // when SaaS billing is enabled; always false otherwise.
  blocked: boolean;
  group_id?: number;
  pricing_group?: string;
  // Per-token RPM override. 0 / absent = fall back to global default.
  rpm?: number;
  total: { cost_usd: number; requests: number };
  last_used?: string;
  managed?: boolean;
  from_config?: boolean;
}

export interface PricingEntry {
  input_per_1m: number;
  output_per_1m: number;
  cache_read_per_1m: number;
  cache_create_per_1m: number;
}

export interface Pricing {
  default: PricingEntry;
  provider_defaults?: Record<string, PricingEntry>;
  models: Record<string, PricingEntry>;
}

export interface Summary {
  auths: AuthRow[];
  clients: ClientRow[];
  active_window_minutes: number;
  default_proxy_url?: string;
  current_week?: string;
  pricing?: Pricing;
}

export interface RequestEntry {
  ts: string;
  client?: string;
  provider?: Provider;
  model: string;
  auth_id: string;
  auth_label?: string;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_create_tokens: number;
  cost_usd: number;
  status: number;
  duration_ms: number;
}

export interface RequestAgg {
  count: number;
  cost_usd: number;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_create_tokens: number;
  errors: number;
}

export interface RequestsResp {
  entries: RequestEntry[];
  summary: RequestAgg;
  by_client: Record<string, RequestAgg>;
  by_model: Record<string, RequestAgg>;
  by_day: Record<string, RequestAgg>;
  scanned: number;
}

export interface HourBucket {
  hour: string; // RFC3339 UTC, truncated to hour
  count: number;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_create_tokens: number;
  cost_usd: number;
  errors: number;
}

export interface HourlyResp {
  buckets: HourBucket[];
}

export interface OrphanToken {
  token: string;
  masked: string;
  label?: string;
  last_used?: string;
  total: { cost_usd: number; requests: number };
}

export interface OAuthStart {
  session_id: string;
  auth_url: string;
}

export interface UpstreamUsage {
  status?: number;
  error?: string;
  body?: {
    five_hour?: UsageWindow;
    seven_day?: UsageWindow;
    seven_day_oauth_apps?: UsageWindow;
    seven_day_opus?: UsageWindow;
    seven_day_sonnet?: UsageWindow;
    seven_day_cowork?: UsageWindow;
    iguana_necktie?: UsageWindow;
    extra_usage?: {
      is_enabled?: boolean;
      utilization?: number;
      used_credits?: number;
      monthly_limit?: number;
    };
  };
}

export interface UsageWindow {
  utilization?: number;
  resets_at?: string;
}

export interface UpstreamProfile {
  body?: {
    account?: {
      email?: string;
      email_address?: string;
      has_claude_max?: boolean;
      has_claude_pro?: boolean;
    };
    organization?: {
      rate_limit_tier?: string;
    };
  };
}

export interface UpstreamResponse {
  usage?: UpstreamUsage;
  profile?: UpstreamProfile;
}
