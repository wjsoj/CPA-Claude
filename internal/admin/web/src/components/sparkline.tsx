import { useState } from "react";
import type { DayEntry } from "@/lib/types";
import { cn, fmtUSD } from "@/lib/utils";
import { api, ApiError } from "@/lib/api";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";

interface DailyCostEntry {
  date: string;
  cost_usd: number;
  requests: number;
}

// dayLabel turns a "YYYY-MM-DD" bucket label into "08/20" without going
// through Date/toLocaleDateString — that round-trip reads the string as a
// UTC instant and can shift the displayed day by one in negative-UTC-offset
// zones. Mirrors dashboard-board.tsx's local fmtDay for the same reason.
const dayLabel = (d: string) => d.slice(5).replace("-", "/");

export function Sparkline({ daily, authId }: { daily: DayEntry[]; authId?: string }) {
  const [cost, setCost] = useState<DailyCostEntry[] | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  if (!daily.length) return null;
  const map = Object.fromEntries(daily.map((d) => [d.date, d.counts]));
  const end = daily[daily.length - 1]!.date;
  const days = 14;
  const out: { date: string; val: number }[] = [];
  const endD = new Date(end + "T00:00:00Z");
  for (let i = days - 1; i >= 0; i--) {
    const d = new Date(endD);
    d.setUTCDate(endD.getUTCDate() - i);
    const key = d.toISOString().slice(0, 10);
    const c = map[key] || { input_tokens: 0, output_tokens: 0 };
    out.push({
      date: key,
      val: (c.input_tokens || 0) + (c.output_tokens || 0),
    });
  }
  const max = Math.max(1, ...out.map((o) => o.val));

  const bars = (
    <div className={cn("flex items-end gap-[2px] h-10 w-[88px]", authId && "cursor-help")}>
      {out.map((o) => {
        const pct = Math.round((o.val / max) * 100);
        return (
          <div
            key={o.date}
            className={cn(
              "w-[4px] rounded-sm",
              o.val > 0 ? "bg-slate-700 dark:bg-slate-300" : "bg-slate-200 dark:bg-slate-600",
            )}
            style={{ height: `${Math.max(pct, o.val > 0 ? 6 : 2)}%` }}
          />
        );
      })}
    </div>
  );

  // No authId (e.g. a context that never wired one through): keep the plain
  // bars, no fetch to make.
  if (!authId) return bars;

  const load = () => {
    if (cost || loading) return;
    setLoading(true);
    setError(null);
    api<{ days: DailyCostEntry[] }>(`/admin/api/auths/${encodeURIComponent(authId)}/daily-cost`)
      .then((r) => setCost(r.days))
      .catch((e) => setError(e instanceof ApiError ? e.message : String(e)))
      .finally(() => setLoading(false));
  };

  const total = cost ? cost.reduce((s, d) => s + d.cost_usd, 0) : 0;

  return (
    <TooltipProvider delayDuration={150}>
      <Tooltip onOpenChange={(open) => open && load()}>
        <TooltipTrigger asChild>{bars}</TooltipTrigger>
        <TooltipContent side="top" className="w-52 p-2.5">
          <div className="mb-1.5 text-[11px] font-medium text-muted-foreground">近 14 天花费</div>
          {loading && <div className="text-xs text-muted-foreground">加载中…</div>}
          {error && <div className="text-xs text-destructive">{error}</div>}
          {cost && (
            <div className="space-y-0.5">
              {cost.map((d) => (
                <div
                  key={d.date}
                  className="mono tabular flex items-center justify-between text-[11px]"
                >
                  <span className="text-muted-foreground">{dayLabel(d.date)}</span>
                  <span className={cn("font-medium", d.cost_usd === 0 && "text-muted-foreground")}>
                    {fmtUSD(d.cost_usd)}
                  </span>
                </div>
              ))}
              <div className="mono tabular mt-1.5 flex items-center justify-between border-t border-border pt-1 text-[11px] font-semibold">
                <span>合计</span>
                <span>{fmtUSD(total)}</span>
              </div>
            </div>
          )}
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  );
}
