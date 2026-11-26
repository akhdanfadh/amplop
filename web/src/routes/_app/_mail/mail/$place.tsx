import { useEffect, useMemo, type ReactElement } from "react";
import {
  Outlet,
  createFileRoute,
  notFound,
  redirect,
  useMatch,
  useRouter,
  type NotFoundRouteProps,
} from "@tanstack/react-router";
import { cachedRead } from "@/api/cached-read";
import {
  getListConnectedAccountsQueryOptions,
  getListPlacesQueryOptions,
  useListConnectedAccounts,
} from "@/api/generated/amplop";
import { isPlace } from "@/features/mail/address";
import { LabelGone } from "@/features/mail/label-gone";
import { PlacePage } from "@/features/mail/place-page";
import { labelPath } from "@/features/mail/place-counts";
import { mailData } from "@/features/mail/conversations";
import { useQueryClient } from "@tanstack/react-query";
import { useLiveQuery } from "@tanstack/react-db";
import { isViewPlace, searchPlace } from "@/features/mail/places";
import { mailSearch } from "@/features/mail/search";
import { placeName, resultsHeading } from "@/features/mail/text";
import { NotFound } from "@/components/route-states";

// What a label's address that names no label of the member's throws, so
// its page says the label is gone rather than that the address is wrong.
const labelGone = "label-gone";

// A place's mail, /mail/<place>, a view or a label's ID, of one account
// with ?account=<id>: its conversation list beside the reading pane, which
// holds the open conversation or nothing. A label the member doesn't have,
// e.g. deleted in Gmail or by an old link, says so (LabelGone). /mail/search is a search's
// results in place of a list, the search in the address's search
// parameters (mailSearch).
//
// DESIGN: a search is this route's place, not a route of its own, so the
// search box, which the place's page draws, stays the same box as typing
// moves the address from a place to its results, and keeps focus.
export const Route = createFileRoute("/_app/_mail/mail/$place")({
  params: {
    parse: ({ place }) => {
      if (place !== searchPlace && !isPlace(place)) throw notFound();
      return { place };
    },
  },
  validateSearch: mailSearch,
  loaderDeps: ({ search }) => ({ account: search.account }),
  // A label the member doesn't have is gone (LabelGone); one they have opens
  // in its account. A read already cached serves unless a write marked it
  // out of date (cachedRead); the page reads again as it mounts. A failure,
  // an ended session's 401 included, goes to the route's error view.
  loader: async ({ context: { queryClient }, params: { place }, deps: { account }, location }) => {
    const search = place === searchPlace;
    const view = isViewPlace(place);
    const [places, accounts] = await Promise.all([
      // A search names labels in its filters, as a label's list does.
      view ? undefined : cachedRead(queryClient, getListPlacesQueryOptions()),
      // Only a view's or a search's filter is checked against the accounts.
      (!view && !search) || account === undefined
        ? undefined
        : cachedRead(queryClient, getListConnectedAccountsQueryOptions()),
    ]);
    if (view || search) {
      // An account the member no longer has, e.g. in an old link, only
      // filtered: the view, or the search, covers every account's mail
      // instead, at the same address, the conversation in it included.
      if (account !== undefined && !accounts?.data.accounts.some((a) => a.id === account)) {
        throw redirect({
          to: location.pathname,
          search: (was: Record<string, unknown>) => ({ ...was, account: undefined }),
          replace: true,
        });
      }
      const name = view ? placeName[place] : resultsHeading;
      return { name, ownName: name };
    }
    const label = places?.data.places.find((p) => p.place === place && p.name !== null);
    if (!label?.name) throw notFound({ data: labelGone });
    // A label is one account's and opens in it, so the sidebar marks that
    // account: without its filter, e.g. an old link, or with another's,
    // the address takes the label's account (DESIGN.md "Sidebar").
    if (account !== label.accountId) {
      throw redirect({ href: `${location.pathname}?account=${label.accountId}`, replace: true });
    }
    // A nested label is named with its parents, joined by "/", amplop's
    // separator for every provider's; ownName is its own, e.g. for an
    // empty list's words.
    return { name: labelPath(places?.data.places ?? [], place).join("/"), ownName: label.name };
  },
  component: Place,
  notFoundComponent: PlaceNotFound,
  // head also runs for an address that is no place, which shows Page not
  // found and names itself.
  head: ({ loaderData }) =>
    loaderData ? { meta: [{ title: `${loaderData.name} - amplop` }] } : {},
});

function Place(): ReactElement {
  const { place } = Route.useParams();
  // Only the search's own keys: the router hands this route its child's
  // too, e.g. an open result's message, which is no part of the search.
  const {
    account: accountId,
    q,
    place: places,
    from,
    date,
    since,
    before,
    has,
  } = Route.useSearch();
  const filters = useMemo(
    () => ({ q, place: places, from, date, since, before, has }),
    [q, places, from, date, since, before, has],
  );
  const { name, ownName } = Route.useLoaderData();
  useFollowLabel(place, name);
  const accounts = useListConnectedAccounts();
  const account = accounts.data?.data.accounts.find((a) => a.id === accountId);
  // The reading pane's index shows when no conversation is open.
  const nothingOpen = useMatch({ from: "/_app/_mail/mail/$place/", shouldThrow: false });
  const open = useMatch({ from: "/_app/_mail/mail/$place/$conversationId", shouldThrow: false });

  return (
    <PlacePage
      place={place}
      name={name}
      ownName={ownName}
      account={account}
      openId={open?.params.conversationId}
      pane={nothingOpen ? "list" : "reader"}
      reader={<Outlet />}
      search={place === searchPlace ? filters : undefined}
    />
  );
}

// PlaceNotFound is a place's page that names nothing the member has: a
// label gone, or an address that is no place.
function PlaceNotFound({ data }: NotFoundRouteProps): ReactElement {
  return data === labelGone ? <LabelGone /> : <NotFound />;
}

// useFollowLabel keeps the open label's page, place's, named name by its
// loader, as the member's labels now are: once a rename or move changes
// its name with its parents', or it leaves them, e.g. deleted in Gmail, the
// loader runs again, renaming the page, or saying the label is gone. A
// view's or a search's page has no label to follow.
function useFollowLabel(place: string, name: string): void {
  const router = useRouter();
  const client = useQueryClient();
  const { data: counts, isReady } = useLiveQuery(mailData(client).placeCounts);
  const label = place !== searchPlace && !isViewPlace(place);
  // Not before the places are read; once read, none at all, e.g. the last
  // account disconnected, is the label gone too.
  const now = label && isReady ? labelPath(counts, place).join("/") : name;
  useEffect(() => {
    if (now === name) return;
    void router.invalidate({ filter: (match) => match.routeId === Route.id });
  }, [now, name, router]);
}
