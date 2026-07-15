import { useEffect, useState } from "react";
import { Gauge, RefreshCw, Zap } from "lucide-react";
import { api } from "@/lib/api";
import type {
  AuthRow,
  CodexResetResponse,
  CodexUsageResponse,
  UpstreamResponse,
  UpstreamUsage,
  UsageWindow,
} from "@/lib/types";
import { Button } from "@/components/ui/button";
import { cn, fmtCountdown, fmtLocalTime } from "@/lib/utils";

interface State {
  loading?: boolean;
  error?: string;
  data?: UpstreamResponse;
  ts?: number;
}

type WindowKey =
  | "five_hour"
  | "seven_day"
  | "seven_day_oauth_apps"
  | "seven_day_opus"
  | "seven_day_sonnet"
  | "seven_day_cowork"
  | "iguana_necktie";

const WINDOW_KEYS: [WindowKey, string][] = [
  ["five_hour", "5-hour"],
  ["seven_day", "7-day"],
  ["seven_day_oauth_apps", "7-day (OAuth apps)"],
  ["seven_day_opus", "7-day Opus"],
  ["seven_day_sonnet", "7-day Sonnet"],
  ["seven_day_cowork", "7-day Cowork"],
  ["iguana_necktie", "iguana_necktie"],
];

function pctAndColor(raw: number | undefined | null): { pct: number | null; color: string } {
  const pct = typeof raw === "number" ? Math.round(raw <= 1 ? raw * 100 : raw) : null;
  const color =
    pct == null
      ? "bg-slate-400"
      : pct >= 90
        ? "bg-red-500"
        : pct >= 70
          ? "bg-amber-500"
          : "bg-emerald-500";
  return { pct, color };
}

function renderProfile(profile: UpstreamResponse["profile"] | undefined) {
  if (!profile || !profile.body) return null;
  const p = profile.body;
  let plan = "unknown";
  if (p.account) {
    if (p.account.has_claude_max) plan = "Max";
    else if (p.account.has_claude_pro) plan = "Pro";
    else if (p.account.has_claude_max === false && p.account.has_claude_pro === false) plan = "Free";
  }
  const tier = p.organization?.rate_limit_tier;
  const email = p.account?.email || p.account?.email_address || "";
  return (
    <div className="text-xs text-muted-foreground">
      plan: <span className="text-foreground font-semibold">{plan}</span>
      {tier && <> · tier {tier}</>}
      {email && <> · {email}</>}
    </div>
  );
}

// reachedLabel renders rate_limit_reached_type, which the wham/usage backend
// returns as either a bare string ("primary"/"secondary") or, more recently,
// an object ({type, resets_at, ...}). Coerce to a short string so the badge
// never prints the literal "[object Object]".
function reachedLabel(x: string | { type?: string } | null | undefined): string {
  if (!x) return "";
  if (typeof x === "string") return x;
  if (typeof x === "object" && typeof x.type === "string") return x.type;
  return "";
}

function renderWindows(usage: UpstreamUsage | undefined, tick: number) {
  if (!usage || !usage.body) return null;
  const body = usage.body;
  const rows: [string, UsageWindow][] = WINDOW_KEYS.map(
    ([k, label]) => [label, body[k]] as [string, UsageWindow | undefined],
  ).filter(([, v]) => !!v && typeof v === "object" && (v.utilization != null || v.resets_at != null)) as [
    string,
    UsageWindow,
  ][];
  const extra = body.extra_usage;
  if (!rows.length && !extra) {
    return (
      <pre className="mono text-[11px] text-muted-foreground whitespace-pre-wrap">
        {JSON.stringify(body, null, 2)}
      </pre>
    );
  }
  return (
    <table className="w-full text-xs" key={tick}>
      <thead>
        <tr className="eyebrow text-muted-foreground border-b">
          <th className="py-1.5 pr-2 text-left font-medium">Window</th>
          <th className="py-1.5 text-right font-medium">Used</th>
          <th className="py-1.5 pl-2 font-medium">&nbsp;</th>
          <th className="py-1.5 pl-2 text-right font-medium">Resets in</th>
        </tr>
      </thead>
      <tbody>
        {rows.map(([label, w]) => {
          const { pct, color } = pctAndColor(w.utilization);
          return (
            <tr key={label} className="border-b last:border-b-0">
              <td className="py-1.5 pr-2">{label}</td>
              <td className="py-1.5 mono text-right tabular">{pct != null ? pct + "%" : "—"}</td>
              <td className="py-1.5 pl-2 w-24">
                {pct != null && (
                  <div className="h-1 bg-muted rounded overflow-hidden">
                    <div className={cn("h-full", color)} style={{ width: `${Math.min(100, pct)}%` }} />
                  </div>
                )}
              </td>
              <td className="py-1.5 pl-2 text-right">
                {w.resets_at ? (
                  <span
                    className="mono tabular"
                    title={new Date(w.resets_at).toLocaleString()}
                  >
                    <span>{fmtCountdown(w.resets_at)}</span>
                    <span className="text-muted-foreground"> · {fmtLocalTime(w.resets_at)}</span>
                  </span>
                ) : (
                  <span className="text-muted-foreground">—</span>
                )}
              </td>
            </tr>
          );
        })}
        {extra && extra.is_enabled && (
          <tr>
            <td className="py-1.5 pr-2">extra credits</td>
            <td className="py-1.5 mono text-right tabular">
              {(() => {
                const { pct } = pctAndColor(extra.utilization);
                return pct != null ? pct + "%" : "—";
              })()}
            </td>
            <td className="py-1.5 pl-2 w-24">
              {(() => {
                const { pct, color } = pctAndColor(extra.utilization);
                return pct != null ? (
                  <div className="h-1 bg-muted rounded overflow-hidden">
                    <div className={cn("h-full", color)} style={{ width: `${Math.min(100, pct)}%` }} />
                  </div>
                ) : null;
              })()}
            </td>
            <td className="py-1.5 pl-2 text-right mono text-[11px] text-muted-foreground">
              ${Number(extra.used_credits || 0).toFixed(2)} / $
              {Number(extra.monthly_limit || 0).toFixed(0)}
            </td>
          </tr>
        )}
      </tbody>
    </table>
  );
}

// Inline panel on an OAuth credential card that fetches upstream Anthropic
// quota/plan info for that specific credential. Request is proxied through
// the credential's configured proxy_url (enforced server-side).
export function CardUpstreamQuota({ auth }: { auth: AuthRow }) {
  const [open, setOpen] = useState(false);
  const [st, setSt] = useState<State>({});
  // Trigger a periodic re-render so the countdown stays fresh once data is shown.
  const [tick, setTick] = useState(0);
  useEffect(() => {
    if (!open || !st.data) return;
    const id = setInterval(() => setTick((t) => t + 1), 1000);
    return () => clearInterval(id);
  }, [open, st.data]);

  const run = async () => {
    setSt((s) => ({ ...s, loading: true, error: "" }));
    try {
      const d = await api<UpstreamResponse>(
        `/admin/api/auths/${encodeURIComponent(auth.id)}/anthropic-usage`,
        { method: "POST" },
      );
      setSt({ loading: false, data: d, ts: Date.now() });
      setOpen(true);
    } catch (x: any) {
      setSt({ loading: false, error: x?.message || String(x) });
      setOpen(true);
    }
  };

  const onClick = () => {
    if (!st.data && !st.loading) {
      void run();
    } else {
      setOpen((o) => !o);
    }
  };

  const usage = st.data?.usage;
  const profile = st.data?.profile;

  return (
    <div className="px-5 py-3 border-t border-border bg-muted/20">
      <div className="flex items-center justify-between gap-2 flex-wrap">
        <div className="eyebrow">Upstream quota</div>
        <div className="flex items-center gap-1.5">
          {st.ts && (
            <span className="text-[10px] text-muted-foreground mono">
              as of {fmtLocalTime(new Date(st.ts).toISOString())}
            </span>
          )}
          {st.data && (
            <Button
              size="sm"
              variant="ghost"
              className="h-7 px-2"
              onClick={(e) => {
                e.stopPropagation();
                void run();
              }}
              disabled={st.loading}
              title="Refetch"
            >
              <RefreshCw className={cn("h-3 w-3", st.loading && "animate-spin")} />
            </Button>
          )}
          <Button
            size="sm"
            variant="outline"
            className="h-7"
            onClick={onClick}
            disabled={st.loading}
          >
            <Gauge className="h-3 w-3" />
            {st.loading ? "Fetching…" : st.data ? (open ? "Hide" : "Show") : "Check quota"}
          </Button>
        </div>
      </div>
      {open && (
        <div className="mt-2 space-y-2">
          {st.error && (
            <div className="text-xs text-destructive mono whitespace-pre-wrap">{st.error}</div>
          )}
          {usage && usage.error && (
            <div className="text-xs text-destructive mono">
              usage http {usage.status}: {usage.error}
            </div>
          )}
          {renderProfile(profile)}
          {renderWindows(usage, tick)}
        </div>
      )}
    </div>
  );
}

// CardUpstreamCodex is the OpenAI/Codex sibling of CardUpstreamQuota. Same
// outer chrome and same trigger model, different upstream endpoint and
// payload shape: chatgpt.com/backend-api/wham/usage returns primary (5h) +
// secondary (weekly) windows, plus plan_type / credits / spend control.
// Safe to call any time — wham/usage is the official portal endpoint,
// not a CLI signal, so it doesn't risk third-party detection like the
// /responses endpoint would.
interface CodexState {
  loading?: boolean;
  error?: string;
  data?: CodexUsageResponse;
  ts?: number;
  resetting?: boolean;
  resetMsg?: string;
}

function fmtUnix(ts?: number): string | null {
  if (!ts) return null;
  return new Date(ts * 1000).toLocaleString();
}

function codexCountdown(ts?: number): string {
  if (!ts) return "—";
  const dt = ts * 1000 - Date.now();
  if (dt < 0) return "now";
  const h = Math.floor(dt / 3600000);
  const m = Math.floor((dt % 3600000) / 60000);
  if (h > 0) return `${h}h ${m}m`;
  return `${m}m`;
}

// windowLabel renders a rate-limit window's duration from the length the
// upstream actually reported, instead of a hardcoded "5h"/"7d". ChatGPT retired
// the 5h primary window in 2026-07 and now reports a 7-day (604800s) primary
// window, so the old fixed "primary (5h)" label was simply wrong — the 59% a
// user sees is really their weekly usage. Falls back to a bare name when the
// upstream omits the length. Duration format matches hypitoken's
// fmtWindowSeconds so both admin UIs read identically (e.g. 604800 -> "7d").
function windowLabel(name: string, secs: number | undefined): string {
  if (!secs || secs <= 0) return name;
  let dur: string;
  if (secs % 86400 === 0) dur = `${secs / 86400}d`;
  else if (secs % 3600 === 0) dur = `${secs / 3600}h`;
  else if (secs % 60 === 0) dur = `${secs / 60}m`;
  else dur = `${secs}s`;
  return `${name} (${dur})`;
}

function pctTone(raw: number | undefined): { pct: number | null; color: string } {
  // wham/usage `used_percent` is ALREADY a 0–100 percentage (e.g. 1 = 1%),
  // unlike Anthropic's 0–1 `utilization`. Do NOT apply the "<=1 ? *100"
  // fraction heuristic here — it turned a real 1% into 100% and made the
  // 5h window read as fully exhausted when it wasn't.
  const pct = typeof raw === "number" ? Math.round(Math.max(0, Math.min(100, raw))) : null;
  const color =
    pct == null
      ? "bg-slate-400"
      : pct >= 90
        ? "bg-red-500"
        : pct >= 70
          ? "bg-amber-500"
          : "bg-emerald-500";
  return { pct, color };
}

export function CardUpstreamCodex({ auth }: { auth: AuthRow }) {
  const [open, setOpen] = useState(false);
  const [st, setSt] = useState<CodexState>({});
  const [tick, setTick] = useState(0);
  useEffect(() => {
    if (!open || !st.data) return;
    const id = setInterval(() => setTick((t) => t + 1), 1000);
    return () => clearInterval(id);
  }, [open, st.data]);

  const run = async () => {
    setSt((s) => ({ ...s, loading: true, error: "" }));
    try {
      const d = await api<CodexUsageResponse>(
        `/admin/api/auths/${encodeURIComponent(auth.id)}/codex-usage`,
        { method: "POST" },
      );
      setSt({ loading: false, data: d, ts: Date.now() });
      setOpen(true);
    } catch (x: any) {
      setSt({ loading: false, error: x?.message || String(x) });
      setOpen(true);
    }
  };

  const onClick = () => {
    if (!st.data && !st.loading) {
      void run();
    } else {
      setOpen((o) => !o);
    }
  };

  const u = st.data?.usage;
  const rl = u?.rate_limit;
  const credits = u?.credits;
  const spend = u?.spend_control;
  const primary = rl?.primary_window;
  const secondary = rl?.secondary_window;
  const resetAvailable = u?.rate_limit_reset_credits?.available_count ?? 0;

  // Consume one rate-limit reset credit. Irreversible (burns a card), so we
  // confirm first. On success the response carries a refreshed usage snapshot
  // — swap it in so the countdowns and remaining-credit count update at once.
  const doReset = async () => {
    if (st.resetting) return;
    if (resetAvailable <= 0) return;
    if (
      !window.confirm(
        `Consume one reset credit for ${auth.email || auth.id}? This immediately resets the rate-limit window and cannot be undone. ${resetAvailable} credit(s) available.`,
      )
    )
      return;
    setSt((s) => ({ ...s, resetting: true, error: "", resetMsg: "" }));
    try {
      const d = await api<CodexResetResponse>(
        `/admin/api/auths/${encodeURIComponent(auth.id)}/reset-codex-credit`,
        { method: "POST" },
      );
      const windows = d.reset?.windows_reset ?? 0;
      setSt((s) => ({
        ...s,
        resetting: false,
        resetMsg: `Reset done — ${windows} window(s) reset${d.reset?.code ? ` (${d.reset.code})` : ""}.`,
        data: d.usage ? { usage: d.usage } : s.data,
        ts: d.usage ? Date.now() : s.ts,
      }));
    } catch (x: any) {
      setSt((s) => ({ ...s, resetting: false, error: x?.message || String(x) }));
    }
  };

  return (
    <div className="px-5 py-3 border-t border-border bg-muted/20" key={tick}>
      <div className="flex items-center justify-between gap-2 flex-wrap">
        <div className="eyebrow">Upstream usage (wham)</div>
        <div className="flex items-center gap-1.5">
          {st.ts && (
            <span className="text-[10px] text-muted-foreground mono">
              as of {fmtLocalTime(new Date(st.ts).toISOString())}
            </span>
          )}
          {st.data && (
            <Button
              size="sm"
              variant="ghost"
              className="h-7 px-2"
              onClick={(e) => {
                e.stopPropagation();
                void run();
              }}
              disabled={st.loading}
              title="Refetch wham/usage"
            >
              <RefreshCw className={cn("h-3 w-3", st.loading && "animate-spin")} />
            </Button>
          )}
          <Button
            size="sm"
            variant="outline"
            className="h-7"
            onClick={onClick}
            disabled={st.loading}
          >
            <Gauge className="h-3 w-3" />
            {st.loading ? "Fetching…" : st.data ? (open ? "Hide" : "Show") : "Check usage"}
          </Button>
        </div>
      </div>
      {open && (
        <div className="mt-2 space-y-2">
          {st.error && (
            <div className="text-xs text-destructive mono whitespace-pre-wrap">{st.error}</div>
          )}
          {u && (
            <div className="text-xs text-muted-foreground flex flex-wrap items-center gap-2">
              <span>{u.email || "—"}</span>
              {u.plan_type && <span className="text-foreground font-semibold">plan {u.plan_type}</span>}
              {rl?.limit_reached && (
                <span className="text-destructive mono uppercase">
                  limit reached{reachedLabel(u.rate_limit_reached_type) ? ` (${reachedLabel(u.rate_limit_reached_type)})` : ""}
                </span>
              )}
              {spend?.reached && <span className="text-amber-500 mono uppercase">spend cap reached</span>}
            </div>
          )}
          {(primary || secondary) && (
            <table className="w-full text-xs">
              <thead>
                <tr className="eyebrow text-muted-foreground border-b">
                  <th className="py-1.5 pr-2 text-left font-medium">Window</th>
                  <th className="py-1.5 text-right font-medium">Used</th>
                  <th className="py-1.5 pl-2 font-medium">&nbsp;</th>
                  <th className="py-1.5 pl-2 text-right font-medium">Resets in</th>
                </tr>
              </thead>
              <tbody>
                {primary && (
                  <tr className="border-b last:border-b-0">
                    <td className="py-1.5 pr-2">{windowLabel("primary", primary.limit_window_seconds)}</td>
                    <td className="py-1.5 mono text-right tabular">
                      {(() => {
                        const { pct } = pctTone(primary.used_percent);
                        return pct != null ? pct + "%" : "—";
                      })()}
                    </td>
                    <td className="py-1.5 pl-2 w-24">
                      {(() => {
                        const { pct, color } = pctTone(primary.used_percent);
                        return pct != null ? (
                          <div className="h-1 bg-muted rounded overflow-hidden">
                            <div className={cn("h-full", color)} style={{ width: `${Math.min(100, pct)}%` }} />
                          </div>
                        ) : null;
                      })()}
                    </td>
                    <td className="py-1.5 pl-2 text-right">
                      <span className="mono tabular" title={fmtUnix(primary.reset_at) ?? ""}>
                        <span>{codexCountdown(primary.reset_at)}</span>
                      </span>
                    </td>
                  </tr>
                )}
                {secondary && (
                  <tr>
                    <td className="py-1.5 pr-2">{windowLabel("secondary", secondary.limit_window_seconds)}</td>
                    <td className="py-1.5 mono text-right tabular">
                      {(() => {
                        const { pct } = pctTone(secondary.used_percent);
                        return pct != null ? pct + "%" : "—";
                      })()}
                    </td>
                    <td className="py-1.5 pl-2 w-24">
                      {(() => {
                        const { pct, color } = pctTone(secondary.used_percent);
                        return pct != null ? (
                          <div className="h-1 bg-muted rounded overflow-hidden">
                            <div className={cn("h-full", color)} style={{ width: `${Math.min(100, pct)}%` }} />
                          </div>
                        ) : null;
                      })()}
                    </td>
                    <td className="py-1.5 pl-2 text-right">
                      <span className="mono tabular" title={fmtUnix(secondary.reset_at) ?? ""}>
                        <span>{codexCountdown(secondary.reset_at)}</span>
                      </span>
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          )}
          {credits && (credits.has_credits || credits.unlimited || (credits.balance && credits.balance !== "0")) && (
            <div className="text-[11px] text-muted-foreground mono flex gap-4 flex-wrap">
              <span>
                balance:{" "}
                <span className="text-foreground">
                  {credits.unlimited ? "∞" : credits.balance ?? "0"}
                </span>
              </span>
              {credits.approx_local_messages && credits.approx_local_messages.length > 0 && (
                <span>local msgs ≈ {credits.approx_local_messages.join("–")}</span>
              )}
              {credits.approx_cloud_messages && credits.approx_cloud_messages.length > 0 && (
                <span>cloud msgs ≈ {credits.approx_cloud_messages.join("–")}</span>
              )}
              {credits.overage_limit_reached && (
                <span className="text-destructive">overage limit reached</span>
              )}
            </div>
          )}
          {u && (
            <div className="flex items-center justify-between gap-2 flex-wrap border-t border-border pt-2">
              <div className="text-[11px] text-muted-foreground mono">
                reset cards:{" "}
                <span className={cn("font-semibold", resetAvailable > 0 ? "text-foreground" : "")}>
                  {resetAvailable}
                </span>{" "}
                available
              </div>
              <div className="flex items-center gap-2">
                {st.resetMsg && <span className="text-[11px] text-emerald-500 mono">{st.resetMsg}</span>}
                <Button
                  size="sm"
                  variant="outline"
                  className="h-7"
                  onClick={(e) => {
                    e.stopPropagation();
                    void doReset();
                  }}
                  disabled={st.resetting || resetAvailable <= 0}
                  title={
                    resetAvailable > 0
                      ? "Consume one reset credit to reset the rate-limit window now"
                      : "No reset credits available"
                  }
                >
                  <Zap className={cn("h-3 w-3", st.resetting && "animate-pulse")} />
                  {st.resetting ? "Resetting…" : "Reset quota"}
                </Button>
              </div>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
