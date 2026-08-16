import { Fragment, useCallback, useEffect, useRef, useState } from "react";
import { AlertTriangle, BarChart3, ChevronDown, ChevronRight, Loader2, RefreshCw } from "lucide-react";
import { CartesianGrid, Line, LineChart, XAxis, YAxis } from "recharts";
import { Button } from "./ui/button";
import { Badge } from "./ui/badge";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "./ui/table";
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "./ui/chart";
import { DateRangeRow } from "./date-range-row";
import { formatTimestampIn, rangeProblem, trailingDays } from "@/lib/date-range";
import { ApiError } from "@/lib/api";
import type { GroupUsage, TeamRequestRow, TeamRequestsResp } from "@/lib/team-api";
import { cn } from "@/lib/utils";

// The group's real consumption over a date range, split by member / model / day.
//
// The numbers come from the request log rather than from the pool ledger, which
// is the whole point: a member who blew through their daily cap keeps working
// out of their own wallet, and a team that never funded a pool spends entirely
// out of personal wallets. Neither reaches workspace_tx, so a pool-only view
// reports the heaviest users as the lightest.
//
// `load` is injected so the group admin's console (/api/team/usage) and the
// operator panel (/mgmt-console/api/workspaces/:id/usage) — identical response
// bodies, different auth — render through one component. `loadRequests` is the
// same arrangement for the per-member drill-down, and optional: the operator
// panel has no itemised endpoint, and where it is absent the member rows simply
// don't expand.

const usd = (n: number) => `$${(n || 0).toFixed(4)}`;
const cny = (n: number) =>
  `¥${(n || 0).toLocaleString("zh-CN", { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;
const int = (n: number) => (n || 0).toLocaleString("zh-CN");
const pct = (part: number, whole: number) =>
  whole > 0 ? `${((part / whole) * 100).toFixed(1)}%` : "—";

export type GroupRequestLoader = (args: {
  from: string;
  to: string;
  member: string;
}) => Promise<TeamRequestsResp>;

const dailySpendConfig: ChartConfig = {
  billed_usd: {
    label: "消费 (USD)",
    theme: { light: "oklch(0.45 0.16 155)", dark: "oklch(0.8 0.16 145)" },
  },
};

export function GroupUsageView({
  load,
  loadRequests,
  defaultDays = 30,
  className,
}: {
  load: (from: string, to: string) => Promise<GroupUsage>;
  loadRequests?: GroupRequestLoader;
  defaultDays?: number;
  className?: string;
}) {
  const initial = useRef(trailingDays(defaultDays)).current;
  const [from, setFrom] = useState(initial.from);
  const [to, setTo] = useState(initial.to);
  const [data, setData] = useState<GroupUsage | null>(null);
  const [loading, setLoading] = useState(false);
  const [err, setErr] = useState("");

  // seq guards against an out-of-order response overwriting a newer one when
  // the user clicks through presets quickly.
  const seq = useRef(0);
  const refresh = useCallback(
    async (f: string, t: string) => {
      const mine = ++seq.current;
      setLoading(true);
      try {
        const r = await load(f, t);
        if (seq.current !== mine) return;
        setData(r);
        setErr("");
      } catch (e) {
        if (seq.current !== mine) return;
        setErr(e instanceof ApiError ? e.message : String(e));
        // Drop the stale numbers with the failed range: the figures on screen
        // belong to the previous window, and leaving them under the new dates
        // reads as an answer to a question that was never answered.
        setData(null);
      } finally {
        if (seq.current === mine) setLoading(false);
      }
    },
    [load],
  );

  const problem = rangeProblem(from, to);

  // Debounced: a date input fires on every keystroke and each query is a scan
  // of the log index.
  useEffect(() => {
    if (!from || !to || problem) return;
    const id = setTimeout(() => void refresh(from, to), 350);
    return () => clearTimeout(id);
  }, [from, to, problem, refresh]);

  // An unusable range hides the previous range's figures for the same reason a
  // failed one drops them: the numbers on screen must always be the answer to
  // the dates on screen.
  const shown = problem ? null : data;
  const total = shown?.total;
  const hasSpend = !!shown && shown.by_day.some((d) => d.billed_usd > 0);

  return (
    <section className={cn("space-y-3 rounded-lg border border-border/60 bg-card/40 p-4", className)}>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <BarChart3 className="h-4 w-4 text-primary" />
          <span className="font-semibold">组用量统计</span>
          <span className="text-xs text-muted-foreground">按请求日志计（含个人钱包支付部分）</span>
        </div>
        <Button
          size="sm"
          variant="outline"
          onClick={() => void refresh(from, to)}
          disabled={loading || !from || !to || !!problem}
        >
          <RefreshCw className={cn("h-3.5 w-3.5", loading && "animate-spin")} />
        </Button>
      </div>

      <DateRangeRow
        from={from}
        to={to}
        onChange={(f, t) => {
          setFrom(f);
          setTo(t);
        }}
        hint={shown ? `区间按 ${shown.timezone} 计，含首尾两日` : undefined}
      />

      {problem ? (
        <div className="rounded-md border border-destructive/40 bg-destructive/5 px-3 py-2 text-sm text-destructive">
          {problem}
        </div>
      ) : (
        err && (
          <div className="rounded-md border border-destructive/40 bg-destructive/5 px-3 py-2 text-sm text-destructive">
            用量统计加载失败：{err}
          </div>
        )
      )}

      {shown?.partial && shown.notes.length > 0 && (
        <div className="rounded-md border border-[color:var(--warning)]/40 bg-[color:var(--warning)]/10 px-3 py-2">
          {shown.notes.map((n, i) => (
            <div
              key={i}
              className="flex items-start gap-1.5 text-[11px] leading-relaxed text-[color:var(--warning)]"
            >
              <AlertTriangle className="mt-0.5 h-3 w-3 shrink-0" />
              <span>{n}</span>
            </div>
          ))}
        </div>
      )}

      {/* No figures at all until a range has actually been answered — a grid of
          zeros under a range nobody measured is indistinguishable from a team
          that spent nothing, which is the exact confusion this view exists to
          remove. */}
      {!shown ? (
        <div className="flex items-center gap-2 py-4 text-xs text-muted-foreground">
          {loading && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
          {loading ? "正在统计…" : problem ? "请调整时间区间。" : "选择时间区间以查看该区间的组消费。"}
        </div>
      ) : (
        <div
          className={cn(
            "grid grid-cols-2 gap-3 md:grid-cols-4",
            loading && "opacity-50 transition-opacity",
          )}
        >
          <Figure label="区间总消费" value={usd(total?.billed_usd || 0)} strong />
          <Figure label="请求数" value={`${int(total?.requests || 0)} 笔`} />
          <Figure
            label="其中组池支付"
            value={usd(shown.pool_billed_usd)}
            sub={pct(shown.pool_billed_usd, total?.billed_usd || 0)}
          />
          <Figure
            label="其中个人钱包支付"
            value={usd(shown.personal_billed_usd)}
            sub={pct(shown.personal_billed_usd, total?.billed_usd || 0)}
          />
        </div>
      )}

      {shown && hasSpend && (
        <div>
          <div className="mb-2 flex items-baseline justify-between gap-2">
            <div className="eyebrow opacity-80">每日消费 · {shown.by_day.length} 天</div>
            <span className="eyebrow tabular opacity-60">{shown.timezone}</span>
          </div>
          <ChartContainer
            config={dailySpendConfig}
            className="aspect-auto h-[140px] w-full md:h-[170px]"
          >
            {/* left: 0 — the axis prints "$0.00" at the origin and a negative
                left margin clips its first character. */}
            <LineChart data={shown.by_day} margin={{ top: 6, right: 6, left: 0, bottom: 0 }}>
              <CartesianGrid strokeDasharray="3 3" vertical={false} />
              <XAxis
                dataKey="day"
                tickLine={false}
                axisLine={false}
                tickMargin={6}
                tickFormatter={(s: string) => s.slice(5).replace("-", "/")}
                minTickGap={16}
              />
              <YAxis
                tickLine={false}
                axisLine={false}
                width={52}
                tickFormatter={(v: number) => (v < 1 ? `$${v.toFixed(2)}` : `$${Math.round(v)}`)}
              />
              <ChartTooltip
                cursor={{ stroke: "var(--border)" }}
                content={
                  <ChartTooltipContent
                    indicator="dot"
                    labelFormatter={(v) => `${v}`}
                    valueFormatter={(v) => (typeof v === "number" ? usd(v) : String(v))}
                  />
                }
              />
              <Line
                type="monotone"
                dataKey="billed_usd"
                stroke="var(--color-billed_usd)"
                strokeWidth={2.5}
                dot={{ r: 3, strokeWidth: 0, fill: "var(--color-billed_usd)" }}
                activeDot={{ r: 6 }}
              />
            </LineChart>
          </ChartContainer>
        </div>
      )}

      {shown && (
        <div className="grid gap-3 lg:grid-cols-2">
          <MemberUsageTable data={shown} loadRequests={loadRequests} />
          <ModelUsageTable data={shown} />
        </div>
      )}

      {shown && total && total.requests === 0 && (
        <p className="text-xs text-muted-foreground">该区间内没有计费请求。</p>
      )}
    </section>
  );
}

function MemberUsageTable({
  data,
  loadRequests,
}: {
  data: GroupUsage;
  loadRequests?: GroupRequestLoader;
}) {
  const whole = data.total.billed_usd;
  // The expanded member is dropped whenever the answered range moves: the rows
  // underneath belong to the old window, and leaving them open under new dates
  // reads as an answer to a question nobody asked.
  const [open, setOpen] = useState("");
  // Fetched rows live here rather than inside the expanded panel, which is
  // conditionally rendered and so loses its state on every collapse. Held at
  // this level, clicking a member shut and open again is free; held below it,
  // each toggle was another /api/team/requests query — the one endpoint in the
  // usage family with no server-side cache in front of it.
  const [fetched, setFetched] = useState<Record<string, MemberRequestRows>>({});
  useEffect(() => {
    setOpen("");
    // Same reasoning as `open`: these rows answer the old range.
    setFetched({});
  }, [data.from, data.to]);
  const remember = useCallback((member: string, rows: MemberRequestRows) => {
    setFetched((prev) => ({ ...prev, [member]: rows }));
  }, []);

  return (
    <div className="overflow-x-auto rounded-lg border border-border/60">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>成员</TableHead>
            <TableHead className="text-right">请求</TableHead>
            <TableHead className="text-right">总消费</TableHead>
            <TableHead className="text-right">池 / 个人</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {data.by_member.map((m) => {
            // A member whose masked token can't be told apart from another's
            // has no rows of its own to show, so there is nothing to drill into.
            const drillable = !!loadRequests && !m.unmeasurable;
            const expanded = open === m.masked;
            return (
            <Fragment key={m.masked}>
            <TableRow
              className={cn(drillable && "cursor-pointer", expanded && "bg-muted/40")}
              onClick={drillable ? () => setOpen(expanded ? "" : m.masked) : undefined}
            >
              {/* nowrap so a narrow screen scrolls the table rather than
                  hyphenating a masked token down four lines. */}
              <TableCell className="whitespace-nowrap">
                <div className="flex items-center gap-1.5">
                  {drillable &&
                    (expanded ? (
                      <ChevronDown className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                    ) : (
                      <ChevronRight className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                    ))}
                  <span className="font-mono text-xs">{m.masked}</span>
                  {m.role === "admin" && (
                    <Badge variant="secondary" className="px-1 py-0 text-[10px]">
                      管理员
                    </Badge>
                  )}
                </div>
                {m.label && <div className="text-xs text-muted-foreground">{m.label}</div>}
              </TableCell>
              <TableCell className="text-right font-mono text-xs text-muted-foreground">
                {m.unmeasurable ? "—" : int(m.requests)}
              </TableCell>
              <TableCell className="text-right font-mono text-xs">
                {m.unmeasurable ? (
                  <span className="text-muted-foreground" title="令牌过短，脱敏后无法在请求日志中区分">
                    无法统计
                  </span>
                ) : (
                  <>
                    {usd(m.billed_usd)}
                    <div className="text-[10px] opacity-60">{pct(m.billed_usd, whole)}</div>
                  </>
                )}
              </TableCell>
              <TableCell className="text-right font-mono text-xs text-muted-foreground">
                {usd(m.pool_billed_usd)}
                <div className="text-[10px] opacity-80">{usd(m.personal_billed_usd)}</div>
              </TableCell>
            </TableRow>
            {expanded && loadRequests && (
              <TableRow className="hover:bg-transparent">
                <TableCell colSpan={4} className="bg-muted/20 p-0">
                  <MemberRequests
                    load={loadRequests}
                    member={m.masked}
                    from={data.from}
                    to={data.to}
                    cached={fetched[m.masked]}
                    onLoaded={remember}
                    fallbackZone={data.timezone}
                  />
                </TableCell>
              </TableRow>
            )}
            </Fragment>
            );
          })}
          {data.by_member.length === 0 && (
            <TableRow>
              <TableCell colSpan={4} className="text-center text-sm text-muted-foreground">
                暂无成员
              </TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>
      <p className="px-3 py-2 text-[11px] leading-relaxed text-muted-foreground">
        「池」来自组池流水，「个人」是总消费减去池消费的推算值——两者出自不同账本，边界处可能有微小出入。
      </p>
    </div>
  );
}

interface MemberRequestRows {
  rows: TeamRequestRow[];
  truncated: boolean;
  /** Zone the server cut the day window on; row times are printed in it. */
  timezone: string;
}

// One member's requests, one row each, over the range the view above is
// already showing — the range is never picked twice.
//
// This is a drill-down, not a second set of figures: the server caps the rows
// and says so with `truncated`, so the amounts here are not meant to add up to
// the member's total in the table above.
//
// The rows themselves are owned by MemberUsageTable; this component only asks
// for them when they aren't there yet.
function MemberRequests({
  load,
  member,
  from,
  to,
  cached,
  onLoaded,
  fallbackZone,
}: {
  load: GroupRequestLoader;
  member: string;
  from: string;
  to: string;
  cached?: MemberRequestRows;
  onLoaded: (member: string, rows: MemberRequestRows) => void;
  fallbackZone: string;
}) {
  // Starts loading unless the rows are already in hand, so the first render of
  // a fresh expand shows the spinner rather than a flash of "no requests".
  const [loading, setLoading] = useState(!cached);
  const [err, setErr] = useState("");

  useEffect(() => {
    // Already answered for this range — a re-expand must not re-query.
    if (cached) {
      setLoading(false);
      setErr("");
      return;
    }
    let live = true;
    setLoading(true);
    setErr("");
    load({ from, to, member })
      .then((r) => {
        if (!live) return;
        onLoaded(member, {
          rows: r.requests || [],
          truncated: !!r.truncated,
          timezone: r.timezone || fallbackZone,
        });
      })
      .catch((e) => {
        if (!live) return;
        // Deliberately not cached: a failure should be retried on re-expand.
        setErr(e instanceof ApiError ? e.message : String(e));
      })
      .finally(() => {
        if (live) setLoading(false);
      });
    return () => {
      live = false;
    };
  }, [load, member, from, to, cached, onLoaded, fallbackZone]);

  const rows = cached?.rows ?? null;
  const truncated = !!cached?.truncated;
  const zone = cached?.timezone || fallbackZone;

  if (loading && !rows) {
    return (
      <div className="flex items-center gap-2 px-3 py-3 text-xs text-muted-foreground">
        <Loader2 className="h-3.5 w-3.5 animate-spin" />
        正在拉取逐笔请求…
      </div>
    );
  }
  if (err) {
    return <div className="px-3 py-3 text-xs text-destructive">逐笔请求加载失败：{err}</div>;
  }
  if (!rows || rows.length === 0) {
    return (
      <div className="px-3 py-3 text-xs text-muted-foreground">该区间内该成员没有请求记录。</div>
    );
  }

  return (
    <div className={cn("space-y-1.5 px-3 py-2", loading && "opacity-50 transition-opacity")}>
      {/* Capped height: a 200-row list otherwise pushes the model table and
          everything below it off the bottom of the panel. */}
      <div className="max-h-[320px] overflow-y-auto overflow-x-auto rounded-md border border-border/60 bg-background/40">
        <Table>
          <TableHeader>
            <TableRow>
              {/* The zone is spelled out because it is the display zone the
                  range was cut on, not the reader's — see formatTimestampIn. */}
              <TableHead className="h-8 whitespace-nowrap">时间 · {zone}</TableHead>
              <TableHead className="h-8">模型</TableHead>
              <TableHead className="h-8 text-right">状态</TableHead>
              <TableHead className="h-8 text-right">输入 / 输出</TableHead>
              <TableHead className="h-8 text-right">金额</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((r, i) => (
              <TableRow key={`${r.ts}-${i}`}>
                <TableCell className="whitespace-nowrap py-1.5 font-mono text-xs text-muted-foreground">
                  {formatTimestampIn(r.ts, zone)}
                </TableCell>
                <TableCell className="py-1.5 font-mono text-xs">{r.model || "—"}</TableCell>
                <TableCell className="py-1.5 text-right font-mono text-xs">
                  <span className={cn(r.status >= 400 && "text-destructive")}>{r.status || "—"}</span>
                </TableCell>
                <TableCell className="whitespace-nowrap py-1.5 text-right font-mono text-xs text-muted-foreground">
                  {int(r.input_tokens)} / {int(r.output_tokens)}
                </TableCell>
                <TableCell className="whitespace-nowrap py-1.5 text-right font-mono text-xs">
                  {cny(r.billed_cny)}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
      <p className="text-[11px] leading-relaxed text-muted-foreground">
        {truncated
          ? `仅显示最近 ${int(rows.length)} 笔，区间内更早的请求未列出——金额请以上方汇总为准。`
          : `共 ${int(rows.length)} 笔，按时间倒序。`}
      </p>
    </div>
  );
}

function ModelUsageTable({ data }: { data: GroupUsage }) {
  const whole = data.total.billed_usd;
  return (
    <div className="overflow-x-auto rounded-lg border border-border/60">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>模型</TableHead>
            <TableHead className="text-right">请求</TableHead>
            <TableHead className="text-right">消费</TableHead>
            <TableHead className="text-right">占比</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {data.by_model.map((m) => (
            <TableRow key={m.model}>
              <TableCell className="font-mono text-xs">{m.model || "—"}</TableCell>
              <TableCell className="text-right font-mono text-xs text-muted-foreground">
                {int(m.requests)}
              </TableCell>
              <TableCell className="text-right font-mono text-xs">{usd(m.billed_usd)}</TableCell>
              <TableCell className="text-right font-mono text-xs text-muted-foreground">
                {pct(m.billed_usd, whole)}
              </TableCell>
            </TableRow>
          ))}
          {data.by_model.length === 0 && (
            <TableRow>
              <TableCell colSpan={4} className="text-center text-sm text-muted-foreground">
                该区间内没有模型用量
              </TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>
    </div>
  );
}

function Figure({
  label,
  value,
  sub,
  strong,
}: {
  label: string;
  value: string;
  sub?: string;
  strong?: boolean;
}) {
  return (
    <div className="min-w-0 rounded-md border border-border/60 bg-background/40 px-3 py-2">
      <div className="truncate text-[11px] opacity-60" title={label}>
        {label}
      </div>
      <div
        className={cn(
          "mt-0.5 font-display tracking-tight tabular",
          strong ? "text-lg" : "text-base",
        )}
      >
        {value}
      </div>
      {sub && <div className="mono text-[10px] tabular opacity-50">{sub}</div>}
    </div>
  );
}
