import { useState } from "preact/hooks";
import { html } from "../../util.js";
import { api } from "../../api.js";

export function EditTokenModal({ row, onClose, onSaved }) {
  const [name, setName] = useState(row.label || "");
  const [weekly, setWeekly] = useState(row.weekly_limit > 0 ? String(row.weekly_limit) : "");
  const [group, setGroup] = useState(row.group || "");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  const save = async () => {
    setBusy(true); setErr("");
    try {
      const body = { name, group };
      const w = parseFloat(weekly);
      body.weekly_usd = !isNaN(w) && w > 0 ? w : 0;
      await api(`/admin/api/tokens/${encodeURIComponent(row.full_token)}`, { method: "PATCH", body: JSON.stringify(body) });
      onSaved();
    } catch (x) { setErr(x.message); }
    finally { setBusy(false); }
  };

  return html`
    <div class="fixed inset-0 bg-black/40 dark:bg-black/60 flex items-center justify-center z-50" onClick=${onClose}>
      <div class="bg-white dark:bg-slate-800 rounded-2xl shadow-xl w-full max-h-[90vh] overflow-y-auto max-w-xl p-7 space-y-5" onClick=${(e) => e.stopPropagation()}>
        <div class="flex items-start justify-between">
          <div>
            <h2 class="text-2xl font-semibold tracking-tight">Edit token</h2>
            <div class="mono text-xs text-slate-400 dark:text-slate-500 mt-1">${row.token}</div>
          </div>
          <button onClick=${onClose} class="text-slate-400 dark:text-slate-500 hover:text-slate-700 dark:hover:text-slate-200 text-xl leading-none">✕</button>
        </div>
        <label class="block">
          <span class="text-sm font-medium text-slate-700 dark:text-slate-300">Name</span>
          <input type="text" class="mt-1.5 w-full border border-slate-300 dark:border-slate-600 rounded-lg px-3 py-2 bg-white dark:bg-slate-900 text-base" value=${name} onInput=${(e) => setName(e.target.value)} />
        </label>
        <label class="block">
          <span class="text-sm font-medium text-slate-700 dark:text-slate-300">Weekly USD limit</span>
          <input type="number" min="0" step="0.01" class="mt-1.5 w-full border border-slate-300 dark:border-slate-600 rounded-lg px-3 py-2 mono text-sm bg-white dark:bg-slate-900" placeholder="0 = unlimited" value=${weekly} onInput=${(e) => setWeekly(e.target.value)} />
        </label>
        <label class="block">
          <span class="text-sm font-medium text-slate-700 dark:text-slate-300">Group</span>
          <input list="groups-datalist" type="text" class="mt-1.5 w-full border border-slate-300 dark:border-slate-600 rounded-lg px-3 py-2 bg-white dark:bg-slate-900 text-base" placeholder="public (shared pool)" value=${group} onInput=${(e) => setGroup(e.target.value)} />
        </label>
        ${err && html`<div class="text-sm text-rose-600 dark:text-rose-400">${err}</div>`}
        <div class="flex justify-end gap-3 pt-2">
          <button onClick=${onClose} class="px-4 py-2 rounded-lg border border-slate-300 dark:border-slate-600 hover:bg-slate-100 dark:hover:bg-slate-700 text-base">Cancel</button>
          <button disabled=${busy} onClick=${save} class="px-5 py-2 rounded-lg bg-slate-900 text-white hover:bg-slate-800 dark:bg-slate-200 dark:text-slate-900 dark:hover:bg-slate-300 disabled:opacity-50 text-base">${busy ? "Saving…" : "Save"}</button>
        </div>
      </div>
    </div>
  `;
}
