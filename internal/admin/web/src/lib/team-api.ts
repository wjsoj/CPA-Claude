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
  used_day_usd: number;
  used_month_usd: number;
  created_at?: number;
}

export interface TeamLedgerRow {
  kind: string;
  amount_usd: number;
  note?: string;
  member?: string;
  created_at: number;
}

export interface TeamRequestRow {
  member: string;
  label?: string;
  ts: number;
  provider?: string;
  model?: string;
  status: number;
  input: number;
  output: number;
  cost_usd: number;
  billed_usd?: number;
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
  teamFetch<{ members: TeamMember[] }>(token, "/api/team/members");

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

export const teamRequests = (token: string) =>
  teamFetch<{ requests: TeamRequestRow[] }>(token, "/api/team/requests");

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
