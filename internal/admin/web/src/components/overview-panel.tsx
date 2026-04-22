import { useCallback, useEffect, useState } from "react";
import {
  Area,
  AreaChart,
  Bar,
  BarChart,
  CartesianGrid,
  Line,
  LineChart,
  Cell,
  Pie,
  PieChart,
  XAxis,
  YAxis,
} from "recharts";
import { api } from "@/lib/api";
import type { AuthRow, Pricing, PricingEntry, RequestsResp, Summary } from "@/lib/types";
import {
  ChartContainer,
  ChartLegend,
  ChartLegendContent,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "@/components/ui/chart";
import { cn, fmtInt } from "@/lib/utils";

interface Props {
  summary: Summary | null;
  pricing?: Pricing;
  refreshTick: number;
}

const DAYS = 14;

function pad(n: number) {
  return String(n).padStart(2, "0");
}
function isoDay(d: Date) {
  return `${d.getUTCFullYear()}-${pad(d.getUTCMonth() + 1)}-${pad(d.getUTCDate())}`;
}

// Aggregate per-auth `usage.daily` entries (input + output + cache R/W) into
// one 14-day timeseries. Missing days are zero-padded so the chart reflects
// real elapsed time, not just active days.
function buildTokenTrend(auths: AuthRow[]): {
  day: string;
  input: number;
  output: number;
  cacheR: number;
  cacheW: number;
  requests: number;
}[] {
  const today = new Date();
  today.setUTCHours(0, 0, 0, 0);
  const seed = new Map<string, { input: number; output: number; cacheR: number; cacheW: number; requests: number }>();
  for (let i = DAYS - 1; i >= 0; i--) {
    const d = new Date(today);
    d.setUTCDate(today.getUTCDate() - i);
    seed.set(isoDay(d), { input: 0, output: 0, cacheR: 0, cacheW: 0, requests: 0 });
  }
  for (const a of auths) {
    for (const e of a.usage?.daily || []) {
      const slot = seed.get(e.date);
      if (!slot) continue;
      slot.input += e.counts.input_tokens || 0;
      slot.output += e.counts.output_tokens || 0;
      slot.cacheR += e.counts.cache_read_tokens || 0;
      slot.cacheW += e.counts.cache_create_tokens || 0;
      slot.requests += e.counts.requests || 0;
    }
  }
  return Array.from(seed.entries()).map(([day, v]) => ({ day, ...v }));
}

const tokenConfig: ChartConfig = {
  input: {
    label: "Input",
    theme: { light: "oklch(0.5 0.13 215)", dark: "oklch(0.8 0.16 145)" },
  },
  output: {
    label: "Output",
    theme: { light: "oklch(0.62 0.15 150)", dark: "oklch(0.72 0.14 215)" },
  },
  cacheR: {
    label: "Cache read",
    theme: { light: "oklch(0.68 0.15 70)", dark: "oklch(0.82 0.16 72)" },
  },
  cacheW: {
    label: "Cache write",
    theme: { light: "oklch(0.55 0.18 25)", dark: "oklch(0.68 0.2 25)" },
  },
};

const costConfig: ChartConfig = {
  cost_usd: {
    label: "Cost (USD)",
    theme: { light: "oklch(0.38 0.09 215)", dark: "oklch(0.82 0.16 145)" },
  },
};

const reqConfig: ChartConfig = {
  requests: {
    label: "Requests",
    theme: { light: "oklch(0.48 0.1 215)", dark: "oklch(0.72 0.14 215)" },
  },
};

const healthConfig: ChartConfig = {
  healthy: { label: "Healthy", theme: { light: "oklch(0.58 0.12 150)", dark: "oklch(0.78 0.16 145)" } },
  quota: { label: "Quota", theme: { light: "oklch(0.68 0.15 70)", dark: "oklch(0.82 0.16 72)" } },
  unhealthy: { label: "Unhealthy", theme: { light: "oklch(0.52 0.18 25)", dark: "oklch(0.68 0.2 25)" } },
  disabled: { label: "Disabled", theme: { light: "oklch(0.7 0.01 85)", dark: "oklch(0.5 0.01 260)" } },
};

const fmtDay = (d: string) => d.slice(5).replace("-", "/");

export function OverviewPanel({ summary, pricing, refreshTick }: Props) {
  const [reqData, setReqData] = useState<RequestsResp | null>(null);
  const [lifetimeData, setLifetimeData] = useState<RequestsResp | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    setBusy(true);
    try {
      const today = new Date();
      const fromD = new Date(today);
      fromD.setDate(today.getDate() - (DAYS - 1));
      const from = `${fromD.getFullYear()}-${pad(fromD.getMonth() + 1)}-${pad(fromD.getDate())}`;
      const to = `${today.getFullYear()}-${pad(today.getMonth() + 1)}-${pad(today.getDate())}`;
      const [d, all] = await Promise.all([
        api<RequestsResp>(`/admin/api/requests?limit=1&from=${from}&to=${to}`),
        api<RequestsResp>(`/admin/api/requests?limit=1`),
      ]);
      setReqData(d);
      setLifetimeData(all);
    } catch {
      // ignore
    } finally {
      setBusy(false);
    }
  }, []);
  useEffect(() => {
    load();
  }, [load, refreshTick]);

  const lookupPrice = (model: string): PricingEntry | null => {
    if (!pricing) return null;
    const m = (model || "").toLowerCase().trim();
    const models = pricing.models || {};
    if (m && models[m]) return models[m];
    if (m) {
      for (let i = m.lastIndexOf("-"); i > 0; i = m.lastIndexOf("-", i - 1)) {
        const p = models[m.slice(0, i)];
        if (p) return p;
      }
    }
    return pricing.default || null;
  };

  const cacheStats = (() => {
    if (!lifetimeData) return null;
    const s = lifetimeData.summary;
    const input = s.input_tokens || 0;
    const cacheRead = s.cache_read_tokens || 0;
    const cacheCreate = s.cache_create_tokens || 0;
    const denom = input + cacheRead + cacheCreate;
    const hitRate = denom > 0 ? cacheRead / denom : 0;
    const actualCost = s.cost_usd || 0;
    let noCacheCost = 0;
    if (pricing) {
      for (const [name, a] of Object.entries(lifetimeData.by_model)) {
        const p = lookupPrice(name);
        if (!p) continue;
        const ain = a.input_tokens || 0;
        const acr = a.cache_read_tokens || 0;
        const acw = a.cache_create_tokens || 0;
        const aout = a.output_tokens || 0;
        noCacheCost += ((ain + acr + acw) * p.input_per_1m) / 1e6;
        noCacheCost += (aout * p.output_per_1m) / 1e6;
      }
    }
    return {
      hitRate,
      actualCost,
      noCacheCost,
      savings: Math.max(0, noCacheCost - actualCost),
      input,
      cacheRead,
      cacheCreate,
      hasPricing: !!pricing,
    };
  })();

  if (!summary) {
    return (
      <div className="py-16 text-center eyebrow animate-pulse bg-card border border-border-strong rounded-md">
        <span className="opacity-60">Loading telemetry…</span>
      </div>
    );
  }

  const trend = buildTokenTrend(summary.auths);

  // Daily cost + request series: zero-pad the same 14-day window so charts align.
  const costSeries = (() => {
    const today = new Date();
    const seed = new Map<string, { day: string; cost_usd: number; requests: number }>();
    for (let i = DAYS - 1; i >= 0; i--) {
      const d = new Date(today);
      d.setDate(today.getDate() - i);
      const key = `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
      seed.set(key, { day: key, cost_usd: 0, requests: 0 });
    }
    if (reqData) {
      for (const [k, v] of Object.entries(reqData.by_day)) {
        const slot = seed.get(k);
        if (slot) {
          slot.cost_usd = v.cost_usd;
          slot.requests = v.count;
        }
      }
    }
    return Array.from(seed.values());
  })();

  // Health breakdown across the auth pool
  const health = (() => {
    let healthy = 0,
      quota = 0,
      unhealthy = 0,
      disabled = 0;
    for (const a of summary.auths) {
      if (a.disabled) disabled++;
      else if (a.quota_exceeded) quota++;
      else if (a.hard_failure) unhealthy++;
      else if (a.healthy) healthy++;
      else unhealthy++;
    }
    return [
      { key: "healthy", label: "Healthy", value: healthy },
      { key: "quota", label: "Quota", value: quota },
      { key: "unhealthy", label: "Unhealthy", value: unhealthy },
      { key: "disabled", label: "Disabled", value: disabled },
    ].filter((x) => x.value > 0);
  })();

  return (
    <div className="space-y-8">
      {/* Lifetime cache efficiency */}
      <section>
        <div className="flex items-baseline justify-between mb-3 gap-4">
          <div>
            <div className="eyebrow mb-1.5">All-time cache efficiency</div>
            <h3 className="font-display text-2xl md:text-3xl tracking-tight">
              Prompt <span className="text-muted-foreground">caching</span>
            </h3>
          </div>
          <span className="eyebrow tabular opacity-70 hidden sm:inline">
            since first request
          </span>
        </div>
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4 md:gap-5">
          <CacheCard
            label="Cache hit rate"
            value={cacheStats ? (cacheStats.hitRate * 100).toFixed(2) + "%" : busy ? "…" : "—"}
            ratio={cacheStats?.hitRate ?? 0}
            foot={
              cacheStats ? (
                <span className="mono tabular">
                  cacheR {fmtInt(cacheStats.cacheRead)} / (input {fmtInt(cacheStats.input)} + cacheR{" "}
                  {fmtInt(cacheStats.cacheRead)} + cacheW {fmtInt(cacheStats.cacheCreate)})
                </span>
              ) : null
            }
          />
          <CacheCard
            label="Saved by caching"
            value={
              cacheStats && cacheStats.hasPricing
                ? "$" + cacheStats.savings.toFixed(4)
                : cacheStats && !cacheStats.hasPricing
                  ? "pricing unavailable"
                  : busy
                    ? "…"
                    : "—"
            }
            foot={
              cacheStats && cacheStats.hasPricing ? (
                <span className="mono tabular">
                  no-cache ${cacheStats.noCacheCost.toFixed(4)} − actual $
                  {cacheStats.actualCost.toFixed(4)}
                </span>
              ) : null
            }
          />
        </div>
      </section>

      {/* Token throughput — large feature chart */}
      <section>
        <div className="flex items-baseline justify-between mb-3 gap-4">
          <div>
            <div className="eyebrow mb-1.5">14d throughput</div>
            <h3 className="font-display text-2xl md:text-3xl tracking-tight">
              Token <span className="text-muted-foreground">volume by type</span>
            </h3>
          </div>
          <span className="eyebrow tabular opacity-70 hidden sm:inline">
            {fmtInt(trend.reduce((s, x) => s + x.input + x.output + x.cacheR + x.cacheW, 0))} tok
          </span>
        </div>
        <div className="bg-card border border-border-strong rounded-md p-4 md:p-5">
          <ChartContainer config={tokenConfig} className="h-[240px] md:h-[280px] aspect-auto w-full">
            <AreaChart data={trend} margin={{ top: 10, right: 12, left: -8, bottom: 0 }}>
              <defs>
                {(["input", "output", "cacheR", "cacheW"] as const).map((k) => (
                  <linearGradient key={k} id={`grad-${k}`} x1="0" y1="0" x2="0" y2="1">
                    <stop offset="5%" stopColor={`var(--color-${k})`} stopOpacity={0.5} />
                    <stop offset="95%" stopColor={`var(--color-${k})`} stopOpacity={0} />
                  </linearGradient>
                ))}
              </defs>
              <CartesianGrid strokeDasharray="3 3" vertical={false} />
              <XAxis
                dataKey="day"
                tickLine={false}
                axisLine={false}
                tickMargin={10}
                tickFormatter={fmtDay}
                minTickGap={16}
              />
              <YAxis
                tickLine={false}
                axisLine={false}
                width={56}
                tickFormatter={(v: number) =>
                  v >= 1_000_000 ? `${(v / 1_000_000).toFixed(1)}M` : v >= 1000 ? `${Math.round(v / 1000)}k` : String(v)
                }
              />
              <ChartTooltip
                cursor={{ stroke: "var(--border)" }}
                content={
                  <ChartTooltipContent
                    indicator="dot"
                    labelFormatter={(v) => `Day · ${v}`}
                    valueFormatter={(v) => (typeof v === "number" ? v.toLocaleString() + " tok" : String(v))}
                  />
                }
              />
              <ChartLegend content={<ChartLegendContent />} />
              {(["cacheW", "cacheR", "output", "input"] as const).map((k) => (
                <Area
                  key={k}
                  type="monotone"
                  dataKey={k}
                  stackId="1"
                  stroke={`var(--color-${k})`}
                  fill={`url(#grad-${k})`}
                  strokeWidth={1.5}
                />
              ))}
            </AreaChart>
          </ChartContainer>
        </div>
      </section>

      {/* Two-up: cost + requests */}
      <section className="grid grid-cols-1 lg:grid-cols-2 gap-4 md:gap-5">
        <div className="bg-card border border-border-strong rounded-md p-4 md:p-5">
          <div className="flex items-baseline justify-between mb-3 gap-2">
            <div>
              <div className="eyebrow mb-1">Daily cost</div>
              <h3 className="font-display text-xl tracking-tight">
                Spend <span className="text-muted-foreground">· last {DAYS}d</span>
              </h3>
            </div>
            <span className="eyebrow tabular opacity-70">
              ${costSeries.reduce((s, x) => s + x.cost_usd, 0).toFixed(2)}
            </span>
          </div>
          <ChartContainer
            config={costConfig}
            className={cn("h-[220px] aspect-auto w-full", busy && "opacity-70")}
          >
            <BarChart data={costSeries} margin={{ top: 8, right: 4, left: -12, bottom: 0 }}>
              <CartesianGrid strokeDasharray="3 3" vertical={false} />
              <XAxis
                dataKey="day"
                tickLine={false}
                axisLine={false}
                tickMargin={8}
                tickFormatter={fmtDay}
                minTickGap={20}
              />
              <YAxis
                tickLine={false}
                axisLine={false}
                width={48}
                tickFormatter={(v: number) => `$${v < 1 ? v.toFixed(2) : Math.round(v)}`}
              />
              <ChartTooltip
                cursor={{ fill: "var(--muted)", opacity: 0.5 }}
                content={
                  <ChartTooltipContent
                    indicator="dot"
                    labelFormatter={(v) => `Day · ${v}`}
                    valueFormatter={(v) => (typeof v === "number" ? `$${v.toFixed(4)}` : String(v))}
                  />
                }
              />
              <Bar dataKey="cost_usd" fill="var(--color-cost_usd)" radius={[3, 3, 0, 0]} />
            </BarChart>
          </ChartContainer>
        </div>

        <div className="bg-card border border-border-strong rounded-md p-4 md:p-5">
          <div className="flex items-baseline justify-between mb-3 gap-2">
            <div>
              <div className="eyebrow mb-1">Daily requests</div>
              <h3 className="font-display text-xl tracking-tight">
                Traffic <span className="text-muted-foreground">· last {DAYS}d</span>
              </h3>
            </div>
            <span className="eyebrow tabular opacity-70">
              {fmtInt(costSeries.reduce((s, x) => s + x.requests, 0))} req
            </span>
          </div>
          <ChartContainer config={reqConfig} className="h-[220px] aspect-auto w-full">
            <LineChart data={costSeries} margin={{ top: 8, right: 8, left: -12, bottom: 0 }}>
              <CartesianGrid strokeDasharray="3 3" vertical={false} />
              <XAxis
                dataKey="day"
                tickLine={false}
                axisLine={false}
                tickMargin={8}
                tickFormatter={fmtDay}
                minTickGap={20}
              />
              <YAxis
                tickLine={false}
                axisLine={false}
                width={48}
                tickFormatter={(v: number) =>
                  v >= 1000 ? `${Math.round(v / 1000)}k` : String(Math.round(v))
                }
              />
              <ChartTooltip
                cursor={{ stroke: "var(--border)" }}
                content={
                  <ChartTooltipContent
                    indicator="dot"
                    labelFormatter={(v) => `Day · ${v}`}
                    valueFormatter={(v) => (typeof v === "number" ? v.toLocaleString() + " req" : String(v))}
                  />
                }
              />
              <Line
                type="monotone"
                dataKey="requests"
                stroke="var(--color-requests)"
                strokeWidth={2}
                dot={{ r: 3, strokeWidth: 0, fill: "var(--color-requests)" }}
                activeDot={{ r: 5 }}
              />
            </LineChart>
          </ChartContainer>
        </div>
      </section>

      {/* Three-up: health mix + top models + top clients */}
      <section className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4 md:gap-5">
        {/* Auth health donut */}
        <div className="bg-card border border-border-strong rounded-md p-4 md:p-5 md:col-span-2 lg:col-span-1">
          <div className="mb-4">
            <div className="eyebrow mb-1">Auth pool health</div>
            <h3 className="font-display text-xl tracking-tight">
              <span className="text-muted-foreground">{summary.auths.length} credential(s)</span>
            </h3>
          </div>
          {health.length === 0 ? (
            <div className="py-12 text-center text-sm text-muted-foreground font-mono">no data</div>
          ) : (
            <ChartContainer config={healthConfig} className="h-[220px] aspect-auto w-full">
              <PieChart>
                <ChartTooltip content={<ChartTooltipContent hideLabel indicator="dot" />} />
                <Pie
                  data={health}
                  dataKey="value"
                  nameKey="key"
                  innerRadius={55}
                  outerRadius={85}
                  strokeWidth={2}
                  stroke="var(--card)"
                  paddingAngle={3}
                >
                  {health.map((h) => (
                    <Cell key={h.key} fill={`var(--color-${h.key})`} />
                  ))}
                </Pie>
                <ChartLegend content={<ChartLegendContent />} />
              </PieChart>
            </ChartContainer>
          )}
        </div>

        {/* Top models */}
        <TopList
          title="Top models"
          sub="by cost · last 14d"
          rows={reqData
            ? Object.entries(reqData.by_model)
                .sort(([, a], [, b]) => b.cost_usd - a.cost_usd)
                .slice(0, 6)
                .map(([k, v]) => ({ k, v: v.cost_usd, meta: `${fmtInt(v.count)} req`, fmt: "cost" as const }))
            : []}
        />

        {/* Top clients */}
        <TopList
          title="Top clients"
          sub="by cost · last 14d"
          rows={reqData
            ? Object.entries(reqData.by_client)
                .sort(([, a], [, b]) => b.cost_usd - a.cost_usd)
                .slice(0, 6)
                .map(([k, v]) => ({ k: k || "(unnamed)", v: v.cost_usd, meta: `${fmtInt(v.count)} req`, fmt: "cost" as const }))
            : []}
        />
      </section>
    </div>
  );
}

function CacheCard({
  label,
  value,
  foot,
  ratio,
}: {
  label: string;
  value: string;
  foot?: React.ReactNode;
  ratio?: number;
}) {
  const pct = typeof ratio === "number" ? Math.round(Math.max(0, Math.min(1, ratio)) * 100) : null;
  const bar =
    pct == null
      ? "bg-muted-foreground/40"
      : pct >= 60
        ? "bg-emerald-500"
        : pct >= 30
          ? "bg-amber-500"
          : "bg-slate-400";
  return (
    <div className="bg-card border border-border-strong rounded-md p-5">
      <div className="eyebrow mb-1.5">{label}</div>
      <div className="font-display text-3xl md:text-4xl tracking-tight tabular">{value}</div>
      {pct != null && (
        <div className="mt-3 h-1.5 w-full bg-muted rounded-full overflow-hidden">
          <div
            className={cn("h-full transition-all", bar)}
            style={{ width: `${pct}%` }}
          />
        </div>
      )}
      {foot && <div className="mt-2 text-[11px] text-muted-foreground">{foot}</div>}
    </div>
  );
}

function TopList({
  title,
  sub,
  rows,
}: {
  title: string;
  sub: string;
  rows: { k: string; v: number; meta: string; fmt: "cost" | "int" }[];
}) {
  const max = Math.max(1e-9, ...rows.map((r) => r.v));
  return (
    <div className="bg-card border border-border-strong rounded-md p-4 md:p-5">
      <div className="mb-3">
        <div className="eyebrow mb-1">{title}</div>
        <h3 className="font-display text-xl tracking-tight">
          <span className="text-muted-foreground">{sub}</span>
        </h3>
      </div>
      {rows.length === 0 ? (
        <div className="py-8 text-center text-sm text-muted-foreground font-mono">no data</div>
      ) : (
        <ul className="space-y-2.5">
          {rows.map((r) => (
            <li key={r.k} className="group">
              <div className="flex items-baseline justify-between gap-3 text-sm mb-1">
                <span className="mono text-xs truncate flex-1" title={r.k}>
                  {r.k}
                </span>
                <span className="mono tabular font-medium shrink-0">
                  {r.fmt === "cost" ? `$${r.v.toFixed(4)}` : fmtInt(r.v)}
                </span>
                <span className="eyebrow opacity-60 tabular shrink-0">{r.meta}</span>
              </div>
              <div className="h-1 w-full bg-muted rounded-full overflow-hidden">
                <div
                  className="h-full bg-primary/70 group-hover:bg-primary transition-all"
                  style={{ width: `${Math.round((r.v / max) * 100)}%` }}
                />
              </div>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
