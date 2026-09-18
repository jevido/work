import { svelte } from "@sveltejs/vite-plugin-svelte";
import wails from "@wailsio/runtime/plugins/vite";
import { defineConfig } from "vite";

// Throwaway harness config: the app's own config installs the Wails vite
// plugin, which expects the desktop runtime. Here the transport is stubbed in
// harness.ts instead, so everything except the IPC hop is the real thing.
export default defineConfig({
  root: ".",
  server: { host: "127.0.0.1", port: 9345, strictPort: true },
  // The wails plugin is what resolves the generated bindings' .js specifiers
  // onto the .ts files they actually are; without it every import of a binding
  // fails to resolve and nothing under here loads.
  plugins: [svelte(), wails("./bindings")],
});
