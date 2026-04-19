import { html } from "../util.js";

export function Badge({ color = "slate", children }) {
  const palette = {
    slate: "bg-slate-100 dark:bg-slate-700 text-slate-700 dark:text-slate-200",
    green: "bg-emerald-100 text-emerald-700",
    red:   "bg-red-100 text-red-700",
    amber: "bg-amber-100 text-amber-800",
    blue:  "bg-blue-100 text-blue-700",
  };
  return html`<span class="badge ${palette[color] || palette.slate}">${children}</span>`;
}
