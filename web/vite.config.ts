import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],

  build: {
    // internal/webui embeds this directory, so the build has to land there
    // rather than in the conventional web/dist.
    outDir: "../internal/webui/dist",
    emptyOutDir: true,

    // Relative asset URLs keep the bundle working no matter what path the
    // admin interface ends up mounted on.
    assetsDir: "assets",
  },

  base: "./",

  server: {
    port: 5173,
    // The dev server proxies to a locally running `jarvis run`, so the UI can
    // hot-reload against the real API instead of a mock.
    proxy: {
      "/api": {
        target: "http://127.0.0.1:9527",
        changeOrigin: true,
      },
    },
  },
});
