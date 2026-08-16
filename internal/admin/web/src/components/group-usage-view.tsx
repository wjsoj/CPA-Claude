import { useCallback, useEffect, useRef, useState } from "react";
import { AlertTriangle, BarChart3, Loader2, RefreshCw } from "lucide-react";
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
import { rangeProblem, trailingDays } from "@/lib/date-range";
import { ApiError } from "@/lib/api";
import type { GroupUsage } from "@/lib/team-api";
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
// bodies, different auth — render through one component.

const usd = (n: number) => `$${(n || 0).toFixed(4)}`;
const int = (n: number) => (n || 0).toLocaleString("zh-CN");
const pct = (part: number, whole: number) =>
  whole > 0 ? `${((part / whole) * 100).toFixed(1)}%` : "—";

const dailySpendConfig: ChartConfig = {
  billed_usd: {
    label: "消费 (USD)",
    theme: { light: "oklch(0.45 0.16 155)", dark: "oklch(0.8 0.16 145)" },
  },
};

export function GroupUsageView({
  load,
  defaultDays = 30,
  className,
}: {
  load: (from: string, to: string) => Promise<GroupUsage>;
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
          <MemberUsageTable data={shown} />
          <ModelUsageTable data={shown} />
        </div>
      )}

      {shown && total && total.requests === 0 && (
        <p className="text-xs text-muted-foreground">该区间内没有计费请求。</p>
      )}
    </section>
  );
}

function MemberUsageTable({ data }: { data: GroupUsage }) {
  const whole = data.total.billed_usd;
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
          {data.by_member.map((m) => (
            <TableRow key={m.masked}>
              {/* nowrap so a narrow screen scrolls the table rather than
                  hyphenating a masked token down four lines. */}
              <TableCell className="whitespace-nowrap">
                <div className="flex items-center gap-1.5">
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
          ))}
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
