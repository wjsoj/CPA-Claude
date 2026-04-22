import { useState } from "react";
import { KeyRound, Plus, FileJson, ShieldCheck, Gauge } from "lucide-react";
import type { AuthRow, Summary } from "@/lib/types";
import { AuthCard } from "./auth-card";
import { UpstreamQuota } from "./upstream-quota";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

type Action = "toggle" | "refresh" | "clear-quota" | "clear-failure" | "delete";
type SubTab = "files" | "quota";

interface Props {
  summary: Summary | null;
  onAction: (a: AuthRow, act: Action) => void;
  onEdit: (a: AuthRow) => void;
  onAddOAuth: () => void;
  onAddAPIKey: () => void;
  onUpload: () => void;
}

export function CredentialsPanel({
  summary,
  onAction,
  onEdit,
  onAddOAuth,
  onAddAPIKey,
  onUpload,
}: Props) {
  const [sub, setSub] = useState<SubTab>("files");

  const auths = summary?.auths || [];
  const oauths = auths.filter((a) => a.kind === "oauth");
  const apikeys = auths.filter((a) => a.kind === "apikey");
  const healthy = auths.filter((a) => a.healthy).length;
  const quota = auths.filter((a) => a.quota_exceeded).length;
  const unhealthy = auths.filter((a) => a.hard_failure).length;

  const subTabs: { key: SubTab; label: string; icon: typeof ShieldCheck; hint: string }[] = [
    { key: "files", label: "Auth files", icon: ShieldCheck, hint: `${auths.length} entries` },
    { key: "quota", label: "Upstream quota", icon: Gauge, hint: `${oauths.length} OAuth` },
  ];

  return (
    <div className="space-y-6">
      <header className="flex items-end justify-between gap-4 flex-wrap">
        <div>
          <div className="eyebrow mb-1.5">§ Credentials management</div>
          <h2 className="font-display text-3xl md:text-4xl tracking-tight">
            Auth <span className="text-muted-foreground">pool</span>
          </h2>
          <p className="text-sm text-muted-foreground mt-1.5 mono tabular">
            <span className="text-[color:var(--success)] font-medium">{healthy}</span> healthy ·{" "}
            <span className="text-[color:var(--warning)] font-medium">{quota}</span> quota ·{" "}
            <span className="text-destructive font-medium">{unhealthy}</span> unhealthy ·{" "}
            {oauths.length} OAuth · {apikeys.length} API key(s)
          </p>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button onClick={onAddOAuth} className="gap-2">
            <Plus className="h-4 w-4" />
            Sign in with Claude
          </Button>
          <Button variant="outline" onClick={onAddAPIKey} className="gap-2">
            <KeyRound className="h-4 w-4" />
            API key
          </Button>
          <Button variant="outline" onClick={onUpload} className="gap-2">
            <FileJson className="h-4 w-4" />
            Upload JSON
          </Button>
        </div>
      </header>

      <nav
        role="tablist"
        aria-label="Credentials sub-sections"
        className="flex items-center gap-1 p-1 bg-muted/60 border border-border rounded-md w-fit"
      >
        {subTabs.map(({ key, label, icon: Icon, hint }) => {
          const active = sub === key;
          return (
            <button
              key={key}
              role="tab"
              aria-selected={active}
              onClick={() => setSub(key)}
              className={cn(
                "flex items-center gap-2 px-3 py-1.5 rounded-sm text-sm font-medium transition-all",
                active
                  ? "bg-card text-foreground shadow-sm border border-border"
                  : "text-muted-foreground hover:text-foreground",
              )}
            >
              <Icon className="h-3.5 w-3.5" />
              <span>{label}</span>
              <span className="eyebrow hidden sm:inline opacity-70 !tracking-wider">{hint}</span>
            </button>
          );
        })}
      </nav>

      {sub === "files" && (
        <>
          {!summary ? (
            <div className="py-16 text-center eyebrow animate-pulse bg-card border border-border-strong rounded-md">
              <span className="opacity-60">Loading credentials…</span>
            </div>
          ) : auths.length === 0 ? (
            <div className="py-14 px-6 text-center text-sm text-muted-foreground font-mono bg-card border border-border-strong rounded-md">
              No credentials yet — use the buttons above to add one.
            </div>
          ) : (
            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3 md:gap-4">
              {auths.map((a) => (
                <AuthCard key={a.id} a={a} onAction={onAction} onEdit={onEdit} />
              ))}
            </div>
          )}
        </>
      )}

      {sub === "quota" && (
        <div className="bg-card border border-border-strong rounded-md overflow-hidden">
          {!summary ? (
            <div className="py-16 text-center eyebrow animate-pulse">
              <span className="opacity-60">Loading…</span>
            </div>
          ) : (
            <UpstreamQuota auths={auths} />
          )}
        </div>
      )}
    </div>
  );
}
