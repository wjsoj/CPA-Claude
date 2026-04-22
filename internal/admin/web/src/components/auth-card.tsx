import type { AuthRow } from "@/lib/types";
import { Sparkline } from "./sparkline";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { cn, fmtDate, fmtInt } from "@/lib/utils";
import {
  AlertTriangle,
  Ban,
  CheckCircle2,
  Pencil,
  Power,
  RefreshCw,
  ShieldOff,
  Trash2,
} from "lucide-react";

type Action = "toggle" | "refresh" | "clear-quota" | "clear-failure" | "delete";

interface Props {
  a: AuthRow;
  onAction: (a: AuthRow, act: Action) => void;
  onEdit: (a: AuthRow) => void;
}

function statusMeta(a: AuthRow) {
  if (a.disabled)
    return { label: "Disabled", tone: "text-muted-foreground", dot: "bg-muted-foreground" };
  if (a.quota_exceeded)
    return { label: "Quota", tone: "text-[color:var(--warning)]", dot: "bg-[color:var(--warning)]" };
  if (a.hard_failure)
    return { label: "Unhealthy", tone: "text-destructive", dot: "bg-destructive" };
  if (a.healthy)
    return { label: "Healthy", tone: "text-[color:var(--success)]", dot: "bg-[color:var(--success)]" };
  return { label: "Degraded", tone: "text-[color:var(--warning)]", dot: "bg-[color:var(--warning)]" };
}

export function AuthCard({ a, onAction, onEdit }: Props) {
  const slot =
    a.max_concurrent > 0 ? `${a.active_clients}/${a.max_concurrent}` : `${a.active_clients}/∞`;
  const slotRatio =
    a.max_concurrent > 0
      ? Math.min(100, Math.round((a.active_clients / a.max_concurrent) * 100))
      : 0;
  const status = statusMeta(a);
  const u = a.usage;
  const kindLabel = a.kind === "apikey" ? "API key" : "OAuth";
  const recentCancel =
    a.last_client_cancel && Date.now() - new Date(a.last_client_cancel).getTime() < 3600 * 1000;

  return (
    <article className="relative group bg-card border border-border-strong rounded-md overflow-hidden transition-all duration-300 hover:-translate-y-0.5 hover:shadow-xl hover:shadow-primary/5 hover:border-primary/40">
      {/* Thin accent bar that tinges on hover */}
      <div
        aria-hidden
        className={cn(
          "absolute inset-x-0 top-0 h-[2px] transition-all",
          a.healthy && !a.disabled && !a.quota_exceeded
            ? "bg-gradient-to-r from-transparent via-primary/40 to-transparent opacity-60 group-hover:opacity-100"
            : "bg-transparent",
        )}
      />

      <header className="px-5 py-4 border-b border-border flex items-start justify-between gap-3">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2.5">
            <span className="relative inline-flex h-2 w-2 shrink-0">
              {!a.disabled && a.healthy && (
                <span
                  className={cn(
                    "absolute inline-flex h-full w-full rounded-full opacity-60 animate-ping",
                    status.dot,
                  )}
                />
              )}
              <span className={cn("relative inline-flex h-2 w-2 rounded-full", status.dot)} />
            </span>
            <h3 className="font-display text-xl leading-tight truncate">{a.label || a.id}</h3>
          </div>
          <div className="mt-1 mono text-[11px] text-muted-foreground truncate pl-4.5">{a.id}</div>
        </div>
        <div className="flex flex-col items-end gap-1 shrink-0">
          <Badge variant={a.kind === "apikey" ? "blue" : "slate"}>{kindLabel}</Badge>
          <Badge variant={a.group ? "violet" : "slate"} title="Credential group">
            {a.group || "public"}
          </Badge>
          <span className={cn("eyebrow !text-[10px]", status.tone)}>{status.label}</span>
        </div>
      </header>

      {a.quota_exceeded && (
        <AlertStrip tone="warning" icon={<AlertTriangle className="h-3.5 w-3.5" />} label="Quota exceeded">
          {a.quota_reset_at ? `resets ${fmtDate(a.quota_reset_at)}` : "no reset time reported"}
        </AlertStrip>
      )}
      {!a.quota_exceeded && a.failure_reason && (
        <AlertStrip
          tone={a.hard_failure ? "error" : "warning"}
          icon={a.hard_failure ? <ShieldOff className="h-3.5 w-3.5" /> : <AlertTriangle className="h-3.5 w-3.5" />}
          label={a.hard_failure ? "Unhealthy" : "Recent failure"}
          title={a.failure_reason}
        >
          {a.failure_reason}
        </AlertStrip>
      )}
      {recentCancel && (
        <AlertStrip tone="muted" icon={<Ban className="h-3.5 w-3.5" />} label="Client canceled">
          {fmtDate(a.last_client_cancel!)}
          {a.client_cancel_reason ? " · " + a.client_cancel_reason : ""}
        </AlertStrip>
      )}

      <dl className="px-5 py-4 grid grid-cols-2 gap-x-6 gap-y-3.5 text-sm">
        <div className="relative group/slot">
          <dt className="eyebrow mb-1.5">Slots</dt>
          <dd className="mono font-medium">
            <div className="tabular text-base">{slot}</div>
            {a.max_concurrent > 0 && (
              <div className="mt-1.5 h-1 w-full max-w-[120px] bg-muted rounded-full overflow-hidden">
                <div
                  className="h-full transition-all"
                  style={{
                    width: `${slotRatio}%`,
                    background: slotRatio > 80 ? "var(--warning)" : "var(--success)",
                  }}
                />
              </div>
            )}
          </dd>
          {a.active_clients > 0 && a.client_tokens?.length > 0 && (
            <div className="pointer-events-none absolute left-0 top-full mt-2 z-20 min-w-[180px] max-w-[260px] opacity-0 translate-y-1 group-hover/slot:opacity-100 group-hover/slot:translate-y-0 transition duration-200 rounded-md border border-border-strong bg-popover shadow-xl px-3 py-2 text-xs">
              <div className="eyebrow mb-1">Active clients</div>
              <ul className="space-y-0.5">
                {a.client_tokens.map((t) => (
                  <li key={t} className="truncate mono">
                    {t}
                  </li>
                ))}
              </ul>
            </div>
          )}
        </div>
        <div>
          <dt className="eyebrow mb-1.5">Token exp</dt>
          <dd className="mono text-sm">
            {a.expires_at ? fmtDate(a.expires_at) : <span className="text-muted-foreground">—</span>}
          </dd>
        </div>
        {a.email && (
          <div className="col-span-2">
            <dt className="eyebrow mb-1.5">Email</dt>
            <dd className="text-sm truncate">{a.email}</dd>
          </div>
        )}
        <div className="col-span-2">
          <dt className="eyebrow mb-1.5">Proxy</dt>
          <dd className="mono text-[11px] break-all">
            {a.proxy_url || <span className="text-muted-foreground">direct</span>}
          </dd>
        </div>
        {a.base_url && (
          <div className="col-span-2">
            <dt className="eyebrow mb-1.5">Base URL</dt>
            <dd className="mono text-[11px] break-all">{a.base_url}</dd>
          </div>
        )}
        {a.model_map && Object.keys(a.model_map).length > 0 && (
          <div className="col-span-2">
            <dt className="eyebrow mb-1.5">Model map ({Object.keys(a.model_map).length})</dt>
            <dd className="mt-1 space-y-0.5">
              {Object.keys(a.model_map)
                .sort()
                .map((k) => (
                  <div key={k} className="mono text-[11px] break-all leading-relaxed">
                    <span>{k}</span>
                    {a.model_map![k] ? (
                      <>
                        <span className="text-muted-foreground"> → </span>
                        <span>{a.model_map![k]}</span>
                      </>
                    ) : (
                      <span className="text-muted-foreground"> (no rewrite)</span>
                    )}
                  </div>
                ))}
            </dd>
          </div>
        )}
      </dl>

      {u && (
        <div className="px-5 py-4 bg-muted/30 border-y border-border">
          <div className="grid grid-cols-3 gap-4 text-sm">
            <div>
              <div className="eyebrow mb-1.5">24h in/out</div>
              <div className="mono tabular font-medium">
                {fmtInt(u.sum_24h.input_tokens)} / {fmtInt(u.sum_24h.output_tokens)}
              </div>
            </div>
            <div>
              <div className="eyebrow mb-1.5">Total req</div>
              <div className="mono tabular font-medium">
                {fmtInt(u.total.requests)}
                {u.total.errors && u.total.errors > 0 ? (
                  <span className="text-destructive text-xs ml-1">
                    ({fmtInt(u.total.errors)})
                  </span>
                ) : null}
              </div>
            </div>
            <div>
              <div className="eyebrow mb-1.5">14-day</div>
              <div>
                {u.daily && u.daily.length > 0 ? (
                  <Sparkline daily={u.daily} />
                ) : (
                  <span className="text-muted-foreground text-xs mono">no data</span>
                )}
              </div>
            </div>
          </div>
        </div>
      )}

      <footer className="px-5 py-3 flex gap-1.5 flex-wrap">
        {a.kind === "oauth" && (
          <>
            <Button size="sm" variant="outline" onClick={() => onEdit(a)}>
              <Pencil className="h-3 w-3" /> Edit
            </Button>
            <Button size="sm" variant="outline" onClick={() => onAction(a, "toggle")}>
              <Power className="h-3 w-3" />
              {a.disabled ? "Enable" : "Disable"}
            </Button>
            <Button size="sm" variant="outline" onClick={() => onAction(a, "refresh")}>
              <RefreshCw className="h-3 w-3" /> Refresh
            </Button>
            {a.quota_exceeded && (
              <Button size="sm" variant="warning" onClick={() => onAction(a, "clear-quota")}>
                Clear quota
              </Button>
            )}
            {(a.hard_failure || (!a.healthy && !a.quota_exceeded && !a.disabled)) && (
              <Button size="sm" variant="warning" onClick={() => onAction(a, "clear-failure")}>
                <CheckCircle2 className="h-3 w-3" /> Mark healthy
              </Button>
            )}
            <Button
              size="sm"
              variant="outline"
              className="ml-auto border-destructive/40 text-destructive hover:bg-destructive/10"
              onClick={() => onAction(a, "delete")}
            >
              <Trash2 className="h-3 w-3" />
            </Button>
          </>
        )}
        {a.kind === "apikey" && (
          <>
            {a.file_backed && (
              <>
                <Button size="sm" variant="outline" onClick={() => onEdit(a)}>
                  <Pencil className="h-3 w-3" /> Edit
                </Button>
                <Button size="sm" variant="outline" onClick={() => onAction(a, "toggle")}>
                  <Power className="h-3 w-3" />
                  {a.disabled ? "Enable" : "Disable"}
                </Button>
              </>
            )}
            {a.quota_exceeded && (
              <Button size="sm" variant="warning" onClick={() => onAction(a, "clear-quota")}>
                Clear quota
              </Button>
            )}
            {(a.hard_failure || (!a.healthy && !a.quota_exceeded && !a.disabled)) && (
              <Button size="sm" variant="warning" onClick={() => onAction(a, "clear-failure")}>
                <CheckCircle2 className="h-3 w-3" /> Mark healthy
              </Button>
            )}
            {a.file_backed && (
              <Button
                size="sm"
                variant="outline"
                className="ml-auto border-destructive/40 text-destructive hover:bg-destructive/10"
                onClick={() => onAction(a, "delete")}
              >
                <Trash2 className="h-3 w-3" />
              </Button>
            )}
          </>
        )}
      </footer>
    </article>
  );
}

function AlertStrip({
  tone,
  icon,
  label,
  title,
  children,
}: {
  tone: "warning" | "error" | "muted";
  icon: React.ReactNode;
  label: string;
  title?: string;
  children: React.ReactNode;
}) {
  const tones = {
    warning:
      "bg-[color:var(--warning)]/10 text-[color:var(--warning)] border-[color:var(--warning)]/25",
    error: "bg-destructive/10 text-destructive border-destructive/25",
    muted: "bg-muted text-muted-foreground border-border",
  };
  return (
    <div
      className={cn(
        "px-5 py-2.5 border-b flex items-center gap-3 text-xs",
        tones[tone],
      )}
    >
      <span className="shrink-0">{icon}</span>
      <span className="eyebrow !tracking-wider">{label}</span>
      <span className="mono truncate text-[11px] opacity-90 ml-auto max-w-[60%] text-right" title={title}>
        {children}
      </span>
    </div>
  );
}
