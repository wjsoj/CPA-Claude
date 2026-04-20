import { useState, useEffect } from "preact/hooks";
import { html } from "../util.js";
import { api, getToken, setToken } from "../api.js";
import { Login } from "./login.js";
import { Dashboard } from "./dashboard.js";
import { NoticeHost } from "./notice.js";

export function App() {
  const [authed, setAuthed] = useState(!!getToken());
  useEffect(() => {
    if (!authed) return;
    // Verify on mount.
    api("/admin/api/summary").catch((x) => {
      if (x.status === 401) { setToken(""); setAuthed(false); }
    });
  }, [authed]);
  return html`
    <${NoticeHost} />
    ${authed
      ? html`<${Dashboard} onLogout=${() => setAuthed(false)} />`
      : html`<${Login} onOk=${() => setAuthed(true)} />`}
  `;
}
