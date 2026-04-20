import { useState, useEffect, useCallback } from "preact/hooks";
import { html, fmtInt, fmtDate, isoWeekRange } from "../util.js";
import { api, setToken } from "../api.js";
import { AuthCard } from "./auth-card.js";
import { UpstreamQuota } from "./upstream-quota.js";
import { RequestsExplorer } from "./requests-explorer.js";
import { PricingStats } from "./pricing-stats.js";
import { CopyTokenBtn } from "./copy-token-btn.js";
import { EditModal } from "./modals/edit-auth.js";
import { UploadModal } from "./modals/upload.js";
import { APIKeyModal } from "./modals/apikey.js";
import { OAuthModal } from "./modals/oauth.js";
import { AddTokenModal } from "./modals/add-token.js";
import { EditTokenModal } from "./modals/edit-token.js";
import { confirmDialog, notify } from "./notice.js";

export function Dashboard({ onLogout }) {
  const [data, setData] = useState(null);
  const [err, setErr] = useState("");
  const [editing, setEditing] = useState(null);
  const [uploading, setUploading] = useState(false);
  const [oauthing, setOauthing] = useState(false);
  const [apikeying, setAPIKeying] = useState(false);
  const [addingToken, setAddingToken] = useState(false);
  const [editingToken, setEditingToken] = useState(null);
  const [lastTick, setLastTick] = useState(Date.now());
  // Tab selection — split the dense page into focused views.
  // Persisted to localStorage so a refresh lands on the same view.
  const [tab, setTab] = useState(() => localStorage.getItem("cpa.admin.tab") || "overview");
  useEffect(() => { localStorage.setItem("cpa.admin.tab", tab); }, [tab]);
  // Bumped only on explicit Refresh click. Child components (e.g.
  // RequestsExplorer) watch this to know when to re-pull their own
  // data. The 10s auto-interval intentionally does NOT bump it — we
  // don't want the Requests pane silently re-running possibly
  // expensive queries every 10 seconds.
  const [refreshTick, setRefreshTick] = useState(0);

  const refresh = useCallback(async () => {
    try {
      const d = await api("/admin/api/summary");
      setData(d); setErr(""); setLastTick(Date.now());
    } catch (x) {
      if (x.status === 401) { setToken(""); onLogout(); return; }
      setErr(x.message);
    }
  }, [onLogout]);

  const manualRefresh = useCallback(() => {
    refresh();
    setRefreshTick((t) => t + 1);
  }, [refresh]);

  useEffect(() => { refresh(); const t = setInterval(refresh, 10000); return () => clearInterval(t); }, [refresh]);

  const onDeleteToken = async (cl) => {
    if (!cl.full_token) return;
    const ok = await confirmDialog({
      title: "Delete client token",
      message: `Token "${cl.label || cl.token}" will be removed. Clients using it stop working immediately. This cannot be undone.`,
      confirmLabel: "Delete",
      danger: true,
    });
    if (!ok) return;
    try {
      await api(`/admin/api/tokens/${encodeURIComponent(cl.full_token)}`, { method: "DELETE" });
      await refresh();
    } catch (x) { notify({ title: "Delete failed", message: x.message }); }
  };

  const onAction = async (a, act) => {
    try {
      if (act === "toggle") {
        await api(`/admin/api/auths/${encodeURIComponent(a.id)}`, {
          method: "PATCH",
          body: JSON.stringify({ disabled: !a.disabled }),
        });
      } else if (act === "refresh") {
        await api(`/admin/api/auths/${encodeURIComponent(a.id)}/refresh`, { method: "POST" });
      } else if (act === "clear-quota") {
        await api(`/admin/api/auths/${encodeURIComponent(a.id)}/clear-quota`, { method: "POST" });
      } else if (act === "clear-failure") {
        await api(`/admin/api/auths/${encodeURIComponent(a.id)}/clear-failure`, { method: "POST" });
      } else if (act === "delete") {
        const ok = await confirmDialog({
          title: "Delete credential",
          message: `${a.label || a.id} will be removed and its JSON file deleted. Any in-flight sessions on it will fail.`,
          confirmLabel: "Delete",
          danger: true,
        });
        if (!ok) return;
        await api(`/admin/api/auths/${encodeURIComponent(a.id)}`, { method: "DELETE" });
      }
      await refresh();
    } catch (x) { notify({ title: "Action failed", message: x.message }); }
  };

  const oauths = data ? data.auths.filter((a) => a.kind === "oauth") : [];
  const apikeys = data ? data.auths.filter((a) => a.kind === "apikey") : [];
  // Distinct group names across credentials and client tokens. Powers the
  // <datalist> that every group input autocompletes against. "public" is
  // always offered as a hint even when no one has picked it yet.
  const knownGroups = (() => {
    const s = new Set(["public"]);
    if (data) {
      for (const a of data.auths) if (a.group) s.add(a.group);
      for (const c of data.clients) if (c.group) s.add(c.group);
    }
    return Array.from(s).sort();
  })();
  const totalCreds = oauths.length + apikeys.length;
  const healthyCreds = data ? data.auths.filter((a) => a.healthy).length : 0;
  const healthLabel = healthyCreds + " / " + totalCreds + " healthy";
  const totals = { in: 0, out: 0, req: 0, err: 0, in24: 0, out24: 0 };
  if (data) for (const a of data.auths) {
    const t = a.usage && a.usage.total;
    if (t) {
      totals.in += t.input_tokens || 0;
      totals.out += t.output_tokens || 0;
      totals.req += t.requests || 0;
      totals.err += t.errors || 0;
    }
    const h24 = a.usage && a.usage.sum_24h;
    if (h24) {
      totals.in24 += h24.input_tokens || 0;
      totals.out24 += h24.output_tokens || 0;
    }
  }

  return html`
    <div class="max-w-7xl mx-auto p-8 space-y-8">
      <header class="flex items-center justify-between">
        <div>
          <h1 class="text-4xl font-semibold tracking-tight">CPA-Claude</h1>
          <p class="text-base text-slate-500 dark:text-slate-400">Active window: ${data ? data.active_window_minutes : "…"} min
          ${data && data.default_proxy_url ? html` · default proxy ${data.default_proxy_url}` : ""}</p>
        </div>
        <div class="flex gap-2">
          <button onClick=${() => setOauthing(true)} class="px-3 py-1.5 rounded-lg bg-slate-900 text-white hover:bg-slate-800 dark:bg-slate-200 dark:text-slate-900 dark:hover:bg-slate-300 text-base">Sign in with Claude</button>
          <button onClick=${() => setAPIKeying(true)} class="px-3 py-1.5 rounded-lg border border-slate-300 dark:border-slate-600 dark:bg-slate-900 text-base hover:bg-slate-50 dark:hover:bg-slate-700">Add API key</button>
          <button onClick=${() => setUploading(true)} class="px-3 py-1.5 rounded-lg border border-slate-300 dark:border-slate-600 dark:bg-slate-900 text-base hover:bg-slate-50 dark:hover:bg-slate-700">Upload JSON</button>
          <button onClick=${manualRefresh} class="px-3 py-1.5 rounded-lg border border-slate-300 dark:border-slate-600 dark:bg-slate-900 hover:bg-slate-50 dark:hover:bg-slate-700 text-base">Refresh</button>
          <button onClick=${() => { setToken(""); onLogout(); }} class="px-3 py-1.5 rounded-lg border border-slate-300 dark:border-slate-600 dark:bg-slate-900 hover:bg-slate-50 dark:hover:bg-slate-700 text-base">Logout</button>
        </div>
      </header>

      ${err && html`<div class="rounded-lg bg-red-50 dark:bg-red-900/30 text-red-700 dark:text-red-300 px-4 py-2 text-base">${err}</div>`}

      <datalist id="groups-datalist">
        ${knownGroups.map((g) => html`<option value=${g}></option>`)}
      </datalist>

      <section class="grid grid-cols-2 md:grid-cols-6 gap-4">
        ${[
          ["Credentials", healthLabel],
          ["OAuth", oauths.length],
          ["API keys", apikeys.length],
          ["24h in", fmtInt(totals.in24)],
          ["Σ in", fmtInt(totals.in)],
          ["Σ out", fmtInt(totals.out)],
        ].map(([k, v]) => html`
          <div class="bg-white dark:bg-slate-800 rounded-xl border border-slate-200 dark:border-slate-700 dark:border-slate-700 p-5">
            <div class="text-sm text-slate-500 dark:text-slate-400">${k}</div>
            <div class="mt-2 text-2xl font-semibold">${v}</div>
          </div>
        `)}
      </section>

      <nav class="flex gap-2 border-b border-slate-200 dark:border-slate-700 -mb-2">
        ${[
          ["overview", "Overview"],
          ["requests", "Requests"],
          ["pricing", "Pricing"],
        ].map(([k, label]) => html`
          <button onClick=${() => setTab(k)} class=${"px-5 py-3 text-lg font-medium border-b-2 -mb-px transition " + (tab === k ? "border-slate-900 text-slate-900 dark:border-slate-100 dark:text-slate-100" : "border-transparent text-slate-500 dark:text-slate-400 hover:text-slate-700 dark:hover:text-slate-200")}>${label}</button>
        `)}
      </nav>

      ${tab === "overview" && html`
      <section>
        <div class="flex items-baseline justify-between mb-4">
          <h2 class="text-2xl font-semibold tracking-tight">Credentials</h2>
          <span class="text-sm text-slate-500 dark:text-slate-400">updated ${fmtDate(new Date(lastTick).toISOString())}</span>
        </div>
        ${!data ? html`<div class="p-10 text-center text-slate-400 dark:text-slate-500 bg-white dark:bg-slate-800 rounded-xl border border-slate-200 dark:border-slate-700">loading…</div>` :
          ([...oauths, ...apikeys].length === 0 ? html`<div class="p-10 text-center text-slate-400 dark:text-slate-500 bg-white dark:bg-slate-800 rounded-xl border border-slate-200 dark:border-slate-700">No credentials yet — use the buttons above to add one.</div>` :
          html`
          <div class="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-5">
            ${[...oauths, ...apikeys].map((a) => html`<${AuthCard} key=${a.id} a=${a} onAction=${onAction} onEdit=${setEditing} />`)}
          </div>
        `)}
      </section>

      <section>
        <div class="flex items-baseline justify-between mb-4 gap-4 flex-wrap">
          <h2 class="text-2xl font-semibold tracking-tight">Client tokens <span class="text-base font-normal text-slate-500 dark:text-slate-400 ml-2">week ${data && data.current_week ? data.current_week : "…"}${data && data.current_week ? html` <span class="text-slate-400 dark:text-slate-500">(${isoWeekRange(data.current_week)} UTC)</span>` : ""}</span></h2>
          <button onClick=${() => setAddingToken(true)} class="px-4 py-2 rounded-lg bg-slate-900 text-white hover:bg-slate-800 dark:bg-slate-200 dark:text-slate-900 dark:hover:bg-slate-300 text-base font-medium">+ New token</button>
        </div>
        <div class="bg-white dark:bg-slate-800 rounded-xl border border-slate-200 dark:border-slate-700 overflow-hidden">
        ${!data ? html`<div class="p-10 text-center text-slate-400 dark:text-slate-500">loading…</div>` :
          !data.clients.length ? html`<div class="p-10 text-center text-slate-400 dark:text-slate-500">No client tokens yet — click <b>New token</b> above, or the proxy is running in open mode.</div>` :
          html`
          <div class="overflow-x-auto">
          <table class="w-full text-base">
            <thead class="text-left text-sm uppercase text-slate-500 dark:text-slate-400 border-b border-slate-200 dark:border-slate-700">
              <tr>
                <th class="py-3 px-4">Name</th>
                <th class="py-3 px-4">Token</th>
                <th class="py-3 px-4">Group</th>
                <th class="py-3 px-4">Weekly spend</th>
                <th class="py-3 px-4">Limit</th>
                <th class="py-3 px-4 cursor-help" title="Lifetime cumulative spend per client — persisted in usage state, not derived from request logs. Not affected by the billing week reset or log retention.">Total</th>
                <th class="py-3 px-4">Last used</th>
                <th class="py-3 px-4 text-right">Actions</th>
              </tr>
            </thead>
            <tbody>
              ${data.clients.map((cl) => html`
                <tr class="border-b border-slate-100 dark:border-slate-700/60 hover:bg-slate-50/60 dark:hover:bg-slate-700/40">
                  <td class="py-3 px-4">
                    <div class="font-medium">${cl.label || html`<span class="text-slate-400 dark:text-slate-500">(unnamed)</span>`}</div>
                    ${cl.from_config && html`<div class="text-xs text-slate-400 dark:text-slate-500 mt-0.5">from config.yaml · read-only</div>`}
                  </td>
                  <td class="py-3 px-4 mono text-sm">${cl.token}</td>
                  <td class="py-3 px-4">
                    <span class=${"inline-flex items-center px-2 py-0.5 rounded-full text-xs font-medium " + (cl.group ? "bg-violet-100 dark:bg-violet-900/40 text-violet-700 dark:text-violet-300" : "bg-slate-100 dark:bg-slate-700/60 text-slate-500 dark:text-slate-400")}>${cl.group || "public"}</span>
                  </td>
                  <td class="py-3 px-4 mono text-sm">
                    <div class=${"font-semibold " + (cl.blocked ? "text-rose-600 dark:text-rose-400" : cl.weekly_limit && cl.weekly_usd / cl.weekly_limit > 0.8 ? "text-amber-600 dark:text-amber-400" : "")}>$${cl.weekly_usd.toFixed(4)}</div>
                    ${cl.weekly_limit > 0 && html`
                      <div class="mt-1 h-1.5 w-28 bg-slate-100 dark:bg-slate-700 rounded overflow-hidden">
                        <div class=${"h-full " + (cl.blocked ? "bg-rose-500" : cl.weekly_usd / cl.weekly_limit > 0.8 ? "bg-amber-500" : "bg-emerald-500")} style=${`width:${Math.min(100, Math.round(cl.weekly_usd / cl.weekly_limit * 100))}%`}></div>
                      </div>
                    `}
                  </td>
                  <td class="py-3 px-4 mono text-sm">${cl.weekly_limit > 0 ? "$" + cl.weekly_limit.toFixed(2) : html`<span class="text-slate-400 dark:text-slate-500">none</span>`}</td>
                  <td class="py-3 px-4 mono text-sm">
                    <div>$${cl.total.cost_usd.toFixed(4)}</div>
                    <div class="text-xs text-slate-500 dark:text-slate-400">${fmtInt(cl.total.requests)} req</div>
                  </td>
                  <td class="py-3 px-4 text-sm">${cl.last_used ? fmtDate(cl.last_used) : html`<span class="text-slate-400 dark:text-slate-500">—</span>`}</td>
                  <td class="py-3 px-4 text-right">
                    ${cl.full_token ? html`
                      <div class="flex gap-2 justify-end">
                        <${CopyTokenBtn} token=${cl.full_token} />
                        <button onClick=${() => setEditingToken(cl)} class="px-3 py-1 rounded-md border border-slate-300 dark:border-slate-600 text-sm hover:bg-slate-100 dark:hover:bg-slate-700">Edit</button>
                        ${cl.managed && html`<button onClick=${() => onDeleteToken(cl)} class="px-3 py-1 rounded-md border border-rose-300 dark:border-rose-700/60 text-rose-600 dark:text-rose-400 text-sm hover:bg-rose-50 dark:hover:bg-rose-900/30">Delete</button>`}
                      </div>
                    ` : html`<span class="text-xs text-slate-400 dark:text-slate-500">—</span>`}
                  </td>
                </tr>
              `)}
            </tbody>
          </table>
          </div>
        `}
        </div>
      </section>

      <section>
        <div class="flex items-baseline justify-between mb-4">
          <h2 class="text-2xl font-semibold tracking-tight">Upstream quota</h2>
          <span class="text-sm text-slate-500 dark:text-slate-400">manual · each query goes through that credential's proxy</span>
        </div>
        <div class="bg-white dark:bg-slate-800 rounded-xl border border-slate-200 dark:border-slate-700 overflow-hidden">
          ${!data ? html`<div class="p-10 text-center text-slate-400 dark:text-slate-500">loading…</div>` : html`<${UpstreamQuota} auths=${data.auths} />`}
        </div>
      </section>
      `}

      ${tab === "requests" && html`
      <${RequestsExplorer} refreshTick=${refreshTick} pricing=${data && data.pricing} />
      `}

      ${tab === "pricing" && html`
      <section>
        <div class="flex items-baseline justify-between mb-4">
          <h2 class="text-2xl font-semibold tracking-tight">Pricing table</h2>
          <span class="text-sm text-slate-500 dark:text-slate-400">${data && data.pricing ? Object.keys(data.pricing.models).length : "…"} models · edit in config.yaml pricing.models</span>
        </div>
        <${PricingStats} pricing=${data && data.pricing} refreshTick=${refreshTick} />
        <div class="bg-white dark:bg-slate-800 rounded-xl border border-slate-200 dark:border-slate-700 overflow-hidden">
        ${data && data.pricing && html`
          <div class="overflow-x-auto">
          <table class="w-full text-base">
            <thead class="text-left text-sm uppercase text-slate-500 dark:text-slate-400 border-b border-slate-200 dark:border-slate-700">
              <tr>
                <th class="py-2 px-3">Model</th>
                <th class="py-2 px-3">Input / 1M</th>
                <th class="py-2 px-3">Output / 1M</th>
                <th class="py-2 px-3">Cache-read / 1M</th>
                <th class="py-2 px-3">Cache-create / 1M</th>
              </tr>
            </thead>
            <tbody>
              ${Object.entries(data.pricing.models).sort(([a],[b]) => a.localeCompare(b)).map(([name, p]) => html`
                <tr class="border-b border-slate-100 dark:border-slate-700/60">
                  <td class="py-2 px-3 mono text-sm">${name}</td>
                  <td class="py-2 px-3 mono text-sm">$${p.input_per_1m.toFixed(2)}</td>
                  <td class="py-2 px-3 mono text-sm">$${p.output_per_1m.toFixed(2)}</td>
                  <td class="py-2 px-3 mono text-sm">$${p.cache_read_per_1m.toFixed(2)}</td>
                  <td class="py-2 px-3 mono text-sm">$${p.cache_create_per_1m.toFixed(2)}</td>
                </tr>
              `)}
              <tr class="bg-slate-50">
                <td class="py-2 px-3 mono text-sm text-slate-500 dark:text-slate-400">(default / fallback)</td>
                <td class="py-2 px-3 mono text-sm">$${data.pricing.default.input_per_1m.toFixed(2)}</td>
                <td class="py-2 px-3 mono text-sm">$${data.pricing.default.output_per_1m.toFixed(2)}</td>
                <td class="py-2 px-3 mono text-sm">$${data.pricing.default.cache_read_per_1m.toFixed(2)}</td>
                <td class="py-2 px-3 mono text-sm">$${data.pricing.default.cache_create_per_1m.toFixed(2)}</td>
              </tr>
            </tbody>
          </table>
          </div>
        `}
        </div>
      </section>
      `}

      <footer class="text-sm text-slate-400 dark:text-slate-500 text-center py-4">
        v1 · credentials and client tokens are mutable from the panel · config.yaml-defined entries stay read-only
      </footer>

      ${editing && html`<${EditModal} auth=${editing} onClose=${() => setEditing(null)} onSaved=${() => { setEditing(null); refresh(); }} />`}
      ${uploading && html`<${UploadModal} onClose=${() => setUploading(false)} onSaved=${() => { setUploading(false); refresh(); }} />`}
      ${oauthing && html`<${OAuthModal} onClose=${() => setOauthing(false)} onSaved=${() => { setOauthing(false); refresh(); }} />`}
      ${apikeying && html`<${APIKeyModal} onClose=${() => setAPIKeying(false)} onSaved=${() => { setAPIKeying(false); refresh(); }} />`}
      ${addingToken && html`<${AddTokenModal} onClose=${() => setAddingToken(false)} onSaved=${() => { setAddingToken(false); refresh(); }} />`}
      ${editingToken && html`<${EditTokenModal} row=${editingToken} onClose=${() => setEditingToken(null)} onSaved=${() => { setEditingToken(null); refresh(); }} />`}
    </div>
  `;
}
