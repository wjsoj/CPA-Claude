import { useCallback, useEffect, useState } from "react";
import { toast } from "sonner";
import { Users, Plus, RefreshCw, Trash2, Wallet } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
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
  teamMe,
  teamMembers,
  teamAddMember,
  teamPatchMember,
  teamRemoveMember,
  teamLedger,
  teamTopup,
  type TeamMe,
  type TeamMember,
  type TeamLedgerRow,
} from "@/lib/team-api";
import { confirmDialog } from "@/hooks/use-confirm";

const usd = (n: number) => `$${n.toFixed(4)}`;
const cap = (n: number) => (n > 0 ? `$${n.toFixed(2)}` : "∞");

/**
 * TeamPanel is the group-admin console embedded in the public status page. It
 * only renders when the looked-up token administers a workspace. All calls
 * carry the admin's own token as the Bearer credential.
 */
export function TeamPanel({ token }: { token: string }) {
  const [me, setMe] = useState<TeamMe | null>(null);
  const [members, setMembers] = useState<TeamMember[]>([]);
  const [ledger, setLedger] = useState<TeamLedgerRow[]>([]);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    setBusy(true);
    setErr("");
    try {
      const [m, ms, lg] = await Promise.all([
        teamMe(token),
        teamMembers(token),
        teamLedger(token),
      ]);
      setMe(m);
      setMembers(ms.members || []);
      setLedger(lg.ledger || []);
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }, [token]);

  useEffect(() => {
    void load();
  }, [load]);

  if (err) {
    return (
      <div className="rounded-lg border border-destructive/40 bg-destructive/5 p-3 text-sm text-destructive">
        团队面板加载失败：{err}
      </div>
    );
  }
  if (!me) {
    return <div className="p-3 text-sm text-muted-foreground">加载团队…</div>;
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <Users className="h-4 w-4 text-primary" />
          <span className="font-semibold">{me.workspace.name}</span>
          {me.workspace.disabled && <Badge variant="destructive">已禁用</Badge>}
        </div>
        <div className="flex items-center gap-3">
          <div className="text-sm">
            组共享池余额：
            <span className="font-mono font-semibold text-primary">
              {usd(me.workspace.balance_usd)}
            </span>
          </div>
          <Button size="sm" variant="outline" onClick={() => void load()} disabled={busy}>
            <RefreshCw className={busy ? "h-3.5 w-3.5 animate-spin" : "h-3.5 w-3.5"} />
          </Button>
        </div>
      </div>

      <TopupRow token={token} onDone={load} />

      <MembersTable token={token} members={members} onChange={load} />

      {ledger.length > 0 && <LedgerTable rows={ledger} />}
    </div>
  );
}

function TopupRow({ token, onDone }: { token: string; onDone: () => void }) {
  const [amt, setAmt] = useState("");
  const [busy, setBusy] = useState(false);

  const go = async () => {
    const usdVal = parseFloat(amt);
    if (!Number.isFinite(usdVal) || usdVal < 1) {
      toast.error("最低充值 $1");
      return;
    }
    setBusy(true);
    try {
      const r = await teamTopup(token, usdVal);
      const url = r.pay_url || r.img || r.qr_code;
      if (url && /^https?:/.test(url)) {
        window.open(url, "_blank", "noopener");
        toast.success("已打开支付页，支付完成后点「刷新」更新池余额");
      } else {
        toast.success(`订单已创建：${r.out_trade_no}`);
      }
      setAmt("");
      // Give the mock gateway (dev) a moment to auto-confirm, then refresh.
      setTimeout(onDone, 2500);
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="flex items-end gap-2 rounded-lg border border-border/60 bg-muted/20 p-3">
      <Wallet className="mb-2 h-4 w-4 text-muted-foreground" />
      <div className="flex-1">
        <label className="text-xs text-muted-foreground">给组共享池充值 (USD)</label>
        <Input
          type="number"
          min={1}
          step={1}
          placeholder="例如 50"
          value={amt}
          onChange={(e) => setAmt(e.target.value)}
        />
      </div>
      <Button onClick={go} disabled={busy}>
        充值
      </Button>
    </div>
  );
}

function MembersTable({
  token,
  members,
  onChange,
}: {
  token: string;
  members: TeamMember[];
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

  return (
    <div className="space-y-2">
      <div className="flex items-center gap-2">
        <Input
          placeholder="加入成员：粘贴其 client token (sk-...)"
          value={newTok}
          onChange={(e) => setNewTok(e.target.value)}
          className="font-mono text-xs"
        />
        <Button onClick={add} disabled={adding}>
          <Plus className="mr-1 h-3.5 w-3.5" />
          加入
        </Button>
      </div>

      <div className="overflow-x-auto rounded-lg border border-border/60">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>成员</TableHead>
              <TableHead>角色</TableHead>
              <TableHead className="text-right">日上限 / 已用</TableHead>
              <TableHead className="text-right">月上限 / 已用</TableHead>
              <TableHead className="w-10" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {members.map((m) => (
              <MemberRow key={m.masked} token={token} m={m} onChange={onChange} />
            ))}
            {members.length === 0 && (
              <TableRow>
                <TableCell colSpan={5} className="text-center text-sm text-muted-foreground">
                  暂无成员
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </div>
      <p className="text-xs text-muted-foreground">
        份额按北京时间日 / 月计；上限为 0 表示不限（仅受池总额约束）。组内成员请求优先扣组池，超出份额或池耗尽后回落扣其个人余额。
      </p>
    </div>
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
    <TableRow>
      <TableCell>
        <div className="font-mono text-xs">{m.masked}</div>
        {m.label && <div className="text-xs text-muted-foreground">{m.label}</div>}
      </TableCell>
      <TableCell>
        {m.role === "admin" ? <Badge>管理员</Badge> : <Badge variant="secondary">成员</Badge>}
      </TableCell>
      <TableCell className="text-right">
        <div className="flex items-center justify-end gap-1">
          <Input
            type="number"
            min={0}
            value={day}
            onChange={(e) => setDay(e.target.value)}
            className="h-7 w-20 text-right text-xs"
            placeholder="∞"
          />
          <span className="w-16 text-xs text-muted-foreground">{usd(m.used_day_usd)}</span>
        </div>
      </TableCell>
      <TableCell className="text-right">
        <div className="flex items-center justify-end gap-1">
          <Input
            type="number"
            min={0}
            value={month}
            onChange={(e) => setMonth(e.target.value)}
            className="h-7 w-20 text-right text-xs"
            placeholder="∞"
          />
          <span className="w-16 text-xs text-muted-foreground">{usd(m.used_month_usd)}</span>
        </div>
      </TableCell>
      <TableCell>
        <div className="flex items-center gap-1">
          {dirty && (
            <Button size="sm" variant="outline" onClick={save} disabled={busy} className="h-7 px-2 text-xs">
              保存
            </Button>
          )}
          <Button
            size="sm"
            variant="ghost"
            onClick={remove}
            disabled={busy}
            className="h-7 w-7 p-0 text-destructive"
          >
            <Trash2 className="h-3.5 w-3.5" />
          </Button>
        </div>
      </TableCell>
    </TableRow>
  );
}

function LedgerTable({ rows }: { rows: TeamLedgerRow[] }) {
  return (
    <details className="rounded-lg border border-border/60">
      <summary className="cursor-pointer px-3 py-2 text-sm font-medium">
        组池流水（{rows.length}）
      </summary>
      <div className="max-h-72 overflow-y-auto">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>时间</TableHead>
              <TableHead>类型</TableHead>
              <TableHead>成员</TableHead>
              <TableHead className="text-right">金额</TableHead>
              <TableHead>备注</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((r, i) => (
              <TableRow key={i}>
                <TableCell className="whitespace-nowrap text-xs text-muted-foreground">
                  {new Date(r.created_at * 1000).toLocaleString()}
                </TableCell>
                <TableCell className="text-xs">{kindLabel(r.kind)}</TableCell>
                <TableCell className="font-mono text-xs">{r.member || "—"}</TableCell>
                <TableCell
                  className={
                    r.amount_usd >= 0
                      ? "text-right font-mono text-xs text-emerald-600"
                      : "text-right font-mono text-xs text-muted-foreground"
                  }
                >
                  {r.amount_usd >= 0 ? "+" : "-"}
                  {usd(Math.abs(r.amount_usd))}
                </TableCell>
                <TableCell className="text-xs text-muted-foreground">{r.note}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </details>
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
