// team-api.ts — client for the group-admin console (/api/team/*). Every call is
// authenticated with the admin's own client token as a Bearer credential (the
// product decision to reuse a client token as the login credential). The token
// is passed explicitly per-call rather than stored globally, because the public
// status page is where a group admin lands and it has no session.
import { ApiError } from "./api";

async function teamFetch<T>(token: string, path: string, opts: RequestInit = {}): Promise<T> {
  const res = await fetch(path, {
    ...opts,
    headers: {
      "Content-Type": "application/json",
      Authorization: `Bearer ${token}`,
      ...(opts.headers || {}),
    },
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

export interface TeamMe {
  workspace: { id: number; name: string; balance_usd: number; disabled: boolean };
  role: string;
}

export interface TeamMember {
  masked: string;
  label?: string;
  role: string;
  daily_usd_cap: number;
  monthly_usd_cap: number;
  /**
   * used_* is *pool* spend — the only thing the caps above meter. A team that
   * never funded a pool reads 0 here forever, which is correct and not a fault.
   */
  used_day_usd: number;
  used_month_usd: number;
  /**
   * spend_* is total spend from the request log: pool plus whatever fell back
   * to the member's own wallet. Absent unless spend_source is "requestlog", so
   * "we could not measure it" never renders as ¥0.
   */
  spend_source?: "requestlog" | "unmeasurable" | "unavailable";
  spend_day_usd?: number;
  spend_month_usd?: number;
  spend_day_requests?: number;
  spend_month_requests?: number;
  created_at?: number;
}

export interface TeamMembersResp {
  members: TeamMember[];
  /** Display zone the spend_* day/month windows are cut on. */
  timezone?: string;
  spend_partial?: boolean;
}

// ---- Group usage ------------------------------------------------------
//
// /api/team/usage reads the request log, not workspace_tx, so it sees the spend
// that fell back to members' personal wallets — for a team that only shares an
// invoice, that is all of it. Amounts are USD; the statement endpoints below
// are the CNY view.

export interface UsageAgg {
  requests: number;
  billed_usd: number;
  cost_usd: number;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_create_tokens: number;
  errors: number;
}

export interface GroupUsageMember extends UsageAgg {
  masked: string;
  label?: string;
  role: string;
  /** Token too short to mask distinguishably — its rows can't be told apart. */
  unmeasurable: boolean;
  pool_billed_usd: number;
  /** Log total minus the pool half — derived, not a ledger figure. */
  personal_billed_usd: number;
  /** What wallet_tx actually debited this member; see the statement's note. */
  personal_ledger_usd: number;
}

export interface GroupUsageModel extends UsageAgg {
  model: string;
}

export interface GroupUsageDay extends UsageAgg {
  day: string;
}

export interface GroupUsage {
  from: string;
  to: string;
  timezone: string;
  currency: string;
  /** Some figure below is known to be incomplete; `notes` says which. */
  partial: boolean;
  notes: string[];
  total: UsageAgg;
  pool_billed_usd: number;
  personal_billed_usd: number;
  personal_ledger_usd: number;
  by_member: GroupUsageMember[];
  by_model: GroupUsageModel[];
  by_day: GroupUsageDay[];
}

// ---- Group statement --------------------------------------------------

export interface TeamStatementMember {
  masked: string;
  label?: string;
  role: string;
  unmeasurable: boolean;
  requests: number;
  billed_cny: number;
  /** Fraction of the range total, 0–1. */
  share: number;
  /**
   * The ledger's own view of this member: what the shared pool covered and what
   * their personal wallet was debited. Not the same quantity as GroupUsage's
   * personal_billed_usd, which is the log total minus the pool half — these two
   * agree only where both books are complete.
   */
  pool_ledger_cny: number;
  personal_ledger_cny: number;
}

export interface TeamStatementModel {
  model: string;
  requests: number;
  billed_cny: number;
}

export interface TeamStatementPreview {
  workspace: { id: number; name: string };
  from: string;
  to: string;
  timezone: string;
  cny_per_usd: number;
  requests: number;
  billed_cny: number;
  /** Debited but with no matching log line; printed as its own line. */
  unitemised_cny: number;
  charged_cny: number;
  lifetime_requests: number;
  lifetime_billed_cny: number;
  lifetime_days: number;
  member_count: number;
  by_member: TeamStatementMember[];
  by_model: TeamStatementModel[];
  detail_lines: number;
  truncated: boolean;
  partial: boolean;
  notes: string[];
}

export interface TeamLedgerRow {
  kind: string;
  amount_usd: number;
  note?: string;
  member?: string;
  created_at: number;
}

// ---- Per-request drill-down -------------------------------------------
//
// /api/team/requests is the itemised view under /usage, not a second source of
// truth for it: the rows are capped and `truncated` says so, so summing them
// undercounts. The totals stay with /usage.

export interface TeamRequestRow {
  /** Masked token of the member the row belongs to. */
  member: string;
  label?: string;
  ts: number;
  provider?: string;
  model?: string;
  status: number;
  input_tokens: number;
  output_tokens: number;
  /** What was actually charged; the server reads BilledOrCost, not the raw field. */
  billed_usd: number;
  /** Same amount at the statement's rate — this is the column users reconcile. */
  billed_cny: number;
}

export interface TeamRequestsResp {
  requests: TeamRequestRow[];
  /** The cap was hit: only the newest rows of the range came back. */
  truncated: boolean;
  /** Zone the day range was cut on. */
  timezone: string;
}

export interface TeamTopupResp {
  out_trade_no: string;
  cny_amount: number;
  usd_credit: number;
  rate: number;
  qr_code?: string;
  pay_url?: string;
  img?: string;
}

export const teamMe = (token: string) => teamFetch<TeamMe>(token, "/api/team/me");

export const teamMembers = (token: string) =>
  teamFetch<TeamMembersResp>(token, "/api/team/members");

export const teamUsage = (token: string, from?: string, to?: string) => {
  const q = new URLSearchParams();
  if (from) q.set("from", from);
  if (to) q.set("to", to);
  const qs = q.toString();
  return teamFetch<GroupUsage>(token, `/api/team/usage${qs ? `?${qs}` : ""}`);
};

export const teamStatementPreview = (
  token: string,
  body: { from?: string; to?: string; detail?: "summary" | "full" },
) =>
  teamFetch<TeamStatementPreview>(token, "/api/team/statement", {
    method: "POST",
    body: JSON.stringify(body),
  });

/** Blob download of the group statement PDF (mirrors downloadStatementPDF). */
export async function teamDownloadStatementPDF(
  token: string,
  body: { from?: string; to?: string; detail?: "summary" | "full" },
): Promise<Blob> {
  const res = await fetch("/api/team/statement.pdf", {
    method: "POST",
    headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    const text = await res.text();
    let msg = text || `HTTP ${res.status}`;
    try {
      const j = JSON.parse(text);
      if (j && j.error) msg = j.error;
    } catch {
      /* keep the raw body */
    }
    throw new ApiError(msg, res.status);
  }
  return await res.blob();
}

export const teamAddMember = (
  token: string,
  body: { token: string; role?: string; daily_usd_cap?: number; monthly_usd_cap?: number },
) => teamFetch<TeamMember>(token, "/api/team/members", { method: "POST", body: JSON.stringify(body) });

export const teamPatchMember = (
  token: string,
  masked: string,
  body: { role?: string; daily_usd_cap?: number; monthly_usd_cap?: number },
) =>
  teamFetch<TeamMember>(token, `/api/team/members/${encodeURIComponent(masked)}`, {
    method: "PATCH",
    body: JSON.stringify(body),
  });

export const teamRemoveMember = (token: string, masked: string) =>
  teamFetch<{ status: string }>(token, `/api/team/members/${encodeURIComponent(masked)}`, {
    method: "DELETE",
  });

export const teamLedger = (token: string) =>
  teamFetch<{ ledger: TeamLedgerRow[] }>(token, "/api/team/ledger");

/**
 * Itemised requests for the group. `member` is a **masked** token (the form
 * /api/team/usage reports), and the window is the same inclusive day-label pair
 * every other range in this app speaks — never a timestamp, which would cost
 * the server its pre-summed index.
 */
export const teamRequests = (
  token: string,
  opts: { from?: string; to?: string; member?: string; limit?: number } = {},
) => {
  const q = new URLSearchParams();
  if (opts.from) q.set("from", opts.from);
  if (opts.to) q.set("to", opts.to);
  if (opts.member) q.set("member", opts.member);
  if (opts.limit) q.set("limit", String(opts.limit));
  const qs = q.toString();
  return teamFetch<TeamRequestsResp>(token, `/api/team/requests${qs ? `?${qs}` : ""}`);
};

export const teamTopup = (token: string, usd: number) =>
  teamFetch<TeamTopupResp>(token, "/api/team/topup", {
    method: "POST",
    body: JSON.stringify({ usd }),
  });

// ---- Team invoicing ---------------------------------------------------
//
// One invoice for the whole workspace. The amount is drawn from the members'
// own remaining invoice quotas (paid − issued − pending), consumed in join
// order; the server does the authoritative split, the client previews it with
// the same rule so the admin can see whose quota a request will spend.

export interface TeamInvoiceMemberQuota {
  masked: string;
  label?: string;
  paid_cny: number;
  locked_cny: number;
  issued_cny: number;
  available_cny: number;
}

export interface TeamInvoiceSummary {
  workspace: { id: number; name: string };
  total: {
    paid_cny: number;
    locked_cny: number;
    issued_cny: number;
    available_cny: number;
  };
  members: TeamInvoiceMemberQuota[];
}

export interface TeamInvoiceAllocation {
  masked: string;
  label?: string;
  cny_amount: number;
}

export interface TeamInvoice {
  id: number;
  cny_amount: number;
  title_name: string;
  contact_email?: string;
  status: "pending" | "issued" | "rejected";
  note: string;
  created_at: number;
  issued_at?: number;
  rejected_at?: number;
  downloadable?: boolean;
  allocations?: TeamInvoiceAllocation[];
}

export const teamInvoiceSummary = (token: string) =>
  teamFetch<TeamInvoiceSummary>(token, "/api/team/invoice/summary");

export const teamInvoices = (token: string) =>
  teamFetch<{ invoices: TeamInvoice[] }>(token, "/api/team/invoices");

export const teamCreateInvoice = (
  token: string,
  body: {
    cny_amount: number;
    title: Record<string, unknown>;
    contact_email: string;
  },
) =>
  teamFetch<TeamInvoice>(token, "/api/team/invoices", {
    method: "POST",
    body: JSON.stringify(body),
  });

/** Blob download of an issued team invoice PDF (mirrors downloadInvoicePDF). */
export async function teamDownloadInvoicePDF(token: string, id: number): Promise<Blob> {
  const res = await fetch(`/api/team/invoices/${id}/download`, {
    headers: { Authorization: `Bearer ${token}` },
  });
  if (!res.ok) {
    const text = await res.text();
    let msg = text || `HTTP ${res.status}`;
    try {
      const j = JSON.parse(text);
      if (j && j.error) msg = j.error;
    } catch {
      /* keep the raw body */
    }
    throw new ApiError(msg, res.status);
  }
  return await res.blob();
}

export interface AllocationPreviewRow {
  masked: string;
  label?: string;
  available_cny: number;
  cny_amount: number;
}

export interface AllocationPreview {
  rows: AllocationPreviewRow[];
  /** Amount that could not be covered by any member's quota (> 0 ⇒ 额度不足). */
  short_cny: number;
}

/**
 * Splits `amount` across `members` in join order — each member gives what is
 * left of their own quota until the invoice is covered.
 *
 * Arithmetic runs in integer 分 so the rows always sum to exactly the invoice
 * face value: the final member simply receives whatever remains rather than a
 * separately-rounded share (which would leave a stray cent).
 */
export function previewAllocations(
  members: TeamInvoiceMemberQuota[],
  amountCNY: number,
): AllocationPreview {
  let remaining = Math.round((Number(amountCNY) || 0) * 100);
  const rows: AllocationPreviewRow[] = [];
  if (remaining <= 0) return { rows, short_cny: 0 };
  for (const m of members) {
    if (remaining <= 0) break;
    const avail = Math.round((m.available_cny || 0) * 100);
    if (avail <= 0) continue;
    const take = Math.min(avail, remaining);
    rows.push({
      masked: m.masked,
      label: m.label,
      available_cny: m.available_cny,
      cny_amount: take / 100,
    });
    remaining -= take;
  }
  return { rows, short_cny: remaining / 100 };
}
