// Single source of truth for credential health as the UI presents it.
//
// The rule: the *backend* decides what state a credential is in and ships it
// as `state`. The frontend never re-derives health from a ladder of booleans —
// that is exactly how the old UI ended up painting a circuit-broken API key
// green the instant its quarantine window expired (`healthy` flips true before
// a single request has succeeded), and how three panels each reached a
// different conclusion about the same credential.
//
// The only derivation left here is `legacyState`, a transitional fallback for
// a server that has not shipped `state` yet. It is deliberately pessimistic:
// it never returns "healthy" for a credential whose breaker has struck and not
// yet been re-verified.

export type CredState =
  | "healthy"
  | "half_open"
  | "degraded"
  | "quota"
  | "cooling"
  | "hard_failed"
  | "disabled";

export const CRED_STATES: readonly CredState[] = [
  "healthy",
  "half_open",
  "degraded",
  "quota",
  "cooling",
  "hard_failed",
  "disabled",
] as const;

function isCredState(v: unknown): v is CredState {
  return typeof v === "string" && (CRED_STATES as readonly string[]).includes(v);
}

/**
 * The shape every credential-bearing row shares, across the admin summary and
 * the public status overview. New fields are the contract; the legacy booleans
 * remain only so a pre-upgrade server still renders something honest.
 */
export interface CredStateFields {
  state?: string;
  serving?: boolean;
  reason?: string;
  retry_after_seconds?: number;
  consecutive_failures?: number;
  quarantine_strikes?: number;
  quarantined_until?: string;
  last_success_at?: string;

  // ---- legacy (pre-`state`) signals, read only by `legacyState` ----
  healthy?: boolean;
  disabled?: boolean;
  quota_exceeded?: boolean;
  quota_reset_at?: string;
  hard_failure?: boolean;
  failure_reason?: string;
}

/**
 * Ordering by "how much an operator should care", worst last. Used to pick a
 * `worst_state` when the backend didn't supply one, and to sort cards.
 *
 * `disabled` sits just above `healthy`: it is an operator decision, not a
 * fault, so it must never outrank a real failure in a banner headline.
 */
export const STATE_SEVERITY: Record<CredState, number> = {
  healthy: 0,
  disabled: 1,
  half_open: 2,
  degraded: 3,
  quota: 4,
  cooling: 5,
  hard_failed: 6,
};

/** Pessimistic fallback for a server that predates the `state` field. */
function legacyState(r: CredStateFields): CredState {
  if (r.disabled) return "disabled";
  if (r.hard_failure) return "hard_failed";
  if (r.quarantined_until && new Date(r.quarantined_until).getTime() > Date.now()) {
    return "cooling";
  }
  if (r.quota_exceeded) return "quota";
  // The breaker has tripped at least once and the pause has since lapsed. The
  // old UI called this "healthy". It is not: nothing has succeeded since.
  if ((r.quarantine_strikes ?? 0) > 0) return "half_open";
  if ((r.consecutive_failures ?? 0) > 0) return "degraded";
  if (r.healthy) return "healthy";
  return "degraded";
}

/** The credential's state — straight from the backend whenever it says. */
export function credState(r: CredStateFields | null | undefined): CredState {
  if (!r) return "disabled";
  if (isCredState(r.state)) return r.state;
  return legacyState(r);
}

/** Whether this credential can currently take traffic. */
export function credServing(r: CredStateFields | null | undefined): boolean {
  if (!r) return false;
  if (typeof r.serving === "boolean") return r.serving;
  const s = credState(r);
  return s === "healthy" || s === "half_open" || s === "degraded";
}

// ---------------------------------------------------------------------------
// Pool aggregate
// ---------------------------------------------------------------------------

export type ByState = Record<CredState, number>;

export function emptyByState(): ByState {
  return {
    healthy: 0,
    half_open: 0,
    degraded: 0,
    quota: 0,
    cooling: 0,
    hard_failed: 0,
    disabled: 0,
  };
}

export interface PoolAgg {
  /** "anthropic" | "openai" | "" when the backend reports one flat pool. */
  provider: string;
  available: boolean;
  total: number;
  serving: number;
  worst_state: CredState;
  by_state: ByState;
}

const PROVIDER_LABELS: Record<string, string> = {
  anthropic: "Claude",
  claude: "Claude",
  openai: "Codex",
  codex: "Codex",
};

export function providerLabel(p: string | undefined): string {
  if (!p) return "凭据池";
  return PROVIDER_LABELS[p.toLowerCase()] ?? p;
}

function worstOf(by: ByState): CredState {
  let worst: CredState = "healthy";
  for (const s of CRED_STATES) {
    if (by[s] > 0 && STATE_SEVERITY[s] > STATE_SEVERITY[worst]) worst = s;
  }
  return worst;
}

/** Aggregate a set of credential rows into the same shape the backend ships. */
export function poolFromRows(rows: CredStateFields[], provider = ""): PoolAgg {
  const by = emptyByState();
  let serving = 0;
  for (const r of rows) {
    by[credState(r)]++;
    if (credServing(r)) serving++;
  }
  return {
    provider,
    available: serving > 0,
    total: rows.length,
    serving,
    worst_state: worstOf(by),
    by_state: by,
  };
}

/** Group rows by provider, then aggregate each group. Providers with no rows
 *  at all are omitted — a banner about an unconfigured provider is noise. */
export function poolsFromRows(
  rows: (CredStateFields & { provider?: string })[],
): PoolAgg[] {
  const groups = new Map<string, CredStateFields[]>();
  for (const r of rows) {
    const p = (r.provider || "anthropic").toLowerCase();
    const list = groups.get(p);
    if (list) list.push(r);
    else groups.set(p, [r]);
  }
  return Array.from(groups.entries()).map(([p, list]) => poolFromRows(list, p));
}

/** Fold several provider pools into one. Used by charts that show the fleet
 *  as a whole; banners deliberately do NOT merge, because "Claude is down"
 *  and "Codex is fine" must not average into "mostly fine". */
export function mergePools(pools: PoolAgg[]): PoolAgg {
  const by = emptyByState();
  let total = 0;
  let serving = 0;
  for (const p of pools) {
    for (const s of CRED_STATES) by[s] += p.by_state[s];
    total += p.total;
    serving += p.serving;
  }
  return {
    provider: "",
    available: serving > 0,
    total,
    serving,
    worst_state: worstOf(by),
    by_state: by,
  };
}

function coerceByState(raw: any): ByState {
  const by = emptyByState();
  if (raw && typeof raw === "object") {
    for (const s of CRED_STATES) {
      const n = Number(raw[s]);
      if (Number.isFinite(n) && n > 0) by[s] = n;
    }
  }
  return by;
}

function looksLikePool(raw: any): boolean {
  return (
    raw &&
    typeof raw === "object" &&
    ("by_state" in raw || "available" in raw || "worst_state" in raw)
  );
}

function coercePool(raw: any, provider: string): PoolAgg {
  const by = coerceByState(raw?.by_state);
  const summed = CRED_STATES.reduce((n, s) => n + by[s], 0);
  const total = Number.isFinite(Number(raw?.total)) ? Number(raw.total) : summed;
  const serving = Number.isFinite(Number(raw?.serving))
    ? Number(raw.serving)
    : by.healthy + by.half_open + by.degraded;
  return {
    provider,
    available: typeof raw?.available === "boolean" ? raw.available : serving > 0,
    total,
    serving,
    worst_state: isCredState(raw?.worst_state) ? raw.worst_state : worstOf(by),
    by_state: by,
  };
}

/**
 * Normalize the `pool` field of /status/api/overview and /status/api/dashboard.
 *
 * The contract calls it "grouped by provider", which the wire can express two
 * ways — a single aggregate object, or a map of provider → aggregate — and the
 * legacy shape (`{total, healthy, quota, unhealthy, disabled}`) is still out
 * there on un-upgraded servers. Accept all three rather than render nothing.
 */
export function normalizePools(raw: any): PoolAgg[] {
  if (!raw || typeof raw !== "object") return [];
  if (Array.isArray(raw)) {
    return raw.map((p) => coercePool(p, String(p?.provider ?? "")));
  }
  if (looksLikePool(raw)) return [coercePool(raw, String(raw.provider ?? ""))];
  // Provider-keyed map. Any value that isn't pool-shaped is the legacy flat
  // object, which carries no state breakdown — skip it rather than invent one.
  const out: PoolAgg[] = [];
  for (const [k, v] of Object.entries(raw)) {
    if (looksLikePool(v)) out.push(coercePool(v, k));
  }
  return out;
}

// ---------------------------------------------------------------------------
// Reason sanitization (public status page)
// ---------------------------------------------------------------------------

// The public status page needs no authentication, and `reason` carries raw
// upstream error text. These patterns are the things that must never leave the
// admin console: credential ids and file paths, internal hostnames and IPs,
// bearer/API-key fragments, request ids, and account emails.
const REDACTIONS: { re: RegExp; with: string }[] = [
  { re: /\b(?:sk|pk|rk)-[A-Za-z0-9_-]{6,}/g, with: "<key>" },
  { re: /\bBearer\s+[A-Za-z0-9._-]+/gi, with: "<key>" },
  { re: /\b(?:req|request)[-_][A-Za-z0-9]{6,}/gi, with: "<id>" },
  {
    re: /\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b/gi,
    with: "<id>",
  },
  { re: /\b[\w.+-]+@[\w-]+\.[\w.-]+\b/g, with: "<account>" },
  { re: /\bhttps?:\/\/\S+/gi, with: "<url>" },
  { re: /\b\d{1,3}(?:\.\d{1,3}){3}(?::\d+)?\b/g, with: "<host>" },
  { re: /(?:^|\s)\/[\w./-]{4,}/g, with: " <path>" },
  // Long opaque runs — hashes, base64 blobs, account keys.
  { re: /\b[A-Za-z0-9_-]{24,}\b/g, with: "<redacted>" },
];

const MAX_PUBLIC_REASON = 120;

/** Generic phrasing used when a reason redacts down to nothing meaningful. */
const GENERIC_REASON: Record<CredState, string> = {
  healthy: "",
  half_open: "暂停已结束，等待下一次请求验证",
  degraded: "近期请求出现失败",
  quota: "上游配额已用尽",
  cooling: "连续失败后已暂停轮换",
  hard_failed: "凭据已被判定为不可用",
  disabled: "已由管理员停用",
};

/**
 * Public-safe rendering of a credential's failure reason.
 *
 * Redacts identity-bearing fragments, collapses whitespace, and truncates. If
 * what survives is mostly placeholders (or too short to mean anything), falls
 * back to a generic phrase for the state — a vague truth beats a leaked one.
 */
export function publicReason(
  raw: string | undefined,
  state: CredState,
): string {
  const generic = GENERIC_REASON[state];
  if (!raw) return generic;
  let s = String(raw).replace(/\s+/g, " ").trim();
  if (!s) return generic;
  for (const r of REDACTIONS) s = s.replace(r.re, r.with);
  s = s.replace(/\s+/g, " ").trim();

  const placeholders = (s.match(/<(?:key|id|url|host|path|account|redacted)>/g) || []).length;
  const residue = s.replace(/<[a-z]+>/g, "").replace(/[^A-Za-z一-龥]/g, "");
  if (placeholders >= 3 || residue.length < 8) return generic;

  if (s.length > MAX_PUBLIC_REASON) s = s.slice(0, MAX_PUBLIC_REASON - 1).trimEnd() + "…";
  return s;
}

// ---------------------------------------------------------------------------
// Countdown
// ---------------------------------------------------------------------------

/**
 * "2m 30s" from the recovery hint. `retry_after_seconds` is the contract's
 * primary signal (0 = unknown); `quarantined_until` is the fallback deadline.
 * Returns "" when neither says anything.
 */
export function recoveryCountdown(r: CredStateFields): string {
  let secs = 0;
  if (typeof r.retry_after_seconds === "number" && r.retry_after_seconds > 0) {
    secs = r.retry_after_seconds;
  } else if (r.quarantined_until) {
    const t = new Date(r.quarantined_until).getTime();
    if (Number.isFinite(t)) secs = Math.round((t - Date.now()) / 1000);
  } else if (r.quota_reset_at) {
    const t = new Date(r.quota_reset_at).getTime();
    if (Number.isFinite(t)) secs = Math.round((t - Date.now()) / 1000);
  }
  if (!Number.isFinite(secs) || secs <= 0) return "";
  const d = Math.floor(secs / 86400);
  const h = Math.floor((secs % 86400) / 3600);
  const m = Math.floor((secs % 3600) / 60);
  const s = secs % 60;
  if (d) return `${d}d ${h}h`;
  if (h) return `${h}h ${m}m`;
  if (m) return `${m}m ${s}s`;
  return `${s}s`;
}
