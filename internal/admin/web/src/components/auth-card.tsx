import React from "react";
import type { AuthRow } from "@/lib/types";
import { Sparkline } from "./sparkline";
import { CardUpstreamCodex, CardUpstreamQuota } from "./upstream-quota";
import { CardCodexBilling } from "./codex-billing";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { GroupBadge } from "./group-badge";
import { cn, fmtDate, fmtDay, fmtInt, fmtUSD } from "@/lib/utils";
import { credState, credServing } from "@/lib/cred-state";
import { CredStateDot, STATE_META, stateDetail, toneText } from "./cred-state-badge";
import {
  AlertTriangle,
  Ban,
  CheckCircle2,
  ChevronRight,
  CreditCard,
  Gauge,
  PauseCircle,
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
  // Optional drag handle (rendered in the header) for sortable API-key cards.
  // Omitted for OAuth and read-only (config.yaml) API keys.
  dragHandle?: React.ReactNode;
}

export function AuthCard({ a, onAction, onEdit, dragHandle }: Props) {
  const slot =
    a.max_concurrent > 0 ? `${a.active_clients}/${a.max_concurrent}` : `${a.active_clients}/∞`;
  const slotRatio =
    a.max_concurrent > 0
      ? Math.min(100, Math.round((a.active_clients / a.max_concurrent) * 100))
      : 0;
  // Health comes straight from the backend's `state`; the card never rebuilds
  // its own ladder (see lib/cred-state.ts).
  const state = credState(a);
  const meta = STATE_META[state];
  const detail = stateDetail(a, state);
  // `reason` is the contract field; `failure_reason` is the pre-contract one.
  // The admin console is authenticated, so it shows the raw text — only the
  // public status page sanitizes.
  const failureText = a.reason || a.failure_reason || "";
  // The model map can hold dozens of entries; fully expanded it dwarfs the rest
  // of the card, so it stays folded until asked for.
  const [mapOpen, setMapOpen] = React.useState(false);
  const mapKeys = React.useMemo(
    () => (a.model_map ? Object.keys(a.model_map).sort() : []),
    [a.model_map],
  );
  const u = a.usage;
  const kindLabel = a.kind === "apikey" ? "API key" : "OAuth";
  const recentCancel =
    a.last_client_cancel && Date.now() - new Date(a.last_client_cancel).getTime() < 3600 * 1000;

  return (
    // No overflow-hidden: the alert strips' hover panels extend past the card
    // on short cards. Nothing needs the clip — the accent bar below is a
    // gradient that already fades to transparent at both ends.
    <article className="relative group bg-card border border-border-strong rounded-md transition-all duration-300 hover:-translate-y-0.5 hover:shadow-xl hover:shadow-primary/5 hover:border-primary/40">
      {/* Thin accent bar that tinges on hover */}
      <div
        aria-hidden
        className={cn(
          "absolute inset-x-0 top-0 h-[2px] transition-all",
          // Only a verified-healthy credential gets the "alive" accent.
          state === "healthy"
            ? "bg-gradient-to-r from-transparent via-primary/40 to-transparent opacity-60 group-hover:opacity-100"
            : "bg-transparent",
        )}
      />

      <header className="px-5 py-4 border-b border-border flex items-start justify-between gap-3">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2.5">
            {dragHandle}
            <CredStateDot row={a} />
            <h3 className="font-display text-xl leading-tight truncate">{a.label || a.id}</h3>
          </div>
          <div className="mt-1 mono text-[11px] text-muted-foreground truncate pl-4.5">{a.id}</div>
        </div>
        <div className="flex flex-col items-end gap-1 shrink-0">
          <Badge variant={a.provider === "openai" ? "violet" : "slate"} title="Upstream provider">
            {a.provider === "openai" ? "Codex" : "Claude"}
          </Badge>
          <Badge variant={a.kind === "apikey" ? "blue" : "slate"}>{kindLabel}</Badge>
          {a.plan_type && (
            <Badge variant="slate" title="ChatGPT subscription plan (from id_token)">
              {a.plan_type}
            </Badge>
          )}
          <GroupBadge group={a.group} />

          <span
            className={cn("eyebrow !text-[10px]", toneText(meta.tone))}
            title={meta.blurb}
          >
            {meta.label}
          </span>
          {detail && (
            <span className="mono text-[10px] text-muted-foreground text-right max-w-[150px]">
              {detail}
            </span>
          )}
          {!credServing(a) && state !== "disabled" && (
            <span className="eyebrow !text-[10px] text-muted-foreground">not serving</span>
          )}
        </div>
      </header>

      {/* One strip per state, driven by `state` alone — no overlapping
          conditions, so a cooling credential can no longer be described as
          both quota-exceeded and unhealthy depending on which branch won. */}
      {state === "quota" && (
        <AlertStrip tone="warning" icon={<Gauge className="h-3.5 w-3.5" />} label="Quota exceeded">
          {a.quota_reset_at
            ? `resets ${fmtDate(a.quota_reset_at)}`
            : detail || "no reset time reported"}
        </AlertStrip>
      )}
      {state === "cooling" && (
        <AlertStrip tone="warning" icon={<PauseCircle className="h-3.5 w-3.5" />} label="Channel paused">
          {`repeated upstream errors — traffic rotated to other keys. ${
            detail || "retry time unknown"
          }; one good response restores it automatically.`}
        </AlertStrip>
      )}
      {state === "half_open" && (
        <AlertStrip
          tone="warning"
          icon={<AlertTriangle className="h-3.5 w-3.5" />}
          label="Unverified"
        >
          {`pause elapsed, no successful request since — the next request decides. ${
            a.last_success_at ? `last success ${fmtDate(a.last_success_at)}` : "no recorded success"
          }`}
        </AlertStrip>
      )}
      {(state === "hard_failed" || state === "degraded") && failureText && (
        <AlertStrip
          tone={state === "hard_failed" ? "error" : "warning"}
          icon={
            state === "hard_failed" ? (
              <ShieldOff className="h-3.5 w-3.5" />
            ) : (
              <AlertTriangle className="h-3.5 w-3.5" />
            )
          }
          label={state === "hard_failed" ? "Failed" : "Recent failure"}
          title={failureText}
        >
          {failureText}
        </AlertStrip>
      )}
      {/* A billing problem is invisible to every health signal we have: the
          account keeps serving traffic normally until its grace period ends,
          then stops. Surface it at the top of the card rather than only inside
          the billing panel, which requires knowing to open it. */}
      {a.codex_subscription?.at_risk && (
        <AlertStrip
          tone={a.codex_subscription.risk_reason === "delinquent" ? "error" : "warning"}
          icon={<CreditCard className="h-3.5 w-3.5" />}
          label={
            a.codex_subscription.risk_reason === "delinquent" ? "Payment failed" : "Not renewing"
          }
        >
          {a.codex_subscription.risk_reason === "delinquent"
            ? `a renewal charge failed — this credential keeps working until ${
                a.codex_subscription.risk_deadline
                  ? `${fmtDay(a.codex_subscription.risk_deadline)} (${fmtDate(a.codex_subscription.risk_deadline)})`
                  : "its grace period ends"
              }, then loses entitlement.`
            : `subscription is set not to renew and lapses on ${
                a.codex_subscription.risk_deadline
                  ? `${fmtDay(a.codex_subscription.risk_deadline)} (${fmtDate(a.codex_subscription.risk_deadline)})`
                  : "its term end"
              }.`}
        </AlertStrip>
      )}
      {recentCancel && (
        <AlertStrip
          tone="muted"
          icon={<Ban className="h-3.5 w-3.5" />}
          label="Client canceled"
          title={
            fmtDate(a.last_client_cancel!) +
            (a.client_cancel_reason ? " · " + a.client_cancel_reason : "")
          }
        >
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
        {a.kind === "apikey" && !!a.price_multiplier && a.price_multiplier > 0 && (
          <div className="col-span-2">
            <dt className="eyebrow mb-1.5">Billing override</dt>
            <dd className="mono text-[11px]">
              official × {a.price_multiplier}
              <span className="text-muted-foreground"> (bypasses group rate)</span>
            </dd>
          </div>
        )}
        {mapKeys.length > 0 && (
          <div className="col-span-2">
            <dt>
              <button
                type="button"
                onClick={() => setMapOpen((v) => !v)}
                aria-expanded={mapOpen}
                className="eyebrow flex w-full items-center gap-1 hover:text-foreground transition-colors"
              >
                <ChevronRight
                  className={cn(
                    "h-3 w-3 shrink-0 transition-transform",
                    mapOpen && "rotate-90",
                  )}
                />
                <span>Model map ({mapKeys.length})</span>
              </button>
            </dt>
            {mapOpen && (
              <dd className="mt-1.5 space-y-0.5 pl-4">
                {mapKeys.map((k) => (
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
            )}
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
              <div className="eyebrow mt-2 mb-0.5">Total cost</div>
              <div className="mono tabular text-xs font-medium">
                {fmtUSD(u.total_cost_usd)}
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

      {a.kind === "oauth" && a.provider === "anthropic" && <CardUpstreamQuota auth={a} />}
      {a.kind === "oauth" && a.provider === "openai" && <CardUpstreamCodex auth={a} />}
      {a.kind === "oauth" && a.provider === "openai" && <CardCodexBilling auth={a} />}
      {a.kind === "oauth" && a.provider === "openai" && u && (
        <div className="px-5 py-3 border-t border-border bg-muted/20">
          <div className="flex items-center justify-between gap-2">
            <div className="eyebrow">Rolling 5h (local)</div>
            <div className="mono tabular text-xs">
              in {fmtInt(u.sum_5h.input_tokens)} · out {fmtInt(u.sum_5h.output_tokens)}
              {u.sum_5h.cache_read_tokens ? (
                <span className="text-muted-foreground"> · cache {fmtInt(u.sum_5h.cache_read_tokens)}</span>
              ) : null}
            </div>
          </div>
          <div className="mt-1 text-[10px] text-muted-foreground leading-snug">
            Local counter from our own request log. Backend-reported quota, when available,
            appears below.
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
            {state === "quota" && (
              <Button size="sm" variant="warning" onClick={() => onAction(a, "clear-quota")}>
                Clear quota
              </Button>
            )}
            {(state === "hard_failed" ||
              state === "cooling" ||
              state === "degraded" ||
              state === "half_open") && (
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
            {state === "quota" && (
              <Button size="sm" variant="warning" onClick={() => onAction(a, "clear-quota")}>
                Clear quota
              </Button>
            )}
            {(state === "hard_failed" ||
              state === "cooling" ||
              state === "degraded" ||
              state === "half_open") && (
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
  // The body is clipped to one line so cards keep a uniform height, which
  // means the tail of a long message — the part naming the failing model or
  // the retry time — is otherwise unreachable. Show it in full on hover:
  // an explicit `title` wins, otherwise reuse the body when it is plain text.
  const full = title ?? (typeof children === "string" ? children : undefined);
  return (
    <div
      className={cn(
        "relative group/strip px-5 py-2.5 border-b flex items-center gap-3 text-xs",
        full && "cursor-help",
        tones[tone],
      )}
    >
      <span className="shrink-0">{icon}</span>
      <span className="eyebrow !tracking-wider">{label}</span>
      <span className="mono truncate text-[11px] opacity-90 ml-auto max-w-[60%] text-right">
        {children}
      </span>
      {full && (
        // Rendered inside the strip so moving the pointer down into the panel
        // keeps the group hovered; that in turn lets the text stay selectable
        // (pointer-events-none would make it uncopyable).
        <div className="absolute left-0 right-0 top-full z-30 hidden group-hover/strip:block">
          <div className="mx-3 mt-1 rounded-md border border-border-strong bg-popover text-foreground shadow-xl px-3 py-2 mono text-[11px] leading-relaxed whitespace-pre-wrap break-words max-h-48 overflow-auto">
            {full}
          </div>
        </div>
      )}
    </div>
  );
}

