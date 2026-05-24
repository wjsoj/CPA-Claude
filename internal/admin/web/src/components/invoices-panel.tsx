// Admin-side invoice queue. Two actions per pending request:
//
//   1. Upload PDF → marks issued + auto-emails the user (Resend, attachment).
//   2. Reject with note → frees up the locked CNY in the user's quota.
//
// Re-download is available on already-issued rows for audit / re-checks;
// it pulls the PDF straight from the server (no client-side cache).

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { toast } from "sonner";
import {
  FileText,
  RefreshCw,
  Upload,
  XCircle,
  Download,
  Filter,
  Search,
} from "lucide-react";
import { api, getToken, ADMIN_BASE } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { confirmDialog } from "@/hooks/use-confirm";
import { cn, fmtDate } from "@/lib/utils";

interface AdminInvoice {
  id: number;
  token: string; // masked
  label: string;
  cny_amount: number;
  title_name: string;
  title: {
    name?: string;
    tax_no?: string;
    address?: string;
    phone?: string;
    bank?: string;
    bank_account?: string;
  };
  contact_email: string;
  status: "pending" | "issued" | "rejected";
  pdf_uploaded: boolean;
  note: string;
  created_at: number;
  issued_at: number;
  rejected_at: number;
}

const fmtCNY = (n: number): string =>
  `¥${n.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;

const fmtUnix = (ts: number): string => (ts > 0 ? fmtDate(new Date(ts * 1000)) : "—");

type StatusFilter = "" | "pending" | "issued" | "rejected";

export function InvoicesPanel({ refreshTick }: { refreshTick: number }) {
  const [items, setItems] = useState<AdminInvoice[]>([]);
  const [loading, setLoading] = useState(false);
  const [err, setErr] = useState("");
  const [status, setStatus] = useState<StatusFilter>("pending");
  const [q, setQ] = useState("");
  const [uploadTarget, setUploadTarget] = useState<AdminInvoice | null>(null);
  const [detailTarget, setDetailTarget] = useState<AdminInvoice | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const qs = new URLSearchParams();
      if (status) qs.set("status", status);
      if (q.trim()) qs.set("q", q.trim());
      const d = await api<{ invoices: AdminInvoice[] }>(
        "/admin/api/invoices" + (qs.toString() ? `?${qs}` : ""),
      );
      setItems(d.invoices || []);
      setErr("");
    } catch (x: any) {
      setErr(x.message || String(x));
      toast.error("Failed to load invoices", { description: x.message });
    } finally {
      setLoading(false);
    }
  }, [status, q]);

  useEffect(() => {
    load();
  }, [load, refreshTick]);

  const counts = useMemo(() => {
    const c = { pending: 0, issued: 0, rejected: 0 };
    for (const v of items) c[v.status]++;
    return c;
  }, [items]);

  const totalCNY = items.reduce((acc, v) => acc + v.cny_amount, 0);

  const reject = async (v: AdminInvoice) => {
    const note = window.prompt(`Reject reason for invoice #${v.id}:`, "");
    if (note === null) return;
    const ok = await confirmDialog({
      title: `Reject invoice #${v.id}?`,
      message: "Frees ¥" + v.cny_amount.toFixed(2) + " back to the user's quota.",
      confirmLabel: "Reject",
      danger: true,
    });
    if (!ok) return;
    try {
      await api(`/admin/api/invoices/${v.id}/reject`, {
        method: "POST",
        body: JSON.stringify({ note }),
      });
      toast.success(`Invoice #${v.id} rejected`);
      load();
    } catch (x: any) {
      toast.error("Reject failed", { description: x.message || String(x) });
    }
  };

  const reDownload = async (v: AdminInvoice) => {
    try {
      const url = `${ADMIN_BASE}/api/invoices/${v.id}/download`;
      const res = await fetch(url, { headers: { "X-Admin-Token": getToken() } });
      if (!res.ok) throw new Error("HTTP " + res.status);
      const blob = await res.blob();
      const a = document.createElement("a");
      a.href = URL.createObjectURL(blob);
      a.download = `invoice-${v.id}.pdf`;
      a.click();
      URL.revokeObjectURL(a.href);
    } catch (x: any) {
      toast.error("Download failed", { description: x.message || String(x) });
    }
  };

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between flex-wrap gap-3">
        <div className="flex items-center gap-2">
          <FileText className="h-5 w-5 text-primary" />
          <h2 className="font-display text-xl">Invoice queue</h2>
          <Badge variant="outline" className="font-mono">
            pending {counts.pending} · issued {counts.issued} · rejected {counts.rejected}
          </Badge>
        </div>
        <div className="flex items-center gap-2 flex-wrap">
          <div className="flex items-center gap-1">
            <Filter className="h-3.5 w-3.5 text-muted-foreground" />
            {(["pending", "issued", "rejected", ""] as StatusFilter[]).map((s) => (
              <button
                key={s || "all"}
                onClick={() => setStatus(s)}
                className={cn(
                  "rounded-md border px-2.5 py-1 text-xs font-mono",
                  status === s
                    ? "border-primary bg-primary/10 text-primary"
                    : "border-border hover:border-foreground/40",
                )}
              >
                {s || "all"}
              </button>
            ))}
          </div>
          <div className="relative">
            <Input
              placeholder="search title / email / token…"
              value={q}
              onInput={(e) => setQ((e.target as HTMLInputElement).value)}
              className="w-64 font-mono text-xs pr-8"
            />
            <Search className="h-3.5 w-3.5 absolute right-2 top-1/2 -translate-y-1/2 text-muted-foreground" />
          </div>
          <Button variant="outline" size="sm" onClick={load} disabled={loading} className="gap-1.5">
            <RefreshCw className={cn("h-3.5 w-3.5", loading && "animate-spin")} />
            Refresh
          </Button>
        </div>
      </div>

      {err && (
        <div className="rounded-md border border-destructive/40 bg-destructive/10 px-4 py-2.5 text-sm text-destructive font-mono">
          {err}
        </div>
      )}

      <div className="text-xs text-muted-foreground font-mono">
        showing {items.length} · total {fmtCNY(totalCNY)}
      </div>

      <div className="rounded-lg border border-border-strong bg-card/60 overflow-x-auto">
        <table className="w-full text-sm">
          <thead className="text-xs uppercase tracking-wider text-muted-foreground">
            <tr className="border-b border-border/60">
              <th className="text-left px-3 py-2 font-mono">#</th>
              <th className="text-left px-3 py-2 font-mono">User</th>
              <th className="text-left px-3 py-2 font-mono">Title</th>
              <th className="text-right px-3 py-2 font-mono">CNY</th>
              <th className="text-left px-3 py-2 font-mono">Email</th>
              <th className="text-left px-3 py-2 font-mono">Created</th>
              <th className="text-left px-3 py-2 font-mono">Status</th>
              <th className="text-right px-3 py-2 font-mono">Actions</th>
            </tr>
          </thead>
          <tbody>
            {items.length === 0 ? (
              <tr>
                <td colSpan={8} className="text-center py-8 text-muted-foreground">
                  No invoices
                </td>
              </tr>
            ) : (
              items.map((v) => (
                <tr
                  key={v.id}
                  className="border-b border-border/40 hover:bg-muted/30 cursor-pointer"
                  onClick={() => setDetailTarget(v)}
                >
                  <td className="px-3 py-2 font-mono">#{v.id}</td>
                  <td className="px-3 py-2">
                    <div className="font-mono text-xs">{v.token}</div>
                    {v.label && <div className="text-[11px] text-muted-foreground">{v.label}</div>}
                  </td>
                  <td className="px-3 py-2 max-w-[260px] truncate">{v.title_name}</td>
                  <td className="px-3 py-2 text-right font-mono tabular">{fmtCNY(v.cny_amount)}</td>
                  <td className="px-3 py-2 font-mono text-xs">{v.contact_email}</td>
                  <td className="px-3 py-2 font-mono text-xs">{fmtUnix(v.created_at)}</td>
                  <td className="px-3 py-2">{statusBadge(v)}</td>
                  <td className="px-3 py-2" onClick={(e) => e.stopPropagation()}>
                    <div className="flex justify-end gap-1.5">
                      {v.status === "pending" && (
                        <>
                          <Button
                            size="sm"
                            onClick={() => setUploadTarget(v)}
                            className="gap-1 h-7 px-2 text-xs"
                          >
                            <Upload className="h-3 w-3" /> Issue
                          </Button>
                          <Button
                            size="sm"
                            variant="outline"
                            onClick={() => reject(v)}
                            className="gap-1 h-7 px-2 text-xs"
                          >
                            <XCircle className="h-3 w-3" /> Reject
                          </Button>
                        </>
                      )}
                      {v.status === "issued" && v.pdf_uploaded && (
                        <Button
                          size="sm"
                          variant="outline"
                          onClick={() => reDownload(v)}
                          className="gap-1 h-7 px-2 text-xs"
                        >
                          <Download className="h-3 w-3" /> PDF
                        </Button>
                      )}
                    </div>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>

      <UploadInvoiceDialog
        target={uploadTarget}
        onClose={() => setUploadTarget(null)}
        onDone={() => {
          setUploadTarget(null);
          load();
        }}
      />
      <DetailDialog target={detailTarget} onClose={() => setDetailTarget(null)} />
    </div>
  );
}

function statusBadge(v: AdminInvoice) {
  switch (v.status) {
    case "pending":
      return <Badge className="bg-amber-500/10 text-amber-700 dark:text-amber-300 border-amber-500/30">pending</Badge>;
    case "issued":
      return <Badge className="bg-emerald-500/10 text-emerald-700 dark:text-emerald-300 border-emerald-500/30">issued</Badge>;
    case "rejected":
      return <Badge variant="destructive">rejected</Badge>;
  }
}

function UploadInvoiceDialog({
  target,
  onClose,
  onDone,
}: {
  target: AdminInvoice | null;
  onClose: () => void;
  onDone: () => void;
}) {
  const [file, setFile] = useState<File | null>(null);
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);
  const inputRef = useRef<HTMLInputElement | null>(null);

  useEffect(() => {
    if (target) {
      setFile(null);
      setNote("");
    }
  }, [target]);

  const submit = async () => {
    if (!target || !file || busy) return;
    setBusy(true);
    try {
      const fd = new FormData();
      fd.append("pdf", file, file.name);
      if (note.trim()) fd.append("note", note.trim());
      const res = await fetch(`${ADMIN_BASE}/api/invoices/${target.id}/issue`, {
        method: "POST",
        headers: { "X-Admin-Token": getToken() },
        body: fd,
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.error || `HTTP ${res.status}`);
      toast.success(
        `Invoice #${target.id} issued` + (data.email_sent ? " · email sent" : " · email skipped"),
      );
      onDone();
    } catch (x: any) {
      toast.error("Issue failed", { description: x.message || String(x) });
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={!!target} onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>Issue invoice {target ? `#${target.id}` : ""}</DialogTitle>
        </DialogHeader>
        {target && (
          <div className="space-y-4">
            <div className="rounded-md border border-border/60 bg-muted/30 p-3 text-sm space-y-1">
              <div className="flex justify-between"><span className="opacity-70">User</span><span className="font-mono">{target.token}</span></div>
              <div className="flex justify-between"><span className="opacity-70">Title</span><span className="text-right truncate ml-4">{target.title_name}</span></div>
              <div className="flex justify-between"><span className="opacity-70">Amount</span><span className="font-mono">{fmtCNY(target.cny_amount)}</span></div>
              <div className="flex justify-between"><span className="opacity-70">Email</span><span className="font-mono">{target.contact_email}</span></div>
            </div>
            <div>
              <label className="eyebrow text-[10px] opacity-70">PDF file</label>
              <input
                ref={inputRef}
                type="file"
                accept="application/pdf,.pdf"
                onChange={(e) => setFile(e.currentTarget.files?.[0] || null)}
                className="mt-1 block w-full text-sm file:mr-3 file:rounded-md file:border file:border-border file:bg-muted/40 file:px-3 file:py-1.5 file:text-sm hover:file:bg-muted/60"
              />
              {file && (
                <div className="mt-1 text-[11px] text-muted-foreground font-mono">
                  {file.name} · {(file.size / 1024).toFixed(1)} KB
                </div>
              )}
            </div>
            <div>
              <label className="eyebrow text-[10px] opacity-70">Note (optional)</label>
              <Input
                value={note}
                onInput={(e) => setNote((e.target as HTMLInputElement).value)}
                placeholder="Free-form remark, stored on the invoice"
                className="mt-1 font-mono text-sm"
              />
            </div>
            <div className="text-[11px] text-muted-foreground font-mono">
              Upload triggers an automatic email to <code>{target.contact_email}</code> with the PDF
              attached. If Resend is not configured the email step is skipped (the invoice is still
              marked issued).
            </div>
            <div className="flex justify-end gap-2 pt-1">
              <Button variant="outline" onClick={onClose}>Cancel</Button>
              <Button onClick={submit} disabled={!file || busy} className="gap-1.5">
                {busy ? <RefreshCw className="h-4 w-4 animate-spin" /> : <Upload className="h-4 w-4" />}
                Issue
              </Button>
            </div>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}

function DetailDialog({ target, onClose }: { target: AdminInvoice | null; onClose: () => void }) {
  if (!target) return null;
  const t = target.title || {};
  return (
    <Dialog open={!!target} onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>Invoice #{target.id} — {statusBadge(target)}</DialogTitle>
        </DialogHeader>
        <dl className="text-sm space-y-2">
          <Row k="Amount" v={fmtCNY(target.cny_amount)} mono />
          <Row k="User" v={target.token} mono />
          {target.label && <Row k="Label" v={target.label} />}
          <Row k="Title" v={t.name || target.title_name} />
          {t.tax_no && <Row k="Tax No." v={t.tax_no} mono />}
          {t.address && <Row k="Address" v={t.address} />}
          {t.phone && <Row k="Phone" v={t.phone} mono />}
          {t.bank && <Row k="Bank" v={t.bank} />}
          {t.bank_account && <Row k="Account" v={t.bank_account} mono />}
          <Row k="Contact email" v={target.contact_email} mono />
          <Row k="Created" v={fmtUnix(target.created_at)} mono />
          {target.issued_at > 0 && <Row k="Issued" v={fmtUnix(target.issued_at)} mono />}
          {target.rejected_at > 0 && <Row k="Rejected" v={fmtUnix(target.rejected_at)} mono />}
          {target.note && <Row k="Note" v={target.note} />}
        </dl>
      </DialogContent>
    </Dialog>
  );
}

function Row({ k, v, mono }: { k: string; v: string; mono?: boolean }) {
  return (
    <div className="flex justify-between gap-3">
      <dt className="text-muted-foreground">{k}</dt>
      <dd className={cn("text-right break-all", mono && "font-mono text-xs")}>{v}</dd>
    </div>
  );
}
