import { useState, useEffect } from "preact/hooks";
import { html, copyToClipboard, fmtDate, fmtInt } from "../../util.js";
import { api } from "../../api.js";
import { confirmDialog, notify } from "../notice.js";

export function EditTokenModal({ row, onClose, onSaved }) {
  const [name, setName] = useState(row.label || "");
  const [weekly, setWeekly] = useState(row.weekly_limit > 0 ? String(row.weekly_limit) : "");
  const [group, setGroup] = useState(row.group || "");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  // Orphan-token list for the "inherit usage" picker. Fetched lazily on
  // mount so opening Edit for a fresh token doesn't stall on a big scan.
  const [orphans, setOrphans] = useState(null); // null = loading, [] = none
  const [pickedOrphan, setPickedOrphan] = useState("");
  const [merging, setMerging] = useState(false);

  // If Reset succeeds we flip into a one-time reveal panel before closing.
  const [resetToken, setResetToken] = useState("");
  const [resetCopied, setResetCopied] = useState(false);
  const [resetting, setResetting] = useState(false);

  useEffect(() => {
    let cancel = false;
    api("/admin/api/orphan-tokens").then((d) => {
      if (!cancel) setOrphans(d.orphans || []);
    }).catch(() => { if (!cancel) setOrphans([]); });
    return () => { cancel = true; };
  }, []);

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

  const doReset = async () => {
    const ok = await confirmDialog({
      title: "Reset token",
      message: "A new random token will be generated. The current token stops working immediately — every client using it must be updated with the new value. Usage history (weekly spend, totals) stays on this row.",
      confirmLabel: "Reset",
      danger: true,
    });
    if (!ok) return;
    setResetting(true);
    try {
      const d = await api(`/admin/api/tokens/${encodeURIComponent(row.full_token)}/reset`, { method: "POST" });
      setResetToken(d.token);
    } catch (x) {
      notify({ title: "Reset failed", message: x.message });
    } finally { setResetting(false); }
  };

  const doInherit = async () => {
    if (!pickedOrphan) return;
    const src = orphans.find((o) => o.token === pickedOrphan);
    const ok = await confirmDialog({
      title: "Inherit usage",
      message: `Merge historical usage from ${src ? (src.label || src.masked) : "the selected orphan"} into "${row.label || row.token}"? Weekly spend and totals accumulate. The orphan row disappears. This can't be undone.`,
      confirmLabel: "Merge",
    });
    if (!ok) return;
    setMerging(true);
    try {
      await api(`/admin/api/tokens/${encodeURIComponent(row.full_token)}/inherit`, {
        method: "POST",
        body: JSON.stringify({ from: pickedOrphan }),
      });
      notify({ title: "Usage merged", kind: "success" });
      onSaved();
    } catch (x) {
      notify({ title: "Merge failed", message: x.message });
    } finally { setMerging(false); }
  };

  const copyReset = async () => {
    try {
      await copyToClipboard(resetToken);
      setResetCopied(true);
      setTimeout(() => setResetCopied(false), 2000);
    } catch {}
  };

  return html`
    <div class="fixed inset-0 bg-black/40 dark:bg-black/60 flex items-center justify-center z-50 p-4" onClick=${onClose}>
      <div class="bg-white dark:bg-slate-800 rounded-2xl shadow-xl w-full max-h-[90vh] overflow-y-auto max-w-xl p-7 space-y-5" onClick=${(e) => e.stopPropagation()}>
        <div class="flex items-start justify-between">
          <div>
            <h2 class="text-2xl font-semibold tracking-tight">${resetToken ? "Token reset" : "Edit token"}</h2>
            <div class="mono text-xs text-slate-400 dark:text-slate-500 mt-1">${row.token}</div>
          </div>
          <button onClick=${onClose} class="text-slate-400 dark:text-slate-500 hover:text-slate-700 dark:hover:text-slate-200 text-xl leading-none">✕</button>
        </div>

        ${resetToken ? html`
          <div class="space-y-4">
            <div class="text-base text-slate-600 dark:text-slate-300">
              New token — save it now, you won't see the full value again. Every client using the old token needs to switch to this one.
            </div>
            <div class="bg-slate-50 dark:bg-slate-900 border border-slate-200 dark:border-slate-700 rounded-lg px-4 py-3 mono text-sm break-all select-all">${resetToken}</div>
            <div class="flex justify-between gap-3">
              <button onClick=${copyReset} class="px-4 py-2 rounded-lg border border-slate-300 dark:border-slate-600 hover:bg-slate-100 dark:hover:bg-slate-700 text-base">${resetCopied ? "Copied ✓" : "Copy to clipboard"}</button>
              <button onClick=${onSaved} class="px-5 py-2 rounded-lg bg-slate-900 text-white hover:bg-slate-800 dark:bg-slate-200 dark:text-slate-900 dark:hover:bg-slate-300 text-base">Done</button>
            </div>
          </div>
        ` : html`
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

          <div class="border-t border-slate-200 dark:border-slate-700 pt-5 space-y-3">
            <div>
              <div class="text-sm font-medium text-slate-700 dark:text-slate-300">Reset token</div>
              <div class="text-xs text-slate-500 dark:text-slate-400 mt-0.5">Issue a new random <code class="mono">sk-…</code>; the old value stops working. Usage history stays on this row.</div>
            </div>
            <button disabled=${resetting} onClick=${doReset} class="px-4 py-2 rounded-lg border border-amber-400 dark:border-amber-600/80 text-amber-700 dark:text-amber-300 hover:bg-amber-50 dark:hover:bg-amber-900/30 text-sm disabled:opacity-50">${resetting ? "Resetting…" : "Reset token"}</button>
          </div>

          ${orphans && orphans.length > 0 && html`
            <div class="border-t border-slate-200 dark:border-slate-700 pt-5 space-y-3">
              <div>
                <div class="text-sm font-medium text-slate-700 dark:text-slate-300">Inherit usage from orphan</div>
                <div class="text-xs text-slate-500 dark:text-slate-400 mt-0.5">Fold a deleted token's historical spend into this one. Only unregistered tokens (no Edit/Delete actions) appear here.</div>
              </div>
              <select value=${pickedOrphan} onChange=${(e) => setPickedOrphan(e.target.value)} class="w-full border border-slate-300 dark:border-slate-600 rounded-lg px-3 py-2 bg-white dark:bg-slate-900 text-sm">
                <option value="">Select an orphan token…</option>
                ${orphans.map((o) => html`
                  <option value=${o.token}>
                    ${(o.label || "(unnamed)")} · ${o.masked} · $${o.total.cost_usd.toFixed(2)} · ${fmtInt(o.total.requests)} req${o.last_used ? " · " + fmtDate(o.last_used) : ""}
                  </option>
                `)}
              </select>
              <button disabled=${!pickedOrphan || merging} onClick=${doInherit} class="px-4 py-2 rounded-lg border border-slate-300 dark:border-slate-600 hover:bg-slate-100 dark:hover:bg-slate-700 text-sm disabled:opacity-50">${merging ? "Merging…" : "Merge selected into this token"}</button>
            </div>
          `}

          <div class="flex justify-end gap-3 pt-2 border-t border-slate-200 dark:border-slate-700">
            <button onClick=${onClose} class="px-4 py-2 rounded-lg border border-slate-300 dark:border-slate-600 hover:bg-slate-100 dark:hover:bg-slate-700 text-base">Cancel</button>
            <button disabled=${busy} onClick=${save} class="px-5 py-2 rounded-lg bg-slate-900 text-white hover:bg-slate-800 dark:bg-slate-200 dark:text-slate-900 dark:hover:bg-slate-300 disabled:opacity-50 text-base">${busy ? "Saving…" : "Save"}</button>
          </div>
        `}
      </div>
    </div>
  `;
}
