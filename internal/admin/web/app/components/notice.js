// Centralized confirm/notify replacements for window.alert/confirm.
// A single <NoticeHost /> mounted near the App root drives two imperative
// helpers: confirmDialog(opts) → Promise<bool>, and notify(opts).
// Call sites get a promise-based alert() substitute without threading
// react state through every component.
import { useState, useEffect } from "preact/hooks";
import { html } from "../util.js";

let pushConfirm = null;
let pushNotice = null;

export function confirmDialog({ title, message, confirmLabel = "Confirm", cancelLabel = "Cancel", danger = false } = {}) {
  return new Promise((resolve) => {
    if (!pushConfirm) { resolve(window.confirm(message || title || "")); return; }
    pushConfirm({ title, message, confirmLabel, cancelLabel, danger, resolve });
  });
}

export function notify({ title, message, kind = "error" } = {}) {
  if (!pushNotice) { window.alert(`${title ? title + ": " : ""}${message || ""}`); return; }
  pushNotice({ title, message, kind });
}

export function NoticeHost() {
  const [confirms, setConfirms] = useState([]); // stack so overlapping confirms queue
  const [toasts, setToasts] = useState([]);

  useEffect(() => {
    pushConfirm = (c) => setConfirms((cs) => [...cs, c]);
    pushNotice = (n) => {
      const id = Math.random().toString(36).slice(2);
      setToasts((ts) => [...ts, { ...n, id }]);
      setTimeout(() => setToasts((ts) => ts.filter((t) => t.id !== id)), 5000);
    };
    return () => { pushConfirm = null; pushNotice = null; };
  }, []);

  const settleTop = (result) => {
    setConfirms((cs) => {
      if (!cs.length) return cs;
      const top = cs[cs.length - 1];
      top.resolve(result);
      return cs.slice(0, -1);
    });
  };

  const top = confirms[confirms.length - 1];

  return html`
    <${ToastStack} toasts=${toasts} onDismiss=${(id) => setToasts((ts) => ts.filter((t) => t.id !== id))} />
    ${top && html`
      <div class="fixed inset-0 bg-black/50 dark:bg-black/70 flex items-center justify-center z-[100] p-4" onClick=${() => settleTop(false)}>
        <div class="bg-white dark:bg-slate-800 rounded-2xl shadow-2xl w-full max-w-md p-6 space-y-4" onClick=${(e) => e.stopPropagation()}>
          ${top.title && html`<h3 class="text-lg font-semibold tracking-tight">${top.title}</h3>`}
          ${top.message && html`<p class="text-sm text-slate-600 dark:text-slate-300 whitespace-pre-line">${top.message}</p>`}
          <div class="flex justify-end gap-2 pt-2">
            <button onClick=${() => settleTop(false)} class="px-4 py-2 rounded-lg border border-slate-300 dark:border-slate-600 hover:bg-slate-100 dark:hover:bg-slate-700 text-sm">${top.cancelLabel}</button>
            <button onClick=${() => settleTop(true)} class=${"px-4 py-2 rounded-lg text-sm text-white " + (top.danger ? "bg-rose-600 hover:bg-rose-700" : "bg-slate-900 hover:bg-slate-800 dark:bg-slate-200 dark:text-slate-900 dark:hover:bg-slate-300")}>${top.confirmLabel}</button>
          </div>
        </div>
      </div>
    `}
  `;
}

function ToastStack({ toasts, onDismiss }) {
  if (!toasts.length) return null;
  return html`
    <div class="fixed top-4 right-4 z-[110] flex flex-col gap-2 max-w-sm">
      ${toasts.map((t) => html`
        <div key=${t.id} class=${"rounded-xl shadow-lg border px-4 py-3 flex items-start gap-3 " + kindClasses(t.kind)}>
          <div class="flex-1">
            ${t.title && html`<div class="font-semibold text-sm">${t.title}</div>`}
            ${t.message && html`<div class="text-sm opacity-90 mt-0.5 whitespace-pre-line break-words">${t.message}</div>`}
          </div>
          <button onClick=${() => onDismiss(t.id)} class="text-current opacity-60 hover:opacity-100 text-lg leading-none">✕</button>
        </div>
      `)}
    </div>
  `;
}

function kindClasses(kind) {
  switch (kind) {
    case "success": return "bg-emerald-50 border-emerald-200 text-emerald-900 dark:bg-emerald-900/40 dark:border-emerald-700/60 dark:text-emerald-100";
    case "info":    return "bg-sky-50 border-sky-200 text-sky-900 dark:bg-sky-900/40 dark:border-sky-700/60 dark:text-sky-100";
    case "warn":    return "bg-amber-50 border-amber-200 text-amber-900 dark:bg-amber-900/40 dark:border-amber-700/60 dark:text-amber-100";
    case "error":
    default:        return "bg-rose-50 border-rose-200 text-rose-900 dark:bg-rose-900/40 dark:border-rose-700/60 dark:text-rose-100";
  }
}
