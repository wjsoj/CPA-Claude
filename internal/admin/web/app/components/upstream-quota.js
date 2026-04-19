import { useState } from "preact/hooks";
import { html, fmtDate } from "../util.js";
import { api } from "../api.js";

export function UpstreamQuota({ auths }) {
  // Per-auth UI state: { [id]: { loading, error, data, ts } }
  const [state, setState] = useState({});
  const run = async (id) => {
    setState((s) => ({ ...s, [id]: { ...s[id], loading: true, error: "" } }));
    try {
      const d = await api(`/admin/api/auths/${encodeURIComponent(id)}/anthropic-usage`, { method: "POST" });
      setState((s) => ({ ...s, [id]: { loading: false, data: d, ts: Date.now() } }));
    } catch (x) {
      setState((s) => ({ ...s, [id]: { loading: false, error: x.message } }));
    }
  };
  const oauths = auths.filter((a) => a.kind === "oauth");
  if (oauths.length === 0) {
    return html`<div class="p-6 text-base text-slate-400 dark:text-slate-500">No OAuth credentials to query.</div>`;
  }
  const renderWindows = (usage) => {
    if (!usage || !usage.body) return null;
    // Schema cross-checked with CLIProxyAPI Management Center
    // (src/types/quota.ts: ClaudeUsagePayload). Real fields are:
    //   <window>.utilization  (0-100, already a percent)
    //   <window>.resets_at    (ISO8601 string)
    const keys = [
      ["five_hour", "5-hour"],
      ["seven_day", "7-day"],
      ["seven_day_oauth_apps", "7-day (OAuth apps)"],
      ["seven_day_opus", "7-day Opus"],
      ["seven_day_sonnet", "7-day Sonnet"],
      ["seven_day_cowork", "7-day Cowork"],
      ["iguana_necktie", "iguana_necktie"],
    ];
    const rows = keys
      .map(([k, label]) => [label, usage.body[k]])
      .filter(([, v]) => v && typeof v === "object" && (v.utilization != null || v.resets_at != null));
    const extra = usage.body.extra_usage;
    if (!rows.length && !extra) {
      return html`<pre class="mono text-xs text-slate-500 dark:text-slate-400 whitespace-pre-wrap">${JSON.stringify(usage.body, null, 2)}</pre>`;
    }
    const renderPctCell = (pctRaw) => {
      const pct = typeof pctRaw === "number" ? Math.round(pctRaw <= 1 ? pctRaw * 100 : pctRaw) : null;
      const color = pct == null ? "bg-slate-400" : pct >= 90 ? "bg-red-500" : pct >= 70 ? "bg-amber-500" : "bg-emerald-500";
      return { pct, color };
    };
    return html`
      <table class="w-full text-sm">
        <thead>
          <tr class="text-xs uppercase tracking-wide text-slate-500 dark:text-slate-400 border-b border-slate-200 dark:border-slate-700">
            <th class="py-2 pr-3 text-left font-medium">Window</th>
            <th class="py-2 text-right font-medium">Used</th>
            <th class="py-2 pl-2 font-medium">&nbsp;</th>
            <th class="py-2 pl-3 text-right font-medium">Resets</th>
          </tr>
        </thead>
        <tbody>
        ${rows.map(([label, w]) => {
          const { pct, color } = renderPctCell(w.utilization);
          return html`<tr class="border-b border-slate-100 dark:border-slate-700/60">
            <td class="py-2 pr-3">${label}</td>
            <td class="py-2 mono text-right">${pct != null ? pct + "%" : "—"}</td>
            <td class="py-2 pl-2 w-40">
              ${pct != null && html`<div class="h-1.5 bg-slate-100 dark:bg-slate-700 rounded overflow-hidden"><div class=${"h-full " + color} style=${`width:${Math.min(100, pct)}%`}></div></div>`}
            </td>
            <td class="py-2 pl-3 text-right">
              ${w.resets_at
                ? html`<span class="mono text-sm text-slate-700 dark:text-slate-200">${fmtDate(w.resets_at)}</span>`
                : html`<span class="text-xs text-slate-400 dark:text-slate-500">—</span>`}
            </td>
          </tr>`;
        })}
        ${extra && extra.is_enabled && html`
          <tr class="border-b border-slate-100 dark:border-slate-700/60">
            <td class="py-2 pr-3">extra credits</td>
            <td class="py-2 mono text-right">${(() => { const { pct } = renderPctCell(extra.utilization); return pct != null ? pct + "%" : "—"; })()}</td>
            <td class="py-2 pl-2 w-40">
              ${(() => { const { pct, color } = renderPctCell(extra.utilization); return pct != null && html`<div class="h-1.5 bg-slate-100 dark:bg-slate-700 rounded overflow-hidden"><div class=${"h-full " + color} style=${`width:${Math.min(100, pct)}%`}></div></div>`; })()}
            </td>
            <td class="py-2 pl-3 text-right mono text-xs text-slate-500 dark:text-slate-400">$${Number(extra.used_credits || 0).toFixed(2)} / $${Number(extra.monthly_limit || 0).toFixed(0)}</td>
          </tr>
        `}
        </tbody>
      </table>
    `;
  };
  const renderProfile = (profile) => {
    if (!profile || !profile.body) return null;
    const p = profile.body;
    // Plan is derived from two booleans on the account, per CLIProxyAPI.
    // has_claude_max → Max, has_claude_pro → Pro, otherwise Free.
    let plan = "unknown";
    if (p.account) {
      if (p.account.has_claude_max) plan = "Max";
      else if (p.account.has_claude_pro) plan = "Pro";
      else if (p.account.has_claude_max === false && p.account.has_claude_pro === false) plan = "Free";
    }
    const tier = p.organization && p.organization.rate_limit_tier;
    const email = (p.account && (p.account.email || p.account.email_address)) || "";
    return html`<div class="text-sm text-slate-500 dark:text-slate-400">plan: <span class="text-slate-800 dark:text-slate-200 font-semibold">${plan}</span>${tier && html` · tier ${tier}`}${email && html` · ${email}`}</div>`;
  };
  return html`
    <div class="divide-y divide-slate-100 dark:divide-slate-700/60">
      ${oauths.map((a) => {
        const st = state[a.id] || {};
        const usage = st.data && st.data.usage;
        const profile = st.data && st.data.profile;
        return html`
          <div class="p-4 space-y-2">
            <div class="flex items-center justify-between gap-3 flex-wrap">
              <div>
                <div class="font-medium">${a.label || a.id}</div>
                <div class="mono text-xs text-slate-400 dark:text-slate-500">${a.id}${a.proxy_url && ` · via ${a.proxy_url}`}</div>
              </div>
              <div class="flex items-center gap-2 flex-wrap">
                ${st.ts && html`<span class="text-xs text-slate-400 dark:text-slate-500">fetched ${fmtDate(new Date(st.ts).toISOString())}</span>`}
                <button disabled=${st.loading} onClick=${() => run(a.id)} class="px-3 py-1 rounded-lg border border-slate-300 dark:border-slate-600 text-sm hover:bg-slate-50 dark:hover:bg-slate-700 disabled:opacity-50">${st.loading ? "Fetching…" : st.ts ? "Refetch" : "Check upstream"}</button>
              </div>
            </div>
            ${st.error && html`<div class="text-sm text-red-600 dark:text-red-400 mono whitespace-pre-wrap">${st.error}</div>`}
            ${usage && usage.error && html`<div class="text-sm text-red-600 dark:text-red-400 mono">usage http ${usage.status}: ${usage.error}</div>`}
            ${renderProfile(profile)}
            ${renderWindows(usage)}
          </div>
        `;
      })}
    </div>
  `;
}
