import { useCallback, useEffect, useState } from "react";
import { loadStatusDashboard, type StatusDashboardResp } from "@/lib/status-api";
import { DashboardBoard } from "./dashboard-board";
import { mergePools, normalizePools } from "@/lib/cred-state";

interface Props {
  refreshTick: number;
}

// Public-dashboard adapter. Consumes /status/api/dashboard (single round
// trip; the backend bundles 14d + all-time + hourly + pool health) and
// delegates rendering to the shared DashboardBoard. clientsAnonymized
// flips on the "pseudonym" tooltip next to Top clients because the
// server-side ByClient map is keyed by Alice/Bob/... rather than real
// labels.
export function StatusDashboardPanel({ refreshTick }: Props) {
  const [data, setData] = useState<StatusDashboardResp | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    setBusy(true);
    try {
      const d = await loadStatusDashboard();
      setData(d);
    } catch {
      // ignore — the outer StatusPage surfaces overview errors already
    } finally {
      setBusy(false);
    }
  }, []);
  useEffect(() => {
    load();
  }, [load, refreshTick]);

  // `pool` arrives provider-grouped; the pie shows the fleet, so merge. If the
  // server predates the seven-state contract normalizePools yields nothing and
  // the chart renders its own empty state rather than a wrong one.
  const merged = data ? mergePools(normalizePools(data.pool)) : null;

  return (
    <DashboardBoard
      pool={merged && merged.total > 0 ? { total: merged.total, by_state: merged.by_state } : null}
      pricing={data?.pricing}
      reqData={data?.requests_14d ?? null}
      lifetimeData={data?.requests_all ?? null}
      hourly={data?.hourly_24h ?? null}
      busy={busy}
      clientsAnonymized
    />
  );
}
