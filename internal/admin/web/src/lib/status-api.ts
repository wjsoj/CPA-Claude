import { ApiError } from "./api";

export interface StatusOverview {
  counts: {
    total: number;
    healthy: number;
    quota: number;
    unhealthy: number;
    disabled: number;
    oauth: number;
    apikey: number;
    models: number;
  };
  window_24h: {
    requests: number;
    cost_usd: number;
    errors: number;
  };
  auths: {
    kind: "oauth" | "apikey";
    label?: string;
    group?: string;
    healthy: boolean;
    disabled?: boolean;
    quota_exceeded?: boolean;
    hard_failure?: boolean;
  }[];
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
  pool: {
    total: number;
    healthy: number;
    quota: number;
    unhealthy: number;
    disabled: number;
    oauth: number;
    apikey: number;
  };
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

export function suggestInvoiceTitles(token: string, q: string): Promise<{ titles: InvoiceTitle[] }> {
  return authedJSON<{ titles: InvoiceTitle[] }>(
    `/api/wallet/invoice/title-suggest?q=${encodeURIComponent(q)}`,
    token,
  );
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
