import { useCallback, useEffect, useState } from "react";
import { Activity, CheckCircle2, AlertTriangle, XCircle, HelpCircle } from "lucide-react";
import {
  loadStatusMonitor,
  type MonitorResp,
  type MonitorProvider,
  type MonitorDay,
  type MonitorSample,
  type MonitorStatus,
} from "@/lib/status-api";
import { Card } from "./ui/card";
import { cn } from "@/lib/utils";

interface Props {
  refreshTick: number;
}

// Public uptime monitor. Renders one card per provider (Claude / OpenAI) with
// a live capacity badge, a statuspage-style 90-day uptime strip, and a 24h
// fine-grained probe timeline. Consumes /status/api/monitor (passive pool
// signal + persisted end-to-end probe history). Degrades gracefully: when
// active probing is disabled the badge reflects pool capacity only and the
// history strip shows "no data".
export function StatusMonitorPanel({ refreshTick }: Props) {
  const [data, setData] = useState<MonitorResp | null>(null);
  const [err, setErr] = useState(false);

  const load = useCallback(async () => {
    try {
      const d = await loadStatusMonitor();
      setData(d);
      setErr(false);
    } catch {
      setErr(true);
    }
  }, []);
  useEffect(() => {
    load();
  }, [load, refreshTick]);

  if (err || !data || data.providers.length === 0) return null;

  return (
    <section className="stagger space-y-3">
      <div className="flex items-baseline gap-2">
        <Activity className="h-4 w-4 self-center text-muted-foreground" />
        <h2 className="font-display text-lg md:text-xl tracking-tight">Service status</h2>
        <span className="eyebrow opacity-60 hidden sm:inline">
          PROBED EVERY {data.interval_minutes}M · 90-DAY UPTIME
        </span>
      </div>
      <div className="grid gap-3 md:grid-cols-2">
        {data.providers.map((p) => (
          <ProviderCard key={p.provider} p={p} generatedAt={data.generated_at} />
        ))}
      </div>
    </section>
  );
}

const STATUS_META: Record<
  MonitorStatus,
  { label: string; dot: string; text: string; Icon: typeof CheckCircle2 }
> = {
  operational: { label: "Operational", dot: "bg-emerald-500", text: "text-emerald-500", Icon: CheckCircle2 },
  degraded: { label: "Degraded", dot: "bg-amber-500", text: "text-amber-500", Icon: AlertTriangle },
  down: { label: "Down", dot: "bg-rose-500", text: "text-rose-500", Icon: XCircle },
  unknown: { label: "Unknown", dot: "bg-muted-foreground", text: "text-muted-foreground", Icon: HelpCircle },
};

// The recent strip mirrors the 90-day strip bar-for-bar: 90 slots of 10 minutes
// each (a 900-minute / 15-hour window) — one slot per probe interval — so the two
// strips line up and fill the same width.
const RECENT_SLOTS = 90;
const RECENT_SLOT_MS = 10 * 60 * 1000;
const RECENT_WINDOW_MS = RECENT_SLOTS * RECENT_SLOT_MS;

interface Slot {
  total: number;
  ok: number;
  from: number;
}

function ProviderCard({ p, generatedAt }: { p: MonitorProvider; generatedAt: string }) {
  const meta = STATUS_META[p.operational] ?? STATUS_META.unknown;
  const Icon = meta.Icon;
  const slots = bucketRecent(p.timeline_24h, generatedAt);
  const recentTotal = slots.reduce((n, s) => n + s.total, 0);
  const recentOk = slots.reduce((n, s) => n + s.ok, 0);
  const recentUptime = recentTotal === 0 ? 0 : (recentOk / recentTotal) * 100;
  return (
    <Card className="p-4 md:p-5 space-y-4">
      {/* header */}
      <div className="flex items-start justify-between gap-3">
        <div className="space-y-1">
          <div className="font-display text-lg tracking-tight">{p.name}</div>
          <div className="text-xs text-muted-foreground">
            {p.healthy_creds}/{p.total_creds} credential{p.total_creds === 1 ? "" : "s"} healthy
            {" · "}
            {p.slot_available ? "slot available" : "no free slot"}
          </div>
        </div>
        <div className={cn("flex items-center gap-1.5 shrink-0", meta.text)}>
          <Icon className="h-4 w-4" />
          <span className="text-sm font-medium">{meta.label}</span>
        </div>
      </div>

      {/* 90-day strip */}
      <div className="space-y-1.5">
        <div className="flex items-center justify-between text-[11px] text-muted-foreground">
          <span>90 days ago</span>
          <span className="font-medium text-foreground">
            {p.uptime_90d_pct > 0 ? `${p.uptime_90d_pct.toFixed(2)}% uptime` : "no probe data"}
          </span>
          <span>today</span>
        </div>
        <div className="flex items-end gap-[1.5px] h-8">
          {p.uptime_90d.map((d) => (
            <div
              key={d.date}
              title={dayTip(d)}
              className={cn("flex-1 min-w-[2px] h-full rounded-[1px]", barColor(d.total, d.ok))}
            />
          ))}
        </div>
      </div>

      {/* recent strip — 90 × 10-min slots (900-min window), aligned bar-for-bar
          with the 90-day strip above. */}
      <div className="space-y-1.5">
        <div className="flex items-center justify-between text-[11px] text-muted-foreground">
          <span>15h ago</span>
          <span className="font-medium text-foreground">
            {recentTotal > 0
              ? `${recentUptime.toFixed(2)}% uptime`
              : p.probe_enabled
                ? "awaiting first probe"
                : "active probing disabled"}
          </span>
          <span>now</span>
        </div>
        <div className="flex items-end gap-[1.5px] h-8">
          {slots.map((s, i) => (
            <div
              key={i}
              title={slotTip(s)}
              className={cn("flex-1 min-w-[2px] h-full rounded-[1px]", barColor(s.total, s.ok))}
            />
          ))}
        </div>
      </div>
    </Card>
  );
}

function barColor(total: number, ok: number): string {
  if (total === 0) return "bg-muted";
  if (ok >= total) return "bg-emerald-500";
  if (ok === 0) return "bg-rose-500";
  return "bg-amber-500";
}

function dayTip(d: MonitorDay): string {
  if (d.total === 0) return `${d.date} · no data`;
  const pct = ((d.ok / d.total) * 100).toFixed(1);
  return `${d.date} · ${pct}% (${d.ok}/${d.total})`;
}

// bucketRecent spreads raw probe samples across RECENT_SLOTS fixed 10-min slots
// ending at `generatedAt` (server clock) — 90 slots × 10 min = a 900-min window,
// so the strip lines up bar-for-bar with the 90-day day-buckets.
function bucketRecent(samples: MonitorSample[], generatedAt: string): Slot[] {
  const now = generatedAt ? new Date(generatedAt).getTime() : Date.now();
  const start = now - RECENT_WINDOW_MS;
  const slots: Slot[] = Array.from({ length: RECENT_SLOTS }, (_, i) => ({
    total: 0,
    ok: 0,
    from: start + i * RECENT_SLOT_MS,
  }));
  for (const s of samples) {
    const t = new Date(s.ts).getTime();
    if (Number.isNaN(t) || t < start || t > now) continue;
    let idx = Math.floor((t - start) / RECENT_SLOT_MS);
    if (idx < 0) idx = 0;
    if (idx >= RECENT_SLOTS) idx = RECENT_SLOTS - 1;
    slots[idx].total++;
    // "nodata" probes (transport error / timeout — no HTTP response, status 0)
    // count as healthy (green), not a gap: the active probe couldn't measure,
    // so we defer to the passive pool signal. Only a real HTTP error degrades.
    if (s.ok || (s.status ?? 0) === 0) slots[idx].ok++;
  }
  return slots;
}

function slotTip(s: Slot): string {
  const f = new Date(s.from);
  const hhmm = `${String(f.getHours()).padStart(2, "0")}:${String(f.getMinutes()).padStart(2, "0")}`;
  if (s.total === 0) return `${hhmm} · no data`;
  const pct = ((s.ok / s.total) * 100).toFixed(0);
  return `${hhmm} · ${pct}% (${s.ok}/${s.total})`;
}
