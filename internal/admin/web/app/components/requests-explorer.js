import { useState, useEffect, useCallback } from "preact/hooks";
import { html, fmtInt } from "../util.js";
import { api } from "../api.js";

export function RequestsExplorer({ refreshTick, pricing }) {
  // Mirror Go pricing.Catalog.Lookup: case-insensitive exact match, then
  // successively shorter hyphen-trimmed prefixes, finally the default.
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
  const costBreakdown = (r) => {
    const p = lookupPrice(r.model);
    if (!p) return null;
    const fmt = (tok, per1m) => `${fmtInt(tok)} × $${per1m.toFixed(2)}/1M = $${(tok * per1m / 1e6).toFixed(6)}`;
    const inC = (r.input_tokens || 0) * p.input_per_1m / 1e6;
    const outC = (r.output_tokens || 0) * p.output_per_1m / 1e6;
    const crC = (r.cache_read_tokens || 0) * p.cache_read_per_1m / 1e6;
    const cwC = (r.cache_create_tokens || 0) * p.cache_create_per_1m / 1e6;
    const lines = [
      `input  ${fmt(r.input_tokens || 0, p.input_per_1m)}`,
      `output ${fmt(r.output_tokens || 0, p.output_per_1m)}`,
      `cacheR ${fmt(r.cache_read_tokens || 0, p.cache_read_per_1m)}`,
      `cacheW ${fmt(r.cache_create_tokens || 0, p.cache_create_per_1m)}`,
      `total  $${(inC + outC + crC + cwC).toFixed(6)} (logged $${(r.cost_usd || 0).toFixed(6)})`,
    ];
    return lines.join("\n");
  };
  const localDate = (d) => `${d.getFullYear()}-${String(d.getMonth()+1).padStart(2,'0')}-${String(d.getDate()).padStart(2,'0')}`;
  const today = localDate(new Date());
  const sevenAgo = localDate(new Date(Date.now() - 7 * 86400000));
  const [from, setFrom] = useState(sevenAgo);
  const [to, setTo] = useState(today);
  const [client, setClient] = useState("");
  const [model, setModel] = useState("");
  const [pageSize, setPageSize] = useState(50);
  const [page, setPage] = useState(1); // 1-based
  const [clientsList, setClientsList] = useState([]);
  const [data, setData] = useState(null);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  const loadClients = useCallback(async () => {
    try {
      const d = await api("/admin/api/requests/clients");
      setClientsList(d.clients || []);
    } catch {}
  }, []);
  useEffect(() => { loadClients(); }, [loadClients]);

  const run = useCallback(async (overridePage) => {
    setBusy(true); setErr("");
    const effectivePage = overridePage != null ? overridePage : page;
    try {
      const qs = new URLSearchParams();
      if (from) qs.set("from", from);
      if (to) qs.set("to", to);
      if (client) qs.set("client", client);
      if (model) qs.set("model", model);
      if (pageSize) qs.set("limit", String(pageSize));
      qs.set("offset", String((effectivePage - 1) * pageSize));
      const d = await api("/admin/api/requests?" + qs.toString());
      setData(d);
      if (overridePage != null) setPage(overridePage);
    } catch (x) { setErr(x.message); }
    finally { setBusy(false); }
  }, [from, to, client, model, pageSize, page]);

  // Initial load + react to the global Refresh button.
  useEffect(() => { run(1); /* reset to page 1 on refresh */ }, [refreshTick]); // eslint-disable-line

  // Clicking "Query" resets to page 1 with current filters.
  const onQuery = () => run(1);

  const cost = (v) => "$" + (v || 0).toFixed(4);

  const sortedByClient = data ? Object.entries(data.by_client).sort(([,a],[,b]) => b.cost_usd - a.cost_usd) : [];
  const sortedByModel  = data ? Object.entries(data.by_model).sort(([,a],[,b]) => b.cost_usd - a.cost_usd) : [];
  const sortedByDay    = data ? Object.entries(data.by_day).sort(([a],[b]) => a.localeCompare(b)) : [];

  // derive models list from current data for the filter dropdown
  const modelsList = data ? Object.keys(data.by_model).sort() : [];

  const maxDayCost = Math.max(1e-9, ...sortedByDay.map(([, a]) => a.cost_usd));

  return html`
    <section>
    <div class="flex items-baseline justify-between mb-4">
      <h2 class="text-2xl font-semibold tracking-tight">Requests</h2>
      <span class="text-sm text-slate-500 dark:text-slate-400">${data ? `${fmtInt(data.scanned)} lines scanned · ${fmtInt(data.summary.count)} match` : ""}</span>
    </div>
    <div class="bg-white dark:bg-slate-800 rounded-xl border border-slate-200 dark:border-slate-700 overflow-hidden">
      <div class="p-4 grid grid-cols-1 md:grid-cols-6 gap-3 items-end border-b border-slate-100 dark:border-slate-700/60">
        <label class="block">
          <span class="text-sm text-slate-500 dark:text-slate-400">From</span>
          <input type="date" class="mt-1 w-full border border-slate-300 dark:border-slate-600 rounded-lg px-2 py-1.5 bg-white dark:bg-slate-900" value=${from} onInput=${(e) => setFrom(e.target.value)} />
        </label>
        <label class="block">
          <span class="text-sm text-slate-500 dark:text-slate-400">To</span>
          <input type="date" class="mt-1 w-full border border-slate-300 dark:border-slate-600 rounded-lg px-2 py-1.5 bg-white dark:bg-slate-900" value=${to} onInput=${(e) => setTo(e.target.value)} />
        </label>
        <label class="block">
          <span class="text-sm text-slate-500 dark:text-slate-400">Client</span>
          <select class="mt-1 w-full border border-slate-300 dark:border-slate-600 rounded-lg px-2 py-1.5 bg-white dark:bg-slate-900" value=${client} onChange=${(e) => setClient(e.target.value)}>
            <option value="">(any)</option>
            ${clientsList.map((c) => html`<option value=${c}>${c}</option>`)}
          </select>
        </label>
        <label class="block">
          <span class="text-sm text-slate-500 dark:text-slate-400">Model</span>
          <select class="mt-1 w-full border border-slate-300 dark:border-slate-600 rounded-lg px-2 py-1.5 bg-white dark:bg-slate-900" value=${model} onChange=${(e) => setModel(e.target.value)}>
            <option value="">(any)</option>
            ${modelsList.map((m) => html`<option value=${m}>${m}</option>`)}
          </select>
        </label>
        <label class="block">
          <span class="text-sm text-slate-500 dark:text-slate-400">Page size</span>
          <select class="mt-1 w-full border border-slate-300 dark:border-slate-600 rounded-lg px-2 py-1.5 mono text-sm bg-white dark:bg-slate-900" value=${pageSize} onChange=${(e) => setPageSize(Number(e.target.value))}>
            ${[25, 50, 100, 200, 500].map((n) => html`<option value=${n}>${n}</option>`)}
          </select>
        </label>
        <button disabled=${busy} onClick=${onQuery} class="px-3 py-1.5 rounded-lg bg-slate-900 text-white hover:bg-slate-800 dark:bg-slate-200 dark:text-slate-900 dark:hover:bg-slate-300 disabled:opacity-50">${busy ? "…" : "Query"}</button>
      </div>

      ${err && html`<div class="p-3 bg-red-50 dark:bg-red-900/30 text-red-700 dark:text-red-300 text-base">${err}</div>`}

      ${data && html`
        <div class="p-4 grid grid-cols-2 md:grid-cols-5 gap-3 bg-slate-50 dark:bg-slate-900/40 border-b border-slate-100 dark:border-slate-700/60">
          ${[
            ["Requests", fmtInt(data.summary.count)],
            ["Total $", cost(data.summary.cost_usd)],
            ["Input", fmtInt(data.summary.input_tokens)],
            ["Output", fmtInt(data.summary.output_tokens)],
            ["Errors", fmtInt(data.summary.errors)],
          ].map(([k, v]) => html`
            <div class="bg-white dark:bg-slate-800 rounded-lg border border-slate-200 dark:border-slate-700 dark:border-slate-700 px-3 py-2">
              <div class="text-xs text-slate-500 dark:text-slate-400">${k}</div>
              <div class="text-xl font-semibold">${v}</div>
            </div>
          `)}
        </div>
      `}

      ${data && sortedByDay.length > 0 && html`
        <div class="p-4 border-b border-slate-100 dark:border-slate-700/60">
          <div class="text-base font-medium text-slate-700 dark:text-slate-200 mb-2">Daily cost</div>
          <div class="flex items-end gap-[3px] h-16">
            ${sortedByDay.map(([day, a]) => {
              const pct = Math.round((a.cost_usd / maxDayCost) * 100);
              const title = `${day}: ${cost(a.cost_usd)} · ${fmtInt(a.count)} req`;
              return html`<div class="flex-1 min-w-[6px] flex flex-col items-stretch justify-end">
                <div title=${title} class="bg-slate-700 rounded-sm" style=${"height:" + Math.max(pct, a.cost_usd > 0 ? 4 : 1) + "%"}></div>
              </div>`;
            })}
          </div>
          <div class="flex justify-between mt-1 text-xs text-slate-400 dark:text-slate-500 mono">
            <span>${sortedByDay[0][0]}</span>
            <span>${sortedByDay[sortedByDay.length - 1][0]}</span>
          </div>
        </div>
      `}

      ${data && html`
        <div class="grid grid-cols-1 md:grid-cols-2 gap-4 p-4 border-b border-slate-100 dark:border-slate-700/60">
          <div>
            <div class="text-base font-medium text-slate-700 dark:text-slate-200 mb-2">By client</div>
            ${sortedByClient.length === 0 ? html`<div class="text-sm text-slate-400 dark:text-slate-500">—</div>` :
              html`<table class="w-full text-sm">
                ${sortedByClient.map(([k, a]) => html`
                  <tr class="border-b border-slate-100 dark:border-slate-700/60">
                    <td class="py-1.5 pr-3 font-medium">${k || html`<span class="text-slate-400 dark:text-slate-500">(unnamed)</span>`}</td>
                    <td class="py-1.5 mono text-right">${cost(a.cost_usd)}</td>
                    <td class="py-1.5 mono text-right text-slate-500 dark:text-slate-400 w-20">${fmtInt(a.count)} req</td>
                  </tr>
                `)}
              </table>`}
          </div>
          <div>
            <div class="text-base font-medium text-slate-700 dark:text-slate-200 mb-2">By model</div>
            ${sortedByModel.length === 0 ? html`<div class="text-sm text-slate-400 dark:text-slate-500">—</div>` :
              html`<table class="w-full text-sm">
                ${sortedByModel.map(([k, a]) => html`
                  <tr class="border-b border-slate-100 dark:border-slate-700/60">
                    <td class="py-1.5 pr-3 mono">${k}</td>
                    <td class="py-1.5 mono text-right">${cost(a.cost_usd)}</td>
                    <td class="py-1.5 mono text-right text-slate-500 dark:text-slate-400 w-20">${fmtInt(a.count)} req</td>
                  </tr>
                `)}
              </table>`}
          </div>
        </div>
      `}

      ${data && html`
        <div class="p-4">
          <div class="flex items-center justify-between mb-2">
            <div class="text-base font-medium text-slate-700 dark:text-slate-200">Recent matching entries</div>
            ${(() => {
              const total = data.summary ? (data.summary.count || 0) : 0;
              const totalPages = Math.max(1, Math.ceil(total / pageSize));
              const clampedPage = Math.min(page, totalPages);
              const first = total === 0 ? 0 : (clampedPage - 1) * pageSize + 1;
              const last = Math.min(total, clampedPage * pageSize);
              return html`
                <div class="flex items-center gap-2 text-sm text-slate-500 dark:text-slate-400">
                  <span class="mono">${fmtInt(first)}–${fmtInt(last)} / ${fmtInt(total)}</span>
                  <button disabled=${busy || clampedPage <= 1} onClick=${() => run(clampedPage - 1)} class="px-2 py-1 rounded border border-slate-300 dark:border-slate-600 dark:bg-slate-900 hover:bg-slate-50 dark:hover:bg-slate-700 disabled:opacity-40">Prev</button>
                  <span class="mono">${clampedPage} / ${totalPages}</span>
                  <button disabled=${busy || clampedPage >= totalPages} onClick=${() => run(clampedPage + 1)} class="px-2 py-1 rounded border border-slate-300 dark:border-slate-600 dark:bg-slate-900 hover:bg-slate-50 dark:hover:bg-slate-700 disabled:opacity-40">Next</button>
                </div>
              `;
            })()}
          </div>
          ${(!data.entries || data.entries.length === 0) ? html`
            <div class="text-sm text-slate-400 dark:text-slate-500 py-6 text-center">No entries on this page.</div>
          ` : html`
            <div class="overflow-x-auto">
              <table class="w-full text-sm">
                <thead class="text-left text-xs uppercase text-slate-500 dark:text-slate-400 border-b border-slate-200 dark:border-slate-700">
                  <tr>
                    <th class="py-2 pr-2">Time (UTC)</th>
                    <th class="py-2 pr-2">Client</th>
                    <th class="py-2 pr-2">Model</th>
                    <th class="py-2 pr-2">Auth</th>
                    <th class="py-2 pr-2 text-right">In</th>
                    <th class="py-2 pr-2 text-right">Out</th>
                    <th class="py-2 pr-2 text-right" title="Cache read tokens">Cache R</th>
                    <th class="py-2 pr-2 text-right" title="Cache write / creation tokens">Cache W</th>
                    <th class="py-2 pr-2 text-right">Cost</th>
                    <th class="py-2 pr-2 text-right">Status</th>
                    <th class="py-2 pr-2 text-right">Dur</th>
                  </tr>
                </thead>
                <tbody>
                  ${data.entries.map((r, i) => html`
                    <tr class=${"border-b border-slate-50 dark:border-slate-700/40 " + (r.status >= 400 ? "bg-red-50/40 dark:bg-red-900/20" : "")} key=${i}>
                      <td class="py-1 pr-2 mono text-slate-500 dark:text-slate-400">${r.ts.replace("T"," ").slice(0,19)}</td>
                      <td class="py-1 pr-2">${r.client || html`<span class="text-slate-400 dark:text-slate-500">(—)</span>`}</td>
                      <td class="py-1 pr-2 mono">${r.model}</td>
                      <td class="py-1 pr-2 mono text-xs text-slate-500 dark:text-slate-400">${r.auth_label || r.auth_id}</td>
                      <td class="py-1 pr-2 mono text-right">${fmtInt(r.input_tokens)}</td>
                      <td class="py-1 pr-2 mono text-right">${fmtInt(r.output_tokens)}</td>
                      <td class="py-1 pr-2 mono text-right text-slate-500 dark:text-slate-400">${fmtInt(r.cache_read_tokens)}</td>
                      <td class="py-1 pr-2 mono text-right text-slate-500 dark:text-slate-400">${fmtInt(r.cache_create_tokens)}</td>
                      <td class="py-1 pr-2 mono text-right cursor-help" title=${costBreakdown(r) || "pricing unavailable"}>${cost(r.cost_usd)}</td>
                      <td class=${"py-1 pr-2 mono text-right " + (r.status >= 400 ? "text-red-600 dark:text-red-400" : "")}>${r.status}</td>
                      <td class="py-1 pr-2 mono text-right text-slate-500 dark:text-slate-400">${r.duration_ms}ms</td>
                    </tr>
                  `)}
                </tbody>
              </table>
            </div>
          `}
        </div>
      `}
    </div>
    </section>
  `;
}
