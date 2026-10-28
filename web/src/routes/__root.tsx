import type { ReactNode } from "react";
import { HeadContent, Scripts, createRootRoute } from "@tanstack/react-router";
import appCss from "../styles.css?url";
import { SetupGate } from "@/components/setup-gate";

export const Route = createRootRoute({
  head: () => ({
    meta: [
      { charSet: "utf-8" },
      { name: "viewport", content: "width=device-width, initial-scale=1" },
      { title: "inbok" },
    ],
    // A <link> in the prerendered shell, so the page never shows unstyled.
    links: [{ rel: "stylesheet", href: appCss }],
  }),
  shellComponent: RootDocument,
  notFoundComponent: NotFound,
});

// The HTML document around every route. The build prerenders it once into
// the static shell; the browser fills in the matched route.
function RootDocument({ children }: { children: ReactNode }) {
  return (
    <html lang="en">
      <head>
        <HeadContent />
      </head>
      <body>
        <SetupGate>{children}</SetupGate>
        <Scripts />
      </body>
    </html>
  );
}

// Shown for any URL without a route, e.g. a mistyped path.
function NotFound() {
  return <p className="p-8 text-gray-600">Page not found.</p>;
}
