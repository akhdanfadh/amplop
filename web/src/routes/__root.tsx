import { useEffect, type ReactElement, type ReactNode } from "react";
import {
  CatchBoundary,
  HeadContent,
  Outlet,
  Scripts,
  createRootRoute,
} from "@tanstack/react-router";
import appCss from "../styles.css?url";
import pressableCss from "../react-aria-pressable.css?url";
import { CrashScreen } from "@/components/route-states";
import { SetupGate } from "@/features/setup/gate";
import { themeScript, watchTheme } from "@/lib/theme";

export const Route = createRootRoute({
  head: () => ({
    meta: [
      { charSet: "utf-8" },
      { name: "viewport", content: "width=device-width, initial-scale=1" },
      { title: "inbok" },
    ],
    // A <link> in the prerendered shell, so the page never shows unstyled.
    links: [
      // React Aria skips its own copy of this rule when it finds the id;
      // the file says why. First, as React Aria would place its copy, so
      // the app's touch-* utilities override it.
      { rel: "stylesheet", href: pressableCss, id: "react-aria-pressable-style" },
      { rel: "stylesheet", href: appCss },
    ],
    // In the prerendered shell's head, so the page never shows the wrong
    // theme first. The server's Content-Security-Policy allows the shell's
    // inline scripts by hash.
    scripts: [{ children: themeScript }],
  }),
  shellComponent: RootDocument,
  component: Root,
});

// The HTML document around every route. The build prerenders it once into
// the static shell; the browser fills in the matched route.
function RootDocument({ children }: { children: ReactNode }) {
  // Follow the system theme and other tabs' choices for as long as the app runs.
  useEffect(() => watchTheme(), []);

  return (
    // The head script sets the class before React starts.
    <html lang="en" suppressHydrationWarning>
      <head>
        <HeadContent />
      </head>
      <body>
        {/* The router's own boundaries sit inside SetupGate; this one shows
            inbok's crash screen, not TanStack's, when the gate itself fails. */}
        <CatchBoundary getResetKey={() => "shell"} errorComponent={CrashScreen}>
          <SetupGate>{children}</SetupGate>
        </CatchBoundary>
        <Scripts />
      </body>
    </html>
  );
}

// Root draws every page in a column the height of the window, so a page
// or the pending view can fill it.
function Root(): ReactElement {
  return (
    <div className="flex min-h-svh flex-col">
      <Outlet />
    </div>
  );
}
