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
  last_used?: string;
  daily?: DayEntry[];
}

export interface AuthRow {
  id: string;
  kind: "oauth" | "apikey";
  label: string;
  email?: string;
  proxy_url: string;
  base_url?: string;
  group?: string;
  max_concurrent: number;
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
}

export interface ClientRow {
  token: string;
  full_token?: string;
  label?: string;
  group?: string;
  weekly_usd: number;
  weekly_limit: number;
  blocked: boolean;
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
