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

// docsAsMarkdown concatenates the whole set into one document.
//
// This is the "copy everything" payload, and its audience is an AI agent, not
// a reader: users paste it into Claude Code or Codex and ask it to perform the
// setup. So it leads with an instruction line telling the model what it is
// looking at — without it, a pasted wall of prose reads as a question about
// documentation rather than a task to execute.
export function docsAsMarkdown(): string {
  const parts = DOCS.map((d) => `## ${d.title}\n\n${d.body}`);
  return [
    "# CPA-Claude 接入文档（全文）",
    "",
    "以下是 CPA-Claude API 网关的完整接入说明。请据此为我完成配置：",
    "读取我的操作系统与已安装的客户端，写入对应的配置文件或环境变量，",
    "然后运行文档中的验证命令确认接入成功。密钥需要我提供，不要凭空编造。",
    "",
    "---",
    "",
    parts.join("\n\n---\n\n"),
  ].join("\n");
}
