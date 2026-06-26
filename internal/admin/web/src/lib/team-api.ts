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
