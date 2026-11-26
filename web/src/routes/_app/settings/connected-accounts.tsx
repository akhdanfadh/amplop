import { useEffect, useState, type ReactElement } from "react";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import type { QueryClient } from "@tanstack/react-query";
import {
  getListAccountProvidersQueryOptions,
  getListConnectedAccountsQueryOptions,
  useListAccountProviders,
  useListConnectedAccounts,
} from "@/api/generated/amplop";
import { cachedRead, loadedFreshMs } from "@/api/cached-read";
import { userMessage } from "@/api/problem";
import { ReadError } from "@/components/read-error";
import { isSessionEnded } from "@/features/auth/guard";
import { AccountList } from "@/features/connected-accounts/list";
import { connectSearch } from "@/features/connected-accounts/search";
import { refreshInterval, syncNowRefresh } from "@/features/connected-accounts/sync";
import {
  listReadFailed,
  outcomeNotice,
  pageLead,
  type PageNotice,
} from "@/features/connected-accounts/text";
import { SettingsPage } from "@/features/settings/page";
import { pageName } from "@/features/settings/text";

const unauthorized = 401;

// connectedAccountsLoader reads what the page shows before it draws: the
// accounts and the providers they can be connected with, side by side.
//
// DESIGN: both reads finish before either failure counts, and an ended
// session wins: Promise.all would report only the first failure, so a 401
// answered after the other read's fault would show the failure view
// instead of the signed-out one.
export async function connectedAccountsLoader({
  context,
}: {
  context: { queryClient: QueryClient };
}) {
  const client = context.queryClient;
  const [list, providers] = await Promise.allSettled([
    cachedRead(client, getListConnectedAccountsQueryOptions()),
    cachedRead(client, getListAccountProvidersQueryOptions()),
  ]);
  if (list.status === "fulfilled" && providers.status === "fulfilled")
    return [list.value, providers.value] as const;
  const failures = [list, providers].flatMap((read) =>
    read.status === "rejected" ? [read.reason as unknown] : [],
  );
  throw failures.find(isSessionEnded) ?? failures[0];
}

export const Route = createFileRoute("/_app/settings/connected-accounts")({
  validateSearch: connectSearch,
  loader: connectedAccountsLoader,
  component: ConnectedAccounts,
  head: () => ({ meta: [{ title: "Connected accounts - amplop" }] }),
});

// ConnectedAccounts lists the member's Gmail accounts, connects one through
// Google, and renames, checks, reconnects, and disconnects each. Google's
// return lands here with how it ended, which the notice says.
function ConnectedAccounts(): ReactElement {
  const navigate = useNavigate();
  const { session } = Route.useRouteContext();
  const { outcome } = Route.useSearch();
  const [loadedList, loadedProviders] = Route.useLoaderData();
  // What happened last, under the lead, until the next action.
  const [notice, setNotice] = useState<PageNotice>();
  // When the fast reads after the last Check now end, in milliseconds since
  // the epoch.
  const [fastUntil, setFastUntil] = useState(0);
  const list = useListConnectedAccounts({
    query: {
      staleTime: loadedFreshMs,
      refetchInterval: (query) => refreshInterval(query.state.data?.data, Date.now(), fastUntil),
    },
  });
  const providers = useListAccountProviders({ query: { staleTime: loadedFreshMs } });
  const accounts = (list.data ?? loadedList).data.accounts;
  // A failed refresh of either read, a 401 aside, which the session-end
  // rule answers (watchSessionEnd).
  const refreshFailed = [list.error, providers.error].find(
    (err) => err && err.status !== unauthorized,
  );
  const offered = (providers.data ?? loadedProviders).data.offered.includes("gmail");

  useEffect(() => {
    if (!outcome) return;
    // 1. Remove the outcome from the address: reloading, or coming back with
    // Back, would otherwise report an old connection as if it had just ended.
    // 2. Then show it: the page fills its notice a frame after it draws, so
    // a screen reader announces it.
    void navigate({ to: "/settings/connected-accounts", search: {}, replace: true }).then(() =>
      setNotice(outcomeNotice(outcome)),
    );
  }, [outcome, navigate]);

  return (
    <SettingsPage
      title={pageName.connectedAccounts}
      lead={pageLead}
      notice={notice?.text}
      noticeTone={notice?.tone}
    >
      {/* A failed refresh keeps the page and says why (DESIGN.md
          "States"); a 401 is the session-end rule's. Try again rereads what
          failed. */}
      {refreshFailed && (
        <div className="py-6 text-sm">
          <ReadError
            message={`${listReadFailed} ${userMessage(refreshFailed)}`}
            error={refreshFailed}
            retrying={list.isFetching || providers.isFetching}
            onRetry={() => {
              if (list.error) void list.refetch();
              if (providers.error) void providers.refetch();
            }}
          />
        </div>
      )}
      <AccountList
        accounts={accounts}
        offered={offered}
        admin={session.role === "admin"}
        onNotice={setNotice}
        onSyncStarted={() => setFastUntil(Date.now() + syncNowRefresh)}
      />
    </SettingsPage>
  );
}
