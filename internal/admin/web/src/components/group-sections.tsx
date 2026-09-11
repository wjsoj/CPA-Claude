// The group console's working sections: members, pool funding, and invoicing.
//
// Split out of the old team-panel so the console shell (group-console.tsx) can
// place them on separate sub-tabs instead of stacking every team feature in one
// scroll. Each section owns its own fetch where it has one, and takes the
// admin's client token as its credential — the product decision to reuse a
// client token as the login (see internal/saas/billing/team.go).
import { useCallback, useEffect, useMemo, useState } from "react";
import { toast } from "sonner";
import {
  Plus,
  RefreshCw,
  Trash2,
  Wallet,
  FileText,
  Download,
  ExternalLink,
  Copy,
  CheckCircle2,
  UserPlus,
  ArrowDownLeft,
  ArrowUpRight,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { ApiError } from "@/lib/api";
import {
  teamAddMember,
  teamPatchMember,
  teamRemoveMember,
  teamTopup,
  teamInvoiceSummary,
  teamInvoices,
  teamCreateInvoice,
  teamDownloadInvoicePDF,
  previewAllocations,
  type TeamMember,
  type TeamLedgerRow,
  type TeamInvoice,
  type TeamInvoiceSummary,
  type TeamTopupResp,
} from "@/lib/team-api";
import { loadWalletOrder, type InvoiceTitle } from "@/lib/status-api";
import {
  InvoiceTitleFields,
  LabeledInput,
  emptyInvoiceTitle,
  fmtCNY,
  invoiceStatusBadge,
  taxNoIsValid,
} from "@/components/invoice-common";
import { confirmDialog } from "@/hooks/use-confirm";
import { cn } from "@/lib/utils";

const usd = (n: number) => `$${(n || 0).toFixed(4)}`;
const usd2 = (n: number) => `$${(n || 0).toFixed(2)}`;

/** A section heading in the page's own idiom: eyebrow + display-weight title. */
export function SectionHead({
  eyebrow,
  title,
  sub,
  children,
}: {
  eyebrow: string;
  title: React.ReactNode;
  sub?: React.ReactNode;
  children?: React.ReactNode;
}) {
  return (
    <div className="flex flex-wrap items-end justify-between gap-3">
      <div className="min-w-0">
        <div className="eyebrow mb-1 opacity-70">{eyebrow}</div>
        <h3 className="font-display text-xl tracking-tight md:text-2xl">{title}</h3>
        {sub && <div className="mt-1 text-xs text-muted-foreground">{sub}</div>}
      </div>
      {children && <div className="flex items-center gap-2">{children}</div>}
    </div>
  );
}

// ---- Members ----------------------------------------------------------

export function MembersSection({
  token,
  members,
  timezone,
  spendPartial,
  onChange,
}: {
  token: string;
  members: TeamMember[];
  timezone: string;
  spendPartial: boolean;
  onChange: () => void;
}) {
  const [newTok, setNewTok] = useState("");
  const [adding, setAdding] = useState(false);

  const add = async () => {
    const t = newTok.trim();
    if (!t) return;
    setAdding(true);
    try {
      await teamAddMember(token, { token: t });
      toast.success("已加入成员");
      setNewTok("");
      onChange();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : String(e));
    } finally {
      setAdding(false);
    }
  };

  const admins = members.filter((m) => m.role === "admin").length;

  return (
    <section className="space-y-5">
      <SectionHead
        eyebrow="§ Roster"
        title={
          <>
            成员 <span className="text-muted-foreground">与份额</span>
          </>
        }
        sub={`${members.length} 位成员 · ${admins} 位管理员${timezone ? ` · 份额按 ${timezone} 划分日 / 月` : ""}`}
      />

      <div className="rounded-xl border border-border-strong bg-card/50 p-4">
        <div className="eyebrow mb-2 opacity-70">加入成员</div>
        <div className="flex flex-wrap items-center gap-2">
          <Input
            placeholder="粘贴对方的 client token（sk-…）"
            value={newTok}
            onChange={(e) => setNewTok(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && void add()}
            className="min-w-0 flex-1 font-mono text-xs"
          />
          <Button onClick={add} disabled={adding || !newTok.trim()} className="gap-1.5">
            {adding ? <RefreshCw className="h-3.5 w-3.5 animate-spin" /> : <UserPlus className="h-3.5 w-3.5" />}
            加入
          </Button>
        </div>
        <p className="mt-2 text-[11px] leading-relaxed text-muted-foreground">
          一个 token 只能属于一个组；token 必须已经存在（由我们在后台创建后发给成员）。加入后该成员的请求优先扣组池。
        </p>
      </div>

      <div className="overflow-x-auto rounded-xl border border-border-strong bg-card/40">
        <Table>
          <TableHeader>
            <TableRow className="hover:bg-transparent">
              <TableHead className="eyebrow">成员</TableHead>
              <TableHead className="eyebrow">角色</TableHead>
              <TableHead className="eyebrow text-right">日份额 · 池已用</TableHead>
              <TableHead className="eyebrow text-right">月份额 · 池已用</TableHead>
              <TableHead className="eyebrow text-right">总消费 今日 / 本月</TableHead>
              <TableHead className="w-16" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {members.map((m) => (
              <MemberRow key={m.masked} token={token} m={m} onChange={onChange} />
            ))}
            {members.length === 0 && (
              <TableRow>
                <TableCell colSpan={6} className="py-8 text-center text-sm text-muted-foreground">
                  暂无成员
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </div>

      {/* 两个数字来自两本账，必须一起看：上限只约束「池已用」，成员撞上限后仍继续消费，
          那部分只出现在「总消费」里。不给池充值的团队「池已用」恒为 0，是正常的。 */}
      <div className="rounded-xl border border-border/60 bg-muted/20 p-4 text-xs leading-relaxed text-muted-foreground">
        份额是<span className="font-medium text-foreground">硬上限</span>，只约束「从组池里花」的部分；填 0 表示不限（仅受池总额约束）。
        成员请求优先扣组池，超出份额或池耗尽后自动回落扣其个人余额，请求不会因此失败。
        <br />
        「池已用」只统计组池支付的部分——
        <span className="font-medium text-foreground">若本组未给池充值，这一列恒为 0 属正常</span>
        ；「总消费」来自请求日志，含成员用个人余额支付的部分，也就是份额管不到的那部分
        {timezone ? `（按 ${timezone} 划分今日 / 本月）` : ""}。
        {spendPartial && (
          <>
            <br />
            <span className="font-medium text-[color:var(--warning)]">
              当前无法读取请求日志，本表「总消费」一列整体不可用——不是成员没有消费。
            </span>
          </>
        )}
      </div>
    </section>
  );
}

function MemberRow({
  token,
  m,
  onChange,
}: {
  token: string;
  m: TeamMember;
  onChange: () => void;
}) {
  const [day, setDay] = useState(String(m.daily_usd_cap || ""));
  const [month, setMonth] = useState(String(m.monthly_usd_cap || ""));
  const [busy, setBusy] = useState(false);

  // Re-sync when the parent refetches (another admin edited, or a save
  // returned a normalised value) — without this the inputs keep showing the
  // stale local text and the row reads as permanently dirty.
  useEffect(() => {
    setDay(String(m.daily_usd_cap || ""));
    setMonth(String(m.monthly_usd_cap || ""));
  }, [m.daily_usd_cap, m.monthly_usd_cap]);

  const dirty =
    (parseFloat(day) || 0) !== m.daily_usd_cap || (parseFloat(month) || 0) !== m.monthly_usd_cap;

  const save = async () => {
    setBusy(true);
    try {
      await teamPatchMember(token, m.masked, {
        daily_usd_cap: parseFloat(day) || 0,
        monthly_usd_cap: parseFloat(month) || 0,
      });
      toast.success("已更新份额");
      onChange();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const remove = async () => {
    if (!(await confirmDialog({ title: "移除成员？", message: m.masked }))) return;
    setBusy(true);
    try {
      await teamRemoveMember(token, m.masked);
      toast.success("已移除");
      onChange();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <TableRow className={cn(dirty && "bg-primary/5")}>
      <TableCell className="whitespace-nowrap">
        <div className="font-mono text-xs">{m.masked}</div>
        {m.label && <div className="mt-0.5 text-xs text-muted-foreground">{m.label}</div>}
      </TableCell>
      {/* nowrap: the table scrolls sideways on a phone, and without this the
          badge breaks "管理员" down three lines instead. */}
      <TableCell className="whitespace-nowrap">
        {m.role === "admin" ? (
          <Badge className="whitespace-nowrap text-[10px]">管理员</Badge>
        ) : (
          <Badge variant="secondary" className="whitespace-nowrap text-[10px]">
            成员
          </Badge>
        )}
      </TableCell>
      <CapCell value={day} onChange={setDay} cap={m.daily_usd_cap} used={m.used_day_usd} />
      <CapCell value={month} onChange={setMonth} cap={m.monthly_usd_cap} used={m.used_month_usd} />
      <TableCell className="text-right">
        <MemberSpendCell m={m} />
      </TableCell>
      <TableCell>
        <div className="flex items-center justify-end gap-1">
          {dirty && (
            <Button
              size="sm"
              onClick={save}
              disabled={busy}
              className="h-7 px-2 text-xs"
            >
              保存
            </Button>
          )}
          <Button
            size="sm"
            variant="ghost"
            onClick={remove}
            disabled={busy}
            className="h-7 w-7 p-0 text-destructive hover:bg-destructive/10"
            title="移除成员"
          >
            <Trash2 className="h-3.5 w-3.5" />
          </Button>
        </div>
      </TableCell>
    </TableRow>
  );
}

/**
 * One cap column: the editable limit, the pool spend it meters, and — only
 * when a cap is actually set — how much of it is gone. A capless member gets
 * no bar, because there is no denominator to draw one against.
 */
function CapCell({
  value,
  onChange,
  cap,
  used,
}: {
  value: string;
  onChange: (v: string) => void;
  cap: number;
  used: number;
}) {
  const ratio = cap > 0 ? Math.min(1, used / cap) : 0;
  const hot = cap > 0 && ratio >= 0.9;
  return (
    <TableCell className="text-right">
      <div className="flex items-center justify-end gap-2">
        <div className="relative">
          <span className="pointer-events-none absolute left-2 top-1/2 -translate-y-1/2 font-mono text-[11px] text-muted-foreground">
            $
          </span>
          <Input
            type="number"
            min={0}
            value={value}
            onChange={(e) => onChange(e.target.value)}
            className="h-7 w-24 pl-5 text-right font-mono text-xs"
            placeholder="不限"
          />
        </div>
        <div className="w-20 text-left">
          <div
            className={cn(
              "font-mono text-[11px]",
              hot ? "text-[color:var(--warning)]" : "text-muted-foreground",
            )}
          >
            {usd2(used)}
          </div>
          {cap > 0 && (
            <div className="mt-1 h-1 w-full overflow-hidden rounded-full bg-border">
              <div
                className={cn("h-full rounded-full", hot ? "bg-[color:var(--warning)]" : "bg-primary")}
                style={{ width: `${ratio * 100}%` }}
              />
            </div>
          )}
        </div>
      </div>
    </TableCell>
  );
}

/**
 * Total spend for one member — pool plus whatever their own wallet covered.
 *
 * "We could not measure it" must not render as $0.0000: that is the exact
 * failure this column exists to fix, and a zero here would be read as "this
 * person did nothing" rather than "the log is unavailable".
 */
function MemberSpendCell({ m }: { m: TeamMember }) {
  if (m.spend_source !== "requestlog") {
    const why =
      m.spend_source === "unmeasurable"
        ? "该令牌过短，脱敏后无法在请求日志中区分"
        : "请求日志暂不可用";
    return (
      <span className="text-xs text-muted-foreground" title={why}>
        {m.spend_source === "unmeasurable" ? "无法统计" : "暂不可用"}
      </span>
    );
  }
  return (
    <div className="font-mono text-xs leading-tight">
      <div>{usd(m.spend_day_usd || 0)}</div>
      <div className="text-muted-foreground">
        {usd(m.spend_month_usd || 0)}
        <span className="ml-1 opacity-60">/ {m.spend_month_requests || 0} 笔</span>
      </div>
    </div>
  );
}

// ---- Pool: top-up + ledger --------------------------------------------

export function PoolSection({
  token,
  balanceUSD,
  rows,
  onChange,
}: {
  token: string;
  balanceUSD: number;
  rows: TeamLedgerRow[];
  onChange: () => void;
}) {
  const funded = balanceUSD > 0 || rows.length > 0;
  // The ledger is dominated by per-request charge rows — hundreds of them at
  // fractions of a cent — and the row an admin came to find is almost always a
  // top-up. Filtering is what makes the card readable; the totals above it say
  // what the 200-row window actually contains, so a filtered view can't be
  // mistaken for the whole story.
  const [filter, setFilter] = useState<"all" | "funding" | "charge">("all");
  const shown = rows.filter((r) =>
    filter === "all" ? true : filter === "charge" ? r.kind === "charge" : r.kind !== "charge",
  );
  const credited = rows.filter((r) => r.amount_usd > 0).reduce((a, r) => a + r.amount_usd, 0);
  const spent = rows.filter((r) => r.amount_usd < 0).reduce((a, r) => a - r.amount_usd, 0);

  return (
    <section className="space-y-5">
      <SectionHead
        eyebrow="§ Shared pool"
        title={
          <>
            组池 <span className="text-muted-foreground">充值与流水</span>
          </>
        }
        sub="充进组池的钱由全组共用，按成员份额扣减；不计入任何人的个人余额。"
      />

      <div className="grid items-start gap-4 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.4fr)]">
        <TopupCard token={token} balanceUSD={balanceUSD} onDone={onChange} />
        <div className="rounded-xl border border-border-strong bg-card/40">
          <div className="space-y-2.5 border-b border-border/60 px-4 py-3">
            <div className="flex flex-wrap items-baseline justify-between gap-2">
              <span className="eyebrow opacity-70">组池流水</span>
              <span className="eyebrow tabular opacity-60">
                最近 {rows.length} 条 · 入 {usd2(credited)} · 出 {usd2(spent)}
              </span>
            </div>
            {rows.length > 0 && (
              <div className="flex gap-1">
                {(
                  [
                    ["all", "全部"],
                    ["funding", "充值 / 调整"],
                    ["charge", "消费"],
                  ] as const
                ).map(([k, label]) => (
                  <button
                    key={k}
                    type="button"
                    onClick={() => setFilter(k)}
                    className={cn(
                      "rounded-md px-2 py-0.5 text-[11px] transition-colors cursor-pointer",
                      filter === k
                        ? "bg-primary/15 text-primary"
                        : "text-muted-foreground hover:bg-muted/60 hover:text-foreground",
                    )}
                  >
                    {label}
                  </button>
                ))}
              </div>
            )}
          </div>
          {rows.length === 0 ? (
            <p className="px-4 py-8 text-center text-xs text-muted-foreground">
              {funded ? "暂无流水。" : "本组尚未给组池充值——成员的消费直接扣他们各自的个人余额，这是完全正常的用法。"}
            </p>
          ) : (
            <div className="max-h-[420px] overflow-y-auto">
              <Table>
                <TableHeader>
                  <TableRow className="hover:bg-transparent">
                    <TableHead className="eyebrow">时间</TableHead>
                    <TableHead className="eyebrow">类型</TableHead>
                    <TableHead className="eyebrow">成员</TableHead>
                    <TableHead className="eyebrow text-right">金额</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {shown.map((r, i) => (
                    <TableRow key={i}>
                      <TableCell className="whitespace-nowrap font-mono text-[11px] text-muted-foreground">
                        {new Date(r.created_at * 1000).toLocaleString("zh-CN", { hour12: false })}
                      </TableCell>
                      <TableCell className="text-xs">
                        <span className="inline-flex items-center gap-1">
                          {r.amount_usd >= 0 ? (
                            <ArrowDownLeft className="h-3 w-3 text-primary" />
                          ) : (
                            <ArrowUpRight className="h-3 w-3 text-muted-foreground" />
                          )}
                          {kindLabel(r.kind)}
                        </span>
                      </TableCell>
                      <TableCell className="font-mono text-[11px]">{r.member || "—"}</TableCell>
                      <TableCell
                        className={cn(
                          "text-right font-mono text-xs",
                          r.amount_usd >= 0 ? "text-primary" : "text-muted-foreground",
                        )}
                      >
                        {r.amount_usd >= 0 ? "+" : "-"}
                        {usd(Math.abs(r.amount_usd))}
                      </TableCell>
                    </TableRow>
                  ))}
                  {shown.length === 0 && (
                    <TableRow>
                      <TableCell colSpan={4} className="py-8 text-center text-xs text-muted-foreground">
                        最近 {rows.length} 条流水里没有这一类。
                      </TableCell>
                    </TableRow>
                  )}
                </TableBody>
              </Table>
            </div>
          )}
        </div>
      </div>
    </section>
  );
}

function kindLabel(k: string): string {
  switch (k) {
    case "topup":
      return "充值";
    case "charge":
      return "消费";
    case "adjust":
      return "调整";
    default:
      return k;
  }
}

const TOPUP_PRESETS = [20, 50, 100, 200];

function TopupCard({
  token,
  balanceUSD,
  onDone,
}: {
  token: string;
  balanceUSD: number;
  onDone: () => void;
}) {
  const [amt, setAmt] = useState("");
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<TeamTopupResp | null>(null);

  const go = async () => {
    const usdVal = parseFloat(amt);
    if (!Number.isFinite(usdVal) || usdVal < 1) {
      toast.error("最低充值 $1");
      return;
    }
    setBusy(true);
    try {
      const r = await teamTopup(token, usdVal);
      setResult(r);
      setAmt("");
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="rounded-xl border border-border-strong bg-card/50 p-5">
      <div className="eyebrow opacity-70">组池余额</div>
      <div className="mt-1.5 font-display text-4xl tabular tracking-tight">{usd2(balanceUSD)}</div>

      <div className="mt-5 eyebrow opacity-70">充值金额 (USD)</div>
      <div className="mt-2 flex flex-wrap gap-1.5">
        {TOPUP_PRESETS.map((p) => (
          <button
            key={p}
            type="button"
            onClick={() => setAmt(String(p))}
            className={cn(
              "rounded-md border px-2.5 py-1 font-mono text-xs transition-colors cursor-pointer",
              String(p) === amt
                ? "border-primary bg-primary/10 text-primary"
                : "border-border hover:border-border-strong hover:bg-muted/50",
            )}
          >
            ${p}
          </button>
        ))}
      </div>
      <div className="mt-2 flex items-center gap-2">
        <div className="relative flex-1">
          <span className="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 font-mono text-sm text-muted-foreground">
            $
          </span>
          <Input
            type="number"
            min={1}
            step={1}
            placeholder="自定义金额"
            value={amt}
            onChange={(e) => setAmt(e.target.value)}
            className="pl-6 font-mono"
          />
        </div>
        <Button onClick={go} disabled={busy || !amt} className="gap-1.5">
          {busy ? <RefreshCw className="h-4 w-4 animate-spin" /> : <Wallet className="h-4 w-4" />}
          充值
        </Button>
      </div>
      <p className="mt-2 text-[11px] leading-relaxed text-muted-foreground">
        支付宝扫码，按当前汇率折算为 USD 入池。入池的钱不计入你的个人余额，也不能退回个人钱包。
      </p>

      <TeamTopupDialog
        token={token}
        result={result}
        onClose={() => setResult(null)}
        onSettled={onDone}
      />
    </div>
  );
}

/**
 * Mirrors wallet-panel's TopupModal: same order shape from the same
 * CreateTopup core, so the QR must be rendered the same way — an embedded
 * `result.img`, not a bare window.open on `pay_url` (that URL is the
 * Alipay-app-only "qr.alipay.com/bax…" link; opened in a desktop browser it
 * shows the "use the Alipay app" stub, not a scannable code).
 */
function TeamTopupDialog({
  token,
  result,
  onClose,
  onSettled,
}: {
  token: string;
  result: TeamTopupResp | null;
  onClose: () => void;
  onSettled: () => void;
}) {
  const [settled, setSettled] = useState(false);
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    setSettled(false);
    setCopied(false);
  }, [result?.out_trade_no]);

  useEffect(() => {
    if (!result || settled) return;
    let cancelled = false;
    const id = setInterval(async () => {
      try {
        const o = await loadWalletOrder(token, result.out_trade_no);
        if (cancelled) return;
        if (o.status === "paid") {
          setSettled(true);
          onSettled();
          toast.success(`已到账 · +$${result.usd_credit.toFixed(2)} 入组共享池`);
          setTimeout(() => {
            if (!cancelled) onClose();
          }, 1400);
        } else if (o.status === "expired" || o.status === "failed") {
          if (!cancelled) {
            toast.error(`订单${o.status === "expired" ? "已过期" : "失败"}，请重新发起`);
            onClose();
          }
        }
      } catch {
        // network blip — next tick retries
      }
    }, 3000);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, [result, settled, token, onSettled, onClose]);

  const copyLink = async () => {
    const url = result?.pay_url || result?.qr_code || "";
    if (!url) return;
    try {
      await navigator.clipboard.writeText(url);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      toast.error("复制失败");
    }
  };

  return (
    <Dialog open={!!result} onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>扫码支付</DialogTitle>
        </DialogHeader>
        {result && (
          <div className="space-y-4">
            <div className="rounded-md border border-border/60 bg-muted/30 p-4">
              <div className="font-mono text-xs text-muted-foreground">
                订单 {result.out_trade_no}
              </div>
              <div className="mt-1 flex items-baseline justify-between">
                <span className="font-mono text-2xl tabular">
                  +${result.usd_credit.toFixed(2)}
                </span>
                <span className="font-mono text-sm text-muted-foreground">
                  支付 {fmtCNY(result.cny_amount)}
                </span>
              </div>
              <div className="mt-1 text-[11px] text-muted-foreground">
                入组共享池，不计入你的个人余额
              </div>
            </div>

            {result.img ? (
              <div className="flex flex-col items-center gap-3">
                <img
                  src={result.img}
                  alt="payment QR"
                  className="rounded-md border border-border bg-white p-2"
                  style={{ width: 240, height: 240 }}
                />
                <div className="text-center font-mono text-[11px] text-muted-foreground">
                  使用支付宝扫码支付 {fmtCNY(result.cny_amount)}
                </div>
                {result.pay_url && (
                  <a
                    href={result.pay_url}
                    target="_blank"
                    rel="noreferrer"
                    className="inline-flex items-center gap-1 text-xs text-primary hover:underline"
                  >
                    <ExternalLink className="h-3 w-3" />
                    或在支付宝 App 中打开
                  </a>
                )}
              </div>
            ) : result.pay_url ? (
              <a
                href={result.pay_url}
                target="_blank"
                rel="noreferrer"
                className="block w-full rounded-md bg-primary px-4 py-2.5 text-center font-medium text-primary-foreground hover:bg-primary/90"
              >
                <ExternalLink className="mr-2 inline h-4 w-4" />
                在支付宝 App 中打开
              </a>
            ) : result.qr_code ? (
              <div className="break-all rounded-md border border-border/60 bg-muted/30 px-2 py-3 text-center font-mono text-xs">
                {result.qr_code}
              </div>
            ) : null}

            <div className="flex items-center justify-between gap-2 pt-1">
              {settled ? (
                <span className="inline-flex items-center gap-1.5 font-mono text-sm text-emerald-600 dark:text-emerald-400">
                  <CheckCircle2 className="h-4 w-4" /> 已到账
                </span>
              ) : (
                <span className="inline-flex items-center gap-1.5 font-mono text-xs text-muted-foreground">
                  <span className="relative inline-flex h-2 w-2">
                    <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-primary opacity-75" />
                    <span className="relative inline-flex h-2 w-2 rounded-full bg-primary" />
                  </span>
                  等待支付…
                </span>
              )}
              {(result.pay_url || result.qr_code) && !settled && (
                <Button variant="outline" size="sm" onClick={copyLink} className="gap-1.5">
                  {copied ? (
                    <CheckCircle2 className="h-4 w-4 text-emerald-500" />
                  ) : (
                    <Copy className="h-4 w-4" />
                  )}
                  {copied ? "已复制" : "复制链接"}
                </Button>
              )}
            </div>
            <p className="text-center text-[11px] text-muted-foreground">
              请在 15 分钟内完成支付，订单会自动过期；支付到账后本弹窗会自动关闭。
            </p>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}

// ---- Team invoicing ---------------------------------------------------
//
// One invoice for the whole workspace. The quota is not the group pool — it is
// the sum of what each member could still invoice on their own, and raising a
// team invoice spends those quotas in join order. The per-member table is what
// the admin decides the amount from, so it is shown in full rather than folded
// into the headline card.

export function TeamInvoiceSection({
  token,
  onSummary,
}: {
  token: string;
  /** Lifted so the console's KPI strip can show the quota without a second query. */
  onSummary?: (s: TeamInvoiceSummary | null) => void;
}) {
  const [summary, setSummary] = useState<TeamInvoiceSummary | null>(null);
  const [invoices, setInvoices] = useState<TeamInvoice[]>([]);
  const [loading, setLoading] = useState(false);
  const [showDialog, setShowDialog] = useState(false);
  const [err, setErr] = useState("");

  const refresh = useCallback(async () => {
    if (!token) return;
    setLoading(true);
    try {
      const [s, l] = await Promise.all([teamInvoiceSummary(token), teamInvoices(token)]);
      setSummary(s);
      onSummary?.(s);
      setInvoices(l.invoices || []);
      setErr("");
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, [token, onSummary]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const total = summary?.total;
  const noQuota = !total || total.available_cny <= 0;

  return (
    <section className="space-y-5">
      <SectionHead
        eyebrow="§ Invoicing"
        title={
          <>
            团队 <span className="text-muted-foreground">发票</span>
          </>
        }
        sub="一张票开给整个组，额度来自各成员自己已支付的订单。"
      >
        <Button size="sm" variant="outline" onClick={() => void refresh()} disabled={loading}>
          <RefreshCw className={cn("h-3.5 w-3.5", loading && "animate-spin")} />
        </Button>
        <Button size="sm" onClick={() => setShowDialog(true)} disabled={noQuota} className="gap-1.5">
          <Plus className="h-3.5 w-3.5" />
          申请开票
        </Button>
      </SectionHead>

      {err && (
        <div className="rounded-md border border-destructive/40 bg-destructive/5 px-3 py-2 text-sm text-destructive">
          团队发票加载失败：{err}
        </div>
      )}

      <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
        <QuotaCell label="累计实付" value={total?.paid_cny} />
        <QuotaCell label="已开票" value={total?.issued_cny} />
        <QuotaCell label="待处理" value={total?.locked_cny} muted />
        <QuotaCell label="可开票合计" value={total?.available_cny} highlight />
      </div>

      <p className="text-[11px] leading-relaxed text-muted-foreground">
        额度按各成员已支付的 CNY 订单累计（不随汇率波动）；团队票按成员加入先后依次扣减其可开票额度（同时加入的按令牌排序）。
        pending 与 issued 都会占用额度，驳回后自动归还。
      </p>

      <div>
        <div className="eyebrow mb-2 opacity-70">成员额度</div>
        <MemberQuotaTable members={summary?.members || []} />
      </div>

      <div>
        <div className="eyebrow mb-2 opacity-70">开票记录</div>
        {invoices.length === 0 ? (
          <div className="rounded-xl border border-border/60 bg-card/30 py-8 text-center text-sm text-muted-foreground">
            暂无团队发票记录
          </div>
        ) : (
          <div className="space-y-2">
            {invoices.map((v) => (
              <TeamInvoiceRow key={v.id} v={v} token={token} />
            ))}
          </div>
        )}
      </div>

      <TeamInvoiceDialog
        open={showDialog}
        onClose={() => setShowDialog(false)}
        token={token}
        summary={summary}
        onCreated={refresh}
      />
    </section>
  );
}

function QuotaCell({
  label,
  value,
  highlight,
  muted,
}: {
  label: string;
  value?: number;
  highlight?: boolean;
  muted?: boolean;
}) {
  return (
    <div
      className={cn(
        "rounded-xl border px-4 py-3",
        highlight ? "border-primary/40 bg-primary/5" : "border-border-strong bg-card/40",
      )}
    >
      <div className="eyebrow opacity-70">{label}</div>
      <div
        className={cn(
          "mt-1 font-mono text-lg tabular",
          highlight && "text-primary",
          muted && "text-muted-foreground",
        )}
      >
        {value === undefined ? "—" : fmtCNY(value)}
      </div>
    </div>
  );
}

function MemberQuotaTable({ members }: { members: TeamInvoiceSummary["members"] }) {
  return (
    <div className="overflow-x-auto rounded-xl border border-border-strong bg-card/40">
      <Table>
        <TableHeader>
          <TableRow className="hover:bg-transparent">
            <TableHead className="eyebrow">成员</TableHead>
            <TableHead className="eyebrow text-right">实付</TableHead>
            <TableHead className="eyebrow text-right">已开票</TableHead>
            <TableHead className="eyebrow text-right">待处理</TableHead>
            <TableHead className="eyebrow text-right">可开票</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {members.map((m) => (
            <TableRow key={m.masked}>
              <TableCell className="whitespace-nowrap">
                <div className="font-mono text-xs">{m.masked}</div>
                {m.label && <div className="text-xs text-muted-foreground">{m.label}</div>}
              </TableCell>
              <TableCell className="text-right font-mono text-xs">{fmtCNY(m.paid_cny)}</TableCell>
              <TableCell className="text-right font-mono text-xs">{fmtCNY(m.issued_cny)}</TableCell>
              <TableCell className="text-right font-mono text-xs text-muted-foreground">
                {fmtCNY(m.locked_cny)}
              </TableCell>
              <TableCell
                className={cn(
                  "text-right font-mono text-xs",
                  m.available_cny > 0 ? "text-primary" : "text-muted-foreground",
                )}
              >
                {fmtCNY(m.available_cny)}
              </TableCell>
            </TableRow>
          ))}
          {members.length === 0 && (
            <TableRow>
              <TableCell colSpan={5} className="py-6 text-center text-sm text-muted-foreground">
                暂无成员额度
              </TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>
    </div>
  );
}

function TeamInvoiceRow({ v, token }: { v: TeamInvoice; token: string }) {
  const [busy, setBusy] = useState(false);
  const allocations = v.allocations || [];

  const download = async () => {
    if (busy) return;
    setBusy(true);
    try {
      const blob = await teamDownloadInvoicePDF(token, v.id);
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `team-invoice-${v.id}.pdf`;
      a.click();
      URL.revokeObjectURL(url);
    } catch (e) {
      toast.error("下载失败", {
        description: e instanceof ApiError ? e.message : String(e),
      });
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="rounded-xl border border-border-strong bg-card/40 px-4 py-3">
      <div className="flex items-center justify-between gap-3">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className="font-mono text-sm text-muted-foreground">#{v.id}</span>
            <span className="font-mono text-base tabular">{fmtCNY(v.cny_amount)}</span>
            <span className="truncate text-sm">{v.title_name}</span>
            {invoiceStatusBadge(v.status)}
          </div>
          <div className="mt-0.5 font-mono text-[11px] text-muted-foreground">
            {fmtUnix(v.created_at)}
            {v.contact_email ? ` · ${v.contact_email}` : ""}
            {v.status === "issued" && v.issued_at ? ` · 开具于 ${fmtUnix(v.issued_at)}` : ""}
          </div>
          {v.status === "rejected" && v.note && (
            <div className="mt-1 text-[11px] text-destructive">驳回原因：{v.note}</div>
          )}
          {v.status !== "rejected" && v.note && (
            <div className="mt-1 text-[11px] text-muted-foreground">备注：{v.note}</div>
          )}
        </div>
        {v.status === "issued" && v.downloadable !== false && (
          <Button variant="outline" size="sm" disabled={busy} onClick={download} className="gap-1.5">
            {busy ? (
              <RefreshCw className="h-3.5 w-3.5 animate-spin" />
            ) : (
              <Download className="h-3.5 w-3.5" />
            )}
            下载
          </Button>
        )}
      </div>
      {allocations.length > 0 && (
        <details className="mt-1.5">
          <summary className="cursor-pointer text-[11px] text-muted-foreground">
            分摊明细（{allocations.length} 位成员）
          </summary>
          <div className="mt-1 space-y-0.5">
            {allocations.map((a, i) => (
              <div key={`${a.masked}-${i}`} className="flex justify-between gap-3 text-[11px]">
                <span className="truncate font-mono text-muted-foreground">
                  {a.masked}
                  {a.label ? ` · ${a.label}` : ""}
                </span>
                <span className="font-mono">{fmtCNY(a.cny_amount)}</span>
              </div>
            ))}
          </div>
        </details>
      )}
    </div>
  );
}

function fmtUnix(ts: number | undefined): string {
  if (!ts) return "—";
  return new Date(ts * 1000).toLocaleString("zh-CN", { hour12: false });
}

function TeamInvoiceDialog({
  open,
  onClose,
  token,
  summary,
  onCreated,
}: {
  open: boolean;
  onClose: () => void;
  token: string;
  summary: TeamInvoiceSummary | null;
  onCreated: () => void;
}) {
  const [amount, setAmount] = useState("");
  const [contactEmail, setContactEmail] = useState(
    () => localStorage.getItem("cpa.invoice.email") || "",
  );
  const [selected, setSelected] = useState<InvoiceTitle>(emptyInvoiceTitle);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (open) {
      setAmount(summary ? Math.max(0, summary.total.available_cny).toFixed(2) : "");
      setSelected(emptyInvoiceTitle());
      setBusy(false);
    }
  }, [open, summary]);

  const amountNum = Number(amount) || 0;
  const available = summary?.total.available_cny ?? 0;
  const tooHigh = amountNum > available + 0.005;
  const taxNoValid = taxNoIsValid(selected.tax_no);

  // Mirrors the server's split so the admin sees whose quota this will spend
  // before submitting.
  const preview = useMemo(
    () => previewAllocations(summary?.members || [], amountNum),
    [summary, amountNum],
  );

  const submit = async () => {
    if (busy) return;
    if (!selected.name.trim()) {
      toast.error("请填写公司名称 (抬头)");
      return;
    }
    if (!taxNoValid) {
      toast.error("请填写有效的统一社会信用代码", { description: "18 位字母 / 数字 (大写)" });
      return;
    }
    if (!contactEmail.includes("@")) {
      toast.error("请填写有效的联系邮箱");
      return;
    }
    if (amountNum <= 0) {
      toast.error("开票金额必须大于 0");
      return;
    }
    if (tooHigh || preview.short_cny > 0.005) {
      toast.error(`金额超过可开票合计 ${fmtCNY(available)}`);
      return;
    }
    setBusy(true);
    try {
      await teamCreateInvoice(token, {
        cny_amount: amountNum,
        title: selected as unknown as Record<string, unknown>,
        contact_email: contactEmail.trim(),
      });
      localStorage.setItem("cpa.invoice.email", contactEmail.trim());
      toast.success("团队发票申请已提交");
      onCreated();
      onClose();
    } catch (e) {
      toast.error("提交失败", { description: e instanceof ApiError ? e.message : String(e) });
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="max-h-[90vh] max-w-lg overflow-y-auto">
        <DialogHeader>
          <DialogTitle>
            申请团队发票{summary ? ` · ${summary.workspace.name}` : ""}
          </DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          <div>
            <label className="eyebrow text-[10px] opacity-70">开票金额 (CNY)</label>
            <div className="mt-1 flex items-baseline gap-2">
              <span className="font-mono text-xl">¥</span>
              <Input
                type="number"
                inputMode="decimal"
                min="0.01"
                step="0.01"
                value={amount}
                onInput={(e) => setAmount((e.target as HTMLInputElement).value)}
                className={cn("flex-1 font-mono text-2xl tabular", tooHigh && "border-destructive")}
              />
            </div>
            <div className="mt-1 flex items-center justify-between gap-2 font-mono text-[11px] text-muted-foreground">
              <span>可开票合计 {fmtCNY(available)}</span>
              <button
                type="button"
                className="text-primary hover:underline"
                onClick={() => setAmount(available.toFixed(2))}
              >
                全部
              </button>
            </div>
            {tooHigh && (
              <div className="mt-1 text-[11px] text-destructive">
                超出团队可开票合计 {fmtCNY(available)}，请调低金额。
              </div>
            )}
          </div>

          <AllocationPreview rows={preview.rows} short={preview.short_cny} total={amountNum} />

          <InvoiceTitleFields token={token} open={open} value={selected} onChange={setSelected} />

          <LabeledInput
            label="接收发票邮箱"
            required
            value={contactEmail}
            onChange={setContactEmail}
            placeholder="invoice@example.com"
            type="email"
          />

          <div className="flex justify-end gap-2 pt-2">
            <Button variant="outline" onClick={onClose}>
              取消
            </Button>
            <Button
              onClick={submit}
              // short_cny is the same refusal as tooHigh reached the other way:
              // the preview walks the members and comes up short even when the
              // headline total looks sufficient (a member's quota moved under
              // us). submit() rejects both, so the button has to disable on
              // both — otherwise it invites a click it will only ever toast at.
              disabled={busy || tooHigh || amountNum <= 0 || preview.short_cny > 0.005}
              className="gap-1.5"
            >
              {busy && <RefreshCw className="h-4 w-4 animate-spin" />}
              提交申请
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}

function AllocationPreview({
  rows,
  short,
  total,
}: {
  rows: ReturnType<typeof previewAllocations>["rows"];
  short: number;
  total: number;
}) {
  if (total <= 0) {
    return (
      <div className="rounded-md border border-border/60 bg-muted/20 px-3 py-2 text-[11px] text-muted-foreground">
        填写金额后这里会显示这张票将用掉哪些成员的额度。
      </div>
    );
  }
  return (
    <div className="rounded-md border border-border/60 bg-muted/20 p-3">
      <div className="mb-1.5 flex items-center justify-between gap-2">
        {/* 后端按 (created_at, token) 排序，而 created_at 是整秒：批量拉人建组时
            全组同一秒加入，实际顺序就是令牌序，文案不能只说"加入顺序"。 */}
        <span className="eyebrow text-[10px] opacity-70">分摊预览（按成员加入先后，同时加入的按令牌排序）</span>
        <span className="font-mono text-[11px] text-muted-foreground">
          合计 {fmtCNY(total - short)}
        </span>
      </div>
      {rows.length === 0 ? (
        <div className="text-[11px] text-muted-foreground">没有成员还有可开票额度。</div>
      ) : (
        <div className="space-y-0.5">
          {rows.map((r, i) => (
            <div key={`${r.masked}-${i}`} className="flex items-center justify-between gap-3 text-[11px]">
              <span className="truncate font-mono text-muted-foreground">
                {r.masked}
                {r.label ? ` · ${r.label}` : ""}
              </span>
              <span className="font-mono">
                {fmtCNY(r.cny_amount)}
                <span className="ml-1 opacity-60">/ {fmtCNY(r.available_cny)}</span>
              </span>
            </div>
          ))}
        </div>
      )}
      {short > 0.005 && (
        <div className="mt-1.5 text-[11px] text-destructive">
          还差 {fmtCNY(short)} 无成员额度可扣，请调低金额。
        </div>
      )}
    </div>
  );
}
