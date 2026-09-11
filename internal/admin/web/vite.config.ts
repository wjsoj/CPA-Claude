import { defineConfig, type Plugin } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import path from "node:path";

// @fontsource's CSS reaches these three woff2 files through @font-face
// url()s, so the browser only discovers them after fetching *and parsing*
// index.css — a serial hop behind the stylesheet instead of a fetch running
// in parallel with it. Measured on the deployed panel: each of these three
// sits on the LCP render-delay critical path (732-1310ms). Latin covers the
// body/mono text every view renders; the Greek subset is pulled in too
// because the metric strip's "Σ in" / "Σ out" labels (dashboard.tsx) render
// unconditionally on first paint, not just latin body text — it is not
// dead weight to preload.
// Matched by the un-hashed fontsource asset name since Rollup hashes the
// actual filename on every build.
const PRELOAD_FONT_BASENAMES = [
  "bricolage-grotesque-latin-wght-normal",
  "jetbrains-mono-latin-wght-normal",
  "jetbrains-mono-greek-wght-normal",
];

// Injects <link rel=preload> for the fonts above once their hashed output
// names are known (generateBundle, after Rollup has emitted every asset).
function fontPreloadPlugin(): Plugin {
  let hrefs: string[] = [];
  return {
    name: "font-preload-links",
    generateBundle(_options, bundle) {
      hrefs = Object.keys(bundle).filter((fileName) =>
        PRELOAD_FONT_BASENAMES.some(
          (base) => fileName.startsWith(`assets/${base}-`) && fileName.endsWith(".woff2"),
        ),
      );
    },
    transformIndexHtml() {
      return hrefs.map((fileName) => ({
        tag: "link",
        attrs: {
          rel: "preload",
          as: "font",
          type: "font/woff2",
          crossorigin: "anonymous",
          // Relative like every other emitted asset link (base: "./" --
          // the panel is mounted at a configurable sub-path).
          href: `./${fileName}`,
        },
        injectTo: "head" as const,
      }));
    },
  };
}

// Admin panel is mounted under config.admin_path (default /mgmt-console),
// so use relative asset paths. The Go server serves /dist/* at
// <admin_path>/assets/* via an explicit route.
export default defineConfig({
  plugins: [react(), tailwindcss(), fontPreloadPlugin()],
  base: "./",
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "src"),
    },
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
    assetsDir: "assets",
    sourcemap: false,
    rollupOptions: {
      output: {
        // Only React is pinned to a chunk of its own. It is on the critical
        // path for every view, so it can never be deferred — but it also
        // changes only when the dependency is bumped, so isolating it means a
        // routine app change no longer invalidates 130KB of cached vendor
        // code. Everything else (recharts, radix, dnd-kit) is left to Rollup,
        // which already places it correctly from the dynamic-import
        // boundaries; naming those manually would only risk pulling a
        // deferred library back onto the critical path.
        manualChunks: {
          react: ["react", "react-dom", "react/jsx-runtime"],
        },
      },
    },
  },
  server: {
    port: 5173,
    proxy: {
      // The operator console and the public status page are two separate apps
      // behind one entry (see main.tsx), and the status page has API surface of
      // its own — /status/api/* plus the SaaS routes at /api/*. Proxying only
      // the console's prefix meant `bun run dev` could not develop the status
      // page at all: every call 404'd against Vite itself.
      "/mgmt-console/api": {
        target: "http://localhost:8317",
        changeOrigin: false,
      },
      "/status/api": {
        target: "http://localhost:8317",
        changeOrigin: false,
      },
      "/api": {
        target: "http://localhost:8317",
        changeOrigin: false,
      },
    },
  },
});
