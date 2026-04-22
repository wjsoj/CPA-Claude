import { KeyRound, Plus, FileJson } from "lucide-react";
import type { AuthRow, Summary } from "@/lib/types";
import { AuthCard } from "./auth-card";
import { Button } from "@/components/ui/button";

type Action = "toggle" | "refresh" | "clear-quota" | "clear-failure" | "delete";

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
  const auths = summary?.auths || [];
  const oauths = auths.filter((a) => a.kind === "oauth");
  const apikeys = auths.filter((a) => a.kind === "apikey");
  const healthy = auths.filter((a) => a.healthy).length;
  const quota = auths.filter((a) => a.quota_exceeded).length;
  const unhealthy = auths.filter((a) => a.hard_failure).length;

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
    </div>
  );
}
