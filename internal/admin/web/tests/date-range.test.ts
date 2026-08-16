// Run with `bun test` from internal/admin/web. Kept outside `src/` so the
// production typecheck (`tsc --noEmit`, include: ["src"]) doesn't need bun's
// ambient types, and so vite never sees it.
import { describe, expect, test } from "bun:test";

import { formatTimestampIn } from "../src/lib/date-range";

// 2026-08-17 00:30:00 +08:00 — inside a 2026-08-17 day label as the server cuts
// it, but still 2026-08-16 for anyone reading in UTC. This instant is the whole
// reason the formatter takes a zone.
const justAfterShanghaiMidnight = Date.UTC(2026, 7, 16, 16, 30, 0) / 1000;

describe("formatTimestampIn", () => {
  test("prints the display zone the range was cut on, not the browser's", () => {
    expect(formatTimestampIn(justAfterShanghaiMidnight, "Asia/Shanghai")).toBe("08-17 00:30:00");
  });

  test("a different zone moves the row to a different day", () => {
    // Same instant, and it must be allowed to read as the previous day — what
    // must not happen is a Shanghai range printing Shanghai rows in UTC.
    expect(formatTimestampIn(justAfterShanghaiMidnight, "UTC")).toBe("08-16 16:30:00");
    expect(formatTimestampIn(justAfterShanghaiMidnight, "America/New_York")).toBe("08-16 12:30:00");
  });

  test("midnight is 00, never 24", () => {
    const midnight = Date.UTC(2026, 7, 16, 16, 0, 0) / 1000;
    expect(formatTimestampIn(midnight, "Asia/Shanghai")).toBe("08-17 00:00:00");
  });

  test("an unusable zone falls back to the browser's rather than throwing", () => {
    const local = formatTimestampIn(justAfterShanghaiMidnight, undefined);
    expect(formatTimestampIn(justAfterShanghaiMidnight, "Mars/Olympus_Mons")).toBe(local);
    // …and the fallback is genuinely the browser's zone.
    const d = new Date(justAfterShanghaiMidnight * 1000);
    const p = (n: number) => String(n).padStart(2, "0");
    expect(local).toBe(
      `${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`,
    );
  });

  test("zero/absent timestamps don't throw", () => {
    expect(formatTimestampIn(0, "Asia/Shanghai")).toBe("01-01 08:00:00");
  });
});
