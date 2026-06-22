import { useEffect, useState } from "react";
import { KeyRound, Plus, FileJson, Cookie, GripVertical } from "lucide-react";
import {
  DndContext,
  closestCenter,
  PointerSensor,
  useSensor,
  useSensors,
  type DragEndEvent,
} from "@dnd-kit/core";
import {
  SortableContext,
  useSortable,
  arrayMove,
  rectSortingStrategy,
} from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import type { AuthRow, Provider, Summary } from "@/lib/types";
import { AuthCard } from "./auth-card";
import { Button } from "@/components/ui/button";

type Action = "toggle" | "refresh" | "clear-quota" | "clear-failure" | "delete";

interface Props {
  summary: Summary | null;
  onAction: (a: AuthRow, act: Action) => void;
  onEdit: (a: AuthRow) => void;
  onAddOAuth: (provider: Provider) => void;
  onAddAPIKey: (provider: Provider) => void;
  onUpload: (provider: Provider) => void;
  onAddSessionCookie: () => void;
  // Persist a new API-key priority order (first id = highest priority). Throws
  // on failure so the panel can revert its optimistic reorder.
  onReorder: (provider: Provider, orderedIds: string[]) => Promise<void>;
}

// Sub-tab per upstream provider. The user explicitly asked for strict
// separation — a single credential's auth flow, pricing, and upstream
// are provider-specific, so mixing them in one card list makes the UI
// unclear. Tokens / logs / stats / status stay unified at their own
// top-level tabs.
const TABS: { id: Provider; label: string; signInCta: string }[] = [
  { id: "anthropic", label: "Claude", signInCta: "Sign in with Claude" },
  { id: "openai", label: "Codex (ChatGPT)", signInCta: "Sign in with ChatGPT" },
];

function normProvider(p: string | undefined): Provider {
  return p === "openai" ? "openai" : "anthropic";
}

// SortableAuthCard wraps an AuthCard with dnd-kit sortable behaviour. The drag
// handle (a GripVertical button) carries the listeners so the rest of the card
// stays fully interactive (edit / disable / delete buttons keep working).
function SortableAuthCard({
  a,
  onAction,
  onEdit,
}: {
  a: AuthRow;
  onAction: (a: AuthRow, act: Action) => void;
  onEdit: (a: AuthRow) => void;
}) {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } =
    useSortable({ id: a.id });
  const style = {
    transform: CSS.Transform.toString(transform),
    transition,
    opacity: isDragging ? 0.5 : 1,
    zIndex: isDragging ? 20 : undefined,
  };
  const handle = (
    <button
      type="button"
      className="shrink-0 -ml-1 cursor-grab active:cursor-grabbing text-muted-foreground hover:text-foreground touch-none"
      title="Drag to reorder priority — higher card is tried first"
      aria-label="Drag to reorder"
      {...attributes}
      {...listeners}
    >
      <GripVertical className="h-4 w-4" />
    </button>
  );
  return (
    <div ref={setNodeRef} style={style}>
      <AuthCard a={a} onAction={onAction} onEdit={onEdit} dragHandle={handle} />
    </div>
  );
}

export function CredentialsPanel({
  summary,
  onAction,
  onEdit,
  onAddOAuth,
  onAddAPIKey,
  onUpload,
  onAddSessionCookie,
  onReorder,
}: Props) {
  const [provider, setProvider] = useState<Provider>("anthropic");
  const auths = summary?.auths || [];
  const scoped = auths.filter((a) => normProvider(a.provider) === provider);
  const oauths = scoped.filter((a) => a.kind === "oauth");
  const apikeys = scoped.filter((a) => a.kind === "apikey");
  // Only file-backed API keys can be reordered — their order persists to the
  // credential JSON. config.yaml-declared keys (read-only) render after them,
  // pinned, with no drag handle.
  const sortableKeys = apikeys.filter((a) => a.file_backed);
  const staticKeys = apikeys.filter((a) => !a.file_backed);
  const healthy = scoped.filter((a) => a.healthy).length;
  const quota = scoped.filter((a) => a.quota_exceeded).length;
  const unhealthy = scoped.filter((a) => a.hard_failure).length;

  const current = TABS.find((t) => t.id === provider)!;

  // Local optimistic order for the sortable API keys. Resynced from the backend
  // whenever the set or its server-side order changes (e.g. a poll refresh or
  // another admin reordering), keyed by an id:order signature so an unrelated
  // poll that returns the same order doesn't clobber an in-flight drag.
  const [ordered, setOrdered] = useState<AuthRow[]>(sortableKeys);
  const sig = sortableKeys.map((a) => `${a.id}:${a.order}`).join(",");
  useEffect(() => {
    setOrdered(sortableKeys);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sig, provider]);

  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 6 } }),
  );
  const canSort = ordered.length >= 2;

  const onDragEnd = async (e: DragEndEvent) => {
    const { active, over } = e;
    if (!over || active.id === over.id) return;
    const oldIndex = ordered.findIndex((a) => a.id === active.id);
    const newIndex = ordered.findIndex((a) => a.id === over.id);
    if (oldIndex < 0 || newIndex < 0) return;
    const prev = ordered;
    const next = arrayMove(ordered, oldIndex, newIndex);
    setOrdered(next);
    try {
      await onReorder(
        provider,
        next.map((a) => a.id),
      );
    } catch {
      setOrdered(prev); // toast is raised by the caller; just revert here
    }
  };

  // Per-provider counts so operators can see at a glance which tab has
  // credentials. Rendered inline on the tab buttons.
  const countFor = (p: Provider) =>
    auths.filter((a) => normProvider(a.provider) === p).length;

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
          <Button onClick={() => onAddOAuth(provider)} className="gap-2">
            <Plus className="h-4 w-4" />
            {current.signInCta}
          </Button>
          {provider === "anthropic" && (
            <Button variant="outline" onClick={onAddSessionCookie} className="gap-2">
              <Cookie className="h-4 w-4" />
              Session cookie
            </Button>
          )}
          <Button variant="outline" onClick={() => onAddAPIKey(provider)} className="gap-2">
            <KeyRound className="h-4 w-4" />
            API key
          </Button>
          <Button variant="outline" onClick={() => onUpload(provider)} className="gap-2">
            <FileJson className="h-4 w-4" />
            Upload {current.label === "Claude" ? "Claude" : "Codex"} JSON
          </Button>
        </div>
      </header>

      {/* Provider sub-tabs. Strict separation — Claude and Codex credentials
          never mix in the same list. */}
      <nav className="flex gap-1 border-b border-border-strong">
        {TABS.map((t) => {
          const active = t.id === provider;
          const n = countFor(t.id);
          return (
            <button
              key={t.id}
              onClick={() => setProvider(t.id)}
              className={
                "px-4 py-2 text-sm font-medium transition-colors -mb-px border-b-2 " +
                (active
                  ? "border-foreground text-foreground"
                  : "border-transparent text-muted-foreground hover:text-foreground")
              }
            >
              {t.label}
              <span className="ml-2 mono text-xs opacity-70 tabular">{n}</span>
            </button>
          );
        })}
      </nav>

      {!summary ? (
        <div className="py-16 text-center eyebrow animate-pulse bg-card border border-border-strong rounded-md">
          <span className="opacity-60">Loading credentials…</span>
        </div>
      ) : scoped.length === 0 ? (
        <div className="py-14 px-6 text-center text-sm text-muted-foreground font-mono bg-card border border-border-strong rounded-md">
          No {current.label} credentials yet — use the buttons above to add one.
        </div>
      ) : (
        <div className="space-y-4">
          {/* OAuth credentials — load-balanced by usage, not reorderable. */}
          {oauths.length > 0 && (
            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3 md:gap-4">
              {oauths.map((a) => (
                <AuthCard key={a.id} a={a} onAction={onAction} onEdit={onEdit} />
              ))}
            </div>
          )}

          {/* API keys — operator-ordered priority. Drag a card by its handle to
              promote it; the topmost healthy key is tried first. */}
          {apikeys.length > 0 && (
            <div className="space-y-2">
              {oauths.length > 0 && (
                <div className="eyebrow text-muted-foreground pt-1">
                  API keys{" "}
                  <span className="opacity-60 normal-case tracking-normal">
                    · priority order, drag to reorder
                  </span>
                </div>
              )}
              <DndContext
                sensors={sensors}
                collisionDetection={closestCenter}
                onDragEnd={onDragEnd}
              >
                <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3 md:gap-4">
                  <SortableContext
                    items={ordered.map((a) => a.id)}
                    strategy={rectSortingStrategy}
                  >
                    {ordered.map((a) =>
                      canSort ? (
                        <SortableAuthCard
                          key={a.id}
                          a={a}
                          onAction={onAction}
                          onEdit={onEdit}
                        />
                      ) : (
                        <AuthCard key={a.id} a={a} onAction={onAction} onEdit={onEdit} />
                      ),
                    )}
                  </SortableContext>
                  {/* config.yaml keys: pinned, not reorderable. */}
                  {staticKeys.map((a) => (
                    <AuthCard key={a.id} a={a} onAction={onAction} onEdit={onEdit} />
                  ))}
                </div>
              </DndContext>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
