import { useCallback, useEffect, useState } from "react";
import { toast } from "sonner";
import { Users, Plus, RefreshCw, ChevronDown, Power } from "lucide-react";
import { api, ApiError } from "@/lib/api";
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
import { GroupUsageView } from "@/components/group-usage-view";
import type { GroupUsage } from "@/lib/team-api";
import { confirmDialog } from "@/hooks/use-confirm";

interface Workspace {
  id: number;
  name: string;
  balance_usd: number;
  disabled: boolean;
  member_count: number;
  admin_count: number;
  created_at: number;
}

// Mirrors TeamMember on the group-admin side: used_* is pool spend (all the
// caps meter), spend_* is total spend from the request log.
interface WSMember {
  masked: string;
  label?: string;
  role: string;
  daily_usd_cap: number;
  monthly_usd_cap: number;
  used_day_usd: number;
  used_month_usd: number;
  spend_source?: "requestlog" | "unmeasurable" | "unavailable";
  spend_day_usd?: number;
  spend_month_usd?: number;
}

const usd = (n: number) => `$${n.toFixed(4)}`;

export function WorkspacesPanel({ refreshTick }: { refreshTick: number }) {
  const [list, setList] = useState<Workspace[]>([]);
  const [enabled, setEnabled] = useState(true);
  const [busy, setBusy] = useState(false);
  const [showCreate, setShowCreate] = useState(false);

  const load = useCallback(async () => {
    setBusy(true);
    try {
      const r = await api<{ workspaces: Workspace[]; enabled: boolean }>("/admin/api/workspaces");
      setList(r.workspaces || []);
      setEnabled(r.enabled);
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load, refreshTick]);

  if (!enabled) {
    return (
      <div className="rounded-lg border border-border/60 p-6 text-center text-sm text-muted-foreground">
        SaaS 计费未启用，工作空间功能不可用。
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <Users className="h-4 w-4 text-primary" />
          <h2 className="font-semibold">工作空间（组共享额度）</h2>
        </div>
        <div className="flex items-center gap-2">
          <Button size="sm" variant="outline" onClick={() => void load()} disabled={busy}>
            <RefreshCw className={busy ? "h-3.5 w-3.5 animate-spin" : "h-3.5 w-3.5"} />
          </Button>
          <Button size="sm" onClick={() => setShowCreate((v) => !v)}>
            <Plus className="mr-1 h-3.5 w-3.5" />
            新建组
          </Button>
        </div>
      </div>

      {showCreate && (
        <CreateWorkspace
          onDone={() => {
            setShowCreate(false);
            void load();
          }}
        />
      )}

      <div className="space-y-2">
        {list.map((w) => (
          <WorkspaceRow key={w.id} ws={w} onChange={load} />
        ))}
        {list.length === 0 && (
          <div className="rounded-lg border border-border/60 p-6 text-center text-sm text-muted-foreground">
            还没有工作空间。新建一个，并指定某个 client token 为组管理员。
          </div>
        )}
      </div>
    </div>
  );
}

function CreateWorkspace({ onDone }: { onDone: () => void }) {
  const [name, setName] = useState("");
  const [adminToken, setAdminToken] = useState("");
  const [initial, setInitial] = useState("");
  const [busy, setBusy] = useState(false);

  const create = async () => {
    if (!name.trim() || !adminToken.trim()) {
      toast.error("组名与组管理员 token 必填");
      return;
    }
    setBusy(true);
    try {
      await api("/admin/api/workspaces", {
        method: "POST",
        body: JSON.stringify({
          name: name.trim(),
          admin_token: adminToken.trim(),
          initial_usd: parseFloat(initial) || 0,
        }),
      });
      toast.success("已创建工作空间");
      onDone();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="space-y-2 rounded-lg border border-border/60 bg-muted/20 p-3">
      <div className="grid gap-2 md:grid-cols-3">
        <div>
          <label className="text-xs text-muted-foreground">组名</label>
          <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="例如 Acme 团队" />
        </div>
        <div>
          <label className="text-xs text-muted-foreground">组管理员 client token</label>
          <Input
            value={adminToken}
            onChange={(e) => setAdminToken(e.target.value)}
            placeholder="sk-..."
            className="font-mono text-xs"
          />
        </div>
        <div>
          <label className="text-xs text-muted-foreground">初始池余额 (USD, 可选)</label>
          <Input type="number" min={0} value={initial} onChange={(e) => setInitial(e.target.value)} />
        </div>
      </div>
      <div className="flex justify-end">
        <Button size="sm" onClick={create} disabled={busy}>
          创建
        </Button>
      </div>
    </div>
  );
}

function WorkspaceRow({ ws, onChange }: { ws: Workspace; onChange: () => void }) {
  const [open, setOpen] = useState(false);
  const [members, setMembers] = useState<WSMember[] | null>(null);
  const [busy, setBusy] = useState(false);

  const loadMembers = useCallback(async () => {
    try {
      const r = await api<{ members: WSMember[] }>(`/admin/api/workspaces/${ws.id}/members`);
      setMembers(r.members || []);
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : String(e));
    }
  }, [ws.id]);

  // Stable identity: GroupUsageView re-queries whenever its loader changes, so
  // an inline arrow would re-fetch on every render of this row.
  const loadUsage = useCallback(
    (from: string, to: string) =>
      api<GroupUsage>(`/admin/api/workspaces/${ws.id}/usage?from=${from}&to=${to}`),
    [ws.id],
  );

  const toggleOpen = () => {
    const next = !open;
    setOpen(next);
    if (next && members === null) void loadMembers();
  };

  const patch = async (body: Record<string, unknown>) => {
    setBusy(true);
    try {
      await api(`/admin/api/workspaces/${ws.id}`, { method: "PATCH", body: JSON.stringify(body) });
      onChange();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const adjustBalance = async () => {
    const v = window.prompt(`调整「${ws.name}」组池余额（USD，可负）`, "");
    if (v === null) return;
    const delta = parseFloat(v);
    if (!Number.isFinite(delta) || delta === 0) {
      toast.error("请输入非零数额");
      return;
    }
    await patch({ balance_delta: delta, balance_note: "admin panel adjust" });
    toast.success("已调整组池余额");
  };

  const toggleDisabled = async () => {
    if (
      !ws.disabled &&
      !(await confirmDialog({
        title: "禁用该组？",
        message: "禁用后成员请求将全部回落扣其个人余额。",
      }))
    )
      return;
    await patch({ disabled: !ws.disabled });
  };

  return (
    <div className="rounded-lg border border-border/60">
      <div className="flex flex-wrap items-center justify-between gap-2 p-3">
        <button className="flex items-center gap-2 text-left" onClick={toggleOpen}>
          <ChevronDown className={open ? "h-4 w-4 rotate-0" : "h-4 w-4 -rotate-90"} />
          <span className="font-semibold">{ws.name}</span>
          {ws.disabled && <Badge variant="destructive">已禁用</Badge>}
          <span className="text-xs text-muted-foreground">
            {ws.member_count} 成员 · {ws.admin_count} 管理员
          </span>
        </button>
        <div className="flex items-center gap-2">
          <span className="text-sm">
            池余额 <span className="font-mono font-semibold text-primary">{usd(ws.balance_usd)}</span>
          </span>
          <Button size="sm" variant="outline" onClick={adjustBalance} disabled={busy}>
            调整余额
          </Button>
          <Button
            size="sm"
            variant={ws.disabled ? "default" : "ghost"}
            onClick={toggleDisabled}
            disabled={busy}
            className={ws.disabled ? "" : "text-destructive"}
          >
            <Power className="h-3.5 w-3.5" />
          </Button>
        </div>
      </div>

      {open && (
        <div className="border-t border-border/60 p-3">
          {members === null ? (
            <div className="text-sm text-muted-foreground">加载成员…</div>
          ) : (
            <div className="overflow-x-auto">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>成员</TableHead>
                    <TableHead>角色</TableHead>
                    <TableHead className="text-right">日上限 / 池已用</TableHead>
                    <TableHead className="text-right">月上限 / 池已用</TableHead>
                    <TableHead className="text-right">总消费 今日 / 本月</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {members.map((m) => (
                    <TableRow key={m.masked}>
                      <TableCell>
                        <div className="font-mono text-xs">{m.masked}</div>
                        {m.label && <div className="text-xs text-muted-foreground">{m.label}</div>}
                      </TableCell>
                      <TableCell>
                        {m.role === "admin" ? (
                          <Badge>管理员</Badge>
                        ) : (
                          <Badge variant="secondary">成员</Badge>
                        )}
                      </TableCell>
                      <TableCell className="text-right font-mono text-xs">
                        {m.daily_usd_cap > 0 ? `$${m.daily_usd_cap.toFixed(2)}` : "∞"} /{" "}
                        {usd(m.used_day_usd)}
                      </TableCell>
                      <TableCell className="text-right font-mono text-xs">
                        {m.monthly_usd_cap > 0 ? `$${m.monthly_usd_cap.toFixed(2)}` : "∞"} /{" "}
                        {usd(m.used_month_usd)}
                      </TableCell>
                      <TableCell className="text-right font-mono text-xs">
                        {m.spend_source === "requestlog" ? (
                          <>
                            {usd(m.spend_day_usd || 0)} /{" "}
                            <span className="text-muted-foreground">
                              {usd(m.spend_month_usd || 0)}
                            </span>
                          </>
                        ) : (
                          // 不能显示 $0：那正是这一列要修的谎——"没量"和"量不出来"必须长得不一样。
                          <span
                            className="text-muted-foreground"
                            title={
                              m.spend_source === "unmeasurable"
                                ? "该令牌过短，脱敏后无法在请求日志中区分"
                                : "请求日志暂不可用"
                            }
                          >
                            {m.spend_source === "unmeasurable" ? "无法统计" : "暂不可用"}
                          </span>
                        )}
                      </TableCell>
                    </TableRow>
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
              <p className="mt-2 text-xs leading-relaxed text-muted-foreground">
                成员的份额（日 / 月上限）由组管理员在公开状态页的「团队管理」面板设置。
                「池已用」只含组池支付的部分（未给池充值的团队恒为 0）；「总消费」来自请求日志，含回落到个人余额的部分。
              </p>

              <GroupUsageView className="mt-3" load={loadUsage} />
            </div>
          )}
        </div>
      )}
    </div>
  );
}
