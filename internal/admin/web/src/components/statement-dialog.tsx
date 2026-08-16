import { useCallback, useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { AlertTriangle, Download, FileText, Loader2 } from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "./ui/dialog";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { DateRangeRow } from "./date-range-row";
import { shiftDays, todayLocal } from "@/lib/date-range";
import {
  downloadStatementPDF,
  loadStatementPreview,
  type StatementPreview,
} from "@/lib/status-api";

// Export dialog for the usage statement — the itemised record of what a token
// actually spent, in yuan, as a downloadable PDF.
//
// Each amount is the settled charge converted at the rate its own request
// settled at, so the figures shown here are exactly the ones in the file and
// do not drift with the market between one export and the next.
//
// Two mutually exclusive ways to pick the range: named dates (the default),
// or a target amount — the server walks backward from the newest request
// until spend reaches the figure entered and reports whatever window that
// turned out to be. The target mode is not "make the total say whatever I
// need": every line is a charge that really happened, the server refuses
// outright if the whole retained log can't reach the figure, and the
// resulting document is captioned as target-derived rather than presented
// as an ordinary date-range export. Real spend is the only ceiling — an
// account funded by operator credit rather than by Alipay is still exporting
// its own consumption.

const fmtCNY = (v: number) =>
  `¥${v.toLocaleString("zh-CN", { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;

const fmtInt = (v: number) => v.toLocaleString("zh-CN");

export function StatementDialog({
  open,
  onOpenChange,
  token,
  tokenLabel,
  initialFrom,
  initialTo,
}: {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  token: string;
  tokenLabel: string;
  initialFrom?: string;
  initialTo?: string;
}) {
  const [mode, setMode] = useState<"date" | "target">("date");
  const [from, setFrom] = useState(initialFrom || "");
  const [to, setTo] = useState(initialTo || "");
  const [targetInput, setTargetInput] = useState("");
  const [preview, setPreview] = useState<StatementPreview | null>(null);
  const [loading, setLoading] = useState(false);
  const [err, setErr] = useState("");
  const [downloading, setDownloading] = useState(false);

  // Default to the trailing 30 days on first open so the dialog opens on a
  // range rather than on empty inputs.
  useEffect(() => {
    if (!open) return;
    setFrom((f) => f || initialFrom || shiftDays(todayLocal(), -29));
    setTo((t) => t || initialTo || todayLocal());
  }, [open, initialFrom, initialTo]);

  const targetCNY = mode === "target" ? Number(targetInput) : undefined;
  const targetValid =
    mode !== "target" || (!!targetInput && Number.isFinite(targetCNY) && (targetCNY as number) > 0);

  // seq guards against an out-of-order response overwriting a newer one when
  // the user edits the dates (or the target figure) quickly.
  const seq = useRef(0);
  const refresh = useCallback(
    async (f: string, t: string, target: number | undefined) => {
      const mine = ++seq.current;
      setLoading(true);
      setErr("");
      try {
        const p = await loadStatementPreview(
          target
            ? { token, target_cny: target }
            : { token, from: f || undefined, to: t || undefined },
        );
        if (seq.current === mine) setPreview(p);
      } catch (e: any) {
        if (seq.current === mine) {
          setErr(e?.message || String(e));
          setPreview(null);
        }
      } finally {
        if (seq.current === mine) setLoading(false);
      }
    },
    [token],
  );

  // Debounced: both a date input and a target-amount input fire on every
  // keystroke, and each preview is a scan of the log archive.
  useEffect(() => {
    if (!open) return;
    if (mode === "date") {
      if (!from || !to) return;
      const id = setTimeout(() => void refresh(from, to, undefined), 350);
      return () => clearTimeout(id);
    }
    if (!targetValid) return;
    const id = setTimeout(() => void refresh("", "", targetCNY), 350);
    return () => clearTimeout(id);
  }, [open, mode, from, to, targetCNY, targetValid, refresh]);

  const download = async () => {
    setDownloading(true);
    try {
      const args =
        mode === "target" && targetCNY
          ? { token, target_cny: targetCNY }
          : { token, from: from || undefined, to: to || undefined };
      const blob = await downloadStatementPDF(args);
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      const label =
        mode === "target" && preview
          ? `target-${preview.from}_${preview.to}`
          : `${from}_${to}`;
      a.download = `usage-statement-${label}.pdf`;
      a.click();
      URL.revokeObjectURL(url);
      toast.success("对账单已下载");
      onOpenChange(false);
    } catch (e: any) {
      toast.error(e?.message || "导出失败");
    } finally {
      setDownloading(false);
    }
  };

  const empty = preview !== null && preview.requests === 0;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-[560px]">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <FileText className="h-4 w-4" />
            导出消费对账单
          </DialogTitle>
          <DialogDescription>
            按时间区间导出 {tokenLabel} 的 API 调用明细与实际扣费金额，PDF 格式。
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-4">
          <div className="flex gap-1.5">
            <Button
              type="button"
              size="sm"
              variant={mode === "date" ? "default" : "outline"}
              className="h-6 px-2 text-[11px]"
              onClick={() => setMode("date")}
            >
              按日期区间
            </Button>
            <Button
              type="button"
              size="sm"
              variant={mode === "target" ? "default" : "outline"}
              className="h-6 px-2 text-[11px]"
              onClick={() => setMode("target")}
            >
              按目标金额
            </Button>
          </div>

          {mode === "date" ? (
            <DateRangeRow
              from={from}
              to={to}
              onChange={(f, t) => {
                setFrom(f);
                setTo(t);
              }}
              hint={
                preview?.timezone ? `区间按 ${preview.timezone} 计，含首尾两日` : undefined
              }
            />
          ) : (
            <div className="space-y-1.5">
              <div className="flex items-center gap-1.5">
                <span className="mono text-xs opacity-60">¥</span>
                <Input
                  type="number"
                  min="0"
                  step="0.01"
                  placeholder="目标金额"
                  value={targetInput}
                  onChange={(e) => setTargetInput(e.currentTarget.value)}
                  className="h-8 text-xs mono"
                />
              </div>
              <p className="text-[11px] leading-relaxed opacity-60">
                系统会从最近的请求向前回溯，直到列示金额达到该数值为止，并在对账单上如实标注这不是常规日期区间。
                {typeof preview?.lifetime_billed_cny === "number" && (
                  <>
                    {" "}
                    该账户近 {preview.lifetime_days ?? 90} 天累计消费{" "}
                    <span className="mono">{fmtCNY(preview.lifetime_billed_cny)}</span>
                    ，目标金额不能超过这个数。
                  </>
                )}
              </p>
            </div>
          )}

          {err && (
            <div className="rounded-sm border border-destructive/40 bg-destructive/10 px-3 py-2 text-xs text-destructive mono">
              {err}
            </div>
          )}

          <div className="rounded-md border border-border bg-muted/40 px-4 py-3">
            {loading && !preview ? (
              <div className="flex items-center gap-2 py-4 text-xs opacity-70">
                <Loader2 className="h-3.5 w-3.5 animate-spin" />
                正在统计…
              </div>
            ) : preview ? (
              <div className={loading ? "opacity-50 transition-opacity" : "transition-opacity"}>
                {preview.by_target && (
                  <div className="mb-3 rounded-sm border border-border bg-background/60 px-3 py-2 text-[11px] leading-relaxed">
                    按目标金额生成：目标 <span className="mono">{fmtCNY(preview.target_cny || 0)}</span>
                    ，实际列示 <span className="mono">{fmtCNY(preview.billed_cny)}</span>
                    ，对应区间 <span className="mono">{preview.from}</span> 至{" "}
                    <span className="mono">{preview.to}</span>（{preview.timezone}）。
                  </div>
                )}
                <div className="grid grid-cols-3 gap-3">
                  <Figure label="区间请求数" value={`${fmtInt(preview.requests)} 笔`} />
                  <Figure
                    label={preview.by_target ? "实际列示金额" : "区间消费"}
                    value={fmtCNY(preview.billed_cny)}
                    strong
                  />
                  <Figure
                    label={`累计消费（近 ${preview.lifetime_days} 天）`}
                    value={fmtCNY(preview.lifetime_billed_cny)}
                  />
                </div>

                {!!preview.unitemised_cny && (
                  <div className="mt-3 rounded-sm border border-[color:var(--warning)]/40 bg-[color:var(--warning)]/10 px-3 py-2">
                    <div className="flex items-start gap-1.5 text-[11px] leading-relaxed text-[color:var(--warning)]">
                      <AlertTriangle className="h-3 w-3 shrink-0 mt-0.5" />
                      <span>
                        账本显示该区间实际扣款 {fmtCNY(preview.charged_cny || 0)}，其中{" "}
                        {fmtCNY(preview.unitemised_cny)} 没有对应的请求记录（日志缺失）。
                        对账单会把这部分单列为「未能明细化的消费」，总额以实际扣款为准。
                      </span>
                    </div>
                  </div>
                )}

                {/* 金额由美元账本按导出时汇率折算，汇率印在对账单上（见 statement/pdf.go
                    的「换算汇率」行），因此这里不再单列早期无汇率记录的请求。 */}

                {preview.truncated && (
                  <p className="mt-2 flex items-start gap-1.5 text-[11px] leading-relaxed text-[color:var(--warning)]">
                    <AlertTriangle className="h-3 w-3 shrink-0 mt-0.5" />
                    区间内共 {fmtInt(preview.requests)} 笔请求，明细部分只列示最近{" "}
                    {fmtInt(preview.detail_lines)} 笔；合计金额仍覆盖全部请求。
                  </p>
                )}

                {empty && (
                  <p className="mt-2 text-[11px] opacity-60">
                    该区间内没有计费请求，导出的对账单将只有汇总部分。
                  </p>
                )}
              </div>
            ) : (
              <div className="py-4 text-xs opacity-60">
                {mode === "target" ? "填写目标金额以查看对应区间。" : "选择时间区间以查看该区间的消费。"}
              </div>
            )}
          </div>
        </div>

        <DialogFooter>
          <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
            取消
          </Button>
          <Button
            type="button"
            onClick={download}
            disabled={downloading || loading || !preview || !targetValid}
            className="gap-1.5"
          >
            {downloading ? (
              <Loader2 className="h-3.5 w-3.5 animate-spin" />
            ) : (
              <Download className="h-3.5 w-3.5" />
            )}
            下载 PDF
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function Figure({
  label,
  value,
  sub,
  strong,
}: {
  label: string;
  value: string;
  sub?: string;
  strong?: boolean;
}) {
  return (
    <div className="min-w-0">
      <div className="text-[11px] opacity-60 truncate" title={label}>
        {label}
      </div>
      <div
        className={
          strong
            ? "mt-0.5 font-display text-lg tracking-tight tabular"
            : "mt-0.5 font-display text-base tracking-tight tabular"
        }
      >
        {value}
      </div>
      {sub && <div className="mono text-[10px] tabular opacity-50">{sub}</div>}
    </div>
  );
}
