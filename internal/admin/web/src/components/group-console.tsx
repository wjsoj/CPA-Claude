// GroupConsole — the group admin's own tab on /status.
//
// It replaces a <details> that used to hang off the token-lookup result, where
// every team feature was stacked in one scroll behind a disclosure triangle
// most admins never opened. The console is reached the same way as the wallet
// (the active token in localStorage), and is offered only when that token's
// /api/wallet/balance reports role=admin — the same flag /api/team/* checks, so
// the tab can never appear for someone the API will refuse.
//
// Layout: one identity header, a KPI strip that answers "is the group funded,
// what did it spend, who is in it, what can we invoice", then sub-tabs for the
// working surfaces. The overview reads the SAME trailing-30-day usage query the
// 用量 tab starts from, so the headline figure and the chart under it can never
// disagree.
import { useCallback, useEffect, useMemo, useState } from "react";
import {
  AlertTriangle,
  BarChart3,
  FileText,
  Receipt,
  RefreshCw,
  Users,
  Wallet,
  LayoutDashboard,
} from "lucide-react";
import { CartesianGrid, Line, LineChart, XAxis, YAxis } from "recharts";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "@/components/ui/chart";
import { ApiError } from "@/lib/api";
import {
  teamMe,
  teamMembers,
  teamLedger,
  teamInvoiceSummary,
  teamUsage,
  teamRequests,
  type GroupUsage,
  type TeamInvoiceSummary,
  type TeamLedgerRow,
  type TeamMe,
  type TeamMember,
} from "@/lib/team-api";
import { trailingDays } from "@/lib/date-range";
import { GroupUsageView } from "@/components/group-usage-view";
import { TeamStatementDialog } from "@/components/team-statement-dialog";
import {
  MembersSection,
  PoolSection,
  SectionHead,
  TeamInvoiceSection,
} from "@/components/group-sections";
import { fmtCNY as cny } from "@/components/invoice-common";
import { cn } from "@/lib/utils";

const usd2 = (n: number) => `$${(n || 0).toFixed(2)}`;
const usd4 = (n: number) => `$${(n || 0).toFixed(4)}`;
const int = (n: number) => (n || 0).toLocaleString("zh-CN");

const OVERVIEW_DAYS = 30;

type Sub = "overview" | "members" | "usage" | "invoices" | "pool";

const SUBS: { key: Sub; label: string; icon: typeof Users }[] = [
  { key: "overview", label: "概览", icon: LayoutDashboard },
  { key: "members", label: "成员", icon: Users },
  { key: "usage", label: "用量", icon: BarChart3 },
  { key: "invoices", label: "发票", icon: Receipt },
  { key: "pool", label: "组池", icon: Wallet },
];

export function GroupConsole({ token }: { token: string }) {
  const [me, setMe] = useState<TeamMe | null>(null);
  const [members, setMembers] = useState<TeamMember[]>([]);
  const [spendTZ, setSpendTZ] = useState("");
  // 整份列表的「总消费」是否不可信。逐行已经会显示「暂不可用」，但一眼看去满屏都是
  // 破折号时，读者需要知道这是日志读不到而不是大家真没花钱。
  const [spendPartial, setSpendPartial] = useState(false);
  const [ledger, setLedger] = useState<TeamLedgerRow[]>([]);
  const [invoiceSummary, setInvoiceSummary] = useState<TeamInvoiceSummary | null>(null);
  const [recent, setRecent] = useState<GroupUsage | null>(null);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const [stmtOpen, setStmtOpen] = useState(false);
  const [sub, setSub] = useState<Sub>(() => {
    const s = localStorage.getItem("cpa.status.group.sub");
    return SUBS.some((x) => x.key === s) ? (s as Sub) : "overview";
  });
  useEffect(() => {
    localStorage.setItem("cpa.status.group.sub", sub);
  }, [sub]);

  const load = useCallback(async () => {
    setBusy(true);
    setErr("");
    try {
      // The invoice quota is fetched here, not only by the section that shows
      // it: the KPI strip states it on the overview, and a sub-tab the admin
      // has not opened yet cannot be what supplies it.
      const [m, ms, lg, inv] = await Promise.all([
        teamMe(token),
        teamMembers(token),
        teamLedger(token),
        teamInvoiceSummary(token).catch(() => null),
      ]);
      setMe(m);
      setMembers(ms.members || []);
      setSpendTZ(ms.timezone || "");
      setSpendPartial(!!ms.spend_partial);
      setLedger(lg.ledger || []);
      setInvoiceSummary(inv);
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }, [token]);

  useEffect(() => {
    void load();
  }, [load]);

  // The overview's headline spend. Separate from the roster fetch because it is
  // the one figure that can fail on its own (the request-log index may be shut
  // off) without the console being useless — so it must not take the header and
  // the member list down with it.
  useEffect(() => {
    let cancelled = false;
    const { from, to } = trailingDays(OVERVIEW_DAYS);
    teamUsage(token, from, to)
      .then((u) => {
        if (!cancelled) setRecent(u);
      })
      .catch(() => {
        if (!cancelled) setRecent(null);
      });
    return () => {
      cancelled = true;
    };
  }, [token, busy]);

  // Stable identities so GroupUsageView's debounced effect doesn't re-fire on
  // every render of this console.
  const usageLoader = useCallback(
    (from: string, to: string) => teamUsage(token, from, to),
    [token],
  );
  const requestsLoader = useCallback(
    (args: { from: string; to: string; member: string }) => teamRequests(token, args),
    [token],
  );

  const admins = members.filter((m) => m.role === "admin").length;
  const pool = me?.workspace.balance_usd ?? 0;

  if (err) {
    return (
      <div className="rounded-xl border border-destructive/40 bg-destructive/5 p-4 text-sm text-destructive">
        团队控制台加载失败：{err}
      </div>
    );
  }
  if (!me) {
    return (
      <div className="rounded-xl border border-border-strong bg-card/40 py-16 text-center eyebrow animate-pulse opacity-60">
        Loading group…
      </div>
    );
  }

  return (
    <div className="space-y-8">
      {/* IDENTITY — who this console is for, and the two things an admin
          reaches for that are not a sub-tab of their own. */}
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div className="min-w-0">
          <div className="eyebrow mb-1.5 flex items-center gap-2 opacity-70">
            <Users className="h-3.5 w-3.5" />§ Group console
          </div>
          <h2 className="font-display text-2xl tracking-tight md:text-3xl lg:text-4xl">
            {me.workspace.name}
          </h2>
          <div className="mt-2 flex flex-wrap items-center gap-2">
            <Badge variant="outline" className="font-mono text-[10px]">
              #{me.workspace.id}
            </Badge>
            <Badge className="text-[10px]">组管理员</Badge>
            <span className="eyebrow tabular opacity-60">
              {members.length} 成员 · {admins} 管理员
            </span>
            {me.workspace.disabled && <Badge variant="destructive">已禁用</Badge>}
          </div>
        </div>
        <div className="flex items-center gap-2">
          <Button size="sm" variant="outline" onClick={() => setStmtOpen(true)} className="gap-1.5">
            <FileText className="h-3.5 w-3.5" />
            导出对账单
          </Button>
          <Button size="sm" variant="outline" onClick={() => void load()} disabled={busy}>
            <RefreshCw className={cn("h-3.5 w-3.5", busy && "animate-spin")} />
          </Button>
        </div>
      </div>

      {me.workspace.disabled && (
        <div className="flex items-start gap-2 rounded-xl border border-destructive/40 bg-destructive/5 px-4 py-3 text-sm text-destructive">
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
          <span>
            本组已被停用：组池不再为任何成员付费，成员的请求会全部回落到各自的个人余额。
          </span>
        </div>
      )}

      {/* KPI STRIP */}
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <Kpi
          label="组共享池"
          value={usd2(pool)}
          foot={pool > 0 ? "全组共用，按成员份额扣减" : "未充值 · 各成员走个人余额"}
          tone={pool > 0 ? "primary" : "muted"}
          onClick={() => setSub("pool")}
        />
        <Kpi
          label={`近 ${OVERVIEW_DAYS} 天团队消费`}
          value={recent ? usd2(recent.total.billed_usd) : "—"}
          foot={recent ? `${int(recent.total.requests)} 笔请求` : "用量暂不可用"}
          onClick={() => setSub("usage")}
        />
        <Kpi
          label="成员"
          value={String(members.length)}
          foot={`${admins} 位管理员 · ${members.length - admins} 位成员`}
          onClick={() => setSub("members")}
        />
        <Kpi
          label="可开票额度"
          value={invoiceSummary ? cny(invoiceSummary.total.available_cny) : "—"}
          foot={invoiceSummary ? `累计实付 ${cny(invoiceSummary.total.paid_cny)}` : "额度暂不可用"}
          tone={invoiceSummary && invoiceSummary.total.available_cny > 0 ? "primary" : undefined}
          onClick={() => setSub("invoices")}
        />
      </div>

      {/* SUB-NAV */}
      <div className="flex flex-wrap gap-1 rounded-xl border border-border-strong bg-card/40 p-1">
        {SUBS.map(({ key, label, icon: Icon }) => {
          const active = sub === key;
          return (
            <button
              key={key}
              type="button"
              onClick={() => setSub(key)}
              aria-current={active}
              className={cn(
                "flex items-center gap-1.5 rounded-lg px-3.5 py-2 text-sm transition-colors cursor-pointer",
                active
                  ? "bg-primary/15 text-primary font-medium"
                  : "text-muted-foreground hover:bg-muted/60 hover:text-foreground",
              )}
            >
              <Icon className="h-3.5 w-3.5" />
              {label}
            </button>
          );
        })}
      </div>

      {sub === "overview" && (
        <Overview usage={recent} members={members} onSeeUsage={() => setSub("usage")} />
      )}

      {sub === "members" && (
        <MembersSection
          token={token}
          members={members}
          timezone={spendTZ}
          spendPartial={spendPartial}
          onChange={load}
        />
      )}

      {sub === "usage" && (
        <section className="space-y-5">
          <SectionHead
            eyebrow="§ Usage"
            title={
              <>
                组用量 <span className="text-muted-foreground">统计</span>
              </>
            }
            sub="按请求日志计，含成员用个人余额支付的部分；点成员行可下钻到逐条请求。"
          />
          <GroupUsageView
            hideTitle
            load={usageLoader}
            loadRequests={requestsLoader}
            className="border-border-strong bg-card/40 p-4 md:p-5"
          />
        </section>
      )}

      {sub === "invoices" && <TeamInvoiceSection token={token} onSummary={setInvoiceSummary} />}

      {sub === "pool" && (
        <PoolSection token={token} balanceUSD={pool} rows={ledger} onChange={load} />
      )}

      {/* Mounted once, outside the sub-tabs: the statement is reached from the
          header on any of them. */}
      <TeamStatementDialog
        open={stmtOpen}
        onOpenChange={setStmtOpen}
        token={token}
        workspaceName={me.workspace.name}
      />
    </div>
  );
}

function Kpi({
  label,
  value,
  foot,
  tone,
  onClick,
}: {
  label: string;
  value: string;
  foot: string;
  tone?: "primary" | "muted";
  onClick?: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "group rounded-xl border border-border-strong bg-card/50 px-4 py-4 text-left transition-colors cursor-pointer",
        "hover:border-primary/40 hover:bg-card/80",
      )}
    >
      <div className="eyebrow opacity-70">{label}</div>
      <div
        className={cn(
          "mt-1.5 font-display text-2xl tabular tracking-tight md:text-3xl",
          tone === "primary" && "text-primary",
          tone === "muted" && "text-muted-foreground",
        )}
      >
        {value}
      </div>
      <div className="mt-1 text-[11px] text-muted-foreground">{foot}</div>
    </button>
  );
}

const trendConfig: ChartConfig = {
  billed_usd: {
    label: "消费 (USD)",
    theme: { light: "oklch(0.45 0.16 155)", dark: "oklch(0.8 0.16 145)" },
  },
};

function Overview({
  usage,
  members,
  onSeeUsage,
}: {
  usage: GroupUsage | null;
  members: TeamMember[];
  onSeeUsage: () => void;
}) {
  // Label the ranked rows with the roster's names: /usage returns the same
  // masked tokens, but a name is what an admin recognises.
  const labels = useMemo(() => {
    const m = new Map<string, string>();
    members.forEach((x) => x.label && m.set(x.masked, x.label));
    return m;
  }, [members]);

  if (!usage) {
    return (
      <div className="rounded-xl border border-border-strong bg-card/40 px-4 py-10 text-center text-sm text-muted-foreground">
        近 {OVERVIEW_DAYS} 天的用量暂时读不到（请求日志索引可能已关闭）。成员与发票功能不受影响。
      </div>
    );
  }

  const whole = usage.total.billed_usd;
  const ranked = [...usage.by_member].sort((a, b) => b.billed_usd - a.billed_usd);
  const hasSpend = usage.by_day.some((d) => d.billed_usd > 0);
  const poolShare = whole > 0 ? (usage.pool_billed_usd / whole) * 100 : 0;

  return (
    <div className="space-y-6">
      {usage.partial && usage.notes.length > 0 && (
        <div className="rounded-xl border border-[color:var(--warning)]/40 bg-[color:var(--warning)]/10 px-4 py-3">
          {usage.notes.map((n, i) => (
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

      {/* WHO PAID — one bar, because the answer is a single split and a team
          that never funded a pool should be able to read "all of it came out of
          members' own wallets" at a glance. */}
      <div className="rounded-xl border border-border-strong bg-card/40 p-5">
        <div className="flex flex-wrap items-baseline justify-between gap-2">
          <div className="eyebrow opacity-70">近 {OVERVIEW_DAYS} 天 · 谁付的钱</div>
          <span className="font-mono text-xs text-muted-foreground">
            {usage.from} → {usage.to} · {usage.timezone}
          </span>
        </div>
        <div className="mt-3 flex h-2.5 overflow-hidden rounded-full bg-border">
          <div className="bg-primary" style={{ width: `${poolShare}%` }} />
          <div className="flex-1 bg-muted-foreground/40" />
        </div>
        <div className="mt-2.5 flex flex-wrap gap-x-6 gap-y-1 font-mono text-xs">
          <span className="text-primary">
            组池 {usd4(usage.pool_billed_usd)}
            <span className="ml-1 opacity-60">{poolShare.toFixed(1)}%</span>
          </span>
          <span className="text-muted-foreground">
            个人余额 {usd4(usage.personal_billed_usd)}
            <span className="ml-1 opacity-60">{(100 - poolShare).toFixed(1)}%</span>
          </span>
          <span className="opacity-70">合计 {usd4(whole)}</span>
        </div>
      </div>

      <div className="grid gap-4 lg:grid-cols-2">
        {/* TREND */}
        <div className="rounded-xl border border-border-strong bg-card/40 p-5">
          <div className="flex items-baseline justify-between gap-2">
            <div className="eyebrow opacity-70">每日消费 · {usage.by_day.length} 天</div>
            <button
              type="button"
              onClick={onSeeUsage}
              className="eyebrow cursor-pointer text-primary opacity-80 hover:opacity-100"
            >
              查看明细 →
            </button>
          </div>
          {hasSpend ? (
            <ChartContainer config={trendConfig} className="mt-3 aspect-auto h-[180px] w-full">
              {/* left: 0 — the axis prints "$0.00" at the origin and a negative
                  left margin clips its first character. */}
              <LineChart data={usage.by_day} margin={{ top: 6, right: 6, left: 0, bottom: 0 }}>
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
                      valueFormatter={(v) => (typeof v === "number" ? usd4(v) : String(v))}
                    />
                  }
                />
                <Line
                  type="monotone"
                  dataKey="billed_usd"
                  stroke="var(--color-billed_usd)"
                  strokeWidth={2.5}
                  dot={false}
                  activeDot={{ r: 5 }}
                />
              </LineChart>
            </ChartContainer>
          ) : (
            <p className="py-12 text-center text-xs text-muted-foreground">该区间内没有计费请求。</p>
          )}
        </div>

        {/* LEADERBOARD */}
        <div className="rounded-xl border border-border-strong bg-card/40 p-5">
          <div className="flex items-baseline justify-between gap-2">
            <div className="eyebrow opacity-70">成员消费排行</div>
            <span className="eyebrow tabular opacity-60">{ranked.length} 人</span>
          </div>
          {ranked.length === 0 ? (
            <p className="py-12 text-center text-xs text-muted-foreground">该区间内没有成员消费。</p>
          ) : (
            <div className="mt-3 space-y-2.5">
              {ranked.slice(0, 8).map((m) => {
                const share = whole > 0 ? (m.billed_usd / whole) * 100 : 0;
                return (
                  <div key={m.masked}>
                    <div className="flex items-baseline justify-between gap-3 text-xs">
                      <span className="min-w-0 truncate">
                        <span className="font-mono text-muted-foreground">{m.masked}</span>
                        {(labels.get(m.masked) || m.label) && (
                          <span className="ml-1.5">{labels.get(m.masked) || m.label}</span>
                        )}
                        {m.role === "admin" && (
                          <span className="ml-1.5 text-[10px] text-primary">管理员</span>
                        )}
                      </span>
                      <span className="shrink-0 font-mono tabular">
                        {usd4(m.billed_usd)}
                        <span className="ml-1.5 text-muted-foreground opacity-70">
                          {share.toFixed(0)}%
                        </span>
                      </span>
                    </div>
                    <div className="mt-1 h-1.5 overflow-hidden rounded-full bg-border">
                      <div className="h-full rounded-full bg-primary/70" style={{ width: `${share}%` }} />
                    </div>
                  </div>
                );
              })}
              {ranked.length > 8 && (
                <button
                  type="button"
                  onClick={onSeeUsage}
                  className="eyebrow w-full cursor-pointer pt-1 text-center text-primary opacity-80 hover:opacity-100"
                >
                  另有 {ranked.length - 8} 人 · 查看全部 →
                </button>
              )}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
