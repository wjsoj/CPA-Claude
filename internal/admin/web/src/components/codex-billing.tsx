import { useState } from "react";
import { CreditCard, RefreshCw } from "lucide-react";
import { api } from "@/lib/api";
import type { AuthRow, CodexSubscriptionView } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { cn, fmtDate, fmtDay, fmtLocalTime } from "@/lib/utils";

// Billing panel for a Codex OAuth credential: what plan was bought, when the
// term started, whether it renews, and whether it is about to stop working for
// a payment reason.
//
// This is deliberately separate from the wham/usage panel next to it. Usage
// answers "how much quota is left in this window" and changes minute to
// minute; this answers "what was bought and until when" and changes about
// monthly. The one thing here that usage can never tell you is delinquency: a
// delinquent account serves traffic normally right up until its grace period
// ends, so nothing in the quota view moves before it dies.
//
// Every judgement (plan, free, at-risk, deadline) comes from the server, which
// computes it with cc-core's helpers. Do not re-derive them here — see the note
// on CodexSubscriptionView in lib/types.ts.

interface State {
  loading?: boolean;
  error?: string;
  data?: CodexSubscriptionView;
}

interface SubscriptionResponse {
  subscription?: CodexSubscriptionView;
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-baseline justify-between gap-3">
      <span className="eyebrow text-muted-foreground shrink-0">{label}</span>
      <span className="mono text-[11px] text-right break-words">{children}</span>
    </div>
  );
}

export function CardCodexBilling({ auth }: { auth: AuthRow }) {
  // Seed from the row so a credential probed earlier renders on page load;
  // only a manual refresh costs an upstream round trip.
  const [st, setSt] = useState<State>({ data: auth.codex_subscription });
  const [open, setOpen] = useState(false);

  const run = async () => {
    setSt((s) => ({ ...s, loading: true, error: "" }));
    try {
      const d = await api<SubscriptionResponse>(
        `/admin/api/auths/${encodeURIComponent(auth.id)}/codex-subscription`,
        { method: "POST" },
      );
      setSt({ loading: false, data: d.subscription });
      setOpen(true);
    } catch (x: any) {
      setSt((s) => ({ ...s, loading: false, error: x?.message || String(x) }));
      setOpen(true);
    }
  };

  const onClick = () => {
    if (!st.data && !st.loading) void run();
    else setOpen((o) => !o);
  };

  const s = st.data;
  const portal = s?.info?.portal;
  const ent = s?.info?.entitlement;
  const acct = s?.info?.account;
  const last = s?.info?.last_active_subscription;
  const discount = ent?.discount;
  const delinquent = !!(portal?.is_delinquent || ent?.is_delinquent);
  // An app-store purchase cannot be fixed from the web portal, so it changes
  // what an operator should do about a billing problem, not just what it says.
  const offPortal =
    last?.purchase_origin_platform && last.purchase_origin_platform !== "chatgpt_web";

  return (
    <div className="px-5 py-3 border-t border-border bg-muted/20">
      <div className="flex items-center justify-between gap-2 flex-wrap">
        <div className="flex items-center gap-2">
          <div className="eyebrow">Billing</div>
          {s?.plan && (
            <span className="mono text-[10px] px-1.5 py-0.5 rounded bg-muted text-foreground">
              {s.plan}
            </span>
          )}
          {s?.free && (
            <span
              className="mono text-[10px] px-1.5 py-0.5 rounded bg-[color:var(--success)]/15 text-[color:var(--success)]"
              title={s.free_reason ? `free because: ${s.free_reason}` : undefined}
            >
              $0
            </span>
          )}
          {s?.at_risk && (
            <span
              className={cn(
                "mono text-[10px] px-1.5 py-0.5 rounded",
                s.risk_reason === "delinquent"
                  ? "bg-destructive/15 text-destructive"
                  : "bg-[color:var(--warning)]/15 text-[color:var(--warning)]",
              )}
              title={
                s.risk_deadline
                  ? `${s.risk_reason} — until ${fmtDay(s.risk_deadline)} (${fmtDate(s.risk_deadline)})`
                  : s.risk_reason
              }
            >
              {s.risk_reason === "delinquent" ? "payment failed" : "not renewing"}
            </span>
          )}
        </div>
        <div className="flex items-center gap-1.5">
          {s?.fetched_at && (
            <span className="text-[10px] text-muted-foreground mono">
              as of {fmtLocalTime(s.fetched_at)}
            </span>
          )}
          {s && (
            <Button
              size="sm"
              variant="ghost"
              className="h-7 px-2"
              onClick={(e) => {
                e.stopPropagation();
                void run();
              }}
              disabled={st.loading}
              title="Re-probe the billing endpoints"
            >
              <RefreshCw className={cn("h-3 w-3", st.loading && "animate-spin")} />
            </Button>
          )}
          <Button size="sm" variant="outline" className="h-7" onClick={onClick} disabled={st.loading}>
            <CreditCard className="h-3 w-3" />
            {st.loading ? "Checking…" : s ? (open ? "Hide" : "Show") : "Check billing"}
          </Button>
        </div>
      </div>

      {open && (
        <div className="mt-2 space-y-2">
          {st.error && (
            <div className="text-xs text-destructive mono whitespace-pre-wrap">{st.error}</div>
          )}

          {s?.at_risk && (
            <div
              className={cn(
                "text-[11px] rounded border px-2.5 py-2 leading-relaxed",
                s.risk_reason === "delinquent"
                  ? "border-destructive/30 bg-destructive/10 text-destructive"
                  : "border-[color:var(--warning)]/30 bg-[color:var(--warning)]/10 text-[color:var(--warning)]",
              )}
            >
              {s.risk_reason === "delinquent" ? (
                <>
                  A renewal charge failed. The account keeps serving traffic until
                  {s.risk_deadline ? ` ${fmtDay(s.risk_deadline)} (${fmtDate(s.risk_deadline)})` : " its grace period ends"}, then
                  loses entitlement.
                </>
              ) : (
                <>
                  Set not to renew — the subscription lapses
                  {s.risk_deadline ? ` on ${fmtDay(s.risk_deadline)} (${fmtDate(s.risk_deadline)})` : ""}.
                </>
              )}
              {offPortal && (
                <>
                  {" "}
                  Bought through <span className="mono">{last?.purchase_origin_platform}</span>, so
                  it must be fixed there, not in the web portal.
                </>
              )}
            </div>
          )}

          {s && (
            <div className="space-y-1">
              <Row label="Term">
                {s.purchased_at ? fmtDay(s.purchased_at) : "—"}
                <span className="text-muted-foreground"> → </span>
                {s.expires_at ? fmtDay(s.expires_at) : "—"}
              </Row>
              <Row label="Renews">
                {portal
                  ? portal.will_renew
                    ? <span className="text-[color:var(--success)]">yes</span>
                    : <span className="text-[color:var(--warning)]">no</span>
                  : last
                    ? last.will_renew
                      ? <span className="text-[color:var(--success)]">yes</span>
                      : <span className="text-[color:var(--warning)]">no</span>
                    : "—"}
                {portal?.billing_period && (
                  <span className="text-muted-foreground"> · {portal.billing_period}</span>
                )}
                {portal?.billing_currency && (
                  <span className="text-muted-foreground"> {portal.billing_currency}</span>
                )}
              </Row>
              {s.free && (
                <Row label="Free">
                  {s.free_reason === "gratis" ? "comped" : s.free_reason?.replace(/^promo:/, "") || "yes"}
                  {discount?.discount_expires_at && (
                    <span className="text-muted-foreground">
                      {" "}
                      · ends {fmtDay(discount.discount_expires_at)}
                    </span>
                  )}
                </Row>
              )}
              {!!portal?.seats_entitled && portal.seats_entitled > 1 && (
                <Row label="Seats">
                  {portal.seats_in_use ?? "?"} / {portal.seats_entitled}
                </Row>
              )}
              {ent?.subscription_plan && <Row label="Plan id">{ent.subscription_plan}</Row>}
              {last?.purchase_origin_platform && (
                <Row label="Bought via">{last.purchase_origin_platform}</Row>
              )}
              {acct?.structure && (
                <Row label="Account">
                  {acct.structure}
                  {acct.created_time && (
                    <span className="text-muted-foreground">
                      {" "}
                      · since {fmtDay(acct.created_time)}
                    </span>
                  )}
                </Row>
              )}
              {/* Distinguishes a never-paid free account from one whose paid
                  term lapsed — identical on plan_type alone. */}
              {acct && !ent?.has_active_subscription && (
                <Row label="History">
                  {acct.has_previously_paid_subscription ? "previously paid" : "never paid"}
                </Row>
              )}
              {delinquent && portal?.grace_period_end_timestamp ? (
                <Row label="Grace ends">
                  {fmtDay(new Date(portal.grace_period_end_timestamp * 1000).toISOString())}
                </Row>
              ) : null}
            </div>
          )}

          {!s && !st.error && !st.loading && (
            <div className="text-[11px] text-muted-foreground">
              Not probed yet. This reads the ChatGPT billing portal; it does not send a request
              through the credential.
            </div>
          )}
        </div>
      )}
    </div>
  );
}
