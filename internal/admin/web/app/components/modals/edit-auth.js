import { useState } from "preact/hooks";
import { html, modelMapToText, textToModelMap } from "../../util.js";
import { api } from "../../api.js";

export function EditModal({ auth, onClose, onSaved }) {
  const [disabled, setDisabled] = useState(auth.disabled);
  const [maxC, setMaxC] = useState(auth.max_concurrent || 0);
  const [proxy, setProxy] = useState(auth.proxy_url || "");
  const [baseURL, setBaseURL] = useState(auth.base_url || "");
  const [label, setLabel] = useState(auth.label || "");
  const [group, setGroup] = useState(auth.group || "");
  const [modelMapText, setModelMapText] = useState(modelMapToText(auth.model_map));
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const isApiKey = auth.kind === "apikey";
  const save = async () => {
    setBusy(true); setErr("");
    try {
      const body = { disabled, proxy_url: proxy, label, group };
      if (!isApiKey) body.max_concurrent = Number(maxC);
      if (isApiKey) {
        body.base_url = baseURL;
        const parsed = textToModelMap(modelMapText);
        if (parsed.errors.length > 0) {
          throw new Error("model map: " + parsed.errors.join("; "));
        }
        body.model_map = parsed.map;
      }
      await api(`/admin/api/auths/${encodeURIComponent(auth.id)}`, {
        method: "PATCH",
        body: JSON.stringify(body),
      });
      onSaved();
    } catch (x) { setErr(x.message); }
    finally { setBusy(false); }
  };
  return html`
    <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" onClick=${onClose}>
      <div class="bg-white dark:bg-slate-800 rounded-2xl shadow-xl w-full max-h-[90vh] overflow-y-auto max-w-md p-6 space-y-4" onClick=${(e) => e.stopPropagation()}>
        <div class="flex items-center justify-between">
          <h2 class="text-xl font-semibold">Edit credential</h2>
          <button onClick=${onClose} class="text-slate-400 dark:text-slate-500 hover:text-slate-700 dark:hover:text-slate-200">✕</button>
        </div>
        <div class="text-sm mono text-slate-500 dark:text-slate-400">${auth.id}</div>
        <label class="block">
          <span class="text-base text-slate-600 dark:text-slate-300">Label</span>
          <input class="mt-1 w-full border border-slate-300 dark:border-slate-600 rounded-lg px-3 py-2 bg-white dark:bg-slate-900" value=${label} onInput=${(e)=>setLabel(e.target.value)} />
        </label>
        ${!isApiKey && html`
          <label class="block">
            <span class="text-base text-slate-600 dark:text-slate-300">Max concurrent sessions</span>
            <input type="number" min="0" class="mt-1 w-full border border-slate-300 dark:border-slate-600 rounded-lg px-3 py-2 bg-white dark:bg-slate-900" value=${maxC} onInput=${(e)=>setMaxC(e.target.value)} />
            <span class="text-sm text-slate-400 dark:text-slate-500">0 = unlimited</span>
          </label>
        `}
        ${isApiKey && html`
          <label class="block">
            <span class="text-base text-slate-600 dark:text-slate-300">Base URL</span>
            <input class="mt-1 w-full border border-slate-300 dark:border-slate-600 rounded-lg px-3 py-2 mono bg-white dark:bg-slate-900" placeholder="https://api.your-relay-vendor.com (default: api.anthropic.com)" value=${baseURL} onInput=${(e)=>setBaseURL(e.target.value)} />
            <span class="text-sm text-slate-400 dark:text-slate-500">Per-key upstream override; leave blank for Anthropic.</span>
          </label>
          <label class="block">
            <span class="text-base text-slate-600 dark:text-slate-300">Model map (optional)</span>
            <textarea class="mt-1 w-full border border-slate-300 dark:border-slate-600 rounded-lg px-3 py-2 mono text-sm h-32 bg-white dark:bg-slate-900" placeholder="claude-opus-4-6 = [0.16]稳定喵/claude-opus-4-6&#10;claude-sonnet-4-6 = [0.1]高质量喵/claude-sonnet-4-6&#10;claude-haiku-4-5 =" value=${modelMapText} onInput=${(e)=>setModelMapText(e.target.value)}></textarea>
            <span class="text-sm text-slate-400 dark:text-slate-500">One <span class="mono">client_model = upstream_model</span> per line. When non-empty, this key only serves listed client models, and the request body's <span class="mono">model</span> field is rewritten to the upstream value before forwarding. Leave the right side blank to accept the model without rewriting.</span>
          </label>
        `}
        <label class="block">
          <span class="text-base text-slate-600 dark:text-slate-300">Proxy URL</span>
          <input class="mt-1 w-full border border-slate-300 dark:border-slate-600 rounded-lg px-3 py-2 mono bg-white dark:bg-slate-900" placeholder="http://host:port or socks5://host:port" value=${proxy} onInput=${(e)=>setProxy(e.target.value)} />
        </label>
        <label class="block">
          <span class="text-base text-slate-600 dark:text-slate-300">Group</span>
          <input list="groups-datalist" class="mt-1 w-full border border-slate-300 dark:border-slate-600 rounded-lg px-3 py-2 bg-white dark:bg-slate-900" placeholder="public (shared with everyone)" value=${group} onInput=${(e)=>setGroup(e.target.value)} />
          <span class="text-sm text-slate-400 dark:text-slate-500">Empty or "public" = shared pool. Name a group (e.g. <span class="mono">alice</span>, <span class="mono">research-team</span>) to restrict this credential.</span>
        </label>
        <label class="flex items-center gap-2">
          <input type="checkbox" checked=${disabled} onChange=${(e)=>setDisabled(e.target.checked)} />
          <span class="text-base">Disabled</span>
        </label>
        ${err && html`<div class="text-base text-red-600 dark:text-red-400">${err}</div>`}
        <div class="flex justify-end gap-2 pt-2">
          <button onClick=${onClose} class="px-4 py-2 rounded-lg border border-slate-300 dark:border-slate-600 dark:bg-slate-900 hover:bg-slate-50 dark:hover:bg-slate-700">Cancel</button>
          <button disabled=${busy} onClick=${save} class="px-4 py-2 rounded-lg bg-slate-900 hover:bg-slate-800 text-white dark:bg-slate-200 dark:hover:bg-slate-300 dark:text-slate-900 disabled:opacity-50">${busy ? "Saving..." : "Save"}</button>
        </div>
      </div>
    </div>
  `;
}
