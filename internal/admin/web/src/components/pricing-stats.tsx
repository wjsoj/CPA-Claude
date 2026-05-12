import { useState, useEffect, useCallback } from "react";
import { api } from "@/lib/api";
import type { Pricing, RequestsResp } from "@/lib/types";
import { Card } from "@/components/ui/card";
import { fmtInt } from "@/lib/utils";
import { lookupPriceAnyProvider } from "@/lib/pricing";

interface Props {
  pricing?: Pricing;
  refreshTick: number;
}

export function PricingStats({ pricing, refreshTick }: Props) {
  const [data, setData] = useState<RequestsResp | null>(null);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  // by_model is keyed by bare model name (no provider prefix), so use the
  // any-provider lookup which scans the catalog by suffix-after-"/" with the
  // same prefix-fallback rule as the server.
  const lookupPrice = (model: string) => lookupPriceAnyProvider(pricing, model);

  const load = useCallback(async () => {
    setBusy(true);
    setErr("");
    try {
      const d = await api<RequestsResp>("/admin/api/requests?limit=1");
      setData(d);
    } catch (x: any) {
      setErr(x.message);
    } finally {
      setBusy(false);
    }
  }, []);
  useEffect(() => {
    load();
  }, [load, refreshTick]);

  const stats = (() => {
    if (!data || !pricing) return null;
    const s = data.summary;
    const input = s.input_tokens || 0;
    const cacheRead = s.cache_read_tokens || 0;
    const cacheCreate = s.cache_create_tokens || 0;
    const denom = input + cacheRead + cacheCreate;
    const hitRate = denom > 0 ? cacheRead / denom : 0;
    const actualCost = s.cost_usd || 0;
    let noCacheCost = 0;
    for (const [name, a] of Object.entries(data.by_model)) {
      const p = lookupPrice(name);
      if (!p) continue;
      const ain = a.input_tokens || 0;
      const acr = a.cache_read_tokens || 0;
      const acw = a.cache_create_tokens || 0;
      const aout = a.output_tokens || 0;
      noCacheCost += ((ain + acr + acw) * p.input_per_1m) / 1e6;
      noCacheCost += (aout * p.output_per_1m) / 1e6;
    }
    return {
      hitRate,
      actualCost,
      noCacheCost,
      savings: noCacheCost - actualCost,
      input,
      cacheRead,
      cacheCreate,
    };
  })();

  return (
    <div className="grid grid-cols-1 md:grid-cols-2 gap-4 mb-4">
      <Card className="p-5">
        <div className="text-sm text-muted-foreground">Cache hit rate</div>
        <div className="mt-2 text-2xl font-semibold">
          {stats ? (stats.hitRate * 100).toFixed(2) + "%" : busy ? "…" : "—"}
        </div>
        {stats && (
          <div className="mt-1 text-xs text-muted-foreground mono">
            cacheR {fmtInt(stats.cacheRead)} / (input {fmtInt(stats.input)} + cacheR{" "}
            {fmtInt(stats.cacheRead)} + cacheW {fmtInt(stats.cacheCreate)})
          </div>
        )}
      </Card>
      <Card className="p-5">
        <div className="text-sm text-muted-foreground">Saved by caching</div>
        <div className="mt-2 text-2xl font-semibold">
          {stats ? "$" + stats.savings.toFixed(4) : busy ? "…" : "—"}
        </div>
        {stats && (
          <div className="mt-1 text-xs text-muted-foreground mono">
            no-cache ${stats.noCacheCost.toFixed(4)} − actual ${stats.actualCost.toFixed(4)}
          </div>
        )}
      </Card>
      {err && <div className="md:col-span-2 text-sm text-destructive">{err}</div>}
    </div>
  );
}
