// Shared building blocks for the two invoice request flows on /status:
//
//   * personal — wallet-panel.tsx `InvoiceSection`, quota comes from the
//     token's own paid orders.
//   * team     — team-panel.tsx `TeamInvoiceSection`, one invoice for the whole
//     workspace, quota is the sum of the members' remaining quotas and the
//     amount is split across them in join order.
//
// Both dialogs ask for the same thing (amount / 抬头 / 联系邮箱), so the title
// block — with its company-name suggest and 统一社会信用代码 validation — and
// the money/status primitives live here rather than being forked.

import { useEffect, useRef, useState } from "react";
import { CheckCircle2, Clock3, Search, XCircle } from "lucide-react";
import { Input } from "@/components/ui/input";
import { suggestInvoiceTitles, type InvoiceTitle } from "@/lib/status-api";
import { cn } from "@/lib/utils";

export function fmtCNY(v: number): string {
  return `¥${v.toFixed(2)}`;
}

export const emptyInvoiceTitle = (): InvoiceTitle => ({
  name: "",
  tax_no: "",
  address: "",
  phone: "",
  bank: "",
  bank_account: "",
});

/** 统一社会信用代码 — 15~20 位大写字母 / 数字 (18 位为现行标准)。 */
export function taxNoIsValid(taxNo: string | undefined): boolean {
  return /^[0-9A-Z]{15,20}$/.test((taxNo || "").trim().toUpperCase());
}

export type InvoiceStatus = "pending" | "issued" | "rejected";

export function invoiceStatusBadge(s: InvoiceStatus) {
  switch (s) {
    case "issued":
      return (
        <span className="inline-flex items-center gap-1 text-emerald-600 dark:text-emerald-400 text-[11px] font-mono">
          <CheckCircle2 className="h-3 w-3" /> issued
        </span>
      );
    case "pending":
      return (
        <span className="inline-flex items-center gap-1 text-amber-600 dark:text-amber-400 text-[11px] font-mono">
          <Clock3 className="h-3 w-3" /> pending
        </span>
      );
    case "rejected":
      return (
        <span className="inline-flex items-center gap-1 text-destructive text-[11px] font-mono">
          <XCircle className="h-3 w-3" /> rejected
        </span>
      );
  }
}

export function SummaryCell({
  label,
  value,
  highlight,
  muted,
}: {
  label: string;
  value: number | undefined;
  highlight?: boolean;
  muted?: boolean;
}) {
  return (
    <div
      className={cn(
        "rounded-md border px-3 py-2",
        highlight
          ? "border-primary/40 bg-primary/5"
          : muted
            ? "border-border/60 bg-muted/30"
            : "border-border",
      )}
    >
      <div className="text-[10px] eyebrow opacity-70">{label}</div>
      <div className="mt-0.5 font-mono text-lg tabular">
        {value === undefined ? "···" : fmtCNY(value)}
      </div>
    </div>
  );
}

export function LabeledInput({
  label,
  value,
  onChange,
  placeholder,
  type,
  required,
  invalid,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  type?: string;
  required?: boolean;
  invalid?: boolean;
}) {
  return (
    <div>
      <label className="eyebrow text-[10px] opacity-70">
        {label}
        {required && <span className="text-destructive"> *</span>}
      </label>
      <Input
        type={type || "text"}
        value={value}
        placeholder={placeholder}
        onInput={(e) => onChange((e.target as HTMLInputElement).value)}
        className={cn("mt-1 font-mono text-sm", invalid && "border-destructive")}
      />
    </div>
  );
}

/**
 * The 抬头 block: name with debounced company suggest, plus the optional
 * registration fields. `open` resets the internal search state whenever the
 * hosting dialog is (re)opened.
 *
 * The suggest endpoint is authenticated with the caller's client token — a
 * group admin is a token holder too, so the team dialog reuses it unchanged.
 */
export function InvoiceTitleFields({
  token,
  open,
  value,
  onChange,
}: {
  token: string;
  open: boolean;
  value: InvoiceTitle;
  onChange: (updater: (prev: InvoiceTitle) => InvoiceTitle) => void;
}) {
  const [search, setSearch] = useState("");
  const [picks, setPicks] = useState<InvoiceTitle[]>([]);
  const [searching, setSearching] = useState(false);
  const [searched, setSearched] = useState(false);
  // Suppresses the dropdown after the user picks a candidate — the picked
  // name flows back into `search`, which would otherwise re-fire the
  // debounced fetch and re-open the list. Cleared once the user types again.
  const [pickedLock, setPickedLock] = useState(false);
  const debRef = useRef<number | null>(null);

  useEffect(() => {
    if (open) {
      setSearch("");
      setPicks([]);
      setSearching(false);
      setSearched(false);
      setPickedLock(false);
    }
  }, [open]);

  // Debounced title suggestion — kicks 350ms after the user stops typing.
  useEffect(() => {
    if (!open) return;
    if (debRef.current) window.clearTimeout(debRef.current);
    if (!search.trim()) {
      setPicks([]);
      setSearched(false);
      setSearching(false);
      return;
    }
    if (pickedLock) return; // hold the dropdown closed until the user edits again
    setSearching(true);
    debRef.current = window.setTimeout(async () => {
      try {
        const r = await suggestInvoiceTitles(token, search);
        setPicks(r.titles || []);
      } catch {
        setPicks([]);
      } finally {
        setSearching(false);
        setSearched(true);
      }
    }, 350);
    return () => {
      if (debRef.current) window.clearTimeout(debRef.current);
    };
  }, [search, token, open]);

  const apply = (t: InvoiceTitle) => {
    onChange((prev) => ({
      ...prev,
      name: t.name,
      tax_no: t.tax_no ?? prev.tax_no,
      address: t.address ?? prev.address,
      phone: t.phone ?? prev.phone,
      bank: t.bank ?? prev.bank,
      bank_account: t.bank_account ?? prev.bank_account,
    }));
    setSearch(t.name);
    setPicks([]);
    setSearching(false);
    setSearched(false);
    setPickedLock(true);
  };

  const taxNoTouched = Boolean((value.tax_no || "").trim());

  return (
    <>
      <div>
        <label className="eyebrow text-[10px] opacity-70">
          抬头名称 (公司全称) <span className="text-destructive">*</span>
        </label>
        <div className="relative mt-1">
          <Input
            placeholder="搜索或手动输入抬头…"
            value={search}
            onInput={(e) => {
              const v = (e.target as HTMLInputElement).value;
              setSearch(v);
              onChange((prev) => ({ ...prev, name: v }));
              setPickedLock(false);
            }}
            className="font-mono pr-8"
          />
          <Search className="h-4 w-4 absolute right-2 top-1/2 -translate-y-1/2 text-muted-foreground" />
        </div>
        {picks.length > 0 && (
          <div className="mt-1 max-h-40 overflow-y-auto rounded-md border border-border/60 bg-background divide-y divide-border/40">
            {picks.map((p, i) => (
              <button
                type="button"
                key={`${p.name}-${i}`}
                onClick={() => apply(p)}
                className="w-full text-left px-3 py-1.5 hover:bg-muted/40 flex items-center justify-between gap-2"
              >
                <span className="truncate text-sm">{p.name}</span>
                <span className="text-[10px] font-mono opacity-60">
                  {p.source === "local" ? "已存" : "在线"}
                  {p.tax_no ? ` · ${p.tax_no.slice(0, 6)}…` : ""}
                </span>
              </button>
            ))}
          </div>
        )}
        <div className="mt-1 text-[11px] text-muted-foreground">
          {searching
            ? "正在搜索…"
            : searched && picks.length === 0
              ? "未匹配到企业,可手动输入公司全称与统一社会信用代码"
              : !search.trim()
                ? "输入公司名称关键词以从企业库匹配,或直接手动填写下方字段"
                : null}
        </div>
      </div>

      <div className="grid grid-cols-2 gap-3">
        <LabeledInput
          label="统一社会信用代码"
          required
          value={value.tax_no || ""}
          onChange={(v) =>
            onChange((p) => ({ ...p, tax_no: v.toUpperCase().replace(/\s+/g, "") }))
          }
          placeholder="18 位字母 / 数字"
          invalid={taxNoTouched && !taxNoIsValid(value.tax_no)}
        />
        <LabeledInput
          label="联系电话 (可选)"
          value={value.phone || ""}
          onChange={(v) => onChange((p) => ({ ...p, phone: v }))}
        />
      </div>
      <LabeledInput
        label="注册地址 (可选)"
        value={value.address || ""}
        onChange={(v) => onChange((p) => ({ ...p, address: v }))}
      />
      <div className="grid grid-cols-2 gap-3">
        <LabeledInput
          label="开户银行 (可选)"
          value={value.bank || ""}
          onChange={(v) => onChange((p) => ({ ...p, bank: v }))}
        />
        <LabeledInput
          label="银行账户 (可选)"
          value={value.bank_account || ""}
          onChange={(v) => onChange((p) => ({ ...p, bank_account: v }))}
        />
      </div>
      <p className="text-[11px] text-muted-foreground -mt-1">
        开具增值税普通发票只需公司名称 + 统一社会信用代码;专用发票请补全其余字段(以贵司财务提供的开票信息为准)。
      </p>
    </>
  );
}
