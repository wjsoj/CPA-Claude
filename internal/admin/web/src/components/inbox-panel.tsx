// Admin inbox for Resend-inbound emails. Resend pushes one webhook per
// incoming mail to *@novadiffusion.com; the backend persists metadata and
// asynchronously pulls body+attachments. This panel renders the list +
// detail view with sandbox iframe HTML rendering.

import { useCallback, useEffect, useMemo, useState } from "react";
import { toast } from "sonner";
import {
  Mail,
  MailOpen,
  RefreshCw,
  Search,
  Trash2,
  Paperclip,
  Download,
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

interface InboxListItem {
  id: number;
  resend_email_id: string;
  from: string;
  to: string[];
  subject: string;
  received_at: number;
  fetched: boolean;
  unread: boolean;
  has_attachments: boolean;
}

interface InboxAttachment {
  id: string;
  filename: string;
  content_type: string;
  disposition?: string;
  content_id?: string;
}

interface InboxDetail {
  id: number;
  resend_email_id: string;
  message_id: string;
  from: string;
  to: string[];
  cc: string[];
  subject: string;
  received_at: number;
  body_html: string;
  body_text: string;
  attachments: InboxAttachment[];
  fetched: boolean;
}

const fmtUnix = (ts: number): string => (ts > 0 ? fmtDate(new Date(ts * 1000)) : "—");

type Filter = "" | "unread";

export function InboxPanel({ refreshTick }: { refreshTick: number }) {
  const [items, setItems] = useState<InboxListItem[]>([]);
  const [unreadCount, setUnreadCount] = useState(0);
  const [loading, setLoading] = useState(false);
  const [err, setErr] = useState("");
  const [filter, setFilter] = useState<Filter>("");
  const [q, setQ] = useState("");
  const [detail, setDetail] = useState<InboxDetail | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const qs = new URLSearchParams();
      if (filter) qs.set("status", filter);
      if (q.trim()) qs.set("q", q.trim());
      const d = await api<{ emails: InboxListItem[]; unread: number }>(
        "/admin/api/inbox" + (qs.toString() ? `?${qs}` : ""),
      );
      setItems(d.emails || []);
      setUnreadCount(d.unread || 0);
      setErr("");
    } catch (x: any) {
      setErr(x.message || String(x));
      toast.error("Failed to load inbox", { description: x.message });
    } finally {
      setLoading(false);
    }
  }, [filter, q]);

  useEffect(() => {
    load();
  }, [load, refreshTick]);

  const openDetail = useCallback(async (id: number) => {
    setDetailLoading(true);
    try {
      const d = await api<InboxDetail>(`/admin/api/inbox/${id}`);
      setDetail(d);
      // Optimistic update — backend just flipped read_at, so reflect it.
      setItems((prev) => prev.map((v) => (v.id === id ? { ...v, unread: false } : v)));
      setUnreadCount((u) => Math.max(0, u - 1));
    } catch (x: any) {
      toast.error("Failed to open email", { description: x.message });
    } finally {
      setDetailLoading(false);
    }
  }, []);

  const onDelete = useCallback(
    async (id: number) => {
      const ok = await confirmDialog({
        title: "Delete this email?",
        message: "Removes it from the inbox. Resend still has the original copy on their side.",
        confirmLabel: "Delete",
        danger: true,
      });
      if (!ok) return;
      try {
        await api(`/admin/api/inbox/${id}`, { method: "DELETE" });
        toast.success("Deleted");
        setDetail(null);
        load();
      } catch (x: any) {
        toast.error("Delete failed", { description: x.message });
      }
    },
    [load],
  );

  const downloadAttachment = useCallback(
    (emailId: number, att: InboxAttachment) => {
      const url = `${ADMIN_BASE}/api/inbox/${emailId}/attachments/${att.id}`;
      // X-Admin-Token must travel — use fetch then blob download since
      // a plain <a> link can't set custom headers.
      fetch(url, { headers: { "X-Admin-Token": getToken() } })
        .then(async (r) => {
          if (!r.ok) throw new Error(`HTTP ${r.status}`);
          const blob = await r.blob();
          const a = document.createElement("a");
          a.href = URL.createObjectURL(blob);
          a.download = att.filename || "attachment";
          a.click();
          URL.revokeObjectURL(a.href);
        })
        .catch((x: any) => toast.error("Download failed", { description: x.message || String(x) }));
    },
    [],
  );

  const counts = useMemo(() => {
    return { total: items.length, unread: unreadCount };
  }, [items.length, unreadCount]);

  return (
    <section className="space-y-6">
      <div className="flex items-end justify-between gap-4 flex-wrap">
        <div>
          <div className="eyebrow mb-1.5">§ Inbox</div>
          <h2 className="font-display text-3xl md:text-4xl tracking-tight">
            Inbound mail
          </h2>
          <p className="text-sm text-muted-foreground mt-1">
            Emails received at <code className="font-mono">*@novadiffusion.com</code> via Resend
            inbound. {counts.unread > 0 && <span className="text-foreground">· {counts.unread} unread</span>}
          </p>
        </div>
        <div className="flex items-center gap-2">
          <div className="relative">
            <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
            <Input
              value={q}
              onInput={(e) => setQ((e.target as HTMLInputElement).value)}
              placeholder="search subject / from…"
              className="pl-8 w-56"
            />
          </div>
          <Button
            variant={filter === "" ? "default" : "outline"}
            size="sm"
            onClick={() => setFilter("")}
          >
            All {counts.total > 0 && `(${counts.total})`}
          </Button>
          <Button
            variant={filter === "unread" ? "default" : "outline"}
            size="sm"
            onClick={() => setFilter("unread")}
          >
            Unread {counts.unread > 0 && `(${counts.unread})`}
          </Button>
          <Button
            variant="ghost"
            size="icon"
            onClick={() => load()}
            disabled={loading}
            title="Refresh"
          >
            <RefreshCw className={cn("h-4 w-4", loading && "animate-spin")} />
          </Button>
        </div>
      </div>

      {err && (
        <div className="rounded-md border border-destructive/40 bg-destructive/10 px-4 py-2.5 text-sm text-destructive font-mono">
          {err}
        </div>
      )}

      <div className="border border-border rounded-md overflow-hidden">
        <table className="w-full text-sm">
          <thead className="bg-muted/40 text-xs uppercase tracking-wider text-muted-foreground">
            <tr>
              <th className="text-left px-3 py-2 w-8"></th>
              <th className="text-left px-3 py-2">From</th>
              <th className="text-left px-3 py-2">Subject</th>
              <th className="text-left px-3 py-2 w-44">Received</th>
              <th className="text-left px-3 py-2 w-12"></th>
            </tr>
          </thead>
          <tbody>
            {items.length === 0 && !loading && (
              <tr>
                <td colSpan={5} className="px-3 py-8 text-center text-muted-foreground">
                  No emails yet. Send one to <code className="font-mono">support@novadiffusion.com</code> to verify the pipeline.
                </td>
              </tr>
            )}
            {items.map((v) => (
              <tr
                key={v.id}
                onClick={() => openDetail(v.id)}
                className={cn(
                  "border-t border-border cursor-pointer hover:bg-muted/30 transition-colors",
                  v.unread && "font-medium bg-primary/5",
                )}
              >
                <td className="px-3 py-2">
                  {v.unread ? (
                    <Mail className="h-4 w-4 text-primary" />
                  ) : (
                    <MailOpen className="h-4 w-4 text-muted-foreground" />
                  )}
                </td>
                <td className="px-3 py-2 font-mono text-xs truncate max-w-[220px]">{v.from || "—"}</td>
                <td className="px-3 py-2 truncate max-w-[420px]">
                  {v.subject || <span className="text-muted-foreground italic">(no subject)</span>}
                  {v.has_attachments && (
                    <Paperclip className="inline h-3 w-3 ml-1.5 text-muted-foreground" />
                  )}
                </td>
                <td className="px-3 py-2 text-xs text-muted-foreground">{fmtUnix(v.received_at)}</td>
                <td className="px-3 py-2">
                  {!v.fetched && (
                    <Badge variant="outline" className="text-xs">pending</Badge>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <Dialog open={!!detail} onOpenChange={(o) => !o && setDetail(null)}>
        <DialogContent className="max-w-3xl">
          {detail && (
            <>
              <DialogHeader>
                <DialogTitle className="text-lg">
                  {detail.subject || <span className="italic text-muted-foreground">(no subject)</span>}
                </DialogTitle>
              </DialogHeader>
              <div className="space-y-3 text-sm">
                <div className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 font-mono text-xs">
                  <span className="text-muted-foreground">From:</span>
                  <span>{detail.from}</span>
                  <span className="text-muted-foreground">To:</span>
                  <span>{detail.to.join(", ")}</span>
                  {detail.cc?.length > 0 && (
                    <>
                      <span className="text-muted-foreground">Cc:</span>
                      <span>{detail.cc.join(", ")}</span>
                    </>
                  )}
                  <span className="text-muted-foreground">Date:</span>
                  <span>{fmtUnix(detail.received_at)}</span>
                  <span className="text-muted-foreground">Message-ID:</span>
                  <span className="truncate">{detail.message_id || "—"}</span>
                </div>

                {!detail.fetched && (
                  <div className="rounded-md border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-xs text-amber-700 dark:text-amber-300">
                    Body fetch pending — refresh in a few seconds.
                  </div>
                )}

                {detail.attachments?.length > 0 && (
                  <div className="border border-border rounded-md p-3">
                    <div className="eyebrow mb-2">Attachments</div>
                    <ul className="space-y-1.5">
                      {detail.attachments.map((a) => (
                        <li key={a.id} className="flex items-center justify-between gap-2 text-xs">
                          <span className="font-mono truncate flex-1">{a.filename}</span>
                          <span className="text-muted-foreground">{a.content_type}</span>
                          <Button
                            variant="ghost"
                            size="sm"
                            onClick={() => downloadAttachment(detail.id, a)}
                          >
                            <Download className="h-3 w-3 mr-1" /> Download
                          </Button>
                        </li>
                      ))}
                    </ul>
                  </div>
                )}

                <div className="border border-border rounded-md overflow-hidden">
                  {detail.body_html ? (
                    <iframe
                      // sandbox without allow-same-origin so the HTML body can't
                      // read cookies or hit our own origin. allow-popups so a
                      // user-clicked link works.
                      sandbox="allow-popups"
                      srcDoc={detail.body_html}
                      className="w-full min-h-[400px] max-h-[600px] bg-white"
                      title="email body"
                    />
                  ) : (
                    <pre className="p-3 text-xs whitespace-pre-wrap font-mono max-h-[600px] overflow-auto">
                      {detail.body_text || "(no body yet)"}
                    </pre>
                  )}
                </div>

                <div className="flex justify-end pt-2">
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => onDelete(detail.id)}
                  >
                    <Trash2 className="h-4 w-4 mr-1.5" /> Delete
                  </Button>
                </div>
              </div>
            </>
          )}
          {detailLoading && !detail && (
            <div className="py-8 text-center text-muted-foreground">Loading…</div>
          )}
        </DialogContent>
      </Dialog>
    </section>
  );
}
