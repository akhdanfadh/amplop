import type { ReactNode } from "react";
import { render } from "vitest-browser-react";
import { type QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  Outlet,
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
} from "@tanstack/react-router";
import { testQueryClient } from "./query-client";

// Stub says what stubRouter puts around the component.
type Stub = {
  // The address the router opens at, where the component is the page,
  // "/" when left out. It may carry a search and a fragment, e.g.
  // "/mail/inbox?q=lunch", which the component reads from the router.
  at?: string;
  // The paths of blank pages to leave to, e.g. ["/sign-in"]. Each
  // renders only a level 1 heading holding its path.
  pages?: string[];
  // What goes around every page, e.g. the signed-in frame; it gets the
  // page to place. Left out, a page is rendered alone.
  layout?: (page: ReactNode) => ReactNode;
  // The query cache; left out, a new testQueryClient().
  client?: QueryClient;
};

// stubRouter renders component for a browser test as the page of a
// made-up router, for a component that needs a router around it: it has a
// link, navigates, or warns before leaving. It returns, once the router
// has loaded, the router (to navigate with and to read the location
// from), the query client the component reads, and the rendered screen.
//
//	// A component with a link.
//	await stubRouter(<SignInForm onDone={onDone} />);
//	// A component with a page to leave to.
//	const { router } = await stubRouter(<ComposeForm />, { pages: ["/elsewhere"] });
//	await router.navigate({ href: "/elsewhere" });
//	await expect.element(page.getByRole("heading", { name: "/elsewhere" })).toBeVisible();
//	// A layout around the pages.
//	await stubRouter(<Inbox />, {
//	  pages: ["/sign-in"],
//	  layout: (page) => <Frame label="Mail" nav={null}>{page}</Frame>,
//	});
//	// Something above every page, opened at one of the blank pages.
//	await stubRouter(null, {
//	  at: "/settings/security",
//	  pages: ["/", "/settings/security"],
//	  layout: (page) => <><SetupReminder />{page}</>,
//	});
//	// A component that reads the search, with a client of the test's own.
//	const { router } = await stubRouter(<Reader />, { at: "/mail/inbox?q=lunch", client });
//	expect(router.state.location.search).toEqual({ q: "lunch" });
//
// A null component leaves the page at the address blank, like the other
// pages. A page's path may hold parameters, e.g. "/mail/$place". An
// address no page has renders the router's own "Not Found", and the
// location still says where the component went.
//
// DESIGN: the router is scaffolding, with nothing of the app's routes: no
// guard, loader, search rules, or root component. A test of a page with
// those opens the app's own router instead (appRouter).
export async function stubRouter(
  component: ReactNode,
  { at = "/", pages = [], layout, client = testQueryClient() }: Stub = {},
) {
  // The route's path is the address without its search and fragment.
  const [own] = at.split(/[?#]/);
  const root = createRootRoute({
    component: () => (layout ? layout(<Outlet />) : <Outlet />),
  });
  // A Set, since a test may list the address among the pages.
  const paths = [...new Set([own, ...pages])];
  const routes = paths.map((path) =>
    createRoute({
      getParentRoute: () => root,
      path,
      component: () => (path === own && component !== null ? component : <h1>{path}</h1>),
    }),
  );
  const router = createRouter({
    routeTree: root.addChildren(routes),
    history: createMemoryHistory({ initialEntries: [at] }),
  });
  // Loaded first, so the first render already shows the page.
  await router.load();
  const screen = await render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  return { router, client, screen };
}
