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
          <ProviderCard key={p.provider} p={p} />
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

function ProviderCard({ p }: { p: MonitorProvider }) {
  const meta = STATUS_META[p.operational] ?? STATUS_META.unknown;
  const Icon = meta.Icon;
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
              className={cn("flex-1 min-w-[2px] h-full rounded-[1px]", dayColor(d))}
            />
          ))}
        </div>
      </div>

      {/* 24h timeline */}
      <div className="space-y-1.5">
        <div className="flex items-center justify-between text-[11px] text-muted-foreground">
          <span>Last 24h</span>
          <span>{lastProbeLabel(p)}</span>
        </div>
        {p.timeline_24h.length > 0 ? (
          <div className="flex items-end gap-[2px] h-5">
            {p.timeline_24h.map((s, i) => (
              <div
                key={`${s.ts}-${i}`}
                title={sampleTip(s)}
                className={cn(
                  "flex-1 min-w-[3px] h-full rounded-[1px]",
                  s.ok ? "bg-emerald-500" : "bg-rose-500",
                )}
              />
            ))}
          </div>
        ) : (
          <div className="text-xs text-muted-foreground italic">
            {p.probe_enabled ? "awaiting first probe…" : "active probing disabled"}
          </div>
        )}
      </div>
    </Card>
  );
}

function dayColor(d: MonitorDay): string {
  if (d.total === 0) return "bg-muted";
  if (d.ok >= d.total) return "bg-emerald-500";
  if (d.ok === 0) return "bg-rose-500";
  return "bg-amber-500";
}

function dayTip(d: MonitorDay): string {
  if (d.total === 0) return `${d.date} · no data`;
  const pct = ((d.ok / d.total) * 100).toFixed(1);
  return `${d.date} · ${pct}% (${d.ok}/${d.total})`;
}

function sampleTip(s: MonitorSample): string {
  const t = new Date(s.ts).toLocaleTimeString();
  if (s.ok) return `${t} · ok · ${s.latency_ms}ms`;
  return `${t} · failed${s.err ? ` · ${s.err}` : ""}`;
}

function lastProbeLabel(p: MonitorProvider): string {
  if (!p.last_probe) return "";
  const s = p.last_probe;
  const t = new Date(s.ts).toLocaleTimeString();
  if (s.ok) return `${s.latency_ms}ms · ${t}`;
  return `failed · ${t}`;
}
