import { useState } from "react";
import { api } from "@/lib/api";
import type { AuthRow, UpstreamResponse, UpstreamUsage, UsageWindow } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { cn, fmtDate } from "@/lib/utils";

interface PerAuthState {
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

export function UpstreamQuota({ auths }: { auths: AuthRow[] }) {
  const [state, setState] = useState<Record<string, PerAuthState>>({});
  const run = async (id: string) => {
    setState((s) => ({ ...s, [id]: { ...s[id], loading: true, error: "" } }));
    try {
      const d = await api<UpstreamResponse>(
        `/admin/api/auths/${encodeURIComponent(id)}/anthropic-usage`,
        { method: "POST" },
      );
      setState((s) => ({ ...s, [id]: { loading: false, data: d, ts: Date.now() } }));
    } catch (x: any) {
      setState((s) => ({ ...s, [id]: { loading: false, error: x.message } }));
    }
  };

  const oauths = auths.filter((a) => a.kind === "oauth");
  if (oauths.length === 0) {
    return <div className="p-6 text-base text-muted-foreground">No OAuth credentials to query.</div>;
  }

  const renderWindows = (usage: UpstreamUsage | undefined) => {
    if (!usage || !usage.body) return null;
    const body = usage.body;
    const rows: [string, UsageWindow][] = WINDOW_KEYS.map(
      ([k, label]) => [label, body[k]] as [string, UsageWindow | undefined],
    ).filter(([, v]) => !!v && typeof v === "object" && (v.utilization != null || v.resets_at != null)) as [string, UsageWindow][];
    const extra = body.extra_usage;
    if (!rows.length && !extra) {
      return (
        <pre className="mono text-xs text-muted-foreground whitespace-pre-wrap">
          {JSON.stringify(body, null, 2)}
        </pre>
      );
    }
    return (
      <table className="w-full text-sm">
        <thead>
          <tr className="text-xs uppercase tracking-wide text-muted-foreground border-b">
            <th className="py-2 pr-3 text-left font-medium">Window</th>
            <th className="py-2 text-right font-medium">Used</th>
            <th className="py-2 pl-2 font-medium">&nbsp;</th>
            <th className="py-2 pl-3 text-right font-medium">Resets</th>
          </tr>
        </thead>
        <tbody>
          {rows.map(([label, w]) => {
            const { pct, color } = pctAndColor(w.utilization);
            return (
              <tr key={label} className="border-b">
                <td className="py-2 pr-3">{label}</td>
                <td className="py-2 mono text-right">{pct != null ? pct + "%" : "—"}</td>
                <td className="py-2 pl-2 w-40">
                  {pct != null && (
                    <div className="h-1.5 bg-muted rounded overflow-hidden">
                      <div className={cn("h-full", color)} style={{ width: `${Math.min(100, pct)}%` }} />
                    </div>
                  )}
                </td>
                <td className="py-2 pl-3 text-right">
                  {w.resets_at ? (
                    <span className="mono text-sm">{fmtDate(w.resets_at)}</span>
                  ) : (
                    <span className="text-xs text-muted-foreground">—</span>
                  )}
                </td>
              </tr>
            );
          })}
          {extra && extra.is_enabled && (
            <tr className="border-b">
              <td className="py-2 pr-3">extra credits</td>
              <td className="py-2 mono text-right">
                {(() => {
                  const { pct } = pctAndColor(extra.utilization);
                  return pct != null ? pct + "%" : "—";
                })()}
              </td>
              <td className="py-2 pl-2 w-40">
                {(() => {
                  const { pct, color } = pctAndColor(extra.utilization);
                  return pct != null ? (
                    <div className="h-1.5 bg-muted rounded overflow-hidden">
                      <div className={cn("h-full", color)} style={{ width: `${Math.min(100, pct)}%` }} />
                    </div>
                  ) : null;
                })()}
              </td>
              <td className="py-2 pl-3 text-right mono text-xs text-muted-foreground">
                ${Number(extra.used_credits || 0).toFixed(2)} / $
                {Number(extra.monthly_limit || 0).toFixed(0)}
              </td>
            </tr>
          )}
        </tbody>
      </table>
    );
  };

  const renderProfile = (profile: UpstreamResponse["profile"] | undefined) => {
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
      <div className="text-sm text-muted-foreground">
        plan: <span className="text-foreground font-semibold">{plan}</span>
        {tier && ` · tier ${tier}`}
        {email && ` · ${email}`}
      </div>
    );
  };

  return (
    <div className="divide-y">
      {oauths.map((a) => {
        const st = state[a.id] || {};
        const usage = st.data?.usage;
        const profile = st.data?.profile;
        return (
          <div key={a.id} className="p-4 space-y-2">
            <div className="flex items-center justify-between gap-3 flex-wrap">
              <div>
                <div className="font-medium">{a.label || a.id}</div>
                <div className="mono text-xs text-muted-foreground">
                  {a.id}
                  {a.proxy_url && ` · via ${a.proxy_url}`}
                </div>
              </div>
              <div className="flex items-center gap-2 flex-wrap">
                {st.ts && (
                  <span className="text-xs text-muted-foreground">
                    fetched {fmtDate(new Date(st.ts).toISOString())}
                  </span>
                )}
                <Button size="sm" variant="outline" disabled={st.loading} onClick={() => run(a.id)}>
                  {st.loading ? "Fetching…" : st.ts ? "Refetch" : "Check upstream"}
                </Button>
              </div>
            </div>
            {st.error && (
              <div className="text-sm text-destructive mono whitespace-pre-wrap">{st.error}</div>
            )}
            {usage && usage.error && (
              <div className="text-sm text-destructive mono">
                usage http {usage.status}: {usage.error}
              </div>
            )}
            {renderProfile(profile)}
            {renderWindows(usage)}
          </div>
        );
      })}
    </div>
  );
}
