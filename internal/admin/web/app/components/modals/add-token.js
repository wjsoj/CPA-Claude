import { useState } from "preact/hooks";
import { html, copyToClipboard, generateSkToken } from "../../util.js";
import { api } from "../../api.js";

export function AddTokenModal({ onClose, onSaved }) {
  const [name, setName] = useState("");
  // Pre-filled with a fresh sk-... the moment the modal opens so the
  // user can simply hit Create to save-and-copy if they don't care
  // about the name/limit. The "Regenerate" button just overwrites it.
  const [token, setTokenValue] = useState(() => generateSkToken());
  const [weekly, setWeekly] = useState("");
  const [group, setGroup] = useState("");
  const [created, setCreated] = useState(null); // once POSTed, display it large with copy
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [copied, setCopied] = useState(false);

  const regen = () => { setTokenValue(generateSkToken()); setCopied(false); };

  const copy = async () => {
    try {
      await copyToClipboard(created || token);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {}
  };

  const save = async () => {
    setBusy(true); setErr("");
    try {
      const body = { token: token.trim(), name: name.trim(), group: group.trim() };
      const w = parseFloat(weekly);
      if (!isNaN(w) && w > 0) body.weekly_usd = w;
      const d = await api("/admin/api/tokens", { method: "POST", body: JSON.stringify(body) });
      setCreated(d.token);
    } catch (x) { setErr(x.message); }
    finally { setBusy(false); }
  };

  return html`
    <div class="fixed inset-0 bg-black/40 dark:bg-black/60 flex items-center justify-center z-50" onClick=${onClose}>
      <div class="bg-white dark:bg-slate-800 rounded-2xl shadow-xl w-full max-h-[90vh] overflow-y-auto max-w-xl p-7 space-y-5" onClick=${(e) => e.stopPropagation()}>
        <div class="flex items-start justify-between">
          <h2 class="text-2xl font-semibold tracking-tight">${created ? "Token created" : "New client token"}</h2>
          <button onClick=${onClose} class="text-slate-400 dark:text-slate-500 hover:text-slate-700 dark:hover:text-slate-200 text-xl leading-none">✕</button>
        </div>
        ${created ? html`
          <div class="space-y-4">
            <div class="text-base text-slate-600 dark:text-slate-300">
              Save this token now — you won't see it again in full. Clients send it as
              <span class="mono text-sm bg-slate-100 dark:bg-slate-900 px-1.5 py-0.5 rounded">Authorization: Bearer &lt;token&gt;</span>.
            </div>
            <div class="bg-slate-50 dark:bg-slate-900 border border-slate-200 dark:border-slate-700 rounded-lg px-4 py-3 mono text-sm break-all select-all">${created}</div>
            <div class="flex justify-between gap-3">
              <button onClick=${copy} class="px-4 py-2 rounded-lg border border-slate-300 dark:border-slate-600 hover:bg-slate-100 dark:hover:bg-slate-700 text-base">${copied ? "Copied ✓" : "Copy to clipboard"}</button>
              <button onClick=${onSaved} class="px-5 py-2 rounded-lg bg-slate-900 text-white hover:bg-slate-800 dark:bg-slate-200 dark:text-slate-900 dark:hover:bg-slate-300 text-base">Done</button>
            </div>
          </div>
        ` : html`
          <div class="space-y-4">
            <label class="block">
              <span class="text-sm font-medium text-slate-700 dark:text-slate-300">Name</span>
              <input type="text" class="mt-1.5 w-full border border-slate-300 dark:border-slate-600 rounded-lg px-3 py-2 bg-white dark:bg-slate-900 text-base" placeholder="e.g. alice-laptop" value=${name} onInput=${(e) => setName(e.target.value)} />
            </label>
            <label class="block">
              <span class="text-sm font-medium text-slate-700 dark:text-slate-300">Token</span>
              <div class="mt-1.5 flex gap-2">
                <input type="text" class="flex-1 border border-slate-300 dark:border-slate-600 rounded-lg px-3 py-2 mono text-sm bg-white dark:bg-slate-900" value=${token} onInput=${(e) => setTokenValue(e.target.value)} />
                <button onClick=${regen} class="px-3 py-2 rounded-lg border border-slate-300 dark:border-slate-600 hover:bg-slate-100 dark:hover:bg-slate-700 text-sm shrink-0">Regenerate</button>
              </div>
              <span class="text-xs text-slate-500 dark:text-slate-400 mt-1 block">Format: <code class="mono">sk-</code> + 48 alphanumerics. You can paste an existing value here to import.</span>
            </label>
            <label class="block">
              <span class="text-sm font-medium text-slate-700 dark:text-slate-300">Weekly USD limit</span>
              <input type="number" min="0" step="0.01" class="mt-1.5 w-full border border-slate-300 dark:border-slate-600 rounded-lg px-3 py-2 mono text-sm bg-white dark:bg-slate-900" placeholder="0 = unlimited" value=${weekly} onInput=${(e) => setWeekly(e.target.value)} />
            </label>
            <label class="block">
              <span class="text-sm font-medium text-slate-700 dark:text-slate-300">Group</span>
              <input list="groups-datalist" type="text" class="mt-1.5 w-full border border-slate-300 dark:border-slate-600 rounded-lg px-3 py-2 bg-white dark:bg-slate-900 text-base" placeholder="public (shared pool)" value=${group} onInput=${(e) => setGroup(e.target.value)} />
              <span class="text-xs text-slate-500 dark:text-slate-400 mt-1 block">Binds this client to a named credential group. Traffic first tries that group's credentials, falling back to public if they're saturated or unhealthy.</span>
            </label>
            ${err && html`<div class="text-sm text-rose-600 dark:text-rose-400">${err}</div>`}
            <div class="flex justify-end gap-3 pt-2">
              <button onClick=${onClose} class="px-4 py-2 rounded-lg border border-slate-300 dark:border-slate-600 hover:bg-slate-100 dark:hover:bg-slate-700 text-base">Cancel</button>
              <button disabled=${busy || !token.trim()} onClick=${save} class="px-5 py-2 rounded-lg bg-slate-900 text-white hover:bg-slate-800 dark:bg-slate-200 dark:text-slate-900 dark:hover:bg-slate-300 disabled:opacity-50 text-base">${busy ? "Creating…" : "Create"}</button>
            </div>
          </div>
        `}
      </div>
    </div>
  `;
}
