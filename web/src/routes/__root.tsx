import type { ReactNode } from "react";
import { HeadContent, Scripts, createRootRoute } from "@tanstack/react-router";

export const Route = createRootRoute({
  head: () => ({
    meta: [
      { charSet: "utf-8" },
      { name: "viewport", content: "width=device-width, initial-scale=1" },
      { title: "inbok" },
    ],
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
        {children}
        <Scripts />
      </body>
    </html>
  );
}

// Shown for any URL without a route, e.g. a mistyped path.
function NotFound() {
  return <p>Page not found.</p>;
}
