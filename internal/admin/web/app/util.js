// Shared htm/preact binding + small pure helpers. Every component imports
// `html` from here so there is exactly one htm factory in the page (a
// second factory bound to a second Preact instance would silently break
// hooks).

import { h } from "preact";
import htm from "htm";

export const html = htm.bind(h);

export const fmtInt = (n) => (n == null ? "—" : Number(n).toLocaleString());

// navigator.clipboard requires a secure context (HTTPS or localhost); when
// accessed over plain HTTP on a LAN IP it simply doesn't exist. Fall back
// to the legacy execCommand approach.
export async function copyToClipboard(text) {
  if (navigator.clipboard && typeof navigator.clipboard.writeText === "function") {
    try { await navigator.clipboard.writeText(text); return; } catch {}
  }
  const ta = document.createElement("textarea");
  ta.value = text;
  ta.style.position = "fixed";
  ta.style.left = "-9999px";
  document.body.appendChild(ta);
  ta.select();
  document.execCommand("copy");
  document.body.removeChild(ta);
}

// Client-side sk-<48 alphanumerics> generator. Mirrors the format from
// clienttoken.Generate() on the backend so the two paths produce the
// same shape.
export function generateSkToken() {
  const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789";
  const n = 48;
  const buf = new Uint32Array(n);
  crypto.getRandomValues(buf);
  let out = "sk-";
  for (let i = 0; i < n; i++) {
    out += alphabet[buf[i] % alphabet.length];
  }
  return out;
}

export const fmtDate = (s) => {
  if (!s) return "—";
  const d = new Date(s);
  if (isNaN(d)) return "—";
  const diff = d.getTime() - Date.now();
  const abs = Math.abs(diff);
  const m = Math.round(abs / 60000);
  const h_ = Math.round(abs / 3600000);
  const day = Math.round(abs / 86400000);
  let rel = "";
  if (abs < 60000) rel = "now";
  else if (m < 60) rel = `${m}m`;
  else if (h_ < 48) rel = `${h_}h`;
  else rel = `${day}d`;
  return diff < 0 ? `${rel} ago` : `in ${rel}`;
};

// Serialize/parse a {clientModel: upstreamModel} map to/from a one-entry-
// per-line textarea. Format: `client = upstream`. An entry with an empty
// value (e.g. `client =`) means "accept this client model but don't
// rewrite the name". Blank lines and lines starting with # are ignored.
export function modelMapToText(m) {
  if (!m) return "";
  return Object.keys(m).sort().map((k) => `${k} = ${m[k] || ""}`).join("\n");
}

export function textToModelMap(text) {
  const out = {};
  const errors = [];
  (text || "").split(/\r?\n/).forEach((rawLine, i) => {
    const line = rawLine.trim();
    if (!line || line.startsWith("#")) return;
    const eq = line.indexOf("=");
    if (eq < 0) {
      errors.push(`line ${i + 1}: missing '='`);
      return;
    }
    const k = line.slice(0, eq).trim();
    const v = line.slice(eq + 1).trim();
    if (!k) {
      errors.push(`line ${i + 1}: empty client model name`);
      return;
    }
    out[k] = v;
  });
  return { map: out, errors };
}
