import path from "node:path"
import tailwindcss from "@tailwindcss/vite"
import react from "@vitejs/plugin-react"
import { defineConfig } from "vite"

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      "@": path.resolve(import.meta.dirname, "./src"),
    },
  },
  server: {
    proxy: {
      "/api": {
        target: "http://localhost:3704",
        changeOrigin: true,
        rewrite: (p) => p.replace(/^\/api/, ""),
      },
      // Phase 58 Route A: tiles now come from the Go API (session-authed,
      // territory-scoped MVT), not Martin. The API mounts its tile router at
      // /tiles, so the prefix is kept (no rewrite), unlike Martin which served
      // sources at its root. The frontend no longer talks to Martin at all --
      // the territory overlay is its own API endpoint (GET /territories/
      // boundaries), not a tile source.
      "/tiles": {
        target: "http://localhost:3704",
        changeOrigin: true,
      },
    },
  },
})
