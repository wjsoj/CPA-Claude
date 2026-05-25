// WalletPanel renders the per-token balance dashboard on /status. Users
// enter their access token once (persisted to localStorage as the "active
// token" — distinct from the multi-token saved list the Usage Lookup tab
// keeps); from that point on the panel shows their balance, recent
// transactions, and orders, with a Recharge button that opens a modal
// driving the Z-Pay top-up flow.

import { useCallback, useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import {
  CreditCard,
  LogOut,
  Plus,
  RefreshCw,
  Wallet,
  ExternalLink,
  Copy,
  CheckCircle2,
  Clock3,
  AlertTriangle,
  XCircle,
  Trash2,
  FileText,
  Download,
  Search,
} from "lucide-react";
import {
  loadActiveToken,
  saveActiveToken,
  loadWalletBalance,
  loadWalletTransactions,
  loadWalletOrders,
  loadWalletOrder,
  cancelWalletOrder,
  topupWallet,
  loadExchangeRate,
  loadInvoiceSummary,
  loadInvoices,
  createInvoice,
  suggestInvoiceTitles,
  downloadInvoicePDF,
  type WalletBalance,
  type WalletTx,
  type WalletOrder,
  type ExchangeRate,
  type TopupResp,
  type InvoiceSummary,
  type Invoice,
  type InvoiceTitle,
} from "@/lib/status-api";
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
import { cn, fmtInt } from "@/lib/utils";

function fmtUSD(v: number): string {
  if (v === 0) return "$0.00";
  if (Math.abs(v) >= 1) return `$${v.toFixed(2)}`;
  if (Math.abs(v) >= 0.01) return `$${v.toFixed(4)}`;
  return `$${v.toFixed(6)}`;
}

function fmtCNY(v: number): string {
  return `¥${v.toFixed(2)}`;
}

function fmtTime(epoch: number): string {
  if (!epoch) return "—";
  const d = new Date(epoch * 1000);
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, "0");
  const day = String(d.getDate()).padStart(2, "0");
  const hh = String(d.getHours()).padStart(2, "0");
  const mm = String(d.getMinutes()).padStart(2, "0");
  return `${y}-${m}-${day} ${hh}:${mm}`;
}

function mask(tok: string): string {
  if (tok.length <= 10) return "***";
  return tok.slice(0, 6) + "…" + tok.slice(-4);
}

export function WalletPanel() {
  const [activeToken, setActiveToken] = useState<string>(() => loadActiveToken());
  const [input, setInput] = useState("");
  const [bal, setBal] = useState<WalletBalance | null>(null);
  const [txs, setTxs] = useState<WalletTx[]>([]);
  const [orders, setOrders] = useState<WalletOrder[]>([]);
  const [rate, setRate] = useState<ExchangeRate | null>(null);
  const [loading, setLoading] = useState(false);
  const [err, setErr] = useState("");
  const [showTopup, setShowTopup] = useState(false);

  const refresh = useCallback(async () => {
    if (!activeToken) return;
    setLoading(true);
    setErr("");
    try {
      const [b, t, o] = await Promise.all([
        loadWalletBalance(activeToken),
        loadWalletTransactions(activeToken),
        loadWalletOrders(activeToken),
      ]);
      setBal(b);
      setTxs(t.transactions || []);
      setOrders(o.orders || []);
    } catch (e: any) {
      setErr(e.message || String(e));
      if (e.status === 401) {
        // Stale token — drop it so the user gets the login form back.
        saveActiveToken("");
        setActiveToken("");
      }
    } finally {
      setLoading(false);
    }
  }, [activeToken]);

  useEffect(() => {
    refresh();
  }, [refresh]);

  useEffect(() => {
    loadExchangeRate().then(setRate).catch(() => {});
  }, []);

  const signIn = () => {
    const v = input.trim();
    if (!v) {
      toast.error("Token required");
      return;
    }
    saveActiveToken(v);
    setActiveToken(v);
    setInput("");
  };

  const signOut = () => {
    saveActiveToken("");
    setActiveToken("");
    setBal(null);
    setTxs([]);
    setOrders([]);
  };

  // --- Logged-out view: token entry --------------------------------------
  if (!activeToken) {
    return (
      <div className="max-w-xl mx-auto py-12 space-y-6">
        <div className="text-center space-y-2">
          <Wallet className="h-10 w-10 mx-auto text-primary" />
          <h2 className="font-display text-3xl tracking-tight">Wallet sign-in</h2>
          <p className="text-sm text-muted-foreground">
            Paste your access token to view balance, orders, and top-up history. The
            token never leaves this browser except in the <code>Authorization</code>{" "}
            header of your own wallet calls.
          </p>
        </div>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            signIn();
          }}
          className="rounded-lg border border-border-strong bg-card/60 p-5 space-y-3"
        >
          <label className="eyebrow text-[10px] opacity-70">access token</label>
          <Input
            type="password"
            autoFocus
            placeholder="sk-…"
            value={input}
            onInput={(e) => setInput((e.target as HTMLInputElement).value)}
            className="font-mono"
          />
          <Button type="submit" className="w-full">
            Sign in
          </Button>
        </form>
      </div>
    );
  }

  // --- Logged-in view ----------------------------------------------------
  const blocked = bal !== null && bal.balance_usd <= 0;
  return (
    <div className="space-y-8">
      {/* Header strip — masked token + sign out */}
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-3">
          <div className="rounded-full bg-primary/15 text-primary px-2.5 py-0.5 text-[11px] font-mono">
            {mask(activeToken)}
          </div>
          {bal?.group_name && (
            <Badge variant="outline" className="font-mono text-[10px]">
              group: {bal.group_name} · ×{bal.claude_multiplier?.toFixed(4)} Claude · ×
              {bal.codex_multiplier?.toFixed(4)} Codex
            </Badge>
          )}
        </div>
        <div className="flex items-center gap-2">
          <Button
            variant="outline"
            size="sm"
            onClick={refresh}
            disabled={loading}
            className="gap-1.5"
          >
            <RefreshCw className={cn("h-3.5 w-3.5", loading && "animate-spin")} />
            Refresh
          </Button>
          <Button variant="ghost" size="sm" onClick={signOut} className="gap-1.5">
            <LogOut className="h-3.5 w-3.5" />
            Sign out
          </Button>
        </div>
      </div>

      {err && (
        <div className="rounded-md border border-destructive/40 bg-destructive/10 px-4 py-2.5 text-sm text-destructive font-mono">
          {err}
        </div>
      )}

      {/* Balance hero + recharge CTA */}
      <div
        className={cn(
          "rounded-xl border p-6 md:p-8 relative overflow-hidden",
          blocked
            ? "border-destructive/50 bg-destructive/5"
            : "border-border-strong bg-card/60",
        )}
      >
        <div
          aria-hidden
          className={cn(
            "absolute inset-x-0 top-0 h-[2px]",
            blocked
              ? "bg-gradient-to-r from-destructive/0 via-destructive/70 to-destructive/0"
              : "bg-gradient-to-r from-primary/0 via-primary/70 to-primary/0",
          )}
        />
        <div className="flex flex-wrap items-end justify-between gap-6">
          <div>
            <div className="eyebrow opacity-70">balance</div>
            <div className="mt-2 font-display text-5xl md:text-6xl tabular tracking-tight">
              {bal ? fmtUSD(bal.balance_usd) : "···"}
            </div>
            {blocked && (
              <div className="mt-2 text-sm text-destructive font-mono flex items-center gap-1.5">
                <AlertTriangle className="h-4 w-4" /> wallet empty — requests refused until top-up
              </div>
            )}
            {rate && (
              <div className="mt-1 text-xs text-muted-foreground font-mono">
                1 USD ≈ ¥{rate.cny_per_usd.toFixed(4)}
                {rate.as_of > 0 && (
                  <span className="opacity-70"> · refreshed {fmtTime(rate.as_of)}</span>
                )}
              </div>
            )}
          </div>
          <Button
            size="lg"
            onClick={() => setShowTopup(true)}
            className="gap-2 shadow-lg shadow-primary/20"
          >
            <Plus className="h-4 w-4" /> Recharge
          </Button>
        </div>
      </div>

      {/* Orders + Transactions, two columns on desktop */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
        <OrdersCard
          orders={orders}
          onPaid={refresh}
          activeToken={activeToken}
        />
        <TransactionsCard txs={txs} />
      </div>

      <InvoiceSection token={activeToken} />


      <TopupModal
        open={showTopup}
        onClose={() => setShowTopup(false)}
        token={activeToken}
        rate={rate}
        onCreated={() => {
          refresh();
        }}
      />
    </div>
  );
}

function statusBadge(status: WalletOrder["status"]) {
  switch (status) {
    case "paid":
      return (
        <span className="inline-flex items-center gap-1 text-emerald-600 dark:text-emerald-400 text-xs font-mono">
          <CheckCircle2 className="h-3 w-3" /> paid
        </span>
      );
    case "pending":
      return (
        <span className="inline-flex items-center gap-1 text-amber-600 dark:text-amber-400 text-xs font-mono">
          <Clock3 className="h-3 w-3 animate-pulse" /> pending
        </span>
      );
    case "expired":
      return (
        <span className="inline-flex items-center gap-1 text-muted-foreground text-xs font-mono">
          <XCircle className="h-3 w-3" /> expired
        </span>
      );
    case "failed":
      return (
        <span className="inline-flex items-center gap-1 text-destructive text-xs font-mono">
          <XCircle className="h-3 w-3" /> failed
        </span>
      );
  }
}

function OrdersCard({
  orders,
  onPaid,
  activeToken,
}: {
  orders: WalletOrder[];
  onPaid: () => void;
  activeToken: string;
}) {
  const [polling, setPolling] = useState<string | null>(null);

  // Poll pending orders every 4s until they settle. Stops itself when the
  // first pending order flips to paid (the user's other orders may keep
  // pending but the panel reloads anyway).
  useEffect(() => {
    const pending = orders.find((o) => o.status === "pending");
    if (!pending) {
      setPolling(null);
      return;
    }
    setPolling(pending.out_trade_no);
    const id = setInterval(async () => {
      try {
        const o = await loadWalletOrder(activeToken, pending.out_trade_no);
        if (o.status !== "pending") {
          onPaid();
        }
      } catch {
        // ignore — network blip; next tick will retry
      }
    }, 4000);
    return () => clearInterval(id);
  }, [orders, activeToken, onPaid]);

  return (
    <div className="rounded-lg border border-border-strong bg-card/60">
      <div className="flex items-center justify-between px-4 py-3 border-b border-border">
        <div className="flex items-center gap-2">
          <CreditCard className="h-4 w-4 text-primary" />
          <h3 className="font-display text-base tracking-tight">Orders</h3>
          {polling && (
            <span className="eyebrow text-[9px] opacity-60">watching pending…</span>
          )}
        </div>
        <span className="text-xs text-muted-foreground font-mono">{orders.length}</span>
      </div>
      {orders.length === 0 ? (
        <div className="px-4 py-8 text-center text-sm text-muted-foreground">
          no orders yet — hit Recharge to top up
        </div>
      ) : (
        <ul className="divide-y divide-border/60 max-h-[420px] overflow-y-auto">
          {orders.map((o) => (
            <li key={o.out_trade_no} className="px-4 py-3 hover:bg-muted/30">
              <div className="flex items-baseline justify-between gap-2">
                <div className="flex items-baseline gap-2 min-w-0">
                  {statusBadge(o.status)}
                  <span className="font-mono text-[11px] opacity-60 truncate">
                    {o.out_trade_no}
                  </span>
                </div>
                <span className="font-mono text-sm tabular font-medium">
                  +{fmtUSD(o.usd_credit)}
                </span>
              </div>
              <div className="mt-1 flex items-center justify-between gap-2 text-[11px] font-mono text-muted-foreground">
                <span>{fmtTime(o.created_at)}</span>
                <span>{fmtCNY(o.cny_amount)} @ ¥{o.rate.toFixed(4)}</span>
              </div>
              {o.status === "pending" && (
                <div className="mt-2 flex gap-3 flex-wrap items-center">
                  {o.img && (
                    <a
                      href={o.img}
                      target="_blank"
                      rel="noreferrer"
                      className="inline-flex items-center gap-1 text-xs text-primary hover:underline"
                    >
                      <ExternalLink className="h-3 w-3" /> show QR
                    </a>
                  )}
                  <button
                    type="button"
                    onClick={async () => {
                      const ok = await confirmDialog({
                        title: "Cancel this order?",
                        message: `Order ${o.out_trade_no.slice(0, 18)}… for $${o.usd_credit.toFixed(2)} will be deleted. If you've already paid, do NOT cancel — wait a few seconds for the gateway to settle. This can't be undone.`,
                        confirmLabel: "Cancel order",
                        cancelLabel: "Keep it",
                        danger: true,
                      });
                      if (!ok) return;
                      try {
                        await cancelWalletOrder(activeToken, o.out_trade_no);
                        toast.success("Order cancelled");
                        onPaid();
                      } catch (e: any) {
                        toast.error("Cancel failed", { description: e.message || String(e) });
                      }
                    }}
                    className="inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-destructive transition-colors"
                  >
                    <Trash2 className="h-3 w-3" /> cancel
                  </button>
                  {o.pay_url && (
                    <a
                      href={o.pay_url}
                      target="_blank"
                      rel="noreferrer"
                      className="inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-primary hover:underline"
                    >
                      <ExternalLink className="h-3 w-3" /> open in Alipay app
                    </a>
                  )}
                </div>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function TransactionsCard({ txs }: { txs: WalletTx[] }) {
  return (
    <div className="rounded-lg border border-border-strong bg-card/60">
      <div className="flex items-center justify-between px-4 py-3 border-b border-border">
        <div className="flex items-center gap-2">
          <Wallet className="h-4 w-4 text-primary" />
          <h3 className="font-display text-base tracking-tight">Ledger</h3>
        </div>
        <span className="text-xs text-muted-foreground font-mono">{txs.length}</span>
      </div>
      {txs.length === 0 ? (
        <div className="px-4 py-8 text-center text-sm text-muted-foreground">
          no transactions yet
        </div>
      ) : (
        <ul className="divide-y divide-border/60 max-h-[420px] overflow-y-auto">
          {txs.map((t) => {
            const positive = t.amount_usd >= 0;
            return (
              <li key={t.id} className="px-4 py-2.5 hover:bg-muted/30">
                <div className="flex items-baseline justify-between gap-2">
                  <div className="flex items-baseline gap-2 min-w-0">
                    <Badge
                      variant="outline"
                      className={cn(
                        "font-mono text-[9px] uppercase tracking-[0.12em]",
                        t.kind === "topup" && "text-emerald-600 dark:text-emerald-400",
                        t.kind === "charge" && "text-muted-foreground",
                        t.kind === "adjust" && "text-amber-600 dark:text-amber-400",
                        t.kind === "refund" && "text-sky-600 dark:text-sky-400",
                      )}
                    >
                      {t.kind}
                    </Badge>
                    <span className="font-mono text-[11px] opacity-60 truncate">
                      {t.note || t.ref}
                    </span>
                  </div>
                  <span
                    className={cn(
                      "font-mono text-sm tabular font-medium shrink-0",
                      positive
                        ? "text-emerald-600 dark:text-emerald-400"
                        : "text-foreground/85",
                    )}
                  >
                    {positive ? "+" : ""}
                    {fmtUSD(t.amount_usd)}
                  </span>
                </div>
                <div className="mt-0.5 text-[11px] font-mono text-muted-foreground">
                  {fmtTime(t.created_at)}
                </div>
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}

function TopupModal({
  open,
  onClose,
  token,
  rate,
  onCreated,
}: {
  open: boolean;
  onClose: () => void;
  token: string;
  rate: ExchangeRate | null;
  onCreated: () => void;
}) {
  // Two inputs bound to the same underlying USD amount, kept in sync via
  // `lastEdited` so the user can recharge by typing either side. Submit
  // is always in USD — CNY is informational and re-derived from rate on
  // server side at order creation.
  const [usd, setUsd] = useState("10");
  const [cnyInput, setCnyInput] = useState(() => {
    return rate ? (10 * rate.cny_per_usd).toFixed(2) : "";
  });
  const [lastEdited, setLastEdited] = useState<"usd" | "cny">("usd");
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<TopupResp | null>(null);
  const [copied, setCopied] = useState(false);
  // settled flips true the instant the polling loop sees the order
  // status go non-pending. Holds the modal in a "Payment received"
  // confirmation frame for a beat before auto-closing — without it
  // users would see the modal blink away with no visual ack.
  const [settled, setSettled] = useState(false);

  useEffect(() => {
    if (!open) {
      setResult(null);
      setBusy(false);
      setCopied(false);
      setSettled(false);
    }
  }, [open]);

  // Auto-detect payment. While the modal is showing a pending order,
  // poll its status every 3s. The Z-Pay async notify is the source of
  // truth (server-side credit happens there regardless of this loop) —
  // this polling exists purely so the modal can self-close the moment
  // the wallet flips, no "I've paid" click required.
  useEffect(() => {
    if (!open || !result || settled) return;
    let cancelled = false;
    const id = setInterval(async () => {
      try {
        const o = await loadWalletOrder(token, result.out_trade_no);
        if (cancelled) return;
        if (o.status === "paid") {
          setSettled(true);
          onCreated();
          toast.success(`Payment received · +$${result.usd_credit.toFixed(2)}`);
          // Hold the success frame briefly so the user can register what
          // happened before the dialog vanishes.
          setTimeout(() => {
            if (!cancelled) onClose();
          }, 1400);
        } else if (o.status === "expired" || o.status === "failed") {
          if (!cancelled) {
            toast.error(`Order ${o.status} — please create a new one`);
            onClose();
          }
        }
      } catch {
        // network blip — next tick retries; nothing to do
      }
    }, 3000);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, [open, result, settled, token, onCreated, onClose]);

  const usdNum = Number(usd) || 0;
  const cnyEst = rate ? usdNum * rate.cny_per_usd : 0;

  // Keep the *other* field in sync whenever rate changes or the user
  // types. `lastEdited` is the source-of-truth selector: never overwrite
  // the field the user just touched.
  useEffect(() => {
    if (!rate) return;
    if (lastEdited === "usd") {
      const n = Number(usd);
      if (Number.isFinite(n) && n >= 0) setCnyInput((n * rate.cny_per_usd).toFixed(2));
    } else {
      const n = Number(cnyInput);
      if (Number.isFinite(n) && n >= 0) setUsd((n / rate.cny_per_usd).toFixed(2));
    }
  }, [usd, cnyInput, lastEdited, rate]);

  const submit = async () => {
    if (usdNum < 1 || usdNum > 1000) {
      toast.error("Amount must be between $1 and $1000");
      return;
    }
    setBusy(true);
    try {
      const r = await topupWallet(token, usdNum);
      setResult(r);
      onCreated();
    } catch (e: any) {
      toast.error("Top-up failed", { description: e.message || String(e) });
    } finally {
      setBusy(false);
    }
  };

  const copyLink = async () => {
    const url = result?.pay_url || result?.qr_code || "";
    if (!url) return;
    try {
      await navigator.clipboard.writeText(url);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      toast.error("Copy failed");
    }
  };

  return (
    <Dialog open={open} onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>{result ? "Scan to pay" : "Recharge wallet"}</DialogTitle>
        </DialogHeader>
        {!result ? (
          <div className="space-y-4">
            <div className="grid grid-cols-[1fr_auto_1fr] items-end gap-3">
              <div>
                <label className="eyebrow text-[10px] opacity-70">USD (charged)</label>
                <div className="mt-1 flex items-baseline gap-2">
                  <span className="font-mono text-xl">$</span>
                  <Input
                    type="number"
                    inputMode="decimal"
                    min="1"
                    max="1000"
                    step="0.01"
                    value={usd}
                    onInput={(e) => {
                      setUsd((e.target as HTMLInputElement).value);
                      setLastEdited("usd");
                    }}
                    className="font-mono text-2xl tabular flex-1"
                  />
                </div>
              </div>
              <div className="pb-3 text-center text-muted-foreground text-xs font-mono">
                ×{rate ? rate.cny_per_usd.toFixed(4) : "…"}
              </div>
              <div>
                <label className="eyebrow text-[10px] opacity-70">CNY (paid)</label>
                <div className="mt-1 flex items-baseline gap-2">
                  <span className="font-mono text-xl">¥</span>
                  <Input
                    type="number"
                    inputMode="decimal"
                    min="1"
                    step="0.01"
                    value={cnyInput}
                    onInput={(e) => {
                      setCnyInput((e.target as HTMLInputElement).value);
                      setLastEdited("cny");
                    }}
                    className="font-mono text-2xl tabular flex-1"
                  />
                </div>
              </div>
            </div>
            {rate && (
              <div className="text-[11px] text-muted-foreground font-mono">
                Pay any amount in CNY — your wallet is credited with the USD on
                the left. Rate frozen by the server at order creation.
              </div>
            )}
            <div className="grid grid-cols-4 gap-2">
              {[5, 10, 20, 50].map((v) => (
                <button
                  key={v}
                  type="button"
                  onClick={() => {
                    setUsd(String(v));
                    setLastEdited("usd");
                  }}
                  className={cn(
                    "rounded-md border px-3 py-1.5 text-sm font-mono",
                    String(v) === usd
                      ? "border-primary bg-primary/10 text-primary"
                      : "border-border hover:border-foreground/40",
                  )}
                >
                  ${v}
                </button>
              ))}
            </div>
            <div className="text-xs text-muted-foreground font-mono flex items-center gap-1.5">
              <span className="inline-block h-1.5 w-1.5 rounded-full bg-primary" />
              Pay via Alipay 支付宝
            </div>
            <div className="flex justify-end gap-2 pt-2">
              <Button variant="outline" onClick={onClose}>
                Cancel
              </Button>
              <Button onClick={submit} disabled={busy} className="gap-1.5">
                {busy ? (
                  <RefreshCw className="h-4 w-4 animate-spin" />
                ) : (
                  <CreditCard className="h-4 w-4" />
                )}
                Create order
              </Button>
            </div>
          </div>
        ) : (
          <div className="space-y-4">
            <div className="rounded-md border border-border/60 bg-muted/30 p-4">
              <div className="text-xs font-mono text-muted-foreground">
                order {result.out_trade_no}
              </div>
              <div className="mt-1 flex items-baseline justify-between">
                <span className="font-mono text-2xl tabular">
                  +{fmtUSD(result.usd_credit)}
                </span>
                <span className="font-mono text-sm text-muted-foreground">
                  pay {fmtCNY(result.cny_amount)}
                </span>
              </div>
            </div>
            {/* Render order: QR image > pay_url link > raw code string.
                The pay_url that Z-Pay hands back is the alipay-app-only
                "qr.alipay.com/bax…" URL — opening it in a desktop browser
                shows the "please use the Alipay app" stub, not a QR. The
                actual scannable QR is the hosted .jpg under result.img,
                so prefer that when present. */}
            {result.img ? (
              <div className="flex flex-col items-center gap-3">
                <img
                  src={result.img}
                  alt="payment QR"
                  className="rounded-md border border-border bg-white p-2"
                  style={{ width: 240, height: 240 }}
                />
                <div className="text-[11px] text-center text-muted-foreground font-mono">
                  Scan with Alipay 支付宝 to pay {fmtCNY(result.cny_amount)}
                </div>
                {result.pay_url && (
                  <a
                    href={result.pay_url}
                    target="_blank"
                    rel="noreferrer"
                    className="text-xs text-primary hover:underline inline-flex items-center gap-1"
                  >
                    <ExternalLink className="h-3 w-3" />
                    or open in Alipay app
                  </a>
                )}
              </div>
            ) : result.pay_url ? (
              <a
                href={result.pay_url}
                target="_blank"
                rel="noreferrer"
                className="block w-full text-center rounded-md bg-primary text-primary-foreground px-4 py-2.5 font-medium hover:bg-primary/90"
              >
                <ExternalLink className="inline h-4 w-4 mr-2" />
                Open in Alipay app
              </a>
            ) : result.qr_code ? (
              <div className="text-center font-mono text-xs break-all px-2 py-3 rounded-md border border-border/60 bg-muted/30">
                {result.qr_code}
              </div>
            ) : null}
            <div className="flex items-center justify-between gap-2 pt-1">
              {/* Live status pill replaces the manual "I've paid" button:
                  the modal polls the order every 3s and closes itself
                  when Z-Pay's notify lands. */}
              {settled ? (
                <span className="inline-flex items-center gap-1.5 text-emerald-600 dark:text-emerald-400 font-mono text-sm">
                  <CheckCircle2 className="h-4 w-4" /> Payment received
                </span>
              ) : (
                <span className="inline-flex items-center gap-1.5 text-muted-foreground font-mono text-xs">
                  <span className="relative inline-flex h-2 w-2">
                    <span className="absolute inline-flex h-full w-full rounded-full bg-primary opacity-75 animate-ping" />
                    <span className="relative inline-flex rounded-full h-2 w-2 bg-primary" />
                  </span>
                  Waiting for payment…
                </span>
              )}
              {(result.pay_url || result.qr_code) && !settled && (
                <Button variant="outline" size="sm" onClick={copyLink} className="gap-1.5">
                  {copied ? (
                    <CheckCircle2 className="h-4 w-4 text-emerald-500" />
                  ) : (
                    <Copy className="h-4 w-4" />
                  )}
                  {copied ? "Copied" : "Copy link"}
                </Button>
              )}
            </div>
            <p className="text-[11px] text-muted-foreground text-center">
              Pay within 15 minutes — pending orders auto-expire. This dialog closes
              the moment payment lands.
            </p>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}

// Silence unused-export warning for fmtInt — keep the import surface
// available for future extensions of the panel.
export const __keep = { fmtInt };

// ---- Invoicing --------------------------------------------------------

function InvoiceSection({ token }: { token: string }) {
  const [summary, setSummary] = useState<InvoiceSummary | null>(null);
  const [invoices, setInvoices] = useState<Invoice[]>([]);
  const [loading, setLoading] = useState(false);
  const [showDialog, setShowDialog] = useState(false);

  const refresh = useCallback(async () => {
    if (!token) return;
    setLoading(true);
    try {
      const [s, l] = await Promise.all([
        loadInvoiceSummary(token),
        loadInvoices(token),
      ]);
      setSummary(s);
      setInvoices(l.invoices || []);
    } catch (e: any) {
      // Surface only as a quiet console log — failure shouldn't blow up
      // the rest of the wallet page.
      console.warn("invoice refresh failed:", e);
    } finally {
      setLoading(false);
    }
  }, [token]);

  useEffect(() => {
    refresh();
  }, [refresh]);

  const noQuota = !summary || summary.available_cny <= 0;

  return (
    <section className="rounded-xl border border-border-strong bg-card/60 p-5 space-y-4">
      <div className="flex items-center justify-between gap-3 flex-wrap">
        <div className="flex items-center gap-2">
          <FileText className="h-5 w-5 text-primary" />
          <h3 className="font-display text-xl tracking-tight">发票申请 / Fapiao</h3>
        </div>
        <div className="flex items-center gap-2">
          <Button
            variant="outline"
            size="sm"
            onClick={refresh}
            disabled={loading}
            className="gap-1.5"
          >
            <RefreshCw className={cn("h-3.5 w-3.5", loading && "animate-spin")} />
            刷新
          </Button>
          <Button
            size="sm"
            onClick={() => setShowDialog(true)}
            disabled={noQuota}
            className="gap-1.5"
          >
            <Plus className="h-3.5 w-3.5" /> 开发票
          </Button>
        </div>
      </div>

      <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
        <SummaryCell label="累计实付 CNY" value={summary?.paid_cny} highlight />
        <SummaryCell label="已开票 CNY" value={summary?.issued_cny} />
        <SummaryCell label="待处理 CNY" value={summary?.locked_cny} muted />
        <SummaryCell label="可开票 CNY" value={summary?.available_cny} highlight />
      </div>

      {summary && (
        <p className="text-[11px] text-muted-foreground font-mono">
          按已支付 Alipay 订单累计的实际 CNY 计算 (不随汇率波动)。pending 与 issued 的发票都会占用可开票额度;rejected 后额度自动归还。
        </p>
      )}

      {invoices.length === 0 ? (
        <div className="text-sm text-muted-foreground py-4 text-center">
          暂无发票记录
        </div>
      ) : (
        <div className="space-y-2">
          {invoices.map((v) => (
            <InvoiceRow key={v.id} v={v} token={token} />
          ))}
        </div>
      )}

      <InvoiceDialog
        open={showDialog}
        onClose={() => setShowDialog(false)}
        token={token}
        summary={summary}
        onCreated={refresh}
      />
    </section>
  );
}

function SummaryCell({
  label,
  value,
  highlight,
  muted,
}: {
  label: string;
  value: number | undefined;
  highlight?: boolean;
  muted?: boolean;
}) {
  return (
    <div
      className={cn(
        "rounded-md border px-3 py-2",
        highlight
          ? "border-primary/40 bg-primary/5"
          : muted
            ? "border-border/60 bg-muted/30"
            : "border-border",
      )}
    >
      <div className="text-[10px] eyebrow opacity-70">{label}</div>
      <div className="mt-0.5 font-mono text-lg tabular">
        {value === undefined ? "···" : `¥${value.toFixed(2)}`}
      </div>
    </div>
  );
}

function InvoiceRow({ v, token }: { v: Invoice; token: string }) {
  const [busy, setBusy] = useState(false);

  const download = async () => {
    if (busy) return;
    setBusy(true);
    try {
      const blob = await downloadInvoicePDF(token, v.id);
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `invoice-${v.id}.pdf`;
      a.click();
      URL.revokeObjectURL(url);
    } catch (e: any) {
      toast.error("下载失败", { description: e.message || String(e) });
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="flex items-center justify-between gap-3 rounded-md border border-border/60 bg-background/40 px-3 py-2">
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2 flex-wrap">
          <span className="font-mono text-sm">#{v.id}</span>
          <span className="font-mono text-sm">¥{v.cny_amount.toFixed(2)}</span>
          <span className="text-sm truncate">{v.title_name}</span>
          {invoiceStatusBadge(v.status)}
        </div>
        <div className="mt-0.5 text-[11px] text-muted-foreground font-mono">
          {fmtTime(v.created_at)} · {v.contact_email}
          {v.note && <span className="opacity-70"> · {v.note}</span>}
        </div>
      </div>
      {v.status === "issued" && v.downloadable && (
        <Button variant="outline" size="sm" disabled={busy} onClick={download} className="gap-1.5">
          {busy ? <RefreshCw className="h-3.5 w-3.5 animate-spin" /> : <Download className="h-3.5 w-3.5" />}
          下载
        </Button>
      )}
    </div>
  );
}

function invoiceStatusBadge(s: Invoice["status"]) {
  switch (s) {
    case "issued":
      return (
        <span className="inline-flex items-center gap-1 text-emerald-600 dark:text-emerald-400 text-[11px] font-mono">
          <CheckCircle2 className="h-3 w-3" /> issued
        </span>
      );
    case "pending":
      return (
        <span className="inline-flex items-center gap-1 text-amber-600 dark:text-amber-400 text-[11px] font-mono">
          <Clock3 className="h-3 w-3" /> pending
        </span>
      );
    case "rejected":
      return (
        <span className="inline-flex items-center gap-1 text-destructive text-[11px] font-mono">
          <XCircle className="h-3 w-3" /> rejected
        </span>
      );
  }
}

function InvoiceDialog({
  open,
  onClose,
  token,
  summary,
  onCreated,
}: {
  open: boolean;
  onClose: () => void;
  token: string;
  summary: InvoiceSummary | null;
  onCreated: () => void;
}) {
  const [amount, setAmount] = useState("");
  const [contactEmail, setContactEmail] = useState(() => localStorage.getItem("cpa.invoice.email") || "");
  const [search, setSearch] = useState("");
  const [picks, setPicks] = useState<InvoiceTitle[]>([]);
  const [selected, setSelected] = useState<InvoiceTitle>({
    name: "",
    tax_no: "",
    address: "",
    phone: "",
    bank: "",
    bank_account: "",
  });
  const [busy, setBusy] = useState(false);
  const [searching, setSearching] = useState(false);
  const [searched, setSearched] = useState(false);
  const debRef = useRef<number | null>(null);

  // Reset when reopened.
  useEffect(() => {
    if (open) {
      setAmount(summary ? Math.max(0, summary.available_cny).toFixed(2) : "");
      setSearch("");
      setPicks([]);
      setSelected({ name: "", tax_no: "", address: "", phone: "", bank: "", bank_account: "" });
      setBusy(false);
    }
  }, [open, summary]);

  // Debounced title suggestion — kicks 350ms after the user stops typing.
  useEffect(() => {
    if (!open) return;
    if (debRef.current) window.clearTimeout(debRef.current);
    if (!search.trim()) {
      setPicks([]);
      setSearched(false);
      setSearching(false);
      return;
    }
    setSearching(true);
    debRef.current = window.setTimeout(async () => {
      try {
        const r = await suggestInvoiceTitles(token, search);
        setPicks(r.titles || []);
      } catch {
        setPicks([]);
      } finally {
        setSearching(false);
        setSearched(true);
      }
    }, 350);
    return () => {
      if (debRef.current) window.clearTimeout(debRef.current);
    };
  }, [search, token, open]);

  const apply = (t: InvoiceTitle) => {
    setSelected((prev) => ({
      ...prev,
      name: t.name,
      tax_no: t.tax_no ?? prev.tax_no,
      address: t.address ?? prev.address,
      phone: t.phone ?? prev.phone,
      bank: t.bank ?? prev.bank,
      bank_account: t.bank_account ?? prev.bank_account,
    }));
    setSearch(t.name);
  };

  const amountNum = Number(amount) || 0;
  const available = summary?.available_cny ?? 0;
  const tooHigh = amountNum > available + 0.005;

  const taxNoTrim = (selected.tax_no || "").trim().toUpperCase();
  const taxNoValid = /^[0-9A-Z]{15,20}$/.test(taxNoTrim);

  const submit = async () => {
    if (busy) return;
    if (!selected.name.trim()) {
      toast.error("请填写公司名称 (抬头)");
      return;
    }
    if (!taxNoValid) {
      toast.error("请填写有效的统一社会信用代码", {
        description: "18 位字母 / 数字 (大写)",
      });
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
    if (tooHigh) {
      toast.error(`金额超过可开票额度 ¥${available.toFixed(2)}`);
      return;
    }
    setBusy(true);
    try {
      await createInvoice(token, {
        cny_amount: amountNum,
        title: selected,
        contact_email: contactEmail.trim(),
      });
      localStorage.setItem("cpa.invoice.email", contactEmail.trim());
      toast.success("发票申请已提交");
      onCreated();
      onClose();
    } catch (e: any) {
      toast.error("提交失败", { description: e.message || String(e) });
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="max-w-lg max-h-[90vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>申请发票</DialogTitle>
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
                className={cn("font-mono text-2xl tabular flex-1", tooHigh && "border-destructive")}
              />
            </div>
            <div className="mt-1 text-[11px] text-muted-foreground font-mono flex items-center justify-between gap-2">
              <span>可开票额度 ¥{available.toFixed(2)}</span>
              <button
                type="button"
                className="text-primary hover:underline"
                onClick={() => setAmount(available.toFixed(2))}
              >
                全部
              </button>
            </div>
          </div>

          <div>
            <label className="eyebrow text-[10px] opacity-70">
              抬头名称 (公司全称) <span className="text-destructive">*</span>
            </label>
            <div className="relative mt-1">
              <Input
                placeholder="搜索或手动输入抬头…"
                value={search}
                onInput={(e) => {
                  const v = (e.target as HTMLInputElement).value;
                  setSearch(v);
                  setSelected((prev) => ({ ...prev, name: v }));
                }}
                className="font-mono pr-8"
              />
              <Search className="h-4 w-4 absolute right-2 top-1/2 -translate-y-1/2 text-muted-foreground" />
            </div>
            {picks.length > 0 && (
              <div className="mt-1 max-h-40 overflow-y-auto rounded-md border border-border/60 bg-background divide-y divide-border/40">
                {picks.map((p, i) => (
                  <button
                    type="button"
                    key={`${p.name}-${i}`}
                    onClick={() => apply(p)}
                    className="w-full text-left px-3 py-1.5 hover:bg-muted/40 flex items-center justify-between gap-2"
                  >
                    <span className="truncate text-sm">{p.name}</span>
                    <span className="text-[10px] font-mono opacity-60">
                      {p.source === "local" ? "已存" : "在线"}
                      {p.tax_no ? ` · ${p.tax_no.slice(0, 6)}…` : ""}
                    </span>
                  </button>
                ))}
              </div>
            )}
            <div className="mt-1 text-[11px] text-muted-foreground">
              {searching
                ? "正在搜索…"
                : searched && picks.length === 0
                  ? "未匹配到企业,可手动输入公司全称与统一社会信用代码"
                  : !search.trim()
                    ? "输入公司名称关键词以从企业库匹配,或直接手动填写下方字段"
                    : null}
            </div>
          </div>

          <div className="grid grid-cols-2 gap-3">
            <LabeledInput
              label="统一社会信用代码"
              required
              value={selected.tax_no || ""}
              onChange={(v) =>
                setSelected((p) => ({ ...p, tax_no: v.toUpperCase().replace(/\s+/g, "") }))
              }
              placeholder="18 位字母 / 数字"
              invalid={Boolean((selected.tax_no || "").trim()) && !taxNoValid}
            />
            <LabeledInput
              label="联系电话 (可选)"
              value={selected.phone || ""}
              onChange={(v) => setSelected((p) => ({ ...p, phone: v }))}
            />
          </div>
          <LabeledInput
            label="注册地址 (可选)"
            value={selected.address || ""}
            onChange={(v) => setSelected((p) => ({ ...p, address: v }))}
          />
          <div className="grid grid-cols-2 gap-3">
            <LabeledInput
              label="开户银行 (可选)"
              value={selected.bank || ""}
              onChange={(v) => setSelected((p) => ({ ...p, bank: v }))}
            />
            <LabeledInput
              label="银行账户 (可选)"
              value={selected.bank_account || ""}
              onChange={(v) => setSelected((p) => ({ ...p, bank_account: v }))}
            />
          </div>
          <p className="text-[11px] text-muted-foreground -mt-1">
            开具增值税普通发票只需公司名称 + 统一社会信用代码;专用发票请补全其余字段(以贵司财务提供的开票信息为准)。
          </p>
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
            <Button onClick={submit} disabled={busy || tooHigh || amountNum <= 0} className="gap-1.5">
              {busy && <RefreshCw className="h-4 w-4 animate-spin" />}
              提交申请
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}

function LabeledInput({
  label,
  value,
  onChange,
  placeholder,
  type,
  required,
  invalid,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  type?: string;
  required?: boolean;
  invalid?: boolean;
}) {
  return (
    <div>
      <label className="eyebrow text-[10px] opacity-70">
        {label}
        {required && <span className="text-destructive"> *</span>}
      </label>
      <Input
        type={type || "text"}
        value={value}
        placeholder={placeholder}
        onInput={(e) => onChange((e.target as HTMLInputElement).value)}
        className={cn("mt-1 font-mono text-sm", invalid && "border-destructive")}
      />
    </div>
  );
}

