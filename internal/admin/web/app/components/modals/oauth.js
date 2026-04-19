import { useState } from "preact/hooks";
import { html, copyToClipboard } from "../../util.js";
import { api } from "../../api.js";

export function OAuthModal({ onClose, onSaved }) {
  const [step, setStep] = useState(1); // 1 = form, 2 = waiting-for-callback
  const [proxy, setProxy] = useState("");
  const [label, setLabel] = useState("");
  const [maxC, setMaxC] = useState(5);
  const [group, setGroup] = useState("");
  const [sess, setSess] = useState(null); // {session_id, auth_url}
  const [callback, setCallback] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  const [copied, setCopied] = useState(false);

  const start = async () => {
    setBusy(true); setErr("");
    try {
      const d = await api("/admin/api/oauth/start", {
        method: "POST",
        body: JSON.stringify({ proxy_url: proxy, label }),
      });
      setSess(d);
      setStep(2);
    } catch (x) { setErr(x.message); }
    finally { setBusy(false); }
  };

  const copyUrl = async () => {
    try {
      await copyToClipboard(sess.auth_url);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {}
  };

  const finish = async () => {
    setBusy(true); setErr("");
    try {
      await api("/admin/api/oauth/finish", {
        method: "POST",
        body: JSON.stringify({
          session_id: sess.session_id,
          callback: callback.trim(),
          max_concurrent: Number(maxC),
          group,
        }),
      });
      onSaved();
    } catch (x) { setErr(x.message); }
    finally { setBusy(false); }
  };

  return html`
    <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" onClick=${onClose}>
      <div class="bg-white dark:bg-slate-800 rounded-2xl shadow-xl w-full max-h-[90vh] overflow-y-auto max-w-lg p-6 space-y-4" onClick=${(e) => e.stopPropagation()}>
        <div class="flex items-center justify-between">
          <h2 class="text-xl font-semibold">Sign in with Claude</h2>
          <button onClick=${onClose} class="text-slate-400 dark:text-slate-500 hover:text-slate-700 dark:hover:text-slate-200">✕</button>
        </div>

        ${step === 1 && html`
          <p class="text-base text-slate-500 dark:text-slate-400">We'll open Claude's OAuth page in a new tab. If this server is behind a firewall to <code class="mono">claude.ai / api.anthropic.com</code>, set a proxy URL below — it will be used both for the token exchange and for all subsequent requests made with this credential.</p>
          <label class="block">
            <span class="text-base text-slate-600 dark:text-slate-300">Proxy URL (optional)</span>
            <input class="mt-1 w-full border border-slate-300 dark:border-slate-600 rounded-lg px-3 py-2 mono bg-white dark:bg-slate-900" placeholder="http:// or socks5://" value=${proxy} onInput=${(e) => setProxy(e.target.value)} />
          </label>
          <div class="grid grid-cols-2 gap-3">
            <label class="block">
              <span class="text-base text-slate-600 dark:text-slate-300">Label</span>
              <input class="mt-1 w-full border border-slate-300 dark:border-slate-600 rounded-lg px-3 py-2 bg-white dark:bg-slate-900" placeholder="team-a / alice / …" value=${label} onInput=${(e) => setLabel(e.target.value)} />
            </label>
            <label class="block">
              <span class="text-base text-slate-600 dark:text-slate-300">Max concurrent</span>
              <input type="number" min="0" class="mt-1 w-full border border-slate-300 dark:border-slate-600 rounded-lg px-3 py-2 bg-white dark:bg-slate-900" value=${maxC} onInput=${(e) => setMaxC(e.target.value)} />
            </label>
          </div>
          <label class="block">
            <span class="text-base text-slate-600 dark:text-slate-300">Group (optional)</span>
            <input list="groups-datalist" class="mt-1 w-full border border-slate-300 dark:border-slate-600 rounded-lg px-3 py-2 bg-white dark:bg-slate-900" placeholder="public" value=${group} onInput=${(e) => setGroup(e.target.value)} />
          </label>
          ${err && html`<div class="text-base text-red-600 dark:text-red-400">${err}</div>`}
          <div class="flex justify-end gap-2 pt-2">
            <button onClick=${onClose} class="px-4 py-2 rounded-lg border border-slate-300 dark:border-slate-600 dark:bg-slate-900 hover:bg-slate-50 dark:hover:bg-slate-700">Cancel</button>
            <button disabled=${busy} onClick=${start} class="px-4 py-2 rounded-lg bg-slate-900 hover:bg-slate-800 text-white dark:bg-slate-200 dark:hover:bg-slate-300 dark:text-slate-900 disabled:opacity-50">${busy ? "Starting..." : "Open Claude login"}</button>
          </div>
        `}

        ${step === 2 && html`
          <div class="text-base text-slate-600 dark:text-slate-300 space-y-2">
            <p><b>Step 1.</b> Copy the login URL below and open it in a browser where you can sign in to Claude.</p>
          </div>
          <label class="block">
            <span class="text-base text-slate-600 dark:text-slate-300">Login URL</span>
            <div class="mt-1 flex gap-2">
              <input
                readonly
                onClick=${(e) => e.target.select()}
                class="flex-1 border border-slate-300 dark:border-slate-600 rounded-lg px-3 py-2 mono bg-white dark:bg-slate-900 text-sm bg-slate-50"
                value=${sess.auth_url}
              />
              <button onClick=${copyUrl} class="px-3 py-2 rounded-lg border border-slate-300 dark:border-slate-600 dark:bg-slate-900 text-base hover:bg-slate-50 dark:hover:bg-slate-700 whitespace-nowrap">${copied ? "Copied ✓" : "Copy"}</button>
            </div>
          </label>

          <div class="text-base text-slate-600 dark:text-slate-300 space-y-2 pt-2">
            <p><b>Step 2.</b> After you authorize, Claude redirects to
            <code class="mono break-all">http://localhost:54545/callback?code=…&amp;state=…</code>.
            That page normally fails to load — <b>that's fine</b>.</p>
            <p><b>Step 3.</b> Copy the <b>full URL from the browser address bar</b>
            (or the <code class="mono">code#state</code> value Claude shows on its
            manual-copy page) and paste it below.</p>
          </div>
          <label class="block">
            <span class="text-base text-slate-600 dark:text-slate-300">Callback URL or code#state</span>
            <textarea
              class="mt-1 w-full border border-slate-300 dark:border-slate-600 rounded-lg px-3 py-2 mono text-sm h-28 bg-white dark:bg-slate-900"
              placeholder="http://localhost:54545/callback?code=xxxxx&state=yyyyy"
              value=${callback}
              onInput=${(e) => setCallback(e.target.value)}
            ></textarea>
          </label>
          ${err && html`<div class="text-base text-red-600 dark:text-red-400 whitespace-pre-wrap">${err}</div>`}
          <div class="flex justify-end gap-2 pt-2">
            <button onClick=${() => setStep(1)} class="px-4 py-2 rounded-lg border border-slate-300 dark:border-slate-600 dark:bg-slate-900 hover:bg-slate-50 dark:hover:bg-slate-700">Back</button>
            <button disabled=${busy || !callback.trim()} onClick=${finish} class="px-4 py-2 rounded-lg bg-slate-900 hover:bg-slate-800 text-white dark:bg-slate-200 dark:hover:bg-slate-300 dark:text-slate-900 disabled:opacity-50">${busy ? "Exchanging..." : "Finish"}</button>
          </div>
        `}
      </div>
    </div>
  `;
}
