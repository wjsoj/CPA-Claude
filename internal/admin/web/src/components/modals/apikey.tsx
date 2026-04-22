import { useState } from "react";
import { api } from "@/lib/api";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { textToModelMap } from "@/lib/utils";

interface Props {
  onClose: () => void;
  onSaved: () => void;
}

export function APIKeyModal({ onClose, onSaved }: Props) {
  const [apiKey, setAPIKey] = useState("");
  const [label, setLabel] = useState("");
  const [proxy, setProxy] = useState("");
  const [baseURL, setBaseURL] = useState("");
  const [group, setGroup] = useState("");
  const [modelMapText, setModelMapText] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  const save = async () => {
    setBusy(true);
    setErr("");
    try {
      const parsed = textToModelMap(modelMapText);
      if (parsed.errors.length > 0) {
        throw new Error("model map: " + parsed.errors.join("; "));
      }
      await api("/admin/api/apikeys", {
        method: "POST",
        body: JSON.stringify({
          api_key: apiKey.trim(),
          label: label.trim(),
          proxy_url: proxy.trim(),
          base_url: baseURL.trim(),
          group: group.trim(),
          model_map: parsed.map,
        }),
      });
      onSaved();
    } catch (x: any) {
      setErr(x.message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Add API key</DialogTitle>
          <DialogDescription>
            Anthropic <span className="mono">sk-ant-api…</span> key. Stored as a JSON file in{" "}
            <span className="mono">auth_dir</span>, mutable from the panel.
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-1.5">
          <Label>API key</Label>
          <Input
            type="password"
            autoFocus
            className="mono"
            placeholder="sk-ant-api03-..."
            value={apiKey}
            onChange={(e) => setAPIKey(e.currentTarget.value)}
          />
        </div>
        <div className="space-y-1.5">
          <Label>Label</Label>
          <Input
            placeholder="primary"
            value={label}
            onChange={(e) => setLabel(e.currentTarget.value)}
          />
        </div>
        <div className="space-y-1.5">
          <Label>Base URL (optional, for relay vendors)</Label>
          <Input
            className="mono"
            placeholder="https://api.your-relay-vendor.com (default: api.anthropic.com)"
            value={baseURL}
            onChange={(e) => setBaseURL(e.currentTarget.value)}
          />
          <p className="text-xs text-muted-foreground">
            Requests with this key go to <span className="mono">{"{base_url}/v1/messages"}</span>.
            Leave empty to hit Anthropic directly.
          </p>
        </div>
        <div className="space-y-1.5">
          <Label>Proxy URL (optional)</Label>
          <Input
            className="mono"
            placeholder="http:// or socks5://"
            value={proxy}
            onChange={(e) => setProxy(e.currentTarget.value)}
          />
        </div>
        <div className="space-y-1.5">
          <Label>Group (optional)</Label>
          <Input
            list="groups-datalist"
            placeholder="public"
            value={group}
            onChange={(e) => setGroup(e.currentTarget.value)}
          />
        </div>
        <div className="space-y-1.5">
          <Label>Model map (optional)</Label>
          <Textarea
            className="mono text-sm h-32"
            placeholder={"claude-opus-4-6 = [0.16]稳定喵/claude-opus-4-6\nclaude-haiku-4-5 ="}
            value={modelMapText}
            onChange={(e) => setModelMapText(e.currentTarget.value)}
          />
          <p className="text-xs text-muted-foreground">
            One <span className="mono">client_model = upstream_model</span> per line. When non-empty,
            this key only serves listed client models, and the request body's{" "}
            <span className="mono">model</span> is rewritten before forwarding.
          </p>
        </div>
        {err && <div className="text-sm text-destructive">{err}</div>}
        <DialogFooter className="gap-2 sm:gap-2">
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button disabled={busy || !apiKey.trim()} onClick={save}>
            {busy ? "Saving…" : "Add"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
