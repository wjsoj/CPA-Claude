import { useState } from "preact/hooks";
import { html, copyToClipboard } from "../util.js";

export function CopyTokenBtn({ token }) {
  const [copied, setCopied] = useState(false);
  const onClick = async () => {
    try {
      await copyToClipboard(token);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {}
  };
  return html`<button onClick=${onClick} class="px-3 py-1 rounded-md border border-slate-300 dark:border-slate-600 text-sm hover:bg-slate-100 dark:hover:bg-slate-700">${copied ? "Copied ✓" : "Copy"}</button>`;
}
