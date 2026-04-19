import { useState, useEffect, useCallback } from "preact/hooks";
import { html, fmtInt } from "../util.js";
import { api } from "../api.js";

export function PricingStats({ pricing, refreshTick }) {
  const [data, setData] = useState(null);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  const lookupPrice = (model) => {
    if (!pricing) return null;
    const m = (model || "").toLowerCase().trim();
    const models = pricing.models || {};
    if (m && models[m]) return models[m];
    if (m) {
      for (let i = m.lastIndexOf("-"); i > 0; i = m.lastIndexOf("-", i - 1)) {
        const p = models[m.slice(0, i)];
        if (p) return p;
      }
    }
    return pricing.default || null;
  };

  const load = useCallback(async () => {
    setBusy(true); setErr("");
    try {
      // limit=1 keeps the entries payload tiny; aggregates cover all matches.
      const d = await api("/admin/api/requests?limit=1");
      setData(d);
    } catch (x) { setErr(x.message); }
    finally { setBusy(false); }
  }, []);
  useEffect(() => { load(); }, [load, refreshTick]);

  const stats = (() => {
    if (!data || !pricing) return null;
    const s = data.summary || {};
    const input = s.input_tokens || 0;
    const cacheRead = s.cache_read_tokens || 0;
    const cacheCreate = s.cache_create_tokens || 0;
    const denom = input + cacheRead + cacheCreate;
    const hitRate = denom > 0 ? cacheRead / denom : 0;
    const actualCost = s.cost_usd || 0;
    let noCacheCost = 0;
    const byModel = data.by_model || {};
    for (const [name, a] of Object.entries(byModel)) {
      const p = lookupPrice(name);
      if (!p) continue;
      const ain = a.input_tokens || 0;
      const acr = a.cache_read_tokens || 0;
      const acw = a.cache_create_tokens || 0;
      const aout = a.output_tokens || 0;
      noCacheCost += (ain + acr + acw) * p.input_per_1m / 1e6;
      noCacheCost += aout * p.output_per_1m / 1e6;
    }
    return { hitRate, actualCost, noCacheCost, savings: noCacheCost - actualCost, input, cacheRead, cacheCreate };
  })();

  return html`
    <div class="grid grid-cols-1 md:grid-cols-2 gap-4 mb-4">
      <div class="bg-white dark:bg-slate-800 rounded-xl border border-slate-200 dark:border-slate-700 p-5">
        <div class="text-sm text-slate-500 dark:text-slate-400">Cache hit rate</div>
        <div class="mt-2 text-2xl font-semibold">${stats ? (stats.hitRate * 100).toFixed(2) + "%" : (busy ? "…" : "—")}</div>
        ${stats && html`<div class="mt-1 text-xs text-slate-500 dark:text-slate-400 mono">cacheR ${fmtInt(stats.cacheRead)} / (input ${fmtInt(stats.input)} + cacheR ${fmtInt(stats.cacheRead)} + cacheW ${fmtInt(stats.cacheCreate)})</div>`}
      </div>
      <div class="bg-white dark:bg-slate-800 rounded-xl border border-slate-200 dark:border-slate-700 p-5">
        <div class="text-sm text-slate-500 dark:text-slate-400">Saved by caching</div>
        <div class="mt-2 text-2xl font-semibold">${stats ? "$" + stats.savings.toFixed(4) : (busy ? "…" : "—")}</div>
        ${stats && html`<div class="mt-1 text-xs text-slate-500 dark:text-slate-400 mono">no-cache $${stats.noCacheCost.toFixed(4)} − actual $${stats.actualCost.toFixed(4)}</div>`}
      </div>
      ${err && html`<div class="md:col-span-2 text-sm text-rose-600 dark:text-rose-400">${err}</div>`}
    </div>
  `;
}
