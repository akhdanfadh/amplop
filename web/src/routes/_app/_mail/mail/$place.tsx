import type { ReactElement } from "react";
import { Outlet, createFileRoute, notFound, redirect, useMatch } from "@tanstack/react-router";
import {
  getListConnectedAccountsQueryOptions,
  getListPlacesQueryOptions,
  useListConnectedAccounts,
} from "@/api/generated/inbok";
import { isSessionEnded, sessionEnded } from "@/features/auth/guard";
import { isPlace, placeSearch } from "@/features/mail/address";
import { PlacePage } from "@/features/mail/place-page";
import { labelPath } from "@/features/mail/place-counts";
import { isViewPlace } from "@/features/mail/places";
import { placeName } from "@/features/mail/text";

// A place's mail, /mail/<place>, a view or a label's ID, of one account
// with ?account=<id>: its conversation list beside the reading pane, which
// holds the open conversation or nothing.
export const Route = createFileRoute("/_app/_mail/mail/$place")({
  params: {
    parse: ({ place }) => {
      if (!isPlace(place)) throw notFound();
      return { place };
    },
  },
  validateSearch: placeSearch,
  loaderDeps: ({ search }) => ({ account: search.account }),
  // A label the member doesn't have is Page not found. A read already
  // cached serves; the page reads again as it mounts. An ended session
  // opens sign-in.
  loader: async ({ context: { queryClient }, params: { place }, deps: { account }, location }) => {
    const [places, accounts] = await Promise.all([
      isViewPlace(place)
        ? undefined
        : queryClient.query({ ...getListPlacesQueryOptions(), staleTime: "static" }),
      account === undefined
        ? undefined
        : queryClient.query({ ...getListConnectedAccountsQueryOptions(), staleTime: "static" }),
    ]).catch((err: unknown) => {
      throw isSessionEnded(err) ? sessionEnded(queryClient, location.href) : err;
    });
    // The same address, the conversation in it included, for every
    // account's mail.
    const unfiltered = redirect({ href: location.pathname, replace: true });
    // An account the member no longer has, e.g. in an old link, only
    // filtered: the place shows every account's mail instead.
    if (account !== undefined && !accounts?.data.accounts.some((a) => a.id === account)) {
      throw unfiltered;
    }
    if (isViewPlace(place)) return { name: placeName[place], ownName: placeName[place] };
    const label = places?.data.places.find((p) => p.place === place && p.name !== null);
    if (!label?.name) throw notFound();
    // A label belongs to one account: filtered to another, it shows whole.
    if (account !== undefined && label.accountId !== account) throw unfiltered;
    // A nested label is named with its parents, joined by "/", inbok's
    // separator for every provider's; ownName is its own, e.g. for an
    // empty list's words.
    return { name: labelPath(places?.data.places ?? [], place).join("/"), ownName: label.name };
  },
  component: Place,
  // head also runs for an address that is no place, which shows Page not
  // found and names itself.
  head: ({ loaderData }) => (loaderData ? { meta: [{ title: `${loaderData.name} - inbok` }] } : {}),
});

function Place(): ReactElement {
  const { place } = Route.useParams();
  const { account: accountId } = Route.useSearch();
  const { name, ownName } = Route.useLoaderData();
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
    />
  );
}
