import { Terminal } from "lucide-react";
import { ClientOnboarding } from "@/components/client-onboarding";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import type { ClientRow } from "@/lib/types";

// One-click client setup (CC Switch / Cherry Studio deep links + env-var /
// config-file snippets) for a client token. The ClientOnboarding panel is the
// shared, byte-identical component (see components/client-onboarding.tsx);
// this modal is just the CPA-Claude host for it. Uses the panel's default
// English labels. baseUrl is the gateway the console is served from.
export function TokenSetupModal({
  client,
  onClose,
}: {
  client: ClientRow | null;
  onClose: () => void;
}) {
  return (
    <Dialog open={!!client} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-[680px] max-h-[85vh] overflow-y-auto [&>*]:min-w-0">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Terminal className="h-4 w-4" />
            Set up client
            {client?.label && (
              <span className="font-mono text-sm font-normal text-muted-foreground">
                · {client.label}
              </span>
            )}
          </DialogTitle>
        </DialogHeader>
        {client?.full_token && (
          <ClientOnboarding
            config={{
              token: client.full_token,
              baseUrl: window.location.origin,
              providerName: window.location.hostname,
            }}
          />
        )}
      </DialogContent>
    </Dialog>
  );
}
