import { fileURLToPath, URL } from "node:url";
import { svelte } from "@sveltejs/vite-plugin-svelte";
import { defineConfig } from "vite";

/**
 * The read-only viewer.
 *
 * Plain Svelte, not SvelteKit: this is one static page that fetches a log and
 * draws it. There is no routing, no server rendering and no server to render
 * on -- the Go sync server hands out these files and answers /v1, and that is
 * the whole deployment.
 */
export default defineConfig({
  plugins: [svelte()],

  resolve: {
    alias: {
      /**
       * The merge, and what a document means, shared with the desktop app.
       *
       * A copy would be the obvious thing and would be a bug waiting to
       * happen: the viewer replays the same op log the desktop writes, and
       * two implementations of that replay show two different documents for
       * the same log. Only the plain modules are reached through here --
       * nothing with a rune in it -- so this build never compiles anything
       * from the app.
       */
      "@doc": fileURLToPath(new URL("../frontend/src/lib/workspace", import.meta.url)),
    },
  },

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
