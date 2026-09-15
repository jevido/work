import { svelte } from "@sveltejs/vite-plugin-svelte";
import { defineConfig } from "vite";

/**
 * The read-only viewer.
 *
 * Plain Svelte, not SvelteKit: this is one static page that fetches a merged
 * document and draws it. There is no routing, no server rendering and no
 * server to render on -- the Go sync server hands out these files and answers
 * /v1, and that is the whole deployment.
 *
 * There is no alias into frontend/ any more. The viewer used to reach through
 * one for the merge; the server does the merging now, so nothing under here
 * compiles anything from the desktop app.
 */
export default defineConfig({
  plugins: [svelte()],

  server: {
    port: 5175,
    // The API lives on the Go server. In development it is somewhere else, and
    // the viewer's own fetches are relative -- so they are proxied rather than
    // made cross-origin, which would need CORS on a server that has no reason
    // to allow it in production.
    proxy: {
      "/v1": {
        target: process.env.WORK_SERVER ?? "http://127.0.0.1:8080",
        changeOrigin: true,
      },
    },
  },

  build: {
    // Served from the root of the sync server.
    outDir: "dist",
    emptyOutDir: true,
  },
});
