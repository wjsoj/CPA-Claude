import { ApiError } from "./api";
import type { CredStateFields } from "./cred-state";

// Per-credential health as the public endpoints report it. The seven-state
// `state` field is authoritative — see lib/cred-state.ts. The legacy booleans
// are still present on the wire and still typed here, but nothing should read
// them except the transitional fallback inside credState().
export interface StatusAuthRow extends CredStateFields {
  kind: "oauth" | "apikey";
  provider?: string;
  label?: string;
  group?: string;
  healthy: boolean;
}

/**
 * Fleet counts. The seven state buckets partition the pool:
 *
 *   healthy + half_open + degraded + quota + cooling + unhealthy + disabled == total
 *
 * `unhealthy` is exactly the hard_failed bucket (it is no longer a catch-all).
 * `serving` is ORTHOGONAL — it overlaps healthy/half_open/degraded and must
 * never be added into that sum.
 */
export interface StatusCounts {
  total: number;
  healthy: number;
  half_open: number;
  degraded: number;
  quota: number;
  cooling: number;
  unhealthy: number;
  disabled: number;
  serving: number;
  oauth: number;
  apikey: number;
  models: number;
}

/** Per-provider availability, keyed by normalized provider ("anthropic" /
 *  "openai"). A provider with no credentials at all is simply absent. */
export type PoolWire = Record<
  string,
  {
    available: boolean;
    total: number;
    serving: number;
    worst_state: string;
    by_state: Record<string, number>;
  }
>;

export interface StatusOverview {
  counts: StatusCounts;
  window_24h: {
    requests: number;
    cost_usd: number;
    errors: number;
  };
  // Pool availability, keyed by provider. Run it through normalizePools(),
  // which also copes with a server that predates the field.
  pool?: PoolWire;
  auths: StatusAuthRow[];
}

export interface StatusRecent {
  ts: string;
  provider?: string;
  model?: string;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_create_tokens: number;
  // cost_usd is the official upstream cost (catalog × tokens). The
  // status page surfaces billed_usd (post-multiplier wallet debit) as
  // the primary number; cost_usd is shown inside the hover popup for
  // transparency.
  cost_usd: number;
  billed_usd?: number;
  multiplier?: number;
  status: number;
  duration_ms: number;
  stream?: boolean;
  auth_label?: string;
  auth_kind?: string;
}

export interface StatusAgg {
  count: number;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_create_tokens: number;
  cost_usd: number;
  errors: number;
  total_duration_ms: number;
}

export interface StatusWeekEntry {
  week: string;
  cost: {
    tokens: Record<string, number>;
    cost_usd: number;
    requests: number;
  };
}

export interface StatusDailyEntry {
  date: string;
  cost_usd: number;
  requests: number;
}

export interface StatusTokenResult {
  masked: string;
  found: boolean;
  name?: string;
  group?: string;
  balance_usd: number;
  blocked: boolean;
  weekly_used_usd: number;
  pricing_group?: string;
  group_id?: number;
  // Workspace (group shared pool) — present only when the token is a member.
  workspace?: string;
  pool_avail_usd?: number;
  is_team_admin?: boolean;
  is_team_member?: boolean;
  total: { tokens: Record<string, number>; cost_usd: number; requests: number };
  weekly?: StatusWeekEntry[];
  daily?: StatusDailyEntry[];
  last_used?: string;
  recent?: StatusRecent[];
  recent_total?: number;
  window_24h?: StatusAgg;
}

export interface StatusHistoryResp {
  entries: StatusRecent[];
  total: number;
  offset: number;
  limit: number;
}

async function fetchJSON<T>(
  path: string,
  opts: RequestInit = {},
): Promise<T> {
  const res = await fetch(path, {
    ...opts,
    headers: { "Content-Type": "application/json", ...(opts.headers || {}) },
  });
  const text = await res.text();
  let data: any = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    data = { raw: text };
  }
  if (!res.ok) {
    throw new ApiError((data && data.error) || `HTTP ${res.status}`, res.status);
  }
  return data as T;
}

export function loadStatusOverview(): Promise<StatusOverview> {
  return fetchJSON<StatusOverview>("/status/api/overview");
}

// Shape matches internal/admin/status.go statusDashboard. by_client keys
// are deterministic pseudonyms (Alice/Bob/...), not real customer labels.
export interface StatusDashboardResp {
  // NOTE: what used to be `pool` (the flat total/healthy/quota/... object) is
  // now `counts`. The `pool` key belongs to the per-provider availability
  // aggregate. Reading `data.pool.total` yields undefined against the current
  // server — go through `counts` or normalizePools(data.pool).
  counts?: StatusCounts;
  pool?: PoolWire;
  pricing?: import("./types").Pricing;
  requests_14d: {
    summary: import("./types").RequestAgg;
    by_client: Record<string, import("./types").RequestAgg>;
    by_model: Record<string, import("./types").RequestAgg>;
    by_day: Record<string, import("./types").RequestAgg>;
  };
  requests_all: {
    summary: import("./types").RequestAgg;
    by_client: Record<string, import("./types").RequestAgg>;
    by_model: Record<string, import("./types").RequestAgg>;
    by_day: Record<string, import("./types").RequestAgg>;
  };
  hourly_24h: import("./types").HourBucket[];
}

export function loadStatusDashboard(): Promise<StatusDashboardResp> {
  return fetchJSON<StatusDashboardResp>("/status/api/dashboard");
}

// ---- uptime monitor (shape matches internal/monitor.Snapshot) ----

export interface MonitorSample {
  ts: string;
  ok: boolean;
  status: number;
  latency_ms: number;
  err?: string;
  // Historical: the frontend used to treat this as an override that turned a
  // failed probe green. It no longer does — the uptime strips render `ok`
  // exactly as the backend recorded it. Kept on the type only because old
  // persisted samples still carry it.
  pool_healthy?: boolean;
}

export interface MonitorDay {
  date: string;
  total: number;
  ok: number;
}

export type MonitorStatus = "operational" | "degraded" | "down" | "unknown";

export interface MonitorProvider {
  name: string; // "Claude" | "OpenAI"
  provider: string; // "anthropic" | "openai"
  operational: MonitorStatus;
  slot_available: boolean;
  healthy_creds: number;
  total_creds: number;
  // Seven-state contract additions. serving_creds ≥ healthy_creds: a
  // credential can be carrying traffic while still unverified or degraded.
  // healthy_creds now counts state === "healthy" only.
  serving_creds?: number;
  cooling_creds?: number;
  worst_state?: string;
  by_state?: Record<string, number>;
  probe_enabled: boolean;
  last_probe?: MonitorSample;
  uptime_90d: MonitorDay[];
  uptime_90d_pct: number;
  timeline_24h: MonitorSample[];
}

export interface MonitorResp {
  generated_at: string;
  interval_minutes: number;
  providers: MonitorProvider[];
}

export function loadStatusMonitor(): Promise<MonitorResp> {
  return fetchJSON<MonitorResp>("/status/api/monitor");
}

export function queryStatusTokens(
  tokens: string[],
): Promise<{ results: StatusTokenResult[] }> {
  return fetchJSON<{ results: StatusTokenResult[] }>("/status/api/query", {
    method: "POST",
    body: JSON.stringify({ tokens }),
  });
}

export function queryStatusHistory(args: {
  token: string;
  offset?: number;
  limit?: number;
  from?: string;
  to?: string;
}): Promise<StatusHistoryResp> {
  return fetchJSON<StatusHistoryResp>("/status/api/history", {
    method: "POST",
    body: JSON.stringify(args),
  });
}

// ---- Usage statement (reimbursement export) ----------------------------
//
// The preview and the PDF are built from the same server-side scan, so the
// numbers in the export dialog are the ones that land in the file. Amounts are
// the real settled charges in yuan, each converted at the rate its own request
// settled at — which is why the same range exported twice reads the same.

export interface StatementModelRow {
  model: string;
  requests: number;
  billed_cny: number;
}

export interface StatementPreview {
  from: string;
  to: string;
  timezone: string;
  requests: number;
  billed_cny: number;
  /** Ledger-confirmed spend with no request-log row behind it. */
  unitemised_cny?: number;
  /** The ledger's own total for the range (billed + unitemised). */
  charged_cny?: number;
  lifetime_requests: number;
  lifetime_billed_cny: number;
  lifetime_days: number;
  /**
   * True when `from`/`to` were derived by scanning backward from the newest
   * request until spend reached `target_cny`, rather than named by the
   * caller. The dialog must caption the range differently in this mode.
   */
  by_target?: boolean;
  /** The requested figure that produced the range, when `by_target`. */
  target_cny?: number;
  /**
   * The token's total Alipay-paid CNY — the ceiling a target amount must not
   * exceed. Populated whenever SaaS billing is enabled, not only on target
   * requests, so the dialog can show it before the user switches into
   * target mode.
   */
  total_paid_cny?: number;
  /** Rows the PDF will itemise; fewer than `requests` when `truncated`. */
  detail_lines: number;
  truncated: boolean;
  by_model: StatementModelRow[];
}

export function loadStatementPreview(args: {
  token: string;
  from?: string;
  to?: string;
  target_cny?: number;
}): Promise<StatementPreview> {
  return fetchJSON<StatementPreview>("/status/api/statement", {
    method: "POST",
    body: JSON.stringify(args),
  });
}

export async function downloadStatementPDF(args: {
  token: string;
  from?: string;
  to?: string;
  target_cny?: number;
}): Promise<Blob> {
  const res = await fetch("/status/api/statement.pdf", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(args),
  });
  if (!res.ok) {
    const text = await res.text();
    throw new ApiError(text || `HTTP ${res.status}`, res.status);
  }
  return await res.blob();
}

// ---- Wallet / SaaS billing ---------------------------------------------
//
// All wallet endpoints (except /rate, /notify, /groups) require the active
// token in `Authorization: Bearer <token>`. The status SPA stores the
// "active" token in localStorage under ACTIVE_TOKEN_KEY — see
// loadActiveToken/saveActiveToken below.

export interface WalletBalance {
  balance_usd: number;
  group_id: number;
  group_name?: string;
  claude_multiplier?: number;
  codex_multiplier?: number;
}

export interface WalletTx {
  id: number;
  kind: "topup" | "charge" | "adjust" | "refund";
  amount_usd: number;
  ref: string;
  note: string;
  created_at: number;
}

export interface WalletOrder {
  out_trade_no: string;
  cny_amount: number;
  usd_credit: number;
  rate: number;
  status: "pending" | "paid" | "expired" | "failed";
  trade_no: string;
  qr_code?: string;
  pay_url?: string;
  img?: string;
  created_at: number;
  paid_at: number;
}

export interface WalletPricingGroup {
  id: number;
  name: string;
  description: string;
  codex_multiplier: number;
  claude_multiplier: number;
  is_default: boolean;
}

export interface ExchangeRate {
  cny_per_usd: number;
  as_of: number;
}

export interface TopupResp {
  out_trade_no: string;
  cny_amount: number;
  usd_credit: number;
  rate: number;
  method: string;
  qr_code?: string;
  pay_url?: string;
  img?: string;
}

const ACTIVE_TOKEN_KEY = "cpa.status.active_token";
export function loadActiveToken(): string {
  try {
    return localStorage.getItem(ACTIVE_TOKEN_KEY) || "";
  } catch {
    return "";
  }
}
export function saveActiveToken(tok: string): void {
  if (!tok) {
    localStorage.removeItem(ACTIVE_TOKEN_KEY);
  } else {
    localStorage.setItem(ACTIVE_TOKEN_KEY, tok);
  }
}

function authedJSON<T>(path: string, token: string, opts: RequestInit = {}): Promise<T> {
  return fetchJSON<T>(path, {
    ...opts,
    headers: {
      ...(opts.headers || {}),
      Authorization: `Bearer ${token}`,
    },
  });
}

export function loadWalletBalance(token: string): Promise<WalletBalance> {
  return authedJSON<WalletBalance>("/api/wallet/balance", token);
}

export function loadWalletTransactions(token: string): Promise<{ transactions: WalletTx[] }> {
  return authedJSON<{ transactions: WalletTx[] }>("/api/wallet/transactions", token);
}

export function loadWalletOrders(token: string): Promise<{ orders: WalletOrder[] }> {
  return authedJSON<{ orders: WalletOrder[] }>("/api/wallet/orders", token);
}

export function loadWalletOrder(token: string, outTradeNo: string): Promise<WalletOrder> {
  return authedJSON<WalletOrder>(`/api/wallet/orders/${encodeURIComponent(outTradeNo)}`, token);
}

export function cancelWalletOrder(token: string, outTradeNo: string): Promise<{ status: string }> {
  return authedJSON<{ status: string }>(
    `/api/wallet/orders/${encodeURIComponent(outTradeNo)}`,
    token,
    { method: "DELETE" },
  );
}

export function topupWallet(token: string, usd: number): Promise<TopupResp> {
  return authedJSON<TopupResp>("/api/wallet/topup", token, {
    method: "POST",
    body: JSON.stringify({ usd }),
  });
}

export interface WalletSettings {
  upstream_fallback: boolean;
}

export function loadWalletSettings(token: string): Promise<WalletSettings> {
  return authedJSON<WalletSettings>("/api/wallet/settings", token);
}

export function updateWalletSettings(
  token: string,
  patch: Partial<WalletSettings>,
): Promise<WalletSettings> {
  return authedJSON<WalletSettings>("/api/wallet/settings", token, {
    method: "PATCH",
    body: JSON.stringify(patch),
  });
}

export function loadExchangeRate(): Promise<ExchangeRate> {
  return fetchJSON<ExchangeRate>("/api/wallet/rate");
}

export function loadPricingGroups(): Promise<{ groups: WalletPricingGroup[] }> {
  return fetchJSON<{ groups: WalletPricingGroup[] }>("/api/wallet/groups");
}

// ---- Invoicing --------------------------------------------------------

export interface InvoiceSummary {
  paid_cny: number;
  locked_cny: number;
  issued_cny: number;
  available_cny: number;
}

export interface InvoiceTitle {
  id?: number;
  name: string;
  tax_no?: string;
  address?: string;
  phone?: string;
  bank?: string;
  bank_account?: string;
  last_used_at?: number;
  source?: "local" | "remote";
}

export interface Invoice {
  id: number;
  cny_amount: number;
  title_name: string;
  contact_email: string;
  status: "pending" | "issued" | "rejected";
  note: string;
  created_at: number;
  issued_at?: number;
  rejected_at?: number;
  downloadable?: boolean;
  title?: {
    name?: string;
    tax_no?: string;
    address?: string;
    phone?: string;
    bank?: string;
    bank_account?: string;
  };
}

export function loadInvoiceSummary(token: string): Promise<InvoiceSummary> {
  return authedJSON<InvoiceSummary>("/api/wallet/invoice/summary", token);
}

export function loadInvoiceTitles(token: string, q?: string): Promise<{ titles: InvoiceTitle[] }> {
  const path = q && q.trim()
    ? `/api/wallet/invoice/titles?q=${encodeURIComponent(q)}`
    : "/api/wallet/invoice/titles";
  return authedJSON<{ titles: InvoiceTitle[] }>(path, token);
}

// COMPANY_SUGGEST_URL is the public company-name suggest endpoint. The server
// proxies it too (see internal/saas/billing/invoices.go), but that path is dead
// in production: the provider blocks by geography and this service runs
// offshore, answering every lookup with
// {"errorCode":301000,"message":"bannedLocation"}. Our customers are mostly
// mainland-based, so their browsers can reach what our backend cannot.
//
// Verified against the live endpoint from the production origin: it answers
// preflight with `Access-Control-Allow-Origin: <request origin>` and
// `Access-Control-Allow-Headers: content-type`, so this cross-origin POST is
// allowed.
const COMPANY_SUGGEST_URL = "https://capi.tianyancha.com/cloud-tempest/search/suggest/v3";
const COMPANY_SUGGEST_TIMEOUT_MS = 4000;

/** Matched substrings come back wrapped in <em>; strip it for display. */
function stripSuggestHighlight(s: string): string {
  return s.replace(/<\/?em>/g, "").trim();
}

/** Direct browser lookup. Returns null on any failure so the caller keeps
 *  whatever the server gave it. */
async function suggestDirect(q: string): Promise<InvoiceTitle[] | null> {
  const ctl = new AbortController();
  const timer = setTimeout(() => ctl.abort(), COMPANY_SUGGEST_TIMEOUT_MS);
  try {
    const res = await fetch(COMPANY_SUGGEST_URL, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ keyword: q }),
      signal: ctl.signal,
    });
    if (!res.ok) return null;
    const j = (await res.json()) as {
      state?: string;
      errorCode?: number;
      data?: Array<{ comName?: string; name?: string; entName?: string; taxCode?: string; creditCode?: string }>;
    };
    // The geo-block and the rate-limit both arrive as HTTP 200 with an error
    // code in the body, so the status line proves nothing on its own.
    if (j.errorCode !== 0 || j.state !== "ok" || !Array.isArray(j.data)) return null;
    return j.data
      .map((r) => ({
        name: stripSuggestHighlight(r.comName ?? r.name ?? r.entName ?? ""),
        tax_no: r.taxCode ?? r.creditCode ?? "",
        source: "remote" as const,
      }))
      .filter((t) => t.name !== "");
  } catch {
    return null;
  } finally {
    clearTimeout(timer);
  }
}

/**
 * Merges the operator's saved titles (server, authoritative — these carry
 * address / bank details the remote lookup never returns) with live
 * company-name matches from the browser.
 *
 * The server call is what must not be dropped: local history is the half that
 * actually works offshore. The direct call only adds remote suggestions the
 * backend can no longer fetch.
 */
export async function suggestInvoiceTitles(
  token: string,
  q: string,
): Promise<{ titles: InvoiceTitle[] }> {
  const [server, direct] = await Promise.all([
    authedJSON<{ titles: InvoiceTitle[] }>(
      `/api/wallet/invoice/title-suggest?q=${encodeURIComponent(q)}`,
      token,
    ).catch(() => ({ titles: [] as InvoiceTitle[] })),
    suggestDirect(q),
  ]);

  const titles = [...(server.titles ?? [])];
  const seen = new Set(titles.map((t) => t.name.toLowerCase()));
  for (const t of direct ?? []) {
    const key = t.name.toLowerCase();
    if (seen.has(key)) continue;
    seen.add(key);
    titles.push(t);
    if (titles.length >= 20) break;
  }
  return { titles };
}

export function saveInvoiceTitle(token: string, body: InvoiceTitle): Promise<{ status: string }> {
  return authedJSON<{ status: string }>("/api/wallet/invoice/titles", token, {
    method: "POST",
    body: JSON.stringify(body),
  });
}

export function deleteInvoiceTitle(token: string, id: number): Promise<{ status: string }> {
  return authedJSON<{ status: string }>(`/api/wallet/invoice/titles/${id}`, token, {
    method: "DELETE",
  });
}

export function loadInvoices(token: string): Promise<{ invoices: Invoice[] }> {
  return authedJSON<{ invoices: Invoice[] }>("/api/wallet/invoices", token);
}

export function createInvoice(token: string, body: {
  cny_amount: number;
  title: InvoiceTitle;
  contact_email: string;
}): Promise<Invoice> {
  return authedJSON<Invoice>("/api/wallet/invoices", token, {
    method: "POST",
    body: JSON.stringify(body),
  });
}

export async function downloadInvoicePDF(token: string, id: number): Promise<Blob> {
  const res = await fetch(`/api/wallet/invoices/${id}/download`, {
    headers: { Authorization: `Bearer ${token}` },
  });
  if (!res.ok) {
    const text = await res.text();
    throw new ApiError(text || `HTTP ${res.status}`, res.status);
  }
  return await res.blob();
}

const TOKENS_KEY = "cpa.status.tokens";
export function loadSavedTokens(): string[] {
  try {
    const raw = localStorage.getItem(TOKENS_KEY);
    if (!raw) return [];
    const arr = JSON.parse(raw);
    return Array.isArray(arr) ? arr.filter((x) => typeof x === "string") : [];
  } catch {
    return [];
  }
}
export function saveSavedTokens(tokens: string[]): void {
  localStorage.setItem(TOKENS_KEY, JSON.stringify(tokens));
}
