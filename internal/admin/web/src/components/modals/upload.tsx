import { useState, useRef } from "react";
import { api } from "@/lib/api";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";

interface Props {
  onClose: () => void;
  onSaved: () => void;
}

export function UploadModal({ onClose, onSaved }: Props) {
  const [filename, setFilename] = useState("");
  const [content, setContent] = useState("");
  const [label, setLabel] = useState("");
  const [maxC, setMaxC] = useState("5");
  const [proxy, setProxy] = useState("");
  const [group, setGroup] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const fileInputRef = useRef<HTMLInputElement>(null);

  const onFile = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const f = e.currentTarget.files?.[0];
    if (!f) return;
    setFilename(f.name);
    setContent(await f.text());
  };
  const save = async () => {
    setBusy(true);
    setErr("");
    try {
      const parsed = JSON.parse(content);
      await api("/admin/api/auths/upload", {
        method: "POST",
        body: JSON.stringify({
          filename,
          content: parsed,
          label,
          max_concurrent: Number(maxC),
          proxy_url: proxy,
          group,
        }),
      });
      onSaved();
    } catch (x: any) {
      setErr(x.message || String(x));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Add OAuth credential</DialogTitle>
        </DialogHeader>
        <input
          type="file"
          accept=".json,application/json"
          ref={fileInputRef}
          onChange={onFile}
          className="hidden"
        />
        <div className="flex gap-2">
          <Button variant="outline" onClick={() => fileInputRef.current?.click()}>
            Choose JSON file…
          </Button>
          <Input
            className="flex-1 mono"
            placeholder="filename (optional)"
            value={filename}
            onChange={(e) => setFilename(e.currentTarget.value)}
          />
        </div>
        <div className="space-y-1.5">
          <Label>or paste JSON</Label>
          <Textarea
            className="mono h-40"
            placeholder='{"type":"claude","access_token":"...","refresh_token":"...","email":"..."}'
            value={content}
            onChange={(e) => setContent(e.currentTarget.value)}
          />
        </div>
        <div className="grid grid-cols-2 gap-3">
          <div className="space-y-1.5">
            <Label>Label</Label>
            <Input value={label} onChange={(e) => setLabel(e.currentTarget.value)} />
          </div>
          <div className="space-y-1.5">
            <Label>Max concurrent</Label>
            <Input
              type="number"
              min={0}
              value={maxC}
              onChange={(e) => setMaxC(e.currentTarget.value)}
            />
          </div>
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
        {err && <div className="text-sm text-destructive whitespace-pre-wrap">{err}</div>}
        <DialogFooter className="gap-2 sm:gap-2">
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button disabled={busy || !content} onClick={save}>
            {busy ? "Uploading…" : "Add"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
