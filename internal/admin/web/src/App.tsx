import { useState, useEffect } from "react";
import { api, getToken, setToken, ApiError } from "@/lib/api";
import { Login } from "@/components/login";
import { Dashboard } from "@/components/dashboard";

export function App() {
  const [authed, setAuthed] = useState(!!getToken());
  useEffect(() => {
    if (!authed) return;
    api("/admin/api/summary").catch((x) => {
      if (x instanceof ApiError && x.status === 401) {
        setToken("");
        setAuthed(false);
      }
    });
  }, [authed]);
  return authed ? (
    <Dashboard onLogout={() => setAuthed(false)} />
  ) : (
    <Login onOk={() => setAuthed(true)} />
  );
}
