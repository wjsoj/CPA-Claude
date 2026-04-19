import { useState, useRef } from "preact/hooks";
import { html } from "../../util.js";
import { api } from "../../api.js";

export function UploadModal({ onClose, onSaved }) {
  const [filename, setFilename] = useState("");
  const [content, setContent] = useState("");
  const [label, setLabel] = useState("");
  const [maxC, setMaxC] = useState(5);
  const [proxy, setProxy] = useState("");
  const [group, setGroup] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const fileInputRef = useRef(null);

  const onPick = () => fileInputRef.current && fileInputRef.current.click();
  const onFile = async (e) => {
    const f = e.target.files && e.target.files[0];
    if (!f) return;
    setFilename(f.name);
    setContent(await f.text());
  };
  const save = async () => {
    setBusy(true); setErr("");
    try {
      JSON.parse(content); // validate
      await api("/admin/api/auths/upload", {
        method: "POST",
        body: JSON.stringify({
          filename, content: JSON.parse(content),
          label, max_concurrent: Number(maxC), proxy_url: proxy, group,
        }),
      });
      onSaved();
    } catch (x) { setErr(x.message || String(x)); }
    finally { setBusy(false); }
  };
  return html`
    <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" onClick=${onClose}>
      <div class="bg-white dark:bg-slate-800 rounded-2xl shadow-xl w-full max-h-[90vh] overflow-y-auto max-w-lg p-6 space-y-4" onClick=${(e) => e.stopPropagation()}>
        <div class="flex items-center justify-between">
          <h2 class="text-xl font-semibold">Add OAuth credential</h2>
          <button onClick=${onClose} class="text-slate-400 dark:text-slate-500 hover:text-slate-700 dark:hover:text-slate-200">✕</button>
        </div>
        <input type="file" accept=".json,application/json" ref=${fileInputRef} onChange=${onFile} class="hidden" />
        <div class="flex gap-2">
          <button onClick=${onPick} class="px-3 py-1.5 rounded-lg border border-slate-300 dark:border-slate-600 dark:bg-slate-900 text-base hover:bg-slate-50 dark:hover:bg-slate-700">Choose JSON file…</button>
          <input class="flex-1 border border-slate-300 dark:border-slate-600 rounded-lg px-3 py-2 mono bg-white dark:bg-slate-900 text-base" placeholder="filename (optional)" value=${filename} onInput=${(e)=>setFilename(e.target.value)} />
        </div>
        <label class="block">
          <span class="text-base text-slate-600 dark:text-slate-300">or paste JSON</span>
          <textarea class="mt-1 w-full border border-slate-300 dark:border-slate-600 rounded-lg px-3 py-2 mono h-40 bg-white dark:bg-slate-900" value=${content} onInput=${(e)=>setContent(e.target.value)} placeholder='{"type":"claude","access_token":"...","refresh_token":"...","email":"..."}'></textarea>
        </label>
        <div class="grid grid-cols-2 gap-3">
          <label class="block">
            <span class="text-base text-slate-600 dark:text-slate-300">Label</span>
            <input class="mt-1 w-full border border-slate-300 dark:border-slate-600 rounded-lg px-3 py-2 bg-white dark:bg-slate-900" value=${label} onInput=${(e)=>setLabel(e.target.value)} />
          </label>
          <label class="block">
            <span class="text-base text-slate-600 dark:text-slate-300">Max concurrent</span>
            <input type="number" min="0" class="mt-1 w-full border border-slate-300 dark:border-slate-600 rounded-lg px-3 py-2 bg-white dark:bg-slate-900" value=${maxC} onInput=${(e)=>setMaxC(e.target.value)} />
          </label>
        </div>
        <label class="block">
          <span class="text-base text-slate-600 dark:text-slate-300">Proxy URL (optional)</span>
          <input class="mt-1 w-full border border-slate-300 dark:border-slate-600 rounded-lg px-3 py-2 mono bg-white dark:bg-slate-900" placeholder="http:// or socks5://" value=${proxy} onInput=${(e)=>setProxy(e.target.value)} />
        </label>
        <label class="block">
          <span class="text-base text-slate-600 dark:text-slate-300">Group (optional)</span>
          <input list="groups-datalist" class="mt-1 w-full border border-slate-300 dark:border-slate-600 rounded-lg px-3 py-2 bg-white dark:bg-slate-900" placeholder="public" value=${group} onInput=${(e)=>setGroup(e.target.value)} />
        </label>
        ${err && html`<div class="text-base text-red-600 dark:text-red-400 whitespace-pre-wrap">${err}</div>`}
        <div class="flex justify-end gap-2 pt-2">
          <button onClick=${onClose} class="px-4 py-2 rounded-lg border border-slate-300 dark:border-slate-600 dark:bg-slate-900 hover:bg-slate-50 dark:hover:bg-slate-700">Cancel</button>
          <button disabled=${busy || !content} onClick=${save} class="px-4 py-2 rounded-lg bg-slate-900 hover:bg-slate-800 text-white dark:bg-slate-200 dark:hover:bg-slate-300 dark:text-slate-900 disabled:opacity-50">${busy ? "Uploading..." : "Add"}</button>
        </div>
      </div>
    </div>
  `;
}
