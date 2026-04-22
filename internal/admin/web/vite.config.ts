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
