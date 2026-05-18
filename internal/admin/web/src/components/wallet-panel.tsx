// WalletPanel renders the per-token balance dashboard on /status. Users
// enter their access token once (persisted to localStorage as the "active
// token" — distinct from the multi-token saved list the Usage Lookup tab
// keeps); from that point on the panel shows their balance, recent
// transactions, and orders, with a Recharge button that opens a modal
// driving the Z-Pay top-up flow.

import { useCallback, useEffect, useState } from "react";
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
} from "lucide-react";
import {
  loadActiveToken,
  saveActiveToken,
  loadWalletBalance,
  loadWalletTransactions,
  loadWalletOrders,
  loadWalletOrder,
  topupWallet,
  loadExchangeRate,
  type WalletBalance,
  type WalletTx,
  type WalletOrder,
  type ExchangeRate,
  type TopupResp,
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
              {o.status === "pending" && (o.img || o.pay_url || o.qr_code) && (
                <div className="mt-2 flex gap-3 flex-wrap">
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
  const [usd, setUsd] = useState("10");
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<TopupResp | null>(null);
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    if (!open) {
      setResult(null);
      setBusy(false);
      setCopied(false);
    }
  }, [open]);

  const usdNum = Number(usd) || 0;
  const cnyEst = rate ? usdNum * rate.cny_per_usd : 0;

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
            <div>
              <label className="eyebrow text-[10px] opacity-70">amount (USD)</label>
              <div className="mt-1 flex items-baseline gap-2">
                <span className="font-mono text-2xl">$</span>
                <Input
                  type="number"
                  inputMode="decimal"
                  min="1"
                  max="1000"
                  step="1"
                  value={usd}
                  onInput={(e) => setUsd((e.target as HTMLInputElement).value)}
                  className="font-mono text-2xl tabular flex-1"
                />
              </div>
              {rate && usdNum > 0 && (
                <div className="mt-1 text-xs text-muted-foreground font-mono">
                  ≈ ¥{cnyEst.toFixed(2)} at 1 USD = ¥{rate.cny_per_usd.toFixed(4)}
                </div>
              )}
            </div>
            <div className="grid grid-cols-2 gap-2">
              {[5, 10, 20, 50].map((v) => (
                <button
                  key={v}
                  type="button"
                  onClick={() => setUsd(String(v))}
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
            <div className="flex justify-end gap-2 pt-1">
              {(result.pay_url || result.qr_code) && (
                <Button variant="outline" onClick={copyLink} className="gap-1.5">
                  {copied ? (
                    <CheckCircle2 className="h-4 w-4 text-emerald-500" />
                  ) : (
                    <Copy className="h-4 w-4" />
                  )}
                  {copied ? "Copied" : "Copy link"}
                </Button>
              )}
              <Button
                onClick={() => {
                  onClose();
                  onCreated();
                }}
              >
                I've paid
              </Button>
            </div>
            <p className="text-[11px] text-muted-foreground text-center">
              Pay within 15 minutes — pending orders auto-expire. The panel polls every
              4s and updates your balance the moment payment lands.
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
