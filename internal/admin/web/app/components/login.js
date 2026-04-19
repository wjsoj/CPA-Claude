import { useState } from "preact/hooks";
import { html } from "../util.js";
import { api, setToken } from "../api.js";

export function Login({ onOk }) {
  const [val, setVal] = useState("");
  const [err, setErr] = useState("");
  const submit = async (e) => {
    e.preventDefault();
    setErr("");
    setToken(val.trim());
    try {
      await api("/admin/api/summary");
      onOk();
    } catch (x) {
      setToken("");
      setErr(x.message || "auth failed");
    }
  };
  return html`
    <div class="min-h-screen flex items-center justify-center p-6">
      <form onSubmit=${submit} class="bg-white dark:bg-slate-800 shadow-xl rounded-2xl p-8 w-full max-w-sm space-y-4 border border-slate-200 dark:border-slate-700">
        <h1 class="text-2xl font-semibold">CPA-Claude Admin</h1>
        <p class="text-base text-slate-500 dark:text-slate-400">Enter admin token to continue.</p>
        <input
          type="password" autofocus
          class="w-full border border-slate-300 dark:border-slate-600 bg-white dark:bg-slate-900 rounded-lg px-3 py-2 focus:outline-none focus:ring-2 focus:ring-slate-400"
          placeholder="admin token"
          value=${val}
          onInput=${(e) => setVal(e.target.value)}
        />
        ${err && html`<div class="text-base text-red-600 dark:text-red-400">${err}</div>`}
        <button type="submit" class="w-full bg-slate-900 hover:bg-slate-800 text-white dark:bg-slate-200 dark:hover:bg-slate-300 dark:text-slate-900 font-medium py-2 rounded-lg">Sign in</button>
      </form>
    </div>
  `;
}
