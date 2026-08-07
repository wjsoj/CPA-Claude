import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import path from "node:path";

// Admin panel is mounted under config.admin_path (default /mgmt-console),
// so use relative asset paths. The Go server serves /dist/* at
// <admin_path>/assets/* via an explicit route.
export default defineConfig({
  plugins: [react(), tailwindcss()],
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
      "/mgmt-console/api": {
        target: "http://localhost:8317",
        changeOrigin: false,
      },
    },
  },
});
