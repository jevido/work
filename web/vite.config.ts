import { fileURLToPath } from "node:url";
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
 * There is one alias into frontend/, and it is narrow on purpose. The viewer
 * used to reach through one for the *merge*, and that was the problem: two
 * merge implementations for one log drift, and when they do there is no
 * correct side and no way to see it from either. The server merges now.
 *
 * What is aliased instead is the map -- layout and renderer, which are
 * geometry and canvas calls with no document model in them at all. A drift
 * there means the boxes are a few pixels apart in two apps, not that two
 * people are looking at different documents. The alternative was six hundred
 * lines of tidy-tree and bezier maths copied into this directory, which drifts
 * in exactly the same way and is harder to notice.
 */
export default defineConfig({
  plugins: [svelte()],

  resolve: {
    alias: {
      "@mindmap": fileURLToPath(new URL("../frontend/src/lib/mindmap", import.meta.url)),
      // And the one piece of panel behaviour the two apps share: dragging a
      // floating panel by its header. It is the same trade as the map -- pure
      // geometry with no document model in it, where a drift means a panel
      // that clamps to its edges slightly differently in two apps rather than
      // two people looking at different documents.
      "@ui": fileURLToPath(new URL("../frontend/src/lib/ui", import.meta.url)),
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
