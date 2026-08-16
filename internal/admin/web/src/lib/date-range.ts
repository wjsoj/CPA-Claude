// Day-label helpers shared by every export / usage range picker.
//
// The wire format everywhere these are used is an inclusive `YYYY-MM-DD` day
// label, never a timestamp: the server answers a day window off the pre-summed
// request-log cube and falls back to a row-by-row scan the moment it is handed
// instants instead. So the whole client side of a range only ever deals in
// these strings, and arithmetic on them goes through here rather than through
// ad-hoc `new Date()` maths at each call site.

const pad = (n: number) => String(n).padStart(2, "0");

const fmt = (d: Date) => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;

/** Today as a day label, in the browser's zone. */
export function todayLocal(): string {
  return fmt(new Date());
}

/**
 * Moves a day label by whole days. Parsed at local midnight (not as a bare
 * `YYYY-MM-DD`, which JS reads as UTC and would shift the label by a day for
 * anyone west of Greenwich).
 */
export function shiftDays(day: string, delta: number): string {
  const d = new Date(`${day}T00:00:00`);
  d.setDate(d.getDate() + delta);
  return fmt(d);
}

/** First and last day of the month `offset` months from the current one. */
export function monthBounds(offset: number): { from: string; to: string } {
  const now = new Date();
  const first = new Date(now.getFullYear(), now.getMonth() + offset, 1);
  const last = new Date(now.getFullYear(), now.getMonth() + offset + 1, 0);
  return { from: fmt(first), to: fmt(last) };
}

/** Trailing `n` days ending today, inclusive of both ends. */
export function trailingDays(n: number): { from: string; to: string } {
  return { from: shiftDays(todayLocal(), -(n - 1)), to: todayLocal() };
}

/**
 * Longest window the group usage / statement endpoints accept, mirroring
 * billing.maxUsageWindowDays. Checked here only so an obviously-too-wide range
 * gets a Chinese sentence instead of the server's English 400 — the server
 * remains the authority.
 */
export const MAX_USAGE_WINDOW_DAYS = 92;

/** Inclusive day count of a label pair; 0 when either label is unparseable. */
export function daysBetween(from: string, to: string): number {
  const f = new Date(`${from}T00:00:00`).getTime();
  const t = new Date(`${to}T00:00:00`).getTime();
  if (Number.isNaN(f) || Number.isNaN(t)) return 0;
  return Math.round((t - f) / 86400000) + 1;
}

/** Null when the range is usable, otherwise the reason to show the user. */
export function rangeProblem(from: string, to: string): string | null {
  if (!from || !to) return null;
  const days = daysBetween(from, to);
  if (days <= 0) return "开始日期不能晚于结束日期。";
  if (days > MAX_USAGE_WINDOW_DAYS) {
    return `区间最长 ${MAX_USAGE_WINDOW_DAYS} 天，当前选了 ${days} 天。`;
  }
  return null;
}

// One formatter per zone: an itemised drill-down renders hundreds of rows, and
// constructing an Intl.DateTimeFormat is the expensive half of formatting one.
const tsFormatters = new Map<string, Intl.DateTimeFormat>();

function tsFormatter(zone: string): Intl.DateTimeFormat | null {
  const hit = tsFormatters.get(zone);
  if (hit) return hit;
  try {
    // hourCycle rather than hour12:false — the latter still prints midnight as
    // "24:00" under some locales.
    const f = new Intl.DateTimeFormat("en-CA", {
      timeZone: zone,
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
      hourCycle: "h23",
    });
    tsFormatters.set(zone, f);
    return f;
  } catch {
    // Unrecognised IANA name (old engine, or a zone we've never heard of).
    return null;
  }
}

/**
 * Unix seconds → `MM-DD HH:mm:ss` in `zone`, the display zone the server cut
 * the day window on.
 *
 * Rendering these in the browser's zone instead is what makes an itemised view
 * look wrong to anyone outside the display zone: the range is a day label in
 * `requestlog.BucketLocation()`, so a request at 00:30 CST inside a 08-17 range
 * prints as "08-16 16:30" for a UTC reader and reads as the server having
 * returned a row from outside the range it was asked for. The amount is right;
 * the date attribution is not, and this panel exists to be reconciled against.
 *
 * Falls back to the browser's zone only when the server sent no zone or an
 * unusable one — a caller that has a zone should always pass it.
 */
export function formatTimestampIn(ts: number, zone?: string): string {
  const d = new Date((ts || 0) * 1000);
  const f = zone ? tsFormatter(zone) : null;
  if (f) {
    const parts = f.formatToParts(d);
    const at = (t: Intl.DateTimeFormatPartTypes) =>
      parts.find((p) => p.type === t)?.value ?? "";
    return `${at("month")}-${at("day")} ${at("hour")}:${at("minute")}:${at("second")}`;
  }
  return `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}

export interface RangePreset {
  label: string;
  range: () => { from: string; to: string };
}

/**
 * The preset set offered next to a range picker. Kept in one place so the
 * personal export, the team usage view and the team export all offer the same
 * vocabulary — a user comparing two of them should not have to re-learn what
 * "本月" means.
 */
export const rangePresets: RangePreset[] = [
  { label: "近 7 天", range: () => trailingDays(7) },
  { label: "近 30 天", range: () => trailingDays(30) },
  { label: "本月", range: () => monthBounds(0) },
  { label: "上月", range: () => monthBounds(-1) },
];
