// useMembership — the page shell's view of "who is signed in on this browser,
// and what is their seat in a group".
//
// It exists because two unrelated parts of the status page need the same
// answer: the wallet card that tells a member what the group pool still covers
// for them, and the tab bar, which offers the group console only to a group
// admin. Both read it off /api/wallet/balance, which is the one endpoint that
// already authenticates the active token.
//
// The token itself lives in localStorage (the wallet panel owns sign-in), so
// this subscribes to the change event rather than reading once at mount —
// otherwise signing in on the Wallet tab would leave the tab bar a reload
// behind.
import { useEffect, useState } from "react";
import {
  loadActiveToken,
  loadWalletBalance,
  onActiveTokenChange,
  type WalletWorkspace,
} from "./status-api";

export interface Membership {
  /** The active token, or "" when nobody is signed in. */
  token: string;
  /** The seat, or null when the token holds none (or isn't known yet). */
  workspace: WalletWorkspace | null;
  /** True once the lookup has settled, however it settled. */
  resolved: boolean;
}

export function useMembership(): Membership {
  const [token, setToken] = useState<string>(() => loadActiveToken());
  const [workspace, setWorkspace] = useState<WalletWorkspace | null>(null);
  const [resolved, setResolved] = useState(false);

  useEffect(() => onActiveTokenChange(setToken), []);

  useEffect(() => {
    if (!token) {
      setWorkspace(null);
      setResolved(true);
      return;
    }
    let cancelled = false;
    setResolved(false);
    loadWalletBalance(token)
      .then((b) => {
        if (!cancelled) setWorkspace(b.workspace ?? null);
      })
      .catch(() => {
        // A dead token, billing switched off, or a network blip all mean the
        // same thing here: no console to offer. The wallet panel is the
        // surface that reports the failure — repeating it in the tab bar would
        // only put an error where a tab used to be.
        if (!cancelled) setWorkspace(null);
      })
      .finally(() => {
        if (!cancelled) setResolved(true);
      });
    return () => {
      cancelled = true;
    };
  }, [token]);

  return { token, workspace, resolved };
}
