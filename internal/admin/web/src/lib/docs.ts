// Discovers every `.md` file in src/content/docs/, parses its frontmatter and
// exposes them as DocSection[]. Adding a page is adding a file — no code edit.
//
// Ported from hypitoken's identical loader (internal/admin/web/src/lib/docs.ts)
// so the two products' doc sets stay interchangeable. The difference: hypitoken
// ships bilingual docs under en/ and zh/ subdirectories, CPA-Claude's status
// page is Chinese-only, so there is one flat directory and no language merge.

export interface DocSection {
  slug: string; // url fragment / localStorage key
  title: string;
  group: string; // sidebar grouping, taken verbatim from frontmatter
  intro?: string;
  order: number; // sort key across the whole set, not per group
  body: string; // raw markdown, frontmatter stripped — also the copy payload
}

// Vite inlines every .md as raw text at build time, so the docs ship inside
// the same embedded SPA bundle the Go binary serves. No runtime fetch, which
// matters because /status is anonymous and must not gain new endpoints.
const modules = import.meta.glob<string>("../content/docs/*.md", {
  eager: true,
  query: "?raw",
  import: "default",
});

// Deliberately line-based rather than a YAML parser: the frontmatter contract
// is flat `key: value` and pulling in a YAML dependency to read five strings
// would cost more than the whole docs feature.
function parseFrontmatter(raw: string): { meta: Record<string, string>; body: string } {
  const m = /^---\r?\n([\s\S]*?)\r?\n---\r?\n+([\s\S]*)$/.exec(raw);
  if (!m) return { meta: {}, body: raw };
  const meta: Record<string, string> = {};
  for (const line of (m[1] ?? "").split(/\r?\n/)) {
    const i = line.indexOf(":");
    if (i <= 0) continue;
    const k = line.slice(0, i).trim();
    let v = line.slice(i + 1).trim();
    if ((v.startsWith('"') && v.endsWith('"')) || (v.startsWith("'") && v.endsWith("'"))) {
      v = v.slice(1, -1);
    }
    meta[k] = v;
  }
  return { meta, body: m[2] ?? "" };
}

export const DOCS: DocSection[] = Object.values(modules)
  .map((raw) => {
    const { meta, body } = parseFrontmatter(raw);
    return {
      slug: meta.slug || "untitled",
      title: meta.title || meta.slug || "未命名",
      group: meta.group || "参考",
      intro: meta.intro,
      order: parseInt(meta.order || "999", 10),
      body: body.trim(),
    };
  })
  .sort((a, b) => a.order - b.order);

// Groups in first-appearance order, which after the sort above is order order.
export const DOC_GROUPS: string[] = Array.from(new Set(DOCS.map((d) => d.group)));

export function findDoc(slug: string): DocSection | undefined {
  return DOCS.find((d) => d.slug === slug);
}

// docAsMarkdown renders one page as a standalone markdown document.
//
// This is the clipboard payload, and its usual destination is an agent's
// prompt window rather than a text editor: users copy a page and ask Claude
// Code or Codex to carry out the setup it describes. Hence the title heading —
// the body's own headings start at h2, so without it the pasted text has no
// statement of what it is about.
//
// Deliberately nothing more than that. An instruction preamble would make the
// payload something other than what the button says it copies.
export function docAsMarkdown(doc: DocSection): string {
  return `# ${doc.title}\n\n${doc.body}\n`;
}
