import { useCallback, useEffect, type ReactElement, type ReactNode } from "react";
import type { QueryClient } from "@tanstack/react-query";
import {
  CatchBoundary,
  HeadContent,
  Outlet,
  Scripts,
  createRootRouteWithContext,
  useRouter,
  type NavigateOptions,
  type ToOptions,
} from "@tanstack/react-router";
import { I18nProvider, RouterProvider, type LinkProps } from "react-aria-components";
import appCss from "../styles.css?url";
import pressableCss from "../react-aria-pressable.css?url";
import { CrashScreen } from "@/components/route-states";
import { Toaster } from "@/components/ui/toast";
import { SetupGate } from "@/features/setup/gate";
import { themeScript, watchTheme } from "@/lib/theme";

// A link to something that is not a page of the app, written as the
// browser reads it: another site, an email address, a place on this page,
// or a file the server sends, e.g. an attachment under /api/.
type ExternalHref =
  | `http://${string}`
  | `https://${string}`
  | `mailto:${string}`
  | `#${string}`
  | `/api/${string}`;

// A destination in the app as TanStack Router's options, checked against
// its routes. Write it with TanStack's linkOptions, e.g.
// linkOptions({ to: "/outbox/$id", params: { id } }): a type written once
// here cannot tell which route a link means, so a bare object is checked
// for its route's name and its params' names, not for params it lacks.
// NOTE: every option of ToOptions is optional, so TypeScript would take
// any string for it; requiring to makes a path written as a string, which
// nothing checks, a type error.
type AppHref = ToOptions & { to: NonNullable<ToOptions["to"]> };

declare module "react-aria-components" {
  // What React Aria's links take (React Aria's TanStack Router setup,
  // https://react-aria.adobe.com/routing): an AppHref or an ExternalHref;
  // and the router's own options, e.g. replace.
  interface RouterConfig {
    href: AppHref | ExternalHref;
    routerOptions: Omit<NavigateOptions, keyof ToOptions>;
  }
}

type Href = NonNullable<LinkProps["href"]>;
type RouterOptions = NonNullable<LinkProps["routerOptions"]>;

// RouterContext is what every route's beforeLoad and loader receive.
type RouterContext = {
  // The app's query cache, which route guards read the session from.
  queryClient: QueryClient;
};

export const Route = createRootRouteWithContext<RouterContext>()({
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

// Root wraps every page in what the whole app shares: React Aria's words
// and direction, its links navigating through the router, and the one
// toast region.
function Root(): ReactElement {
  const router = useRouter();
  // React Aria components with an href, e.g. a sidebar item, open it in the
  // app instead of loading the page again.
  const navigate = useCallback(
    (href: Href, options?: RouterOptions) => {
      // React Aria hands over only links within this site; a string one
      // is a place on the page or a file the server sends, which the
      // browser opens itself.
      if (typeof href === "string") {
        window.location.assign(href);
        return;
      }
      void router.navigate({ ...href, ...options });
    },
    [router],
  );
  // The address the link's <a> shows, e.g. for opening it in a new tab,
  // built as TanStack's own Link builds it.
  const toHref = useCallback(
    (href: Href) =>
      typeof href === "string"
        ? href
        : router.history.createHref(router.buildLocation(href).publicHref),
    [router],
  );

  return (
    // React Aria would otherwise take the browser's language: its own
    // words, e.g. a toast's Close, and its direction, which reverses arrow
    // keys, would not match inbok's English.
    <I18nProvider locale="en-US">
      <RouterProvider navigate={navigate} useHref={toHref}>
        {/* A column the height of the window, so a page or the pending
            view can fill it. */}
        <div className="flex min-h-svh flex-col">
          <Outlet />
        </div>
        <Toaster />
      </RouterProvider>
    </I18nProvider>
  );
}
