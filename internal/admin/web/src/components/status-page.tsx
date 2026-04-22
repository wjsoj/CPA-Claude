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
  XCircle,
} from "lucide-react";
import {
  loadStatusOverview,
  loadSavedTokens,
  queryStatusTokens,
  saveSavedTokens,
  type StatusOverview,
  type StatusTokenResult,
} from "@/lib/status-api";
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
    <div className="relative px-4 py-4 md:px-5 md:py-5 bg-card overflow-hidden group">
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
      <div
        aria-hidden
        className="absolute right-0 top-0 h-5 w-5 border-t border-r border-border-strong opacity-50 group-hover:border-primary group-hover:opacity-100 transition-all"
      />
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
          <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-6 border border-border-strong rounded-md overflow-hidden [&>*]:border-r [&>*]:border-b [&>*]:border-border [&>*]:-mr-px [&>*]:-mb-px">
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
            <div className="grid grid-cols-1 lg:grid-cols-2 gap-4 md:gap-5">
              {results.map((r) => (
                <TokenCard key={r.masked} r={r} />
              ))}
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

function TokenCard({ r }: { r: StatusTokenResult }) {
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
  const weeks = r.weekly || [];
  const maxWeek = Math.max(1e-9, ...weeks.map((w) => w.cost.cost_usd));
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

      {/* Weekly sparkline */}
      {weeks.length > 0 && (
        <div>
          <div className="eyebrow mb-2 opacity-80">
            Last {weeks.length} week(s)
          </div>
          <div className="flex items-end gap-1 h-12">
            {weeks.map((w) => (
              <div
                key={w.week}
                className="flex-1 bg-primary/70 hover:bg-primary transition-colors rounded-sm min-h-[2px]"
                style={{
                  height: `${Math.max(4, Math.round((w.cost.cost_usd / maxWeek) * 100))}%`,
                }}
                title={`${w.week} · $${w.cost.cost_usd.toFixed(4)} · ${fmtInt(w.cost.requests)} req`}
              />
            ))}
          </div>
        </div>
      )}

      {/* Recent requests */}
      {r.recent && r.recent.length > 0 && (
        <div>
          <div className="eyebrow mb-2 opacity-80">
            Recent · last {r.recent.length}
          </div>
          <div className="max-h-56 overflow-y-auto -mr-1 pr-1 space-y-1">
            {r.recent.map((e, i) => (
              <div
                key={i}
                className="flex items-baseline justify-between gap-2 text-xs mono py-1 border-b border-border/50 last:border-0"
              >
                <span className="truncate opacity-70 flex-1" title={e.model}>
                  {e.model || "—"}
                </span>
                <span
                  className={cn(
                    "tabular shrink-0",
                    e.status >= 400 && "text-destructive",
                  )}
                >
                  {e.status}
                </span>
                <span className="tabular shrink-0 w-16 text-right">
                  ${e.cost_usd.toFixed(4)}
                </span>
                <span className="tabular shrink-0 opacity-60 w-24 text-right">
                  {fmtDate(e.ts)}
                </span>
              </div>
            ))}
          </div>
        </div>
      )}

      {r.last_used && !(r.recent && r.recent.length > 0) && (
        <div className="text-xs mono opacity-60">
          last used · {fmtDate(r.last_used)}
        </div>
      )}
    </div>
  );
}
