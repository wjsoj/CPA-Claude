import { useCallback, useEffect, useMemo, useState } from "react";
import { toast } from "sonner";
import {
  Activity,
  AlertTriangle,
  CheckCircle2,
  CircleOff,
  Gauge,
  Loader2,
  Plus,
  RefreshCw,
  Search,
  Trash2,
  X,
  XCircle,
} from "lucide-react";
import {
  loadStatusOverview,
  loadSavedTokens,
  queryStatusHistory,
  queryStatusTokens,
  saveSavedTokens,
  type StatusOverview,
  type StatusRecent,
  type StatusTokenResult,
} from "@/lib/status-api";
import { CartesianGrid, Line, LineChart, XAxis, YAxis } from "recharts";
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "@/components/ui/chart";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ThemeToggle } from "./theme-toggle";
import { cn, fmtDate, fmtInt } from "@/lib/utils";

function mask(tok: string): string {
  const t = tok.trim();
  if (t.length <= 10) return "***";
  return t.slice(0, 6) + "…" + t.slice(-4);
}

function HealthBadge({ row }: { row: StatusOverview["auths"][number] }) {
  if (row.disabled) {
    return (
      <Badge variant="slate" className="gap-1">
        <CircleOff className="h-3 w-3" /> disabled
      </Badge>
    );
  }
  if (row.quota_exceeded) {
    return (
      <Badge
        className="gap-1"
        style={{
          background: "color-mix(in oklab, var(--warning) 15%, transparent)",
          color: "var(--warning)",
          borderColor: "color-mix(in oklab, var(--warning) 40%, transparent)",
        }}
      >
        <Gauge className="h-3 w-3" /> quota
      </Badge>
    );
  }
  if (row.hard_failure || !row.healthy) {
    return (
      <Badge variant="destructive" className="gap-1">
        <XCircle className="h-3 w-3" /> unhealthy
      </Badge>
    );
  }
  return (
    <Badge
      className="gap-1"
      style={{
        background: "color-mix(in oklab, var(--success) 15%, transparent)",
        color: "var(--success)",
        borderColor: "color-mix(in oklab, var(--success) 40%, transparent)",
      }}
    >
      <CheckCircle2 className="h-3 w-3" /> healthy
    </Badge>
  );
}

function Metric({
  label,
  value,
  unit,
  accent,
}: {
  label: string;
  value: string | number;
  unit?: string;
  accent?: boolean;
}) {
  return (
    <div className={cn("metric-cell", accent && "metric-cell-accent")}>
      <div className="relative z-10">
        <div className="eyebrow mb-2.5">{label}</div>
        <div className="flex items-baseline gap-1.5">
          <span
            className={cn(
              "font-mono text-2xl md:text-[2rem] leading-none font-medium tracking-tight tabular",
              accent ? "text-primary" : "text-foreground",
            )}
          >
            {value}
          </span>
          {unit && (
            <span className="font-mono text-xs text-muted-foreground uppercase tracking-wider">
              {unit}
            </span>
          )}
        </div>
      </div>
      <span aria-hidden className="metric-cell-corner" />
      <span aria-hidden className="metric-cell-spark" />
    </div>
  );
}

export function StatusPage() {
  const [ov, setOv] = useState<StatusOverview | null>(null);
  const [ovErr, setOvErr] = useState("");
  const [refreshing, setRefreshing] = useState(false);
  const [tokens, setTokens] = useState<string[]>(() => loadSavedTokens());
  const [input, setInput] = useState("");
  const [results, setResults] = useState<StatusTokenResult[] | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    saveSavedTokens(tokens);
  }, [tokens]);

  const refresh = useCallback(async () => {
    setRefreshing(true);
    try {
      const d = await loadStatusOverview();
      setOv(d);
      setOvErr("");
    } catch (e: any) {
      setOvErr(e.message || String(e));
    } finally {
      setTimeout(() => setRefreshing(false), 400);
    }
  }, []);
  useEffect(() => {
    refresh();
    const t = setInterval(refresh, 15000);
    return () => clearInterval(t);
  }, [refresh]);

  const runQuery = useCallback(
    async (toks: string[]) => {
      if (toks.length === 0) {
        setResults([]);
        return;
      }
      setBusy(true);
      try {
        const d = await queryStatusTokens(toks);
        setResults(d.results);
      } catch (e: any) {
        toast.error("Query failed", { description: e.message || String(e) });
      } finally {
        setBusy(false);
      }
    },
    [],
  );

  // On mount, if the user already has saved tokens, auto-run once.
  useEffect(() => {
    if (tokens.length > 0 && results === null) {
      runQuery(tokens);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const addTokens = (raw: string) => {
    // Accept comma / newline / whitespace separated batches.
    const parts = raw
      .split(/[\s,]+/)
      .map((s) => s.trim())
      .filter(Boolean);
    if (parts.length === 0) return;
    const next = [...tokens];
    let added = 0;
    for (const p of parts) {
      if (!next.includes(p)) {
        next.push(p);
        added++;
      }
    }
    if (added === 0) {
      toast.message("Already saved", { description: "No new tokens added." });
      return;
    }
    setTokens(next);
    setInput("");
    toast.success(`Added ${added} token(s)`);
    runQuery(next);
  };

  const removeToken = (t: string) => {
    const next = tokens.filter((x) => x !== t);
    setTokens(next);
    if (next.length > 0) runQuery(next);
    else setResults([]);
  };

  const clearAll = () => {
    setTokens([]);
    setResults([]);
  };

  const byMasked = useMemo(() => {
    const m = new Map<string, StatusTokenResult>();
    for (const r of results || []) m.set(r.masked, r);
    return m;
  }, [results]);

  return (
    <div className="relative min-h-screen pb-16">
      <div className="max-w-[1280px] mx-auto px-4 sm:px-6 lg:px-10 py-6 md:py-9 space-y-8 md:space-y-10">
        {/* MASTHEAD */}
        <header className="stagger space-y-5">
          <div className="flex items-center justify-between gap-4">
            <div className="eyebrow flex items-center gap-2.5">
              <span className="relative inline-flex h-2 w-2">
                <span className="absolute inline-flex h-full w-full rounded-full bg-primary opacity-75 animate-ping" />
                <span className="relative inline-flex rounded-full h-2 w-2 bg-primary" />
              </span>
              <span>CPA · Claude / Public status</span>
            </div>
            <div className="flex items-center gap-2">
              <ThemeToggle />
              <Button
                variant="outline"
                size="icon"
                onClick={refresh}
                disabled={refreshing}
                aria-label="Refresh"
                title="Refresh"
                className="border-border-strong bg-card/60 hover:bg-card"
              >
                <RefreshCw
                  className={cn("h-4 w-4 transition-transform", refreshing && "animate-spin")}
                />
              </Button>
            </div>
          </div>

          <div className="flex flex-col lg:flex-row lg:items-end lg:justify-between gap-5 pt-1">
            <div className="space-y-2.5 max-w-3xl">
              <h1 className="font-display text-4xl sm:text-5xl lg:text-6xl leading-[0.95] tracking-tight">
                Service <span className="text-primary">status</span>.
              </h1>
              <p className="text-sm lg:text-base text-muted-foreground max-w-2xl">
                Live credential pool health and per-token spend. Paste your access token(s) below
                to inspect usage — tokens stay in this browser (localStorage) and are sent only to
                query your own records.
              </p>
            </div>
          </div>

          {ovErr && (
            <div className="rounded-md border border-destructive/40 bg-destructive/10 px-4 py-2.5 text-sm text-destructive font-mono">
              {ovErr}
            </div>
          )}
        </header>

        {/* OVERVIEW METRICS */}
        <section className="stagger">
          <div className="hud-strip">
            <div className="hud-strip-grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-6">
              <Metric
                label="Credentials"
                value={ov ? ov.counts.healthy : "···"}
                unit={ov ? `/ ${ov.counts.total}` : undefined}
                accent
              />
              <Metric label="OAuth" value={ov ? fmtInt(ov.counts.oauth) : "···"} />
              <Metric label="API keys" value={ov ? fmtInt(ov.counts.apikey) : "···"} />
              <Metric label="Models" value={ov ? fmtInt(ov.counts.models) : "···"} />
              <Metric label="24h req" value={ov ? fmtInt(ov.window_24h.requests) : "···"} />
              <Metric
                label="24h cost"
                value={ov ? `$${ov.window_24h.cost_usd.toFixed(2)}` : "···"}
              />
            </div>
          </div>
        </section>

        {/* CREDENTIAL POOL */}
        <section className="stagger space-y-4">
          <div className="flex items-baseline justify-between gap-3 flex-wrap">
            <div>
              <div className="eyebrow mb-1.5">§ Pool health</div>
              <h2 className="font-display text-2xl md:text-3xl tracking-tight">
                Credentials <span className="text-muted-foreground">overview</span>
              </h2>
            </div>
            {ov && (
              <span className="eyebrow tabular opacity-70">
                {ov.counts.healthy} healthy · {ov.counts.quota} quota · {ov.counts.unhealthy}{" "}
                unhealthy · {ov.counts.disabled} disabled
              </span>
            )}
          </div>

          {!ov ? (
            <div className="py-12 text-center eyebrow animate-pulse bg-card border border-border-strong rounded-md">
              <span className="opacity-60">Loading…</span>
            </div>
          ) : (ov.auths || []).length === 0 ? (
            <div className="py-10 px-6 text-center text-sm text-muted-foreground font-mono bg-card border border-border-strong rounded-md">
              No credentials configured.
            </div>
          ) : (
            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3 md:gap-4">
              {(ov.auths || []).map((a, i) => (
                <div
                  key={i}
                  className={cn(
                    "bg-card border border-border-strong rounded-md p-4 transition-colors",
                    a.disabled && "opacity-60",
                  )}
                >
                  <div className="flex items-start justify-between gap-2 mb-2">
                    <div className="min-w-0 flex-1">
                      <div className="eyebrow mb-0.5">
                        {a.kind === "oauth" ? "OAuth" : "API key"}
                        {a.group && (
                          <span className="opacity-60"> · {a.group}</span>
                        )}
                      </div>
                      <div className="font-display text-base truncate" title={a.label}>
                        {a.label || <span className="text-muted-foreground">(unnamed)</span>}
                      </div>
                    </div>
                    <HealthBadge row={a} />
                  </div>
                </div>
              ))}
            </div>
          )}
        </section>

        {/* TOKEN QUERY */}
        <section className="stagger space-y-4">
          <div className="flex items-baseline justify-between gap-3 flex-wrap">
            <div>
              <div className="eyebrow mb-1.5">§ Your tokens</div>
              <h2 className="font-display text-2xl md:text-3xl tracking-tight">
                Usage <span className="text-muted-foreground">lookup</span>
              </h2>
            </div>
            <span className="eyebrow tabular opacity-70">
              {tokens.length} saved · batch up to 20 per query
            </span>
          </div>

          <div className="bg-card border border-border-strong rounded-md p-4 md:p-5 space-y-4">
            <form
              onSubmit={(e) => {
                e.preventDefault();
                addTokens(input);
              }}
              className="flex flex-col sm:flex-row gap-2"
            >
              <div className="relative flex-1">
                <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-muted-foreground pointer-events-none" />
                <Input
                  placeholder="Paste token(s) — sk-... — comma / newline / space separated"
                  value={input}
                  onChange={(e) => setInput(e.currentTarget.value)}
                  className="pl-9 font-mono text-xs"
                  autoComplete="off"
                  spellCheck={false}
                />
              </div>
              <div className="flex gap-2">
                <Button type="submit" className="gap-2" disabled={!input.trim()}>
                  <Plus className="h-4 w-4" /> Add
                </Button>
                <Button
                  type="button"
                  variant="outline"
                  onClick={() => runQuery(tokens)}
                  disabled={busy || tokens.length === 0}
                  className="gap-2"
                >
                  {busy ? (
                    <Loader2 className="h-4 w-4 animate-spin" />
                  ) : (
                    <Activity className="h-4 w-4" />
                  )}
                  <span className="hidden sm:inline">Query</span>
                </Button>
                {tokens.length > 0 && (
                  <Button
                    type="button"
                    variant="outline"
                    onClick={clearAll}
                    className="gap-2 border-destructive/40 text-destructive hover:bg-destructive/10"
                  >
                    <Trash2 className="h-4 w-4" />
                  </Button>
                )}
              </div>
            </form>

            {tokens.length > 0 && (
              <div className="flex flex-wrap gap-1.5">
                {tokens.map((t) => {
                  const m = mask(t);
                  const r = byMasked.get(m);
                  return (
                    <button
                      key={t}
                      onClick={() => removeToken(t)}
                      title="Click to remove"
                      className={cn(
                        "group inline-flex items-center gap-2 pl-2.5 pr-1.5 py-1 rounded-sm border text-xs mono transition-colors",
                        r && r.found
                          ? "border-border bg-muted/40 hover:border-destructive/40"
                          : r && !r.found
                            ? "border-destructive/40 bg-destructive/5 text-destructive"
                            : "border-border bg-muted/40 hover:border-destructive/40",
                      )}
                    >
                      <span className="tabular">{m}</span>
                      {r && !r.found && (
                        <span className="text-[10px] uppercase tracking-wider opacity-80">
                          invalid
                        </span>
                      )}
                      <Trash2 className="h-3 w-3 opacity-50 group-hover:opacity-100" />
                    </button>
                  );
                })}
              </div>
            )}
          </div>

          {/* RESULT CARDS */}
          {results && results.length > 0 && (
            <div className="space-y-4 md:space-y-5">
              {tokens.map((t) => {
                const r = byMasked.get(mask(t));
                if (!r) return null;
                return <TokenCard key={t} r={r} fullToken={t} />;
              })}
            </div>
          )}
        </section>

        <footer className="pt-8 mt-6 border-t border-border eyebrow flex justify-between items-center flex-wrap gap-2">
          <span>tokens stored locally · public endpoint · no secrets collected</span>
          <span className="opacity-60">CPA · Claude / {new Date().getFullYear()}</span>
        </footer>
      </div>
    </div>
  );
}

const LEDGER_PAGE_SIZE = 50;

function TokenCard({ r, fullToken }: { r: StatusTokenResult; fullToken: string }) {
  if (!r.found) {
    return (
      <div className="bg-card border border-destructive/40 rounded-md p-4 md:p-5">
        <div className="flex items-center gap-2 mb-2">
          <AlertTriangle className="h-4 w-4 text-destructive" />
          <span className="mono text-sm text-destructive">{r.masked}</span>
        </div>
        <p className="text-xs text-muted-foreground">
          Token not found. It may be invalid, deleted, or a typo.
        </p>
      </div>
    );
  }
  const ratio = r.weekly_limit > 0 ? r.weekly_used_usd / r.weekly_limit : 0;
  const daily = r.daily || [];
  return (
    <div
      className={cn(
        "bg-card border border-border-strong rounded-md p-4 md:p-5 space-y-4",
        r.blocked && "bg-destructive/5",
      )}
    >
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0 flex-1">
          <div className="eyebrow mb-0.5">
            {r.masked}
            {r.group && <span className="opacity-60"> · {r.group}</span>}
          </div>
          <div className="font-display text-lg md:text-xl tracking-tight truncate">
            {r.name || <span className="text-muted-foreground">(unnamed)</span>}
          </div>
        </div>
        {r.blocked ? (
          <Badge variant="destructive" className="gap-1">
            <XCircle className="h-3 w-3" /> blocked
          </Badge>
        ) : r.weekly_limit > 0 && ratio > 0.8 ? (
          <Badge
            className="gap-1"
            style={{
              background: "color-mix(in oklab, var(--warning) 15%, transparent)",
              color: "var(--warning)",
              borderColor: "color-mix(in oklab, var(--warning) 40%, transparent)",
            }}
          >
            <Gauge className="h-3 w-3" /> near limit
          </Badge>
        ) : (
          <Badge
            className="gap-1"
            style={{
              background: "color-mix(in oklab, var(--success) 15%, transparent)",
              color: "var(--success)",
              borderColor: "color-mix(in oklab, var(--success) 40%, transparent)",
            }}
          >
            <CheckCircle2 className="h-3 w-3" /> ok
          </Badge>
        )}
      </div>

      {/* KPI grid */}
      <div className="grid grid-cols-3 gap-2 text-center">
        <div className="border border-border rounded-sm py-2 px-1">
          <div className="eyebrow mb-1">Week</div>
          <div className="mono text-sm font-medium tabular">
            ${r.weekly_used_usd.toFixed(4)}
          </div>
          {r.weekly_limit > 0 && (
            <div className="mt-1.5 h-1 w-full bg-muted rounded-full overflow-hidden">
              <div
                className="h-full transition-all"
                style={{
                  width: `${Math.min(100, Math.round(ratio * 100))}%`,
                  background: r.blocked
                    ? "var(--destructive)"
                    : ratio > 0.8
                      ? "var(--warning)"
                      : "var(--success)",
                }}
              />
            </div>
          )}
          {r.weekly_limit > 0 && (
            <div className="text-[10px] mono opacity-60 mt-1">
              limit ${r.weekly_limit.toFixed(2)}
            </div>
          )}
        </div>
        <div className="border border-border rounded-sm py-2 px-1">
          <div className="eyebrow mb-1">Lifetime</div>
          <div className="mono text-sm font-medium tabular">
            ${r.total.cost_usd.toFixed(4)}
          </div>
          <div className="text-[10px] mono opacity-60 mt-1">
            {fmtInt(r.total.requests)} req
          </div>
        </div>
        <div className="border border-border rounded-sm py-2 px-1">
          <div className="eyebrow mb-1">24h</div>
          <div className="mono text-sm font-medium tabular">
            {r.window_24h ? `$${r.window_24h.cost_usd.toFixed(4)}` : "$0"}
          </div>
          <div className="text-[10px] mono opacity-60 mt-1">
            {r.window_24h ? fmtInt(r.window_24h.count) : 0} req
          </div>
        </div>
      </div>

      {/* Daily cost line chart */}
      {daily.length > 0 && daily.some((d) => d.cost_usd > 0) && (
        <div>
          <div className="flex items-baseline justify-between mb-2 gap-2">
            <div className="eyebrow opacity-80">
              Daily spend · last {daily.length}d
            </div>
            <span className="eyebrow tabular opacity-60">
              ${daily.reduce((s, d) => s + d.cost_usd, 0).toFixed(4)} ·{" "}
              {fmtInt(daily.reduce((s, d) => s + d.requests, 0))} req
            </span>
          </div>
          <ChartContainer
            config={dailyCostConfig}
            className="h-[140px] md:h-[170px] aspect-auto w-full"
          >
            <LineChart data={daily} margin={{ top: 6, right: 6, left: -14, bottom: 0 }}>
              <CartesianGrid strokeDasharray="3 3" vertical={false} />
              <XAxis
                dataKey="date"
                tickLine={false}
                axisLine={false}
                tickMargin={6}
                tickFormatter={(s: string) => s.slice(5).replace("-", "/")}
                minTickGap={16}
              />
              <YAxis
                tickLine={false}
                axisLine={false}
                width={44}
                tickFormatter={(v: number) => (v < 1 ? `$${v.toFixed(2)}` : `$${Math.round(v)}`)}
              />
              <ChartTooltip
                cursor={{ stroke: "var(--border)" }}
                content={
                  <ChartTooltipContent
                    indicator="dot"
                    labelFormatter={(v) => `Day · ${v}`}
                    valueFormatter={(v) =>
                      typeof v === "number" ? `$${v.toFixed(4)}` : String(v)
                    }
                  />
                }
              />
              <Line
                type="monotone"
                dataKey="cost_usd"
                stroke="var(--color-cost_usd)"
                strokeWidth={2.5}
                dot={{ r: 3, strokeWidth: 0, fill: "var(--color-cost_usd)" }}
                activeDot={{ r: 6 }}
              />
            </LineChart>
          </ChartContainer>
        </div>
      )}

      {/* Detailed request ledger with pagination */}
      <TokenLedger r={r} fullToken={fullToken} />

      {r.last_used && !(r.recent && r.recent.length > 0) && (
        <div className="text-xs mono opacity-60">
          last used · {fmtDate(r.last_used)}
        </div>
      )}
    </div>
  );
}

function TokenLedger({ r, fullToken }: { r: StatusTokenResult; fullToken: string }) {
  // initialEntries = the first page that came back with the overview /query.
  // As soon as the user pages beyond 0, switch to /history results.
  const initialEntries = r.recent || [];
  const initialTotal = r.recent_total || initialEntries.length;
  const [offset, setOffset] = useState(0);
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [filterActive, setFilterActive] = useState(false);
  const [page, setPage] = useState<{
    entries: StatusRecent[];
    total: number;
    limit: number;
    loading: boolean;
    err: string;
  }>({
    entries: initialEntries,
    total: initialTotal,
    limit: LEDGER_PAGE_SIZE,
    loading: false,
    err: "",
  });

  const fetchPage = useCallback(
    async (nextOffset: number, f: string, t: string) => {
      setPage((p) => ({ ...p, loading: true, err: "" }));
      try {
        const d = await queryStatusHistory({
          token: fullToken,
          offset: nextOffset,
          limit: LEDGER_PAGE_SIZE,
          from: f || undefined,
          to: t || undefined,
        });
        setPage({
          entries: d.entries,
          total: d.total,
          limit: d.limit || LEDGER_PAGE_SIZE,
          loading: false,
          err: "",
        });
      } catch (e: any) {
        setPage((p) => ({ ...p, loading: false, err: e.message || String(e) }));
      }
    },
    [fullToken],
  );

  // Initial page came free from /query; subsequent navigation calls /history.
  const goto = (nextOffset: number) => {
    setOffset(nextOffset);
    if (nextOffset === 0 && !filterActive) {
      setPage({
        entries: initialEntries,
        total: initialTotal,
        limit: LEDGER_PAGE_SIZE,
        loading: false,
        err: "",
      });
    } else {
      fetchPage(nextOffset, from, to);
    }
  };

  const applyFilter = () => {
    setFilterActive(Boolean(from || to));
    setOffset(0);
    fetchPage(0, from, to);
  };
  const clearFilter = () => {
    setFrom("");
    setTo("");
    setFilterActive(false);
    setOffset(0);
    setPage({
      entries: initialEntries,
      total: initialTotal,
      limit: LEDGER_PAGE_SIZE,
      loading: false,
      err: "",
    });
  };

  const entries = page.entries;
  const total = page.total;
  const limit = page.limit;
  const pageIdx = Math.floor(offset / limit);
  const pageCount = Math.max(1, Math.ceil(total / limit));
  const firstVisible = total === 0 ? 0 : offset + 1;
  const lastVisible = Math.min(offset + entries.length, total);

  if (!entries.length && !filterActive && total === 0) {
    return (
      <div className="text-xs mono opacity-60 border border-dashed border-border rounded-sm py-4 text-center">
        no requests yet
      </div>
    );
  }

  return (
    <div>
      <div className="flex flex-wrap items-end justify-between gap-2 mb-2">
        <div className="flex items-baseline gap-3">
          <div className="eyebrow opacity-80">Request ledger</div>
          <span className="eyebrow tabular opacity-60">
            {firstVisible}–{lastVisible} / {fmtInt(total)} · newest first · UTC
          </span>
        </div>
        <div className="flex flex-wrap items-center gap-1.5 text-xs">
          <Input
            type="date"
            value={from}
            onChange={(e) => setFrom(e.currentTarget.value)}
            className="h-7 w-[140px] text-[11px] mono"
            title="From (UTC, inclusive)"
          />
          <span className="mono opacity-60">→</span>
          <Input
            type="date"
            value={to}
            onChange={(e) => setTo(e.currentTarget.value)}
            className="h-7 w-[140px] text-[11px] mono"
            title="To (UTC, inclusive)"
          />
          <Button
            type="button"
            size="sm"
            variant="outline"
            className="h-7 px-2.5 text-[11px] gap-1"
            onClick={applyFilter}
            disabled={page.loading}
          >
            {page.loading ? <Loader2 className="h-3 w-3 animate-spin" /> : null}
            Apply
          </Button>
          {(filterActive || from || to) && (
            <Button
              type="button"
              size="sm"
              variant="outline"
              className="h-7 px-2 text-[11px]"
              onClick={clearFilter}
            >
              <X className="h-3 w-3" />
            </Button>
          )}
        </div>
      </div>

      {page.err && (
        <div className="mb-2 rounded-sm border border-destructive/40 bg-destructive/10 px-3 py-1.5 text-xs text-destructive mono">
          {page.err}
        </div>
      )}

      <div className="border border-border rounded-sm overflow-hidden">
        <div className={cn("max-h-[520px] overflow-y-auto overflow-x-auto", page.loading && "opacity-60")}>
          <table className="w-full text-xs mono">
            <thead className="bg-muted/40 border-b border-border sticky top-0 z-10">
              <tr className="eyebrow text-right">
                <th className="py-2 px-3 font-[inherit] text-left">Time</th>
                <th className="py-2 px-3 font-[inherit] text-left">Model</th>
                <th className="py-2 px-3 font-[inherit]">In</th>
                <th className="py-2 px-3 font-[inherit]">Out</th>
                <th className="py-2 px-3 font-[inherit]">Cache R</th>
                <th className="py-2 px-3 font-[inherit]">Cache W</th>
                <th className="py-2 px-3 font-[inherit]">Cost</th>
                <th className="py-2 px-3 font-[inherit]">Status</th>
                <th className="py-2 px-3 font-[inherit]">Dur</th>
              </tr>
            </thead>
            <tbody>
              {entries.length === 0 ? (
                <tr>
                  <td colSpan={9} className="py-6 text-center text-muted-foreground">
                    {page.loading ? "loading…" : "no entries in this range"}
                  </td>
                </tr>
              ) : (
                entries.map((e, i) => (
                  <tr
                    key={i}
                    className={cn(
                      "border-b border-border/50 last:border-0 hover:bg-muted/40 transition-colors",
                      e.status >= 400 && "bg-destructive/5",
                    )}
                  >
                    <td className="py-1.5 px-3 tabular opacity-80 whitespace-nowrap">
                      {fmtTableTime(e.ts)}
                    </td>
                    <td className="py-1.5 px-3 whitespace-nowrap" title={e.model}>
                      {e.model || "—"}
                    </td>
                    <td className="py-1.5 px-3 tabular text-right">{fmtInt(e.input_tokens)}</td>
                    <td className="py-1.5 px-3 tabular text-right">{fmtInt(e.output_tokens)}</td>
                    <td className="py-1.5 px-3 tabular text-right opacity-80">
                      {fmtInt(e.cache_read_tokens)}
                    </td>
                    <td className="py-1.5 px-3 tabular text-right opacity-80">
                      {fmtInt(e.cache_create_tokens)}
                    </td>
                    <td className="py-1.5 px-3 tabular text-right font-medium">
                      ${e.cost_usd.toFixed(4)}
                    </td>
                    <td
                      className={cn(
                        "py-1.5 px-3 tabular text-right",
                        e.status >= 400 && "text-destructive font-medium",
                      )}
                    >
                      {e.status}
                    </td>
                    <td className="py-1.5 px-3 tabular text-right opacity-80 whitespace-nowrap">
                      {fmtDur(e.duration_ms)}
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </div>

      {/* Pager */}
      {pageCount > 1 && (
        <div className="mt-2 flex items-center justify-between gap-2 text-xs mono">
          <span className="opacity-60 tabular">
            page {pageIdx + 1} / {pageCount}
          </span>
          <div className="flex items-center gap-1">
            <Button
              type="button"
              size="sm"
              variant="outline"
              className="h-7 px-2"
              onClick={() => goto(0)}
              disabled={pageIdx === 0 || page.loading}
              title="First"
            >
              «
            </Button>
            <Button
              type="button"
              size="sm"
              variant="outline"
              className="h-7 px-2"
              onClick={() => goto(Math.max(0, offset - limit))}
              disabled={pageIdx === 0 || page.loading}
            >
              ‹ Prev
            </Button>
            <Button
              type="button"
              size="sm"
              variant="outline"
              className="h-7 px-2"
              onClick={() => goto(offset + limit)}
              disabled={pageIdx + 1 >= pageCount || page.loading}
            >
              Next ›
            </Button>
            <Button
              type="button"
              size="sm"
              variant="outline"
              className="h-7 px-2"
              onClick={() => goto((pageCount - 1) * limit)}
              disabled={pageIdx + 1 >= pageCount || page.loading}
              title="Last"
            >
              »
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}

// "YYYY-MM-DD HH:MM:SS" in local time, compact.
function fmtTableTime(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}

function fmtDur(ms: number): string {
  if (ms < 1000) return `${ms}ms`;
  return `${(ms / 1000).toFixed(1)}s`;
}

const dailyCostConfig: ChartConfig = {
  cost_usd: {
    label: "Cost (USD)",
    theme: {
      light: "oklch(0.45 0.16 155)",
      dark: "oklch(0.8 0.16 145)",
    },
  },
};
