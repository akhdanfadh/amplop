import { useEffect, useRef, useState, type ReactElement } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import {
  useGetCurrentSession,
  useListAccountProviders,
  useListConnectedAccounts,
} from "@/api/generated/inbok";
import type { ConnectOutcome } from "@/api/generated/model";
import { userMessage } from "@/api/problem";
import { clearAccountCache } from "@/features/auth/session-cache";
import { AccountList } from "@/features/connected-accounts/list";
import { connectSearch } from "@/features/connected-accounts/search";
import { refreshInterval, syncNowRefresh } from "@/features/connected-accounts/sync";
import { outcomeNotice } from "@/features/connected-accounts/text";
import { AccountNav } from "@/components/account-nav";
import { Button } from "@/components/ui/button";

const unauthorized = 401;
const buttonClass =
  "min-h-11 w-fit border-muted-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid";

export const Route = createFileRoute("/connected-accounts")({
  validateSearch: connectSearch,
  component: ConnectedAccounts,
  head: () => ({ meta: [{ title: "Connected accounts - inbok" }] }),
});

function ConnectedAccounts(): ReactElement {
  const client = useQueryClient();
  const navigate = useNavigate();
  const headingRef = useRef<HTMLHeadingElement>(null);
  const { outcome } = Route.useSearch();
  // The outcome Google's return landed with, kept after the address loses it.
  const [landed, setLanded] = useState<ConnectOutcome>();
  const [writeExpired, setWriteExpired] = useState(false);
  const current = useGetCurrentSession();
  // When the fast reads after the last Check now end, in milliseconds since
  // the epoch.
  const [fastUntil, setFastUntil] = useState(0);
  const list = useListConnectedAccounts({
    query: {
      refetchInterval: (query) => refreshInterval(query.state.data?.data, Date.now(), fastUntil),
    },
  });
  const providers = useListAccountProviders();
  const expired =
    writeExpired ||
    [current.error, list.error, providers.error].some((err) => err?.status === unauthorized);
  const session = current.data?.data;
  const accounts = list.data?.data.accounts;
  const offered = providers.data?.data.offered;
  const notice = landed && outcomeNotice(landed);

  useEffect(() => {
    headingRef.current?.focus();
  }, []);
  useEffect(() => {
    if (!outcome) return;
    // 1. Remove the outcome from the address: reloading, or coming back with
    // Back, would otherwise report an old connection as if it had just ended.
    // 2. Then show it, after the first render: a live region that appears
    // with its text is often not announced.
    void navigate({ to: "/connected-accounts", search: {}, replace: true }).then(() =>
      setLanded(outcome),
    );
  }, [outcome, navigate]);
  useEffect(() => {
    if (!expired) return;
    clearAccountCache(client);
    void navigate({ to: "/sign-in", replace: true });
  }, [expired, client, navigate]);

  // A failed read without data hides the list; a failed refetch keeps it.
  // The session only fills in the navigation, so its failure hides nothing.
  const readError = expired
    ? null
    : (!list.data && list.error) || (!providers.data && providers.error) || null;
  // A kept list may be older than a change, so say it could not be reread.
  const staleError = !expired && list.data && list.error;

  return (
    <>
      <AccountNav account={session} />
      <main className="mx-auto flex w-full max-w-lg flex-col gap-8 px-5 py-8 wrap-anywhere sm:px-8 sm:py-12">
        <h1 ref={headingRef} tabIndex={-1} className="text-3xl font-bold tracking-tight">
          Connected accounts
        </h1>

        {notice?.kind === "alert" && <p role="alert">{notice.text}</p>}
        {/* Stay mounted so screen readers announce changed text. */}
        <output className={notice?.kind === "notice" ? undefined : "sr-only"}>
          {notice?.kind === "notice" ? notice.text : ""}
        </output>

        {(list.isPending || providers.isPending) && !readError && !expired && (
          <output>Loading connected accounts...</output>
        )}
        {expired && <output>Opening sign-in...</output>}

        {readError && (
          <div className="flex flex-col gap-3" role="alert">
            <p>Could not load connected accounts. {userMessage(readError)}</p>
            {readError.requestId && (
              <p className="text-sm text-muted-foreground">
                Request reference: <code className="break-all">{readError.requestId}</code>
              </p>
            )}
            <Button
              type="button"
              variant="outline"
              className={buttonClass}
              disabled={list.isFetching || providers.isFetching}
              onClick={() => {
                // Only a read that has nothing to show is tried again.
                if (!list.data) void list.refetch();
                if (!providers.data) void providers.refetch();
              }}
            >
              Try again
            </Button>
          </div>
        )}
        {staleError && (
          <div className="flex flex-col gap-3" role="alert">
            <p>
              Could not reload connected accounts, so the list may be out of date.{" "}
              {userMessage(staleError)}
            </p>
            <Button
              type="button"
              variant="outline"
              className={buttonClass}
              disabled={list.isFetching}
              onClick={() => void list.refetch()}
            >
              Try again
            </Button>
          </div>
        )}

        {accounts && offered && !expired && (
          <AccountList
            accounts={accounts}
            gmailOffered={offered.includes("gmail")}
            syncStopped={list.data?.data.syncStopped ?? false}
            // A refused start rereads the providers, which can remove the
            // button that had focus.
            fetching={list.isFetching || providers.isFetching}
            onWriteStart={() => setLanded(undefined)}
            onSyncStarted={() => setFastUntil(Date.now() + syncNowRefresh)}
            onUnauthorized={() => setWriteExpired(true)}
          />
        )}
      </main>
    </>
  );
}
