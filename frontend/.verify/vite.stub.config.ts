import { svelte } from "@sveltejs/vite-plugin-svelte";
import wails from "@wailsio/runtime/plugins/vite";
import { resolve } from "node:path";
import { defineConfig } from "vite";

// As .verify/vite.config.ts, plus the config-call stub aliased over the
// generated bindings: the backend has no config folder calls yet, and the setup
// screen cannot be driven without them.
export default defineConfig({
  root: ".",
  server: { host: "127.0.0.1", port: 9346, strictPort: true },
  resolve: {
    // Matches the whole specifier, not its tail: a regex that matches part of
    // an id has that part replaced, which splices the stub's absolute path
    // onto the end of the relative one and resolves to nothing.
    alias: [
      {
        find: /^\.\.\/\.\.\/\.\.\/bindings\/dev\.jevido\/work\/services\/workbenchservice\.js$/,
        replacement: resolve(".verify/config-bindings.ts"),
      },
    ],
  },
  // The wails plugin is what resolves the generated bindings' .js specifiers
  // onto the .ts files they actually are.
  plugins: [svelte(), wails("./bindings")],
});
