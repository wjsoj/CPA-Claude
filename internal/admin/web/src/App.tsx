import { useState } from "react";
import { getToken } from "@/lib/api";
import { Login } from "@/components/login";
import { Dashboard } from "@/components/dashboard";

export function App() {
  const [authed, setAuthed] = useState(!!getToken());
  // No token probe here on purpose. This used to fire /admin/api/summary
  // purely to detect a stale token, which meant every page load issued that
  // request twice concurrently — Dashboard's own refresh() does the same
  // call, and already clears the token and drops back to Login on 401.
  return authed ? (
    <Dashboard onLogout={() => setAuthed(false)} />
  ) : (
    <Login onOk={() => setAuthed(true)} />
  );
}
