import { useState } from "preact/hooks";
import { html, textToModelMap } from "../../util.js";
import { api } from "../../api.js";

export function APIKeyModal({ onClose, onSaved }) {
  const [apiKey, setAPIKey] = useState("");
  const [label, setLabel] = useState("");
  const [proxy, setProxy] = useState("");
  const [baseURL, setBaseURL] = useState("");
  const [group, setGroup] = useState("");
  const [modelMapText, setModelMapText] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const save = async () => {
    setBusy(true); setErr("");
    try {
      const parsed = textToModelMap(modelMapText);
      if (parsed.errors.length > 0) {
        throw new Error("model map: " + parsed.errors.join("; "));
      }
      await api("/admin/api/apikeys", {
        method: "POST",
        body: JSON.stringify({
          api_key: apiKey.trim(), label: label.trim(),
          proxy_url: proxy.trim(), base_url: baseURL.trim(),
          group: group.trim(), model_map: parsed.map,
        }),
      });
      onSaved();
    } catch (x) { setErr(x.message); }
    finally { setBusy(false); }
  };
  return html`
    <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" onClick=${onClose}>
      <div class="bg-white dark:bg-slate-800 rounded-2xl shadow-xl w-full max-h-[90vh] overflow-y-auto max-w-md p-6 space-y-4" onClick=${(e) => e.stopPropagation()}>
        <div class="flex items-center justify-between">
          <h2 class="text-xl font-semibold">Add API key</h2>
          <button onClick=${onClose} class="text-slate-400 dark:text-slate-500 hover:text-slate-700 dark:hover:text-slate-200">✕</button>
        </div>
        <p class="text-base text-slate-500 dark:text-slate-400">Anthropic <span class="mono">sk-ant-api…</span> key. Stored as a JSON file in <span class="mono">auth_dir</span>, mutable from the panel. No per-key concurrency limit — used as the fallback when every OAuth is saturated or over quota.</p>
        <label class="block">
          <span class="text-base text-slate-600 dark:text-slate-300">API key</span>
          <input type="password" autofocus class="mt-1 w-full border border-slate-300 dark:border-slate-600 rounded-lg px-3 py-2 mono bg-white dark:bg-slate-900" placeholder="sk-ant-api03-..." value=${apiKey} onInput=${(e)=>setAPIKey(e.target.value)} />
        </label>
        <label class="block">
          <span class="text-base text-slate-600 dark:text-slate-300">Label</span>
          <input class="mt-1 w-full border border-slate-300 dark:border-slate-600 rounded-lg px-3 py-2 bg-white dark:bg-slate-900" placeholder="primary" value=${label} onInput=${(e)=>setLabel(e.target.value)} />
        </label>
        <label class="block">
          <span class="text-base text-slate-600 dark:text-slate-300">Base URL (optional, for relay vendors)</span>
          <input class="mt-1 w-full border border-slate-300 dark:border-slate-600 rounded-lg px-3 py-2 mono bg-white dark:bg-slate-900" placeholder="https://api.your-relay-vendor.com (default: api.anthropic.com)" value=${baseURL} onInput=${(e)=>setBaseURL(e.target.value)} />
          <span class="text-sm text-slate-400 dark:text-slate-500">Requests with this key go to <span class="mono">{base_url}/v1/messages</span>. Leave empty to hit Anthropic directly.</span>
        </label>
        <label class="block">
          <span class="text-base text-slate-600 dark:text-slate-300">Proxy URL (optional)</span>
          <input class="mt-1 w-full border border-slate-300 dark:border-slate-600 rounded-lg px-3 py-2 mono bg-white dark:bg-slate-900" placeholder="http:// or socks5://" value=${proxy} onInput=${(e)=>setProxy(e.target.value)} />
        </label>
        <label class="block">
          <span class="text-base text-slate-600 dark:text-slate-300">Group (optional)</span>
          <input list="groups-datalist" class="mt-1 w-full border border-slate-300 dark:border-slate-600 rounded-lg px-3 py-2 bg-white dark:bg-slate-900" placeholder="public" value=${group} onInput=${(e)=>setGroup(e.target.value)} />
        </label>
        <label class="block">
          <span class="text-base text-slate-600 dark:text-slate-300">Model map (optional)</span>
          <textarea class="mt-1 w-full border border-slate-300 dark:border-slate-600 rounded-lg px-3 py-2 mono text-sm h-32 bg-white dark:bg-slate-900" placeholder="claude-opus-4-6 = [0.16]稳定喵/claude-opus-4-6&#10;claude-sonnet-4-6 = [0.1]高质量喵/claude-sonnet-4-6&#10;claude-haiku-4-5 =" value=${modelMapText} onInput=${(e)=>setModelMapText(e.target.value)}></textarea>
          <span class="text-sm text-slate-400 dark:text-slate-500">One <span class="mono">client_model = upstream_model</span> per line. When non-empty, this key only serves listed client models, and the request body's <span class="mono">model</span> is rewritten before forwarding. Leave the right side blank to accept the model without rewriting.</span>
        </label>
        ${err && html`<div class="text-base text-red-600 dark:text-red-400">${err}</div>`}
        <div class="flex justify-end gap-2 pt-2">
          <button onClick=${onClose} class="px-4 py-2 rounded-lg border border-slate-300 dark:border-slate-600 dark:bg-slate-900 hover:bg-slate-50 dark:hover:bg-slate-700">Cancel</button>
          <button disabled=${busy || !apiKey.trim()} onClick=${save} class="px-4 py-2 rounded-lg bg-slate-900 hover:bg-slate-800 text-white dark:bg-slate-200 dark:hover:bg-slate-300 dark:text-slate-900 disabled:opacity-50">${busy ? "Saving..." : "Add"}</button>
        </div>
      </div>
    </div>
  `;
}
