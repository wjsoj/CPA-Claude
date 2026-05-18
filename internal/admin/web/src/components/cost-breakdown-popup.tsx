import {
  HoverCard,
  HoverCardContent,
  HoverCardPortal,
  HoverCardTrigger,
} from "@/components/ui/hover-card";
import type { Pricing } from "@/lib/types";
import { lookupPrice, matchedModelKey } from "@/lib/pricing";
import { cn, fmtInt } from "@/lib/utils";

interface Props {
  provider?: string;
  model?: string;
  inputTokens: number;
  outputTokens: number;
  cacheReadTokens: number;
  cacheCreateTokens: number;
  // costUsd is the official upstream cost. billedUsd (if > 0) is what
  // actually hit the user's wallet — costUsd × multiplier. When
  // billedUsd is set the popup's footer strip + the bottom-line total
  // shift to "You paid" semantics; when unset (admin view, or rows
  // pre-dating SaaS billing) we fall back to showing costUsd alone.
  costUsd: number;
  billedUsd?: number;
  multiplier?: number;
  pricing?: Pricing | null;
  children: React.ReactNode;
}

function fmtRate(perMillion: number): string {
  if (!perMillion) return "$0.00";
  if (perMillion >= 1) return `$${perMillion.toFixed(2)}`;
  return `$${perMillion.toFixed(4)}`;
}

function fmtCost(c: number): string {
  if (c === 0) return "$0.000000";
  if (Math.abs(c) >= 0.01) return `$${c.toFixed(4)}`;
  return `$${c.toFixed(6)}`;
}

type Row = {
  key: string;
  label: string;
  tokens: number;
  rate: number;
  cost: number;
  dim?: boolean;
};

export function CostBreakdownPopup({
  provider,
  model,
  inputTokens,
  outputTokens,
  cacheReadTokens,
  cacheCreateTokens,
  costUsd,
  billedUsd,
  multiplier,
  pricing,
  children,
}: Props) {
  const price = lookupPrice(pricing, provider, model);
  const matched = matchedModelKey(pricing, provider, model);
  // When the row carries a billed amount + multiplier (status page side),
  // surface the "you paid" math; the admin panel passes neither so the
  // popup degrades to its original "official cost only" form.
  const hasBilling =
    billedUsd !== undefined && billedUsd > 0 && multiplier !== undefined && multiplier > 0;
  const headlineTotal = hasBilling ? (billedUsd as number) : costUsd;

  const rows: Row[] = price
    ? [
        {
          key: "in",
          label: "Input",
          tokens: inputTokens,
          rate: price.input_per_1m,
          cost: (inputTokens * price.input_per_1m) / 1_000_000,
        },
        {
          key: "out",
          label: "Output",
          tokens: outputTokens,
          rate: price.output_per_1m,
          cost: (outputTokens * price.output_per_1m) / 1_000_000,
        },
        {
          key: "cr",
          label: "Cache read",
          tokens: cacheReadTokens,
          rate: price.cache_read_per_1m,
          cost: (cacheReadTokens * price.cache_read_per_1m) / 1_000_000,
          dim: true,
        },
        {
          key: "cw",
          label: "Cache write",
          tokens: cacheCreateTokens,
          rate: price.cache_create_per_1m,
          cost: (cacheCreateTokens * price.cache_create_per_1m) / 1_000_000,
          dim: true,
        },
      ]
    : [];

  const computed = rows.reduce((s, r) => s + r.cost, 0);
  const diff = costUsd - computed;
  const drift = price ? Math.abs(diff) > 0.0005 : false;

  return (
    <HoverCard openDelay={120} closeDelay={80}>
      <HoverCardTrigger asChild>{children}</HoverCardTrigger>
      <HoverCardPortal>
        <HoverCardContent
          side="left"
          align="start"
          sideOffset={10}
          collisionPadding={12}
          className={cn(
            "w-[22rem] max-w-[92vw] p-0 overflow-hidden mono",
            "rounded-xl border border-border/80 bg-popover text-popover-foreground",
            "shadow-[0_20px_70px_-20px_rgba(0,0,0,0.45),0_4px_12px_-6px_rgba(0,0,0,0.25)]",
          )}
        >
          {/* Hairline accent ribbon along the top edge */}
          <div
            aria-hidden
            className="h-[2px] w-full bg-gradient-to-r from-primary/0 via-primary/70 to-primary/0 animate-in fade-in-0 zoom-in-95 fill-mode-both"
            style={{ animationDuration: "260ms" }}
          />

          {/* Header — model + total */}
          <div
            className="flex items-start justify-between gap-3 px-4 pt-3.5 pb-3 border-b border-border/60 animate-in fade-in-0 slide-in-from-top-1 fill-mode-both"
            style={{ animationDelay: "40ms", animationDuration: "280ms" }}
          >
            <div className="min-w-0 flex-1">
              <div className="eyebrow text-[9px] uppercase tracking-[0.18em] opacity-60">
                Cost breakdown
              </div>
              <div
                className="mt-1 text-xs font-medium text-foreground truncate"
                title={model || ""}
              >
                {model || "—"}
              </div>
              <div className="mt-0.5 text-[10px] opacity-60">
                {(provider || "anthropic")}
              </div>
            </div>
            <div className="text-right shrink-0">
              <div className="eyebrow text-[9px] uppercase tracking-[0.18em] opacity-60">
                {hasBilling ? "You paid" : "Total"}
              </div>
              <div className="mt-1 text-sm font-semibold tabular text-foreground">
                {fmtCost(headlineTotal)}
              </div>
              {hasBilling && (
                <div className="mt-0.5 text-[10px] opacity-60 tabular">
                  ×{(multiplier as number).toFixed(4)} of {fmtCost(costUsd)}
                </div>
              )}
            </div>
          </div>

          {/* Body */}
          {!price ? (
            <div className="px-4 py-4 text-[11px] text-muted-foreground">
              pricing catalog unavailable — cost was logged as{" "}
              <span className="text-foreground">{fmtCost(costUsd)}</span> by the
              server.
            </div>
          ) : (
            <div className="px-4 pt-2.5 pb-3 space-y-1">
              <div className="grid grid-cols-[auto_1fr_auto] gap-x-3 items-baseline pb-1.5 eyebrow text-[9px] uppercase tracking-[0.15em] opacity-55">
                <span>Category</span>
                <span className="text-right">Tokens × Rate / 1M</span>
                <span className="text-right">Subtotal</span>
              </div>
              {rows.map((r, i) => (
                <div
                  key={r.key}
                  className={cn(
                    "grid grid-cols-[auto_1fr_auto] gap-x-3 items-baseline text-[11px] py-0.5",
                    r.dim && "opacity-70",
                    "animate-in fade-in-0 slide-in-from-left-2 fill-mode-both",
                  )}
                  style={{
                    animationDelay: `${100 + i * 50}ms`,
                    animationDuration: "280ms",
                  }}
                >
                  <span className="text-foreground/85">{r.label}</span>
                  <span className="text-right opacity-70 tabular whitespace-nowrap">
                    <span className="text-foreground">{fmtInt(r.tokens)}</span>
                    <span className="opacity-60"> × </span>
                    <span>{fmtRate(r.rate)}</span>
                    <span className="opacity-60">/M</span>
                  </span>
                  <span className="text-right tabular text-foreground">
                    {fmtCost(r.cost)}
                  </span>
                </div>
              ))}

              {/* Σ upstream — sum of the per-bucket subtotals (the
                  catalog rate × tokens, before any group multiplier). */}
              <div
                className="grid grid-cols-[auto_1fr_auto] gap-x-3 items-baseline text-[11px] pt-2 mt-1 border-t border-dashed border-border/60 animate-in fade-in-0 fill-mode-both"
                style={{
                  animationDelay: `${100 + rows.length * 50}ms`,
                  animationDuration: "300ms",
                }}
              >
                <span className="opacity-70">
                  {hasBilling ? "Σ upstream" : "Σ computed"}
                </span>
                <span />
                <span
                  className={cn(
                    "text-right tabular",
                    hasBilling ? "opacity-70" : "font-medium",
                  )}
                >
                  {fmtCost(computed)}
                </span>
              </div>
              {/* Multiplier line — present only when SaaS billing
                  applied to this row. */}
              {hasBilling && (
                <div
                  className="grid grid-cols-[auto_1fr_auto] gap-x-3 items-baseline text-[11px] animate-in fade-in-0 fill-mode-both"
                  style={{
                    animationDelay: `${100 + (rows.length + 1) * 50}ms`,
                    animationDuration: "300ms",
                  }}
                >
                  <span className="opacity-70">× multiplier</span>
                  <span />
                  <span className="text-right tabular opacity-70">
                    {(multiplier as number).toFixed(4)}
                  </span>
                </div>
              )}
              {/* = billed (you paid). */}
              {hasBilling && (
                <div
                  className="grid grid-cols-[auto_1fr_auto] gap-x-3 items-baseline text-[11px] pt-1 mt-0.5 border-t border-dashed border-border/40 animate-in fade-in-0 fill-mode-both"
                  style={{
                    animationDelay: `${100 + (rows.length + 2) * 50}ms`,
                    animationDuration: "300ms",
                  }}
                >
                  <span className="font-medium">= billed</span>
                  <span />
                  <span className="text-right tabular font-medium">
                    {fmtCost(billedUsd as number)}
                  </span>
                </div>
              )}
            </div>
          )}

          {/* Settled — accented strip at the bottom */}
          {price && (
            <div
              className={cn(
                "relative px-4 py-2.5 flex items-baseline justify-between gap-3",
                "border-t border-border/60 bg-primary/[0.07]",
                "animate-in fade-in-0 slide-in-from-bottom-1 fill-mode-both",
              )}
              style={{
                animationDelay: `${100 + (rows.length + 1) * 50}ms`,
                animationDuration: "320ms",
              }}
            >
              <span className="eyebrow text-[10px] uppercase tracking-[0.15em] text-foreground/80">
                {hasBilling ? "Wallet debited" : "Server logged"}
              </span>
              <span className="text-sm font-semibold tabular text-foreground">
                {fmtCost(headlineTotal)}
              </span>
              <span
                aria-hidden
                className="absolute inset-y-0 left-0 w-[2px] bg-primary"
              />
            </div>
          )}

          {/* Drift warning */}
          {drift && (
            <div
              className="px-4 py-2 border-t border-amber-500/30 bg-amber-500/10 text-amber-600 dark:text-amber-400 text-[10px] leading-relaxed animate-in fade-in-0 fill-mode-both"
              style={{
                animationDelay: `${100 + (rows.length + 2) * 50}ms`,
                animationDuration: "320ms",
              }}
              title="Local recompute differs from server-recorded total. Pricing catalog may have updated since the row was logged, or the request triggered an advisor sub-call billed separately."
            >
              Δ vs. server-logged: {diff >= 0 ? "+" : ""}
              {fmtCost(diff)} — catalog may have shifted, or an advisor sub-call
              was billed separately.
            </div>
          )}

          {/* Footer note */}
          {price && (
            <div
              className="px-4 py-2 border-t border-border/40 text-[10px] opacity-55 leading-snug animate-in fade-in-0 fill-mode-both"
              style={{
                animationDelay: `${100 + (rows.length + 3) * 50}ms`,
                animationDuration: "320ms",
              }}
            >
              Rates from catalog
              {matched ? (
                <>
                  {" · "}
                  <span className="text-foreground">{matched}</span>
                </>
              ) : (
                " · provider/global default"
              )}
              . Server cost is authoritative.
            </div>
          )}
        </HoverCardContent>
      </HoverCardPortal>
    </HoverCard>
  );
}
