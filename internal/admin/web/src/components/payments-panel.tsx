import { useEffect, useState, useCallback } from "react";
import { toast } from "sonner";
import { RefreshCw, Receipt } from "lucide-react";
import { api } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { cn, fmtDate, fmtUSD } from "@/lib/utils";

interface AdminPaidOrder {
  out_trade_no: string;
  token: string; // masked
  label: string; // human-readable name from the tokens store (may be empty)
  cny_amount: number;
  usd_credit: number;
  rate: number;
  status: "paid" | "pending" | "expired" | "failed";
  trade_no: string;
  created_at: number;
  paid_at: number;
}

interface AdminOrdersResp {
  orders: AdminPaidOrder[];
  total_cny: number;
  total_usd: number;
  count: number;
}

const fmtCNY = (n: number): string =>
  `¥${n.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;

const fmtUnix = (ts: number): string =>
  ts > 0 ? fmtDate(new Date(ts * 1000)) : "—";

export function PaymentsPanel({ refreshTick }: { refreshTick: number }) {
  const [data, setData] = useState<AdminOrdersResp | null>(null);
  const [loading, setLoading] = useState(false);
  const [err, setErr] = useState("");

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const d = await api<AdminOrdersResp>("/admin/api/orders?status=paid&limit=1000");
      setData(d);
      setErr("");
    } catch (x: any) {
      setErr(x.message || String(x));
      toast.error("Failed to load payments", { description: x.message });
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load, refreshTick]);

  const orders = data?.orders || [];

  return (
    <section className="space-y-8">
      <div className="flex items-end justify-between gap-4 flex-wrap">
        <div>
          <div className="eyebrow mb-1.5">§ Payments</div>
          <h2 className="font-display text-3xl md:text-4xl tracking-tight">
            Top-up ledger{" "}
            <span className="text-muted-foreground">
              · {data ? `${data.count} paid order${data.count === 1 ? "" : "s"}` : "···"}
            </span>
          </h2>
          <p className="mt-2 text-sm text-muted-foreground max-w-2xl">
            Successful Z-Pay top-ups across all tokens. Pending and expired orders are hidden.
            Tokens are masked; the label column is joined from the active token registry —
            paid orders whose token has since been deleted show as "unregistered".
          </p>
        </div>
        <Button variant="outline" onClick={load} className="gap-2">
          <RefreshCw className={cn("h-4 w-4", loading && "animate-spin")} />
          <span>Refresh</span>
        </Button>
      </div>

      <div className="hud-strip">
        <div className="hud-strip-grid grid-cols-2 sm:grid-cols-3">
          <div className="metric-cell metric-cell-accent">
            <div className="relative z-10">
              <div className="eyebrow mb-2.5">Σ CNY collected</div>
              <div className="font-mono text-2xl md:text-[2rem] leading-none font-medium tracking-tight tabular text-primary">
                {data ? fmtCNY(data.total_cny) : "···"}
              </div>
            </div>
            <span aria-hidden className="metric-cell-corner" />
            <span aria-hidden className="metric-cell-spark" />
          </div>
          <div className="metric-cell">
            <div className="relative z-10">
              <div className="eyebrow mb-2.5">Σ USD credited</div>
              <div className="font-mono text-2xl md:text-[2rem] leading-none font-medium tracking-tight tabular">
                {data ? `$${data.total_usd.toFixed(2)}` : "···"}
              </div>
            </div>
            <span aria-hidden className="metric-cell-corner" />
            <span aria-hidden className="metric-cell-spark" />
          </div>
          <div className="metric-cell">
            <div className="relative z-10">
              <div className="eyebrow mb-2.5">Order count</div>
              <div className="font-mono text-2xl md:text-[2rem] leading-none font-medium tracking-tight tabular">
                {data?.count ?? "···"}
              </div>
            </div>
            <span aria-hidden className="metric-cell-corner" />
            <span aria-hidden className="metric-cell-spark" />
          </div>
        </div>
      </div>

      {err && (
        <div className="rounded-md border border-destructive/40 bg-destructive/10 px-4 py-2.5 text-sm text-destructive font-mono">
          {err}
        </div>
      )}

      <div className="bg-card border border-border-strong rounded-md overflow-hidden">
        {orders.length === 0 && !loading ? (
          <div className="py-16 px-6 flex flex-col items-center gap-3 text-muted-foreground">
            <Receipt className="h-8 w-8 opacity-40" />
            <div className="font-mono text-sm">No successful payments yet.</div>
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead className="text-left border-b border-border-strong">
                <tr className="eyebrow">
                  <th className="py-3 px-4 font-[inherit]">Paid at</th>
                  <th className="py-3 px-4 font-[inherit]">Label</th>
                  <th className="py-3 px-4 font-[inherit]">Token</th>
                  <th className="py-3 px-4 font-[inherit] text-right">CNY</th>
                  <th className="py-3 px-4 font-[inherit] text-right">USD</th>
                  <th className="py-3 px-4 font-[inherit] text-right">Rate</th>
                  <th className="py-3 px-4 font-[inherit]">Out trade no.</th>
                  <th className="py-3 px-4 font-[inherit]">Z-Pay trade no.</th>
                </tr>
              </thead>
              <tbody>
                {orders.map((o) => (
                  <tr
                    key={o.out_trade_no}
                    className="border-b border-border last:border-0 hover:bg-muted/50 transition-colors"
                  >
                    <td className="py-2.5 px-4 mono text-xs whitespace-nowrap">
                      {fmtUnix(o.paid_at || o.created_at)}
                    </td>
                    <td className="py-2.5 px-4 text-sm">
                      {o.label ? (
                        <span className="font-medium">{o.label}</span>
                      ) : (
                        <span className="text-muted-foreground italic opacity-60">unregistered</span>
                      )}
                    </td>
                    <td className="py-2.5 px-4 mono text-xs">{o.token}</td>
                    <td className="py-2.5 px-4 mono text-sm text-right tabular">
                      {fmtCNY(o.cny_amount)}
                    </td>
                    <td className="py-2.5 px-4 mono text-sm text-right tabular text-emerald-600 dark:text-emerald-400">
                      {fmtUSD(o.usd_credit)}
                    </td>
                    <td className="py-2.5 px-4 mono text-xs text-right tabular opacity-70">
                      {o.rate.toFixed(4)}
                    </td>
                    <td className="py-2.5 px-4 mono text-xs opacity-70 break-all">
                      {o.out_trade_no}
                    </td>
                    <td className="py-2.5 px-4 mono text-xs opacity-70 break-all">
                      {o.trade_no || "—"}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </section>
  );
}
