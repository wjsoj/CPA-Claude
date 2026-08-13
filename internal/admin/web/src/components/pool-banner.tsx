import { AlertOctagon, AlertTriangle } from "lucide-react";
import { providerLabel, type PoolAgg } from "@/lib/cred-state";
import { STATE_META } from "./cred-state-badge";
import { cn } from "@/lib/utils";

// The banner the old UI never had: when a provider's pool has nothing left to
// serve with, say so at the top of the page instead of painting cards green.
//
// Two severities, and only two:
//
//   red   — pool.available === false. There is no credential able to take a
//           request for this provider right now. Requests are failing.
//   amber — pool.serving > 0 but by_state.healthy === 0. Traffic is going
//           somewhere, but every credential carrying it is unverified,
//           degraded or otherwise unproven. This is the state the old
//           four-state badge rendered as a solid green wall.
//
// Anything else renders nothing — a banner that is always on is a banner
// nobody reads.

type Severity = "down" | "unproven";

function severityOf(p: PoolAgg): Severity | null {
  if (p.total === 0) return null;
  if (!p.available) return "down";
  if (p.serving > 0 && p.by_state.healthy === 0) return "unproven";
  return null;
}

// The banner's one and only state readout. It supersedes the worst-state
// phrase the prose used to carry: `by_state` already contains the worst state
// alongside everything else, so naming it separately said the same thing twice
// ("最严重状态 Failed" directly above "… · Failed 3 · …").
function summarize(p: PoolAgg): string {
  const parts: string[] = [];
  for (const [state, n] of Object.entries(p.by_state)) {
    if (n > 0) parts.push(`${STATE_META[state as keyof typeof STATE_META].label} ${n}`);
  }
  return parts.join(" · ");
}

export function PoolBanner({ pools, className }: { pools: PoolAgg[]; className?: string }) {
  const rows = pools
    .map((p) => ({ p, sev: severityOf(p) }))
    .filter((x): x is { p: PoolAgg; sev: Severity } => x.sev !== null);
  if (rows.length === 0) return null;

  return (
    <div className={cn("space-y-2", className)}>
      {rows.map(({ p, sev }) => {
        const down = sev === "down";
        return (
          <div
            key={p.provider || "pool"}
            role="alert"
            className={cn(
              "rounded-md border px-4 py-3 flex items-start gap-3",
              down
                ? "border-destructive/50 bg-destructive/10 text-destructive"
                : "border-[color:var(--warning)]/45 bg-[color:var(--warning)]/10 text-[color:var(--warning)]",
            )}
          >
            {down ? (
              <AlertOctagon className="h-5 w-5 shrink-0 mt-0.5" />
            ) : (
              <AlertTriangle className="h-5 w-5 shrink-0 mt-0.5" />
            )}
            <div className="min-w-0 flex-1 space-y-1">
              <div className="font-display text-base md:text-lg tracking-tight">
                {providerLabel(p.provider)} ·{" "}
                {down
                  ? "当前无可用凭据"
                  : "正在服务，但没有任何凭据处于已验证的健康状态"}
              </div>
              <p className="text-xs md:text-sm opacity-90 leading-relaxed">
                {down ? (
                  <>
                    全部 {p.total} 个凭据均无法承接请求。此刻发往该上游的请求会失败。
                  </>
                ) : (
                  <>
                    {p.serving}/{p.total} 个凭据在承接流量，但其中没有一个最近成功过。
                    随时可能整体不可用。
                  </>
                )}
              </p>
              <p className="mono text-xs tabular opacity-90">{summarize(p)}</p>
            </div>
          </div>
        );
      })}
    </div>
  );
}
