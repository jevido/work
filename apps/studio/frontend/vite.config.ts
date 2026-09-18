import { fileURLToPath } from "node:url";
import { defineConfig } from "vite";
import { svelte } from "@sveltejs/vite-plugin-svelte";
import wails from "@wailsio/runtime/plugins/vite";

// https://vitejs.dev/config/
export default defineConfig({
  server: {
    host: "127.0.0.1",
    port: Number(process.env.WAILS_VITE_PORT) || 9245,
    strictPort: true,
  },
  // The two shared front-end packages. They live outside this app because the
  // website draws the same map and drags the same panels: geometry and canvas
  // calls with no document model in them, which is exactly the code two apps
  // can hold one copy of. Anything with a document model in it stays here.
  resolve: {
    alias: {
      "@mindmap": fileURLToPath(new URL("../../../packages/mindmap", import.meta.url)),
      "@ui": fileURLToPath(new URL("../../../packages/ui", import.meta.url)),
    },
  },

  plugins: [svelte(), wails("./bindings")],
});
