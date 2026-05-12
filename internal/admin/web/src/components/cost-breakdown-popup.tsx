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
  costUsd: number;
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
  shade: string;
};

export function CostBreakdownPopup({
  provider,
  model,
  inputTokens,
  outputTokens,
  cacheReadTokens,
  cacheCreateTokens,
  costUsd,
  pricing,
  children,
}: Props) {
  const price = lookupPrice(pricing, provider, model);
  const matched = matchedModelKey(pricing, provider, model);

  const rows: Row[] = price
    ? [
        {
          key: "in",
          label: "Input",
          tokens: inputTokens,
          rate: price.input_per_1m,
          cost: (inputTokens * price.input_per_1m) / 1_000_000,
          shade: "text-foreground",
        },
        {
          key: "out",
          label: "Output",
          tokens: outputTokens,
          rate: price.output_per_1m,
          cost: (outputTokens * price.output_per_1m) / 1_000_000,
          shade: "text-foreground",
        },
        {
          key: "cr",
          label: "Cache read",
          tokens: cacheReadTokens,
          rate: price.cache_read_per_1m,
          cost: (cacheReadTokens * price.cache_read_per_1m) / 1_000_000,
          shade: "opacity-80",
        },
        {
          key: "cw",
          label: "Cache write",
          tokens: cacheCreateTokens,
          rate: price.cache_create_per_1m,
          cost: (cacheCreateTokens * price.cache_create_per_1m) / 1_000_000,
          shade: "opacity-80",
        },
      ]
    : [];

  const computed = rows.reduce((s, r) => s + r.cost, 0);
  const diff = costUsd - computed;
  const reconciles = price ? Math.abs(diff) < 0.0001 : false;

  return (
    <HoverCard openDelay={120} closeDelay={80}>
      <HoverCardTrigger asChild>{children}</HoverCardTrigger>
      <HoverCardPortal>
        <HoverCardContent
          side="left"
          align="center"
          sideOffset={8}
          className="w-[320px] p-0 mono"
        >
          {/* Header */}
          <div className="flex items-center justify-between gap-3 px-3 pt-3 pb-2 border-b border-border/60">
            <div className="min-w-0">
              <div className="eyebrow text-[10px] opacity-60 leading-none mb-1">
                Cost breakdown
              </div>
              <div className="text-[11px] truncate" title={model || ""}>
                {model || "—"}
              </div>
            </div>
            <div className="text-right tabular">
              <div className="eyebrow text-[10px] opacity-60 leading-none mb-1">Total</div>
              <div className="text-sm font-medium">{fmtCost(costUsd)}</div>
            </div>
          </div>

          {/* Body */}
          {!price ? (
            <div className="px-3 py-4 text-[11px] text-muted-foreground">
              pricing catalog unavailable — cost was logged as{" "}
              <span className="text-foreground">{fmtCost(costUsd)}</span> by the server.
            </div>
          ) : (
            <div className="px-3 py-2.5 space-y-1.5">
              <div className="grid grid-cols-[1fr_auto_auto] gap-x-3 gap-y-0 items-baseline text-[11px]">
                <div className="eyebrow text-[9px] opacity-50">Category</div>
                <div className="eyebrow text-[9px] opacity-50 text-right">Tokens × Rate / 1M</div>
                <div className="eyebrow text-[9px] opacity-50 text-right">Subtotal</div>
              </div>
              {rows.map((r, i) => (
                <div
                  key={r.key}
                  className={cn(
                    "grid grid-cols-[1fr_auto_auto] gap-x-3 items-baseline text-[11px]",
                    "animate-in fade-in-0 slide-in-from-left-1 fill-mode-both",
                    r.shade,
                  )}
                  style={{ animationDelay: `${60 + i * 45}ms`, animationDuration: "260ms" }}
                >
                  <div className="truncate">{r.label}</div>
                  <div className="tabular text-right opacity-70 whitespace-nowrap">
                    <span className="text-foreground">{fmtInt(r.tokens)}</span>
                    <span className="opacity-50"> × </span>
                    <span>{fmtRate(r.rate)}</span>
                  </div>
                  <div className="tabular text-right whitespace-nowrap">{fmtCost(r.cost)}</div>
                </div>
              ))}
              {/* Sum line */}
              <div
                className="grid grid-cols-[1fr_auto_auto] gap-x-3 items-baseline text-[11px] pt-1.5 mt-1 border-t border-border/50 animate-in fade-in-0 fill-mode-both"
                style={{ animationDelay: `${60 + rows.length * 45}ms`, animationDuration: "300ms" }}
              >
                <div className="opacity-70">Σ computed</div>
                <div />
                <div className="tabular text-right font-medium">{fmtCost(computed)}</div>
              </div>
              {!reconciles && (
                <div
                  className="grid grid-cols-[1fr_auto] gap-x-3 items-baseline text-[10px] text-amber-600 dark:text-amber-400 animate-in fade-in-0 fill-mode-both"
                  style={{
                    animationDelay: `${60 + (rows.length + 1) * 45}ms`,
                    animationDuration: "300ms",
                  }}
                  title="Local recompute differs from server-recorded total. Pricing catalog may have updated since the row was logged, or the request triggered an advisor sub-call billed separately."
                >
                  <div>Δ vs. server-logged</div>
                  <div className="tabular text-right">
                    {diff >= 0 ? "+" : ""}
                    {fmtCost(diff)}
                  </div>
                </div>
              )}
              {/* Footer note */}
              <div
                className="pt-1.5 mt-1 border-t border-border/40 text-[10px] opacity-55 leading-snug animate-in fade-in-0 fill-mode-both"
                style={{
                  animationDelay: `${60 + (rows.length + 2) * 45}ms`,
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
                . Server cost is authoritative; local sum shown for traceability.
              </div>
            </div>
          )}
        </HoverCardContent>
      </HoverCardPortal>
    </HoverCard>
  );
}
