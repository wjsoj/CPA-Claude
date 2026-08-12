import { useCallback, useEffect, useMemo, useState } from "react";
import { Check, ChevronRight, Copy, FileText } from "lucide-react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { toast } from "sonner";
import { Card } from "./ui/card";
import { Button } from "./ui/button";
import { cn, copyToClipboard } from "@/lib/utils";
import { DOCS, DOC_GROUPS, docAsMarkdown, findDoc, type DocSection } from "@/lib/docs";

// CopyButton is used for the whole-page and whole-set actions as well as for
// every fenced code block, so the "copied" acknowledgement lives here rather
// than being re-implemented three times.
function CopyButton({
  text,
  label,
  done = "已复制",
  className,
  variant = "outline",
}: {
  text: string;
  label: string;
  done?: string;
  className?: string;
  variant?: "outline" | "default" | "ghost";
}) {
  const [copied, setCopied] = useState(false);
  useEffect(() => {
    if (!copied) return;
    const t = setTimeout(() => setCopied(false), 1600);
    return () => clearTimeout(t);
  }, [copied]);
  return (
    <Button
      type="button"
      size="sm"
      variant={variant}
      className={cn("gap-1.5", className)}
      onClick={async () => {
        try {
          await copyToClipboard(text);
          setCopied(true);
        } catch {
          // Clipboard access is origin- and gesture-gated; a silent no-op here
          // looks like a broken button, so say so instead.
          toast.error("复制失败，请手动选中文本复制");
        }
      }}
    >
      {copied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
      {copied ? done : label}
    </Button>
  );
}

// Markdown renderer. Deliberately small: the docs are ours, so the component
// map only has to cover what they actually use, and anything unmapped falls
// through to react-markdown's sensible default element.
function DocBody({ doc }: { doc: DocSection }) {
  return (
    <div className="doc-prose">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={{
          // Fenced blocks are the point of this page — every one of them is a
          // command or a file the reader has to reproduce exactly, so each
          // gets its own copy button rather than relying on manual selection.
          pre: ({ children }) => {
            const code = extractText(children);
            return (
              <div className="relative group my-4">
                <pre className="overflow-x-auto rounded-md border border-border-strong bg-muted/40 p-4 text-[13px] leading-relaxed font-mono">
                  {children}
                </pre>
                <CopyButton
                  text={code}
                  label="复制"
                  done="已复制"
                  variant="ghost"
                  className="absolute top-2 right-2 opacity-0 group-hover:opacity-100 focus-visible:opacity-100 transition-opacity bg-background/80 backdrop-blur"
                />
              </div>
            );
          },
          code: ({ className, children, ...props }) =>
            // Inline code only — block code arrives inside <pre> above, which
            // already supplies the framing.
            className?.includes("language-") ? (
              <code className={className} {...props}>
                {children}
              </code>
            ) : (
              <code
                className="rounded bg-muted px-1.5 py-0.5 text-[0.9em] font-mono break-words"
                {...props}
              >
                {children}
              </code>
            ),
          table: ({ children }) => (
            // Wide tables must scroll inside their own box; the page itself
            // must never scroll horizontally on a phone.
            <div className="my-4 overflow-x-auto rounded-md border border-border-strong">
              <table className="w-full text-sm border-collapse">{children}</table>
            </div>
          ),
          th: ({ children }) => (
            <th className="border-b border-border-strong bg-muted/40 px-3 py-2 text-left font-medium whitespace-nowrap">
              {children}
            </th>
          ),
          td: ({ children }) => (
            <td className="border-b border-border/60 px-3 py-2 align-top">{children}</td>
          ),
          a: ({ children, href }) => (
            <a
              href={href}
              target={href?.startsWith("http") ? "_blank" : undefined}
              rel={href?.startsWith("http") ? "noreferrer noopener" : undefined}
              className="text-primary underline underline-offset-2 hover:no-underline"
            >
              {children}
            </a>
          ),
          h2: ({ children }) => (
            <h2 className="font-display text-xl md:text-2xl tracking-tight mt-8 mb-3 first:mt-0">
              {children}
            </h2>
          ),
          h3: ({ children }) => (
            <h3 className="font-display text-lg tracking-tight mt-6 mb-2">{children}</h3>
          ),
          p: ({ children }) => <p className="my-3 leading-7">{children}</p>,
          ul: ({ children }) => <ul className="my-3 ml-5 list-disc space-y-1.5">{children}</ul>,
          ol: ({ children }) => <ol className="my-3 ml-5 list-decimal space-y-1.5">{children}</ol>,
          blockquote: ({ children }) => (
            <blockquote className="my-4 border-l-2 border-primary/60 bg-muted/30 px-4 py-2 text-sm">
              {children}
            </blockquote>
          ),
        }}
      >
        {doc.body}
      </ReactMarkdown>
    </div>
  );
}

// extractText walks the rendered children of a <pre> to recover the raw source
// for the clipboard. react-markdown hands us React elements, not the original
// string, so there is nothing simpler to read it back from.
function extractText(node: unknown): string {
  if (node == null || typeof node === "boolean") return "";
  if (typeof node === "string" || typeof node === "number") return String(node);
  if (Array.isArray(node)) return node.map(extractText).join("");
  if (typeof node === "object" && "props" in (node as any)) {
    return extractText((node as any).props?.children);
  }
  return "";
}

// StatusDocsPanel is the public onboarding documentation, mounted as a tab on
// /status. The whole doc set is bundled into the SPA at build time, so this
// panel makes no network request and works even when the pool is down — which
// is exactly when a confused user goes looking for the setup instructions.
export function StatusDocsPanel() {
  const [slug, setSlug] = useState<string>(() => {
    const stored = localStorage.getItem("cpa.status.doc");
    if (stored && findDoc(stored)) return stored;
    return DOCS[0]?.slug ?? "";
  });
  useEffect(() => {
    if (slug) localStorage.setItem("cpa.status.doc", slug);
  }, [slug]);

  const doc = useMemo(() => findDoc(slug) ?? DOCS[0], [slug]);

  const select = useCallback((s: string) => {
    setSlug(s);
    // Jumping between pages should start at the top of the new one; without
    // this a long page followed by a short one leaves the reader below its end.
    window.scrollTo({ top: 0, behavior: "smooth" });
  }, []);

  if (!doc) {
    return (
      <Card className="p-6 text-sm text-muted-foreground">
        文档尚未构建。请在 <code className="font-mono">src/content/docs/</code> 下添加 .md 文件。
      </Card>
    );
  }

  return (
    <div className="grid grid-cols-1 lg:grid-cols-[15rem_minmax(0,1fr)] gap-5">
      {/* SIDEBAR */}
      <nav aria-label="文档目录" className="lg:sticky lg:top-4 lg:self-start">
        <Card className="p-3">
          <div className="eyebrow px-2 pb-2 opacity-60">目录</div>
          <div className="space-y-3">
            {DOC_GROUPS.map((group) => (
              <div key={group}>
                <div className="px-2 pb-1 text-xs font-medium text-muted-foreground">{group}</div>
                <ul className="space-y-0.5">
                  {DOCS.filter((d) => d.group === group).map((d) => {
                    const active = d.slug === doc.slug;
                    return (
                      <li key={d.slug}>
                        <button
                          type="button"
                          onClick={() => select(d.slug)}
                          aria-current={active ? "page" : undefined}
                          className={cn(
                            "w-full flex items-center gap-1.5 rounded px-2 py-1.5 text-left text-sm transition-colors",
                            active
                              ? "bg-primary/10 text-foreground font-medium"
                              : "text-muted-foreground hover:bg-muted/60 hover:text-foreground",
                          )}
                        >
                          <ChevronRight
                            className={cn(
                              "h-3.5 w-3.5 shrink-0 transition-transform",
                              active && "rotate-90 text-primary",
                            )}
                          />
                          <span className="min-w-0 truncate">{d.title}</span>
                        </button>
                      </li>
                    );
                  })}
                </ul>
              </div>
            ))}
          </div>
        </Card>
      </nav>

      {/* ARTICLE */}
      <Card className="p-5 md:p-7 min-w-0">
        <header className="mb-5 border-b border-border/60 pb-4">
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div className="min-w-0">
              <div className="eyebrow opacity-60 flex items-center gap-1.5">
                <FileText className="h-3 w-3" />
                {doc.group}
              </div>
              <h2 className="font-display text-2xl md:text-3xl tracking-tight mt-1">{doc.title}</h2>
              {doc.intro && (
                <p className="mt-1.5 text-sm text-muted-foreground max-w-2xl">{doc.intro}</p>
              )}
            </div>
            <div className="shrink-0 text-right">
              <CopyButton text={docAsMarkdown(doc)} label="复制本页全文" done="已复制" />
              <p className="mt-1.5 text-[11px] text-muted-foreground">粘贴给 Claude Code / Codex</p>
            </div>
          </div>
        </header>
        <DocBody doc={doc} />
      </Card>
    </div>
  );
}
