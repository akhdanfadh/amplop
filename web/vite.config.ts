import { defineConfig, lazyPlugins } from "vite-plus";
import { tanstackStart } from "@tanstack/react-start/plugin/vite";
import viteReact from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { playwright } from "vite-plus/test/browser-playwright";
import { messageServer } from "./src/test/message-server";
import { defaultViewport } from "./src/test/viewport";

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

  // Run modules in import order even when the bundle merges them into
  // shared chunks; without it, a chunk's top-level code can run before
  // code imported earlier, e.g. src/zod-config.ts. Costs about 5% more
  // JavaScript for the wrappers that enforce the order.
  build: { rolldownOptions: { output: { strictExecutionOrder: true } } },

  // Resolve the `@/` alias from tsconfig.json `paths`.
  resolve: { tsconfigPaths: true },

  // Forward API calls to Go and preserve Host for its Origin checks.
  // `pnpm preview` reuses this rule.
  server: { proxy: { "/api": { target: proxyTarget, changeOrigin: false } } },

  // Two test projects: unit tests run in Node; component tests, named
  // *.browser.test.tsx, run in a real Chromium that Playwright drives, so
  // focus, pointer events, and layout behave as in a browser. web/README.md
  // says how to install that browser.
  test: {
    // Before each test, in both projects: a global a test stubbed
    // (vi.stubGlobal), e.g. fetch, is the real one again, and a method a
    // test spied on (vi.spyOn) is the original. So a stub or spy belongs
    // in the test or a beforeEach; one made for a whole file would last
    // only until its first test.
    unstubGlobals: true,
    restoreMocks: true,
    projects: [
      {
        extends: true,
        test: {
          name: "unit",
          include: ["src/**/*.test.ts"],
          environment: "node",
          setupFiles: ["src/test/setup.ts"],
        },
      },
      {
        extends: true,
        // Message bodies with the server's headers, for the message view.
        plugins: [messageServer()],
        // NOTE: React Aria's Virtualizer stops virtualizing, and keeps every
        // list scrolled to the top, when NODE_ENV is "test", which Vitest
        // sets, unless VIRT_ON is set too (react-aria's ScrollView,
        // updateSize). Browser tests check lists as they run in the app.
        define: { "process.env.VIRT_ON": JSON.stringify("1") },
        test: {
          name: "browser",
          include: ["src/**/*.browser.test.tsx"],
          // In this order: browser-setup.ts's cleanup after each test
          // must run before setup.ts's checks, and the later file's
          // afterEach runs first.
          setupFiles: ["src/test/setup.ts", "src/test/browser-setup.ts"],
          browser: {
            enabled: true,
            headless: true,
            provider: playwright(),
            instances: [{ browser: "chromium" }],
            // The size a test file's page opens at; browser-setup.ts sets
            // it again before each test.
            viewport: defaultViewport,
          },
        },
      },
    ],
  },

  // Oxfmt defaults, like gofmt: no style options to debate.
  fmt: {
    // Also skip the client and cn's tables from `pnpm generate`. They stay
    // linted, so a type error in them, e.g. a call that no longer fits
    // transport, still fails.
    ignorePatterns: [...generated, "src/api/generated/**", "src/lib/generated/**"],
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
      // The package's cn knows only Tailwind's own scales; src/lib/cn.ts
      // knows the theme's too, e.g. text-snippet.
      "no-restricted-imports": [
        "error",
        { paths: [{ name: "cn", message: 'Import cn from "@/lib/cn", which knows the theme.' }] },
      ],
    },
    // Orval's generated code breaks three rules by design: it casts its own
    // types, defines a header helper inside each write function, and turns
    // each value of a repeated query parameter, e.g. Search Conversations'
    // from, into a string with String() whatever its type. Only these rules
    // are off there; every other rule and the type check still apply.
    overrides: [
      {
        files: ["src/api/generated/**"],
        rules: {
          "typescript/no-unsafe-type-assertion": "off",
          "unicorn/consistent-function-scoping": "off",
          "typescript/no-unnecessary-type-conversion": "off",
        },
      },
      // cn build's tables cast typed arrays it already typed; only that
      // rule is off there.
      {
        files: ["src/lib/generated/**"],
        rules: { "typescript/no-unnecessary-type-assertion": "off" },
      },
    ],
    ignorePatterns: generated,
    // Also type-check, so `vp check` is the only static check.
    options: { typeAware: true, typeCheck: true },
  },
});
