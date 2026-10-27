import { defineConfig, lazyPlugins } from "vite-plus";
import { tanstackStart } from "@tanstack/react-start/plugin/vite";
import viteReact from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// Written by the router plugin; it asks to be left unformatted and unlinted.
const generated = ["src/routeTree.gen.ts"];

// Go server address for the dev proxy; override when it runs elsewhere.
// Unset or empty uses the default, as in the Go config.
const defaultProxyTarget = "http://127.0.0.1:8080";
const proxyTarget = process.env.INBOK_PROXY_TARGET || defaultProxyTarget;

export default defineConfig({
  // lazyPlugins skips loading the plugins when a command only reads
  // settings (lint, fmt); dev, build, and preview still load them.
  plugins: lazyPlugins(() => [
    // SPA mode: the build prerenders one HTML shell and the browser
    // renders every route. No SSR, no server functions.
    tanstackStart({ spa: { enabled: true } }),
    // Must come after tanstackStart.
    viteReact(),
    tailwindcss(),
  ]),

  // Resolve the `@/` alias from tsconfig.json `paths`.
  resolve: { tsconfigPaths: true },

  // Forward API calls to Go and preserve Host for its Origin checks.
  // `pnpm preview` reuses this rule.
  server: { proxy: { "/api": { target: proxyTarget, changeOrigin: false } } },

  // Oxfmt defaults, like gofmt: no style options to debate.
  fmt: {
    // Also skip the client from `pnpm generate`. It stays linted, so a type
    // error in it, e.g. a call that no longer fits transport, still fails.
    ignorePatterns: [...generated, "src/api/generated/**"],
    // Also sorts classes inside cn(...) and cva(...). If the stylesheet
    // moves, update the path: a missing file silently turns sorting off.
    sortTailwindcss: { stylesheet: "src/styles.css", functions: ["cn", "cva"] },
  },

  lint: {
    // react-perf flags idiomatic inline handlers; import adds little over
    // TypeScript at these categories.
    plugins: ["eslint", "typescript", "unicorn", "oxc", "react", "jsx-a11y", "vitest"],
    jsPlugins: [{ name: "vite-plus", specifier: "vite-plus/oxlint-plugin" }],
    // Bug-finding categories fail the check. pedantic and style are
    // taste; restriction and nursery are opt-in or unstable.
    categories: { correctness: "error", suspicious: "error", perf: "error" },
    rules: {
      // Picked from pedantic. exhaustive-deps is already in suspicious.
      "react/rules-of-hooks": "error",
      "no-fallthrough": "error",
      // Newer, barely documented duplicates of rules-of-hooks and
      // exhaustive-deps, which are ports of React's own ESLint plugin.
      "react/hooks": "off",
      "react/exhaustive-effect-dependencies": "off",
      // Obsolete since React 17: JSX no longer needs React in scope.
      "react/react-in-jsx-scope": "off",
      // Flags `from "vite"` in this file and `from "vitest"` anywhere,
      // as copied from plain-Vite docs; Vite+ wants `vite-plus` instead.
      "vite-plus/prefer-vite-plus-imports": "error",
      // A deprecated API still works but will go, e.g. Zod's isInt; the
      // message names the replacement.
      "typescript/no-deprecated": "error",
    },
    // Orval's generated code breaks two rules by design: it casts its own types
    // and defines a header helper inside each write function. Only these rules
    // are off there; every other rule and the type check still apply.
    overrides: [
      {
        files: ["src/api/generated/**"],
        rules: {
          "typescript/no-unsafe-type-assertion": "off",
          "unicorn/consistent-function-scoping": "off",
        },
      },
    ],
    ignorePatterns: generated,
    // Also type-check, so `vp check` is the only static check.
    options: { typeAware: true, typeCheck: true },
  },
});
