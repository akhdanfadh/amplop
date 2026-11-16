import { useEffect } from "react";
import {
  linkOptions,
  useRouter,
  useRouterState,
  type MakeRouteMatchUnion,
  type RegisteredRouter,
} from "@tanstack/react-router";
import type { LinkProps } from "react-aria-components";

// The views the sidebar lists, in its order (DESIGN.md "Sidebar"); each
// is a place in the address, /mail/<place>.
export const viewPlaces = ["inbox", "starred", "sent", "all", "spam", "trash"] as const;
export type ViewPlace = (typeof viewPlaces)[number];

// Place is where a list's conversations are, as the address and the API
// name it: a view, or a label's ID.
export type Place = ViewPlace | (string & {});

// The address's place for a search's results, /mail/search: they have no
// place of their own; the places searched are in the address's search
// parameters (searchFilters).
export const searchPlace = "search";

// isViewPlace reports whether place, from the address, names a view.
export function isViewPlace(place: string): place is ViewPlace {
  return (viewPlaces as readonly string[]).includes(place);
}

// A link React Aria opens in the app, e.g. a sidebar item's.
export type AppHref = Exclude<NonNullable<LinkProps["href"]>, string>;

// The page Back to mail opens until the member opens one in mail in this
// tab, and again after the member changes.
const inbox: AppHref = linkOptions({ to: "/mail/$place", params: { place: "inbox" } });

// The last page in mail, which settings' Back to mail returns to.
let lastMailHref = inbox;

// mailPage returns the page matches show at pathname as a link, if it is a
// page in mail that has fully loaded, else undefined: the mail frame still
// draws once the address has moved on, e.g. to settings, and a page still
// loading or not found is no place to return to.
export function mailPage(
  router: RegisteredRouter,
  matches: readonly MakeRouteMatchUnion[],
  pathname: string,
): AppHref | undefined {
  const last = matches.at(-1);
  if (!last?.routeId.startsWith("/_app/_mail/")) return undefined;
  if (!matches.every((match) => match.status === "success")) return undefined;
  // An address deeper than any route, e.g. /mail/inbox/nope, matches its
  // nearest route successfully and shows Page not found there.
  if (withoutEndSlash(last.pathname) !== withoutEndSlash(pathname)) return undefined;
  return { to: router.routesById[last.routeId].to, params: last.params, search: last.search };
}

// useRememberMailPage keeps the page the mail frame shows for Back to
// mail, as mailPage reads it.
export function useRememberMailPage(): void {
  const router = useRouter();
  const page = useRouterState({
    select: (state) => mailPage(router, state.matches, state.location.pathname),
  });
  useEffect(() => {
    if (page) rememberMailHref(page);
  }, [page]);
}

// withoutEndSlash returns path without a closing slash, which an index
// route's path has and the address may lack.
function withoutEndSlash(path: string): string {
  return path.length > 1 && path.endsWith("/") ? path.slice(0, -1) : path;
}

// rememberMailHref records href for Back to mail.
export function rememberMailHref(href: AppHref): void {
  lastMailHref = href;
}

// forgetMailHref sends Back to mail to the Inbox again, e.g. when the
// member signs out, so the next member never gets the last one's page.
export function forgetMailHref(): void {
  lastMailHref = inbox;
}

// mailHref returns the page Back to mail opens.
export function mailHref(): AppHref {
  return lastMailHref;
}
