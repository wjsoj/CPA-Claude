// Admin-token storage + fetch helper. The mount prefix is derived from
// window.location so the SPA works at any `admin_path` the operator
// picked (e.g. /mgmt-console, /mngt-ctrl).

const TOKEN_KEY = "cpa.admin.token";
export const getToken = () => localStorage.getItem(TOKEN_KEY) || "";
export const setToken = (t) => { if (t) localStorage.setItem(TOKEN_KEY, t); else localStorage.removeItem(TOKEN_KEY); };

// Handles:
//   /mgmt-console/          → /mgmt-console
//   /mgmt-console/app/foo   → /mgmt-console
//   /mngt-ctrl/             → /mngt-ctrl
// Call sites use "/admin/api/..." strings; we rewrite the prefix here so
// component code stays mount-agnostic.
export const ADMIN_BASE = (() => {
  const p = window.location.pathname || "/";
  return p.replace(/\/(app(\/.*)?)?\/?$/, "") || "";
})();

export async function api(path, opts = {}) {
  const token = getToken();
  if (path.startsWith("/admin/")) path = ADMIN_BASE + path.slice("/admin".length);
  const res = await fetch(path, {
    ...opts,
    headers: {
      "Content-Type": "application/json",
      "X-Admin-Token": token,
      ...(opts.headers || {}),
    },
  });
  const text = await res.text();
  let data = null;
  try { data = text ? JSON.parse(text) : null; } catch { data = { raw: text }; }
  if (!res.ok) {
    const err = new Error((data && data.error) || `HTTP ${res.status}`);
    err.status = res.status;
    throw err;
  }
  return data;
}
