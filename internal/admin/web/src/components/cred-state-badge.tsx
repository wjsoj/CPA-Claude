import type { CSSProperties } from "react";
import {
  AlertTriangle,
  CheckCircle2,
  CircleOff,
  Gauge,
  PauseCircle,
  ShieldOff,
  ShieldQuestion,
} from "lucide-react";
import {
  credState,
  recoveryCountdown,
  type CredState,
  type CredStateFields,
} from "@/lib/cred-state";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";

// The one badge. Every credential-health indicator in the app renders through
// here so the admin console, the credentials panel and the public status page
// cannot disagree about what a credential is doing.
//
// Colour is deliberately coarse — green / amber / red / grey — and the icon
// carries the distinction inside the amber band. The important invariant:
// ONLY `healthy` is green, and only `healthy` gets the live pulse. `half_open`
// (breaker window elapsed, nothing has succeeded since) is amber, because a
// key that has never once served a request is not a working key.

type Tone = "success" | "warning" | "destructive" | "muted";

export interface StateMeta {
  label: string;
  tone: Tone;
  Icon: typeof CheckCircle2;
  /** Short explanation shown under the badge / in the title attribute. */
  blurb: string;
}

export const STATE_META: Record<CredState, StateMeta> = {
  healthy: {
    label: "Healthy",
    tone: "success",
    Icon: CheckCircle2,
    blurb: "最近有成功请求，正常参与轮换。",
  },
  half_open: {
    label: "Unverified",
    tone: "warning",
    Icon: ShieldQuestion,
    blurb: "暂停已结束，尚未有成功请求验证 — 下一次请求才知道它是否真的恢复。",
  },
  degraded: {
    label: "Degraded",
    tone: "warning",
    Icon: AlertTriangle,
    blurb: "连续失败但仍在轮换中，随时可能被判定为不可用。",
  },
  quota: {
    label: "Quota",
    tone: "warning",
    Icon: Gauge,
    blurb: "上游配额已用尽，等待额度窗口重置。",
  },
  cooling: {
    label: "Paused",
    tone: "warning",
    Icon: PauseCircle,
    blurb: "熔断冷却中 — 已暂时移出轮换，到期后会再探一次。",
  },
  hard_failed: {
    label: "Failed",
    tone: "destructive",
    Icon: ShieldOff,
    blurb: "已判定为不可用，不再接受流量，需要人工处理。",
  },
  disabled: {
    label: "Disabled",
    tone: "muted",
    Icon: CircleOff,
    blurb: "已由管理员停用。",
  },
};

/** Inline style per tone. Uses the theme tokens so dark mode follows. */
export function toneStyle(tone: Tone): CSSProperties | undefined {
  switch (tone) {
    case "success":
      return {
        background: "color-mix(in oklab, var(--success) 15%, transparent)",
        color: "var(--success)",
        borderColor: "color-mix(in oklab, var(--success) 40%, transparent)",
      };
    case "warning":
      return {
        background: "color-mix(in oklab, var(--warning) 15%, transparent)",
        color: "var(--warning)",
        borderColor: "color-mix(in oklab, var(--warning) 40%, transparent)",
      };
    default:
      return undefined;
  }
}

export function toneText(tone: Tone): string {
  switch (tone) {
    case "success":
      return "text-[color:var(--success)]";
    case "warning":
      return "text-[color:var(--warning)]";
    case "destructive":
      return "text-destructive";
    default:
      return "text-muted-foreground";
  }
}

export function toneDot(tone: Tone): string {
  switch (tone) {
    case "success":
      return "bg-[color:var(--success)]";
    case "warning":
      return "bg-[color:var(--warning)]";
    case "destructive":
      return "bg-destructive";
    default:
      return "bg-muted-foreground";
  }
}

/**
 * The quantitative tail of a state: how many failures, how long until the
 * pause lifts, which backoff round we're on. Empty string when the row carries
 * no such detail.
 */
export function stateDetail(row: CredStateFields, state = credState(row)): string {
  const eta = recoveryCountdown(row);
  switch (state) {
    case "degraded": {
      const n = row.consecutive_failures ?? 0;
      return n > 0 ? `连续 ${n} 次失败` : "";
    }
    case "quota":
      return eta ? `${eta} 后恢复` : "恢复时间未知";
    case "cooling": {
      const parts: string[] = [];
      parts.push(eta ? `${eta} 后重试` : "重试时间未知");
      const strikes = row.quarantine_strikes ?? 0;
      if (strikes > 0) parts.push(`退避第 ${strikes} 轮`);
      return parts.join(" · ");
    }
    case "half_open": {
      const strikes = row.quarantine_strikes ?? 0;
      return strikes > 0 ? `退避第 ${strikes} 轮后待验证` : "待验证";
    }
    case "hard_failed": {
      const n = row.consecutive_failures ?? 0;
      return n > 0 ? `连续 ${n} 次失败` : "";
    }
    default:
      return "";
  }
}

export function CredStateBadge({
  row,
  className,
  withDetail = false,
}: {
  row: CredStateFields;
  className?: string;
  /** Append the countdown / failure count inside the badge. */
  withDetail?: boolean;
}) {
  const state = credState(row);
  const meta = STATE_META[state];
  const detail = withDetail ? stateDetail(row, state) : "";
  const variant =
    meta.tone === "destructive" ? "destructive" : meta.tone === "muted" ? "slate" : undefined;
  return (
    <Badge
      variant={variant}
      style={variant ? undefined : toneStyle(meta.tone)}
      className={cn("gap-1 shrink-0", className)}
      title={`${meta.label} — ${meta.blurb}`}
    >
      <meta.Icon className="h-3 w-3" />
      {meta.label}
      {detail && <span className="opacity-70 font-normal">· {detail}</span>}
    </Badge>
  );
}

/**
 * The status dot used on the admin auth card. `healthy` is the only state that
 * animates — a pulsing dot reads as "alive", and a cooling or unverified
 * credential is precisely not that.
 */
export function CredStateDot({ row, className }: { row: CredStateFields; className?: string }) {
  const state = credState(row);
  const dot = toneDot(STATE_META[state].tone);
  return (
    <span className={cn("relative inline-flex h-2 w-2 shrink-0", className)}>
      {state === "healthy" && (
        <span className={cn("absolute inline-flex h-full w-full rounded-full opacity-60 animate-ping", dot)} />
      )}
      <span className={cn("relative inline-flex h-2 w-2 rounded-full", dot)} />
    </span>
  );
}
