import { useState, useEffect } from "react";
import { toast } from "sonner";
import { api } from "@/lib/api";
import type { ClientRow, OrphanToken } from "@/lib/types";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Separator } from "@/components/ui/separator";
import { confirmDialog } from "@/hooks/use-confirm";
import { copyToClipboard, fmtDate, fmtInt } from "@/lib/utils";

interface Props {
  row: ClientRow;
  onClose: () => void;
  onSaved: () => void;
}

export function EditTokenModal({ row, onClose, onSaved }: Props) {
  const [name, setName] = useState(row.label || "");
  const [rpm, setRpm] = useState(row.rpm && row.rpm > 0 ? String(row.rpm) : "");
  const [group, setGroup] = useState(row.group || "");
  const [providerAccess, setProviderAccess] = useState(row.providers?.[0] || "both"); // "both" | "anthropic" | "openai"
  // SaaS — admin balance adjustment + pricing-group reassignment.
  const [balanceDelta, setBalanceDelta] = useState("");
  const [balanceNote, setBalanceNote] = useState("");
  const [groupID, setGroupID] = useState<string>(row.group_id ? String(row.group_id) : "");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  const [orphans, setOrphans] = useState<OrphanToken[] | null>(null);
  const [pickedOrphan, setPickedOrphan] = useState("");
  const [merging, setMerging] = useState(false);

  const [resetToken, setResetToken] = useState("");
  const [resetCopied, setResetCopied] = useState(false);
  const [resetting, setResetting] = useState(false);

  useEffect(() => {
    let cancel = false;
    api<{ orphans?: OrphanToken[] }>("/admin/api/orphan-tokens")
      .then((d) => {
        if (!cancel) setOrphans(d.orphans || []);
      })
      .catch(() => {
        if (!cancel) setOrphans([]);
      });
    return () => {
      cancel = true;
    };
  }, []);

  const save = async () => {
    if (!row.full_token) return;
    setBusy(true);
    setErr("");
    try {
      const body: Record<string, unknown> = {
        name,
        group,
        providers: providerAccess === "both" ? [] : [providerAccess],
      };
      const r = parseInt(rpm, 10);
      body.rpm = !isNaN(r) && r > 0 ? r : 0;
      const gid = parseInt(groupID, 10);
      if (!isNaN(gid) && gid > 0) body.group_id = gid;
      const bd = parseFloat(balanceDelta);
      if (!isNaN(bd) && bd !== 0) {
        body.balance_delta = bd;
        if (balanceNote.trim()) body.balance_note = balanceNote.trim();
      }
      await api(`/admin/api/tokens/${encodeURIComponent(row.full_token)}`, {
        method: "PATCH",
        body: JSON.stringify(body),
      });
      onSaved();
    } catch (x: any) {
      setErr(x.message);
    } finally {
      setBusy(false);
    }
  };

  const doReset = async () => {
    if (!row.full_token) return;
    const ok = await confirmDialog({
      title: "Reset token",
      message:
        "A new random token will be generated. The current token stops working immediately — every client using it must be updated with the new value. Usage history (weekly spend, totals) stays on this row.",
      confirmLabel: "Reset",
      danger: true,
    });
    if (!ok) return;
    setResetting(true);
    try {
      const d = await api<{ token: string }>(
        `/admin/api/tokens/${encodeURIComponent(row.full_token)}/reset`,
        { method: "POST" },
      );
      setResetToken(d.token);
    } catch (x: any) {
      toast.error("Reset failed", { description: x.message });
    } finally {
      setResetting(false);
    }
  };

  const doInherit = async () => {
    if (!pickedOrphan || !row.full_token || !orphans) return;
    const src = orphans.find((o) => o.token === pickedOrphan);
    const ok = await confirmDialog({
      title: "Inherit usage",
      message: `Merge historical usage from ${src ? src.label || src.masked : "the selected orphan"} into "${row.label || row.token}"? Weekly spend and totals accumulate. The orphan row disappears. This can't be undone.`,
      confirmLabel: "Merge",
    });
    if (!ok) return;
    setMerging(true);
    try {
      await api(`/admin/api/tokens/${encodeURIComponent(row.full_token)}/inherit`, {
        method: "POST",
        body: JSON.stringify({ from: pickedOrphan }),
      });
      toast.success("Usage merged");
      onSaved();
    } catch (x: any) {
      toast.error("Merge failed", { description: x.message });
    } finally {
      setMerging(false);
    }
  };

  const copyReset = async () => {
    try {
      await copyToClipboard(resetToken);
      setResetCopied(true);
      setTimeout(() => setResetCopied(false), 2000);
    } catch {
      // ignore
    }
  };

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>{resetToken ? "Token reset" : "Edit token"}</DialogTitle>
          <DialogDescription className="mono text-xs">{row.token}</DialogDescription>
        </DialogHeader>

        {resetToken ? (
          <div className="space-y-4">
            <div className="text-base text-muted-foreground">
              New token — save it now, you won't see the full value again. Every client using the old token needs to switch to this one.
            </div>
            <div className="bg-muted border rounded-lg px-4 py-3 mono text-sm break-all select-all">
              {resetToken}
            </div>
            <DialogFooter className="justify-between sm:justify-between gap-2 sm:gap-2">
              <Button variant="outline" onClick={copyReset}>
                {resetCopied ? "Copied ✓" : "Copy to clipboard"}
              </Button>
              <Button onClick={onSaved}>Done</Button>
            </DialogFooter>
          </div>
        ) : (
          <>
            <div className="space-y-1.5">
              <Label>Name</Label>
              <Input value={name} onChange={(e) => setName(e.currentTarget.value)} />
            </div>
            <div className="space-y-1.5">
              <Label>Wallet adjust (USD)</Label>
              <Input
                type="number"
                step={0.01}
                className="mono text-sm"
                placeholder="+5 to credit · -3 to debit · empty = no change"
                value={balanceDelta}
                onChange={(e) => setBalanceDelta(e.currentTarget.value)}
              />
              {balanceDelta && (
                <Input
                  className="mono text-sm"
                  placeholder="note (optional)"
                  value={balanceNote}
                  onChange={(e) => setBalanceNote(e.currentTarget.value)}
                />
              )}
              <div className="text-[11px] text-muted-foreground">
                Current balance:{" "}
                <span className="font-mono">${row.balance_usd.toFixed(4)}</span>
                {row.pricing_group && (
                  <span>
                    {" "}
                    · group <span className="font-mono">{row.pricing_group}</span>
                  </span>
                )}
              </div>
            </div>
            <div className="space-y-1.5">
              <Label>Pricing group ID</Label>
              <Input
                type="number"
                min={0}
                step={1}
                className="mono text-sm"
                placeholder="leave empty to keep current"
                value={groupID}
                onChange={(e) => setGroupID(e.currentTarget.value)}
              />
            </div>
            <div className="space-y-1.5">
              <Label>RPM limit</Label>
              <Input
                type="number"
                min={0}
                step={1}
                className="mono text-sm"
                placeholder="0 = use global default (60)"
                value={rpm}
                onChange={(e) => setRpm(e.currentTarget.value)}
              />
            </div>
            <div className="space-y-1.5">
              <Label>Group</Label>
              <Input
                list="groups-datalist"
                placeholder="public (shared pool)"
                value={group}
                onChange={(e) => setGroup(e.currentTarget.value)}
              />
            </div>
            <div className="space-y-1.5">
              <Label>Provider access</Label>
              <Select value={providerAccess} onValueChange={setProviderAccess}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="both">Both (Claude + OpenAI)</SelectItem>
                  <SelectItem value="anthropic">Claude only</SelectItem>
                  <SelectItem value="openai">OpenAI only</SelectItem>
                </SelectContent>
              </Select>
              <p className="text-xs text-muted-foreground">
                "Claude only" rejects the OpenAI/Codex endpoint with 403, and vice versa.
              </p>
            </div>
            {err && <div className="text-sm text-destructive">{err}</div>}

            <Separator />
            <div className="space-y-3">
              <div>
                <div className="text-sm font-medium">Reset token</div>
                <div className="text-xs text-muted-foreground mt-0.5">
                  Issue a new random <code className="mono">sk-…</code>; the old value stops working. Usage
                  history stays on this row.
                </div>
              </div>
              <Button variant="warning" disabled={resetting} onClick={doReset}>
                {resetting ? "Resetting…" : "Reset token"}
              </Button>
            </div>

            {orphans && orphans.length > 0 && (
              <>
                <Separator />
                <div className="space-y-3">
                  <div>
                    <div className="text-sm font-medium">Inherit usage from orphan</div>
                    <div className="text-xs text-muted-foreground mt-0.5">
                      Fold a deleted token's historical spend into this one. Only unregistered tokens appear here.
                    </div>
                  </div>
                  <select
                    value={pickedOrphan}
                    onChange={(e) => setPickedOrphan(e.currentTarget.value)}
                    className="w-full h-9 border border-input rounded-md bg-transparent px-3 text-sm"
                  >
                    <option value="">Select an orphan token…</option>
                    {orphans.map((o) => (
                      <option key={o.token} value={o.token}>
                        {o.label || "(unnamed)"} · {o.masked} · ${o.total.cost_usd.toFixed(2)} ·{" "}
                        {fmtInt(o.total.requests)} req
                        {o.last_used ? " · " + fmtDate(o.last_used) : ""}
                      </option>
                    ))}
                  </select>
                  <Button variant="outline" disabled={!pickedOrphan || merging} onClick={doInherit}>
                    {merging ? "Merging…" : "Merge selected into this token"}
                  </Button>
                </div>
              </>
            )}

            <Separator />
            <DialogFooter className="gap-2 sm:gap-2">
              <Button variant="outline" onClick={onClose}>
                Cancel
              </Button>
              <Button disabled={busy} onClick={save}>
                {busy ? "Saving…" : "Save"}
              </Button>
            </DialogFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}
