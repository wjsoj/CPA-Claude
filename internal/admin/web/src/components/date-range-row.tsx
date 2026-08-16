import type { ReactNode } from "react";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { rangePresets, type RangePreset } from "@/lib/date-range";

// Two day inputs plus the shared presets. Every range in this app is an
// inclusive pair of `YYYY-MM-DD` labels (see lib/date-range), so the picker
// deals in strings and never hands a Date to a caller.

export function DateRangeRow({
  from,
  to,
  onChange,
  presets = rangePresets,
  hint,
  disabled,
}: {
  from: string;
  to: string;
  onChange: (from: string, to: string) => void;
  presets?: RangePreset[];
  /** Right-aligned caption, typically the timezone the range is cut on. */
  hint?: ReactNode;
  disabled?: boolean;
}) {
  return (
    <div className="space-y-2">
      <div className="flex items-center gap-1.5">
        <Input
          type="date"
          value={from}
          disabled={disabled}
          onChange={(e) => onChange(e.currentTarget.value, to)}
          className="h-8 text-xs mono"
        />
        <span className="mono text-xs opacity-60">→</span>
        <Input
          type="date"
          value={to}
          disabled={disabled}
          onChange={(e) => onChange(from, e.currentTarget.value)}
          className="h-8 text-xs mono"
        />
      </div>
      <div className="flex flex-wrap gap-1.5">
        {presets.map((p) => (
          <Button
            key={p.label}
            type="button"
            size="sm"
            variant="outline"
            disabled={disabled}
            className="h-6 px-2 text-[11px]"
            onClick={() => {
              const r = p.range();
              onChange(r.from, r.to);
            }}
          >
            {p.label}
          </Button>
        ))}
        {hint && <span className="ml-auto self-center text-[11px] mono opacity-55">{hint}</span>}
      </div>
    </div>
  );
}
