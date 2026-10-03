import { defineConfig, lazyPlugins } from "vite-plus";
import { tanstackStart } from "@tanstack/react-start/plugin/vite";
import viteReact from "@vitejs/plugin-react";

export default defineConfig({
  // lazyPlugins skips loading the plugins when a command only reads
  // settings (lint, fmt); dev, build, and preview still load them.
  plugins: lazyPlugins(() => [
    // SPA mode: the build prerenders one HTML shell and the browser
    // renders every route. No SSR, no server functions.
    tanstackStart({ spa: { enabled: true } }),
    // Must come after tanstackStart.
    viteReact(),
  ]),
});
