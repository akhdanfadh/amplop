import { RouterProvider } from "@tanstack/react-router";
import { afterEach, expect, it } from "vite-plus/test";
import { page } from "vite-plus/test/browser/context";
import { cleanup, render } from "vitest-browser-react";
import { NotFound, RoutePending } from "@/components/route-states";
import { appRouter } from "@/test/app-router";
import { fakeServer } from "@/test/server";

afterEach(async () => {
  await cleanup();
});

// fiberKeys returns the keys React gives an element it renders, e.g.
// "__reactFiber$x1y2", a private name with a fixed prefix.
function fiberKeys(element: Element): string[] {
  return Object.keys(element).filter((key) => key.startsWith("__reactFiber$"));
}

// If React owned the page's <body>, a later event would hang the page
// instead of failing (app-router.ts says why), so this checks it without
// sending one.
it("leaves the page's <html> and <body> to the page", async () => {
  const router = await appRouter("/sign-in");
  await render(<RouterProvider router={router} />);
  const heading = page.getByRole("heading", { level: 1, name: "Sign in to inbok" });
  await expect.element(heading).toBeVisible();

  // The page's own heading carries the key, so a renamed key fails here
  // rather than passing below.
  expect(fiberKeys(heading.element())).toHaveLength(1);
  for (const element of [document.documentElement, document.head, document.body])
    expect(fiberKeys(element)).toEqual([]);
});

it("is the app's router, with its states, but tries no failed read again", async () => {
  let prepared: unknown;
  const router = await appRouter("/sign-in", fakeServer(), (queryClient) => {
    prepared = queryClient;
  });
  const { queryClient } = router.options.context;

  expect(router.options).toMatchObject({
    defaultPendingComponent: RoutePending,
    defaultNotFoundComponent: NotFound,
  });
  expect(queryClient.getDefaultOptions().queries?.retry).toBe(false);
  expect(prepared).toBe(queryClient);
});
