import { svelte } from "@sveltejs/vite-plugin-svelte";
import { defineConfig } from "vite";

/**
 * Charted's reader.
 *
 * Plain Svelte, not SvelteKit, and for once that is not only about keeping the
 * build small: every page is rendered to HTML by the Go server at write time,
 * so there is nothing here to server-render. What ships is one static bundle
 * that fetches JSON and puts already-rendered markup on the screen.
 */
export default defineConfig({
  plugins: [svelte()],

  server: {
    port: 5176,
    // The API is the Go server's. In development it is somewhere else, and the
    // reader's own fetches are relative -- so they are proxied rather than made
    // cross-origin, which would need CORS on a server with no reason to allow
    // it in production.
    proxy: {
      "/v1": {
        target: process.env.CHARTED_SERVER ?? "http://127.0.0.1:8081",
        changeOrigin: true,
      },
    },
  },

  build: {
    // Served from the root of the Charted binary.
    outDir: "dist",
    emptyOutDir: true,
  },
});
