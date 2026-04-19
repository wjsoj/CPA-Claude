import { html, fmtInt, fmtDate } from "../util.js";
import { Sparkline } from "./sparkline.js";

export function AuthCard({ a, onAction, onEdit }) {
  const slot = a.max_concurrent > 0 ? `${a.active_clients} / ${a.max_concurrent}` : `${a.active_clients} / ∞`;
  const slotRatio = a.max_concurrent > 0 ? Math.min(100, Math.round(a.active_clients / a.max_concurrent * 100)) : 0;
  let status, statusTone;
  if (a.disabled) { status = "disabled"; statusTone = "bg-slate-400 dark:bg-slate-500"; }
  else if (a.quota_exceeded) { status = "quota"; statusTone = "bg-amber-500"; }
  else if (a.hard_failure) { status = "unhealthy"; statusTone = "bg-rose-600"; }
  else if (a.healthy) { status = "healthy"; statusTone = "bg-emerald-500"; }
  else { status = "degraded"; statusTone = "bg-amber-500"; }
  const u = a.usage;
  const kindLabel = a.kind === "apikey" ? "API key" : "OAuth";
  const btnBase = "px-3 py-1.5 rounded-md border text-sm font-medium transition";
  const btnNeutral = btnBase + " border-slate-300 dark:border-slate-600 text-slate-700 dark:text-slate-200 hover:bg-slate-100 dark:hover:bg-slate-700/60";
  const btnDanger = btnBase + " border-rose-300 dark:border-rose-700/60 text-rose-600 dark:text-rose-400 hover:bg-rose-50 dark:hover:bg-rose-900/30";
  const btnAmber = btnBase + " border-amber-400 text-amber-700 dark:text-amber-300 hover:bg-amber-50 dark:hover:bg-amber-900/30";
  return html`
    <article class="bg-white dark:bg-slate-800 rounded-xl border border-slate-200 dark:border-slate-700 shadow-sm hover:shadow-md transition-shadow overflow-hidden">
      <header class="px-5 py-4 border-b border-slate-100 dark:border-slate-700/60 flex items-start justify-between gap-4">
        <div class="min-w-0 flex-1">
          <div class="flex items-center gap-2">
            <span class=${"inline-block w-2 h-2 rounded-full " + statusTone} title=${status}></span>
            <div class="text-lg font-semibold truncate">${a.label || a.id}</div>
          </div>
          <div class="mt-1 mono text-xs text-slate-400 dark:text-slate-500 truncate">${a.id}</div>
        </div>
        <div class="flex flex-col items-end gap-1 shrink-0">
          <span class=${"inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium " + (a.kind === "apikey" ? "bg-blue-100 dark:bg-blue-900/40 text-blue-700 dark:text-blue-300" : "bg-slate-100 dark:bg-slate-700 text-slate-700 dark:text-slate-200")}>${kindLabel}</span>
          <span class=${"inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium " + (a.group ? "bg-violet-100 dark:bg-violet-900/40 text-violet-700 dark:text-violet-300" : "bg-slate-100 dark:bg-slate-700/60 text-slate-500 dark:text-slate-400")} title="Credential group">${a.group || "public"}</span>
          <span class="text-xs text-slate-500 dark:text-slate-400 capitalize">${status}</span>
        </div>
      </header>

      ${a.quota_exceeded && html`
        <div class="px-5 py-3 bg-amber-50 dark:bg-amber-900/25 border-b border-amber-200 dark:border-amber-900/40 flex items-center justify-between gap-3 text-sm">
          <span class="text-amber-800 dark:text-amber-300 font-medium">Quota exceeded</span>
          <span class="text-amber-700 dark:text-amber-400 mono">
            ${a.quota_reset_at
              ? html`resets ${fmtDate(a.quota_reset_at)}`
              : "no reset time reported"}
          </span>
        </div>
      `}
      ${!a.quota_exceeded && a.failure_reason && html`
        <div class=${"px-5 py-3 border-b flex items-center justify-between gap-3 text-sm " + (a.hard_failure ? "bg-rose-50 dark:bg-rose-900/20 border-rose-200 dark:border-rose-900/40" : "bg-amber-50 dark:bg-amber-900/20 border-amber-200 dark:border-amber-900/40")}>
          <span class=${"font-medium " + (a.hard_failure ? "text-rose-800 dark:text-rose-300" : "text-amber-800 dark:text-amber-300")}>${a.hard_failure ? "Unhealthy" : "Recent failure"}</span>
          <span class=${"mono text-xs truncate ml-3 " + (a.hard_failure ? "text-rose-700 dark:text-rose-400" : "text-amber-700 dark:text-amber-400")} title=${a.failure_reason}>${a.failure_reason}</span>
        </div>
      `}
      ${a.last_client_cancel && (Date.now() - new Date(a.last_client_cancel).getTime() < 3600 * 1000) && html`
        <div class="px-5 py-3 border-b border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-900/40 flex items-center justify-between gap-3 text-sm">
          <span class="font-medium text-slate-700 dark:text-slate-300">Client canceled</span>
          <span class="mono text-xs truncate ml-3 text-slate-600 dark:text-slate-400" title=${a.client_cancel_reason}>${fmtDate(a.last_client_cancel)}${a.client_cancel_reason ? " · " + a.client_cancel_reason : ""}</span>
        </div>
      `}

      <dl class="px-5 py-4 grid grid-cols-2 gap-x-6 gap-y-3 text-sm">
        <div class="relative group">
          <dt class="text-xs text-slate-500 dark:text-slate-400 uppercase tracking-wide">Slots</dt>
          <dd class=${"mt-1 mono " + (a.active_clients > 0 && a.client_tokens && a.client_tokens.length > 0 ? "cursor-help" : "")}>
            <div>${slot}</div>
            ${a.max_concurrent > 0 && html`
              <div class="mt-1 h-1 w-full max-w-[120px] bg-slate-100 dark:bg-slate-700 rounded overflow-hidden">
                <div class=${"h-full " + (slotRatio > 80 ? "bg-amber-500" : "bg-emerald-500")} style=${"width:" + slotRatio + "%"}></div>
              </div>
            `}
          </dd>
          ${a.active_clients > 0 && a.client_tokens && a.client_tokens.length > 0 && html`
            <div class="pointer-events-none absolute left-0 top-full mt-2 z-20 min-w-[180px] max-w-[260px] opacity-0 translate-y-1 group-hover:opacity-100 group-hover:translate-y-0 transition duration-150 rounded-lg border border-slate-200 dark:border-slate-600 bg-white dark:bg-slate-900 shadow-lg px-3 py-2 text-xs">
              <div class="text-slate-500 dark:text-slate-400 uppercase tracking-wide text-[10px] mb-1">Active clients</div>
              <ul class="space-y-0.5">
                ${a.client_tokens.map(t => html`<li class="truncate text-slate-700 dark:text-slate-200">${t}</li>`)}
              </ul>
            </div>
          `}
        </div>
        <div>
          <dt class="text-xs text-slate-500 dark:text-slate-400 uppercase tracking-wide">Token exp</dt>
          <dd class="mt-1 text-sm">${a.expires_at ? fmtDate(a.expires_at) : html`<span class="text-slate-400 dark:text-slate-500">—</span>`}</dd>
        </div>
        ${a.email && html`
          <div class="col-span-2">
            <dt class="text-xs text-slate-500 dark:text-slate-400 uppercase tracking-wide">Email</dt>
            <dd class="mt-1 text-sm truncate">${a.email}</dd>
          </div>
        `}
        <div class="col-span-2">
          <dt class="text-xs text-slate-500 dark:text-slate-400 uppercase tracking-wide">Proxy</dt>
          <dd class="mt-1 mono text-xs text-slate-700 dark:text-slate-300 break-all">${a.proxy_url || html`<span class="text-slate-400 dark:text-slate-500">direct</span>`}</dd>
        </div>
        ${a.base_url && html`
          <div class="col-span-2">
            <dt class="text-xs text-slate-500 dark:text-slate-400 uppercase tracking-wide">Base URL</dt>
            <dd class="mt-1 mono text-xs break-all">${a.base_url}</dd>
          </div>
        `}
        ${a.model_map && Object.keys(a.model_map).length > 0 && html`
          <div class="col-span-2">
            <dt class="text-xs text-slate-500 dark:text-slate-400 uppercase tracking-wide">Model map (${Object.keys(a.model_map).length})</dt>
            <dd class="mt-1 space-y-0.5">
              ${Object.keys(a.model_map).sort().map((k) => html`
                <div class="mono text-xs break-all text-slate-700 dark:text-slate-300">
                  <span>${k}</span>
                  ${a.model_map[k] ? html` <span class="text-slate-400 dark:text-slate-500">→</span> <span>${a.model_map[k]}</span>` : html` <span class="text-slate-400 dark:text-slate-500 italic">(no rewrite)</span>`}
                </div>
              `)}
            </dd>
          </div>
        `}
      </dl>

      ${u && html`
        <div class="px-5 py-4 bg-slate-50 dark:bg-slate-900/40 border-y border-slate-100 dark:border-slate-700/60">
          <div class="grid grid-cols-3 gap-4 text-sm">
            <div>
              <div class="text-xs text-slate-500 dark:text-slate-400 uppercase tracking-wide">24h in/out</div>
              <div class="mt-1 mono">${fmtInt(u.sum_24h.input_tokens)} / ${fmtInt(u.sum_24h.output_tokens)}</div>
            </div>
            <div>
              <div class="text-xs text-slate-500 dark:text-slate-400 uppercase tracking-wide">Total req</div>
              <div class="mt-1 mono">${fmtInt(u.total.requests)}${u.total.errors > 0 ? html` <span class="text-rose-600 dark:text-rose-400 text-xs">(${fmtInt(u.total.errors)} err)</span>` : ""}</div>
            </div>
            <div>
              <div class="text-xs text-slate-500 dark:text-slate-400 uppercase tracking-wide">14-day</div>
              <div class="mt-1">${u.daily && u.daily.length ? html`<${Sparkline} daily=${u.daily} />` : html`<span class="text-slate-400 dark:text-slate-500 text-xs">no data</span>`}</div>
            </div>
          </div>
        </div>
      `}

      <footer class="px-5 py-3 flex gap-2 flex-wrap">
        ${a.kind === "oauth" && html`
          <button onClick=${() => onEdit(a)} class=${btnNeutral}>Edit</button>
          <button onClick=${() => onAction(a, "toggle")} class=${btnNeutral}>${a.disabled ? "Enable" : "Disable"}</button>
          <button onClick=${() => onAction(a, "refresh")} class=${btnNeutral}>Refresh token</button>
          ${a.quota_exceeded && html`<button onClick=${() => onAction(a, "clear-quota")} class=${btnAmber}>Clear quota</button>`}
          ${(a.hard_failure || (!a.healthy && !a.quota_exceeded && !a.disabled)) && html`<button onClick=${() => onAction(a, "clear-failure")} class=${btnAmber}>Mark healthy</button>`}
          <button onClick=${() => onAction(a, "delete")} class=${btnDanger + " ml-auto"}>Delete</button>
        `}
        ${a.kind === "apikey" && html`
          ${a.file_backed && html`
            <button onClick=${() => onEdit(a)} class=${btnNeutral}>Edit</button>
            <button onClick=${() => onAction(a, "toggle")} class=${btnNeutral}>${a.disabled ? "Enable" : "Disable"}</button>
          `}
          ${a.quota_exceeded && html`<button onClick=${() => onAction(a, "clear-quota")} class=${btnAmber}>Clear quota</button>`}
          ${(a.hard_failure || (!a.healthy && !a.quota_exceeded && !a.disabled)) && html`<button onClick=${() => onAction(a, "clear-failure")} class=${btnAmber}>Mark healthy</button>`}
          ${a.file_backed && html`<button onClick=${() => onAction(a, "delete")} class=${btnDanger + " ml-auto"}>Delete</button>`}
        `}
      </footer>
    </article>
  `;
}
