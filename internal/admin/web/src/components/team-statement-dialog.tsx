import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
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
import { DateRangeRow } from "./date-range-row";
import { rangeProblem, trailingDays } from "@/lib/date-range";
import { ApiError } from "@/lib/api";
import {
  teamStatementPreview,
  teamDownloadStatementPDF,
  type TeamStatementPreview,
} from "@/lib/team-api";
import { cn } from "@/lib/utils";

// Export dialog for the group statement — one merged record of what the whole
// workspace consumed over a range, to staple to a team invoice.
//
// It is the team-scale sibling of StatementDialog, with two deliberate
// differences. There is no target-amount mode: at group scope that would mean
// assembling a figure out of other people's consumption, and the honesty
// argument that carries the per-token feature does not survive the translation
// (the server refuses target_cny outright). And detail is opt-in: a month of
// team traffic is far more requests than any document can itemise, while the
// per-member and per-model rollups are what a reimbursement package needs.

const fmtCNY = (v: number) =>
  `¥${(v || 0).toLocaleString("zh-CN", { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;
const fmtInt = (v: number) => (v || 0).toLocaleString("zh-CN");
const fmtPct = (v: number) => `${((v || 0) * 100).toFixed(1)}%`;

export function TeamStatementDialog({
  open,
  onOpenChange,
  token,
  workspaceName,
}: {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  token: string;
  workspaceName: string;
}) {
  const initial = useRef(trailingDays(30)).current;
  const [from, setFrom] = useState(initial.from);
  const [to, setTo] = useState(initial.to);
  const [detail, setDetail] = useState<"summary" | "full">("summary");
  const [preview, setPreview] = useState<TeamStatementPreview | null>(null);
  const [loading, setLoading] = useState(false);
  const [downloading, setDownloading] = useState(false);
  const [err, setErr] = useState("");

  // seq guards against an out-of-order response overwriting a newer one when
  // the dates are edited quickly.
  const seq = useRef(0);
  const refresh = useCallback(
    async (f: string, t: string, d: "summary" | "full") => {
      const mine = ++seq.current;
      setLoading(true);
      try {
        const p = await teamStatementPreview(token, { from: f, to: t, detail: d });
        if (seq.current !== mine) return;
        setPreview(p);
        setErr("");
      } catch (e) {
        if (seq.current !== mine) return;
        // The stale preview belongs to the previous range; leaving it under the
        // new dates would offer a download of numbers nobody asked for.
        setErr(e instanceof ApiError ? e.message : String(e));
        setPreview(null);
      } finally {
        if (seq.current === mine) setLoading(false);
      }
    },
    [token],
  );

  // A range the server would refuse is refused here first, so the user reads a
  // sentence rather than the endpoint's English 400.
  const problem = rangeProblem(from, to);

  // Debounced for the same reason as the personal export: every keystroke in a
  // date input would otherwise cost a scan of the log archive.
  useEffect(() => {
    if (!open || !from || !to || problem) return;
    const id = setTimeout(() => void refresh(from, to, detail), 350);
    return () => clearTimeout(id);
  }, [open, from, to, detail, problem, refresh]);

  const download = async () => {
    setDownloading(true);
    try {
      const blob = await teamDownloadStatementPDF(token, { from, to, detail });
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `team-statement-${from}_${to}.pdf`;
      a.click();
      URL.revokeObjectURL(url);
      toast.success("团队对账单已下载");
      onOpenChange(false);
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : String(e));
    } finally {
      setDownloading(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-[620px]">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <FileText className="h-4 w-4" />
            导出团队对账单
          </DialogTitle>
          <DialogDescription>
            按时间区间导出「{workspaceName}」全体成员的用量与实际扣费，可作为团队发票的附件。
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-4">
          <DateRangeRow
            from={from}
            to={to}
            onChange={(f, t) => {
              setFrom(f);
              setTo(t);
            }}
            hint={preview ? `区间按 ${preview.timezone} 计，含首尾两日` : undefined}
          />

          <div className="flex flex-wrap items-center gap-1.5">
            <span className="text-[11px] opacity-60">明细：</span>
            <Button
              type="button"
              size="sm"
              variant={detail === "summary" ? "default" : "outline"}
              className="h-6 px-2 text-[11px]"
              onClick={() => setDetail("summary")}
            >
              仅汇总
            </Button>
            <Button
              type="button"
              size="sm"
              variant={detail === "full" ? "default" : "outline"}
              className="h-6 px-2 text-[11px]"
              onClick={() => setDetail("full")}
            >
              含请求明细
            </Button>
            <span className="text-[11px] opacity-55">
              {detail === "summary"
                ? "只印按成员 / 按模型的汇总表"
                : `另附最近 ${fmtInt(preview?.detail_lines || 0)} 条请求明细`}
            </span>
          </div>

          {(problem || err) && (
            <div className="rounded-sm border border-destructive/40 bg-destructive/10 px-3 py-2 text-xs text-destructive">
              {problem || err}
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
                <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
                  <Figure label="区间消费" value={fmtCNY(preview.billed_cny)} strong />
                  <Figure label="区间请求数" value={`${fmtInt(preview.requests)} 笔`} />
                  <Figure label="成员人数" value={`${fmtInt(preview.member_count)} 人`} />
                  <Figure
                    label={`累计消费（近 ${preview.lifetime_days} 天）`}
                    value={fmtCNY(preview.lifetime_billed_cny)}
                  />
                </div>

                <div className="mt-3 max-h-44 overflow-y-auto rounded-sm border border-border/60 bg-background/40">
                  {preview.by_member.length === 0 ? (
                    <div className="px-3 py-2 text-[11px] opacity-60">该区间内没有成员消费。</div>
                  ) : (
                    preview.by_member.map((m) => (
                      <div
                        key={m.masked}
                        className="flex items-center justify-between gap-3 px-3 py-1 text-[11px]"
                      >
                        <span className="truncate font-mono opacity-70">
                          {m.masked}
                          {m.label ? ` · ${m.label}` : ""}
                          {m.unmeasurable && <span className="ml-1 opacity-60">（无法统计）</span>}
                        </span>
                        <span className="mono shrink-0 tabular">
                          {fmtCNY(m.billed_cny)}
                          <span className="ml-1 opacity-55">{fmtPct(m.share)}</span>
                        </span>
                      </div>
                    ))
                  )}
                </div>

                {preview.unitemised_cny > 0 && (
                  <Warning>
                    账本显示该区间实际扣款 {fmtCNY(preview.charged_cny)}，其中{" "}
                    {fmtCNY(preview.unitemised_cny)} 没有对应的请求记录（日志缺失）。
                    对账单会把这部分单列为「未能明细化的消费」，总额以实际扣款为准。
                  </Warning>
                )}

                {detail === "full" && preview.truncated && (
                  <Warning>
                    区间内共 {fmtInt(preview.requests)} 笔请求，明细部分只列示最近{" "}
                    {fmtInt(preview.detail_lines)} 笔；汇总金额仍覆盖全部请求。
                  </Warning>
                )}

                {preview.notes.map((n, i) => (
                  <Warning key={i}>{n}</Warning>
                ))}

                <p className="mt-2 text-[11px] leading-relaxed opacity-60">
                  金额按 1 USD = {preview.cny_per_usd.toFixed(4)} CNY
                  折算并印在对账单上；成员名单以导出当时为准，涵盖组池与个人钱包两部分支出。
                </p>
              </div>
            ) : (
              <div className="py-4 text-xs opacity-60">选择时间区间以查看该区间的团队消费。</div>
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
            disabled={downloading || loading || !preview || !!problem}
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

function Warning({ children }: { children: ReactNode }) {
  return (
    <div className="mt-2 flex items-start gap-1.5 rounded-sm border border-[color:var(--warning)]/40 bg-[color:var(--warning)]/10 px-3 py-2 text-[11px] leading-relaxed text-[color:var(--warning)]">
      <AlertTriangle className="mt-0.5 h-3 w-3 shrink-0" />
      <span>{children}</span>
    </div>
  );
}

function Figure({ label, value, strong }: { label: string; value: string; strong?: boolean }) {
  return (
    <div className="min-w-0">
      <div className="truncate text-[11px] opacity-60" title={label}>
        {label}
      </div>
      <div
        className={cn(
          "mt-0.5 font-display tracking-tight tabular",
          strong ? "text-lg" : "text-base",
        )}
      >
        {value}
      </div>
    </div>
  );
}
