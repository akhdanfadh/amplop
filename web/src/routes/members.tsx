import { useEffect, useRef, useState, type ReactElement } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import {
  getGetCurrentSessionQueryKey,
  useGetCurrentSession,
  useListMembers,
} from "@/api/generated/inbok";
import { userMessage } from "@/api/problem";
import type { ApiError } from "@/api/transport";
import { clearAccountCache } from "@/features/auth/session-cache";
import { MemberTable } from "@/features/members/table";
import { AccountNav } from "@/components/account-nav";
import { Button } from "@/components/ui/button";

const unauthorized = 401;
const forbidden = 403;

export const Route = createFileRoute("/members")({
  component: Members,
  head: () => ({ meta: [{ title: "Members - inbok" }] }),
});

function Members(): ReactElement {
  const client = useQueryClient();
  const navigate = useNavigate();
  const headingRef = useRef<HTMLHeadingElement>(null);
  const [writeExpired, setWriteExpired] = useState(false);
  const current = useGetCurrentSession();
  const list = useListMembers();
  const expired =
    writeExpired || [current.error, list.error].some((err) => err?.status === unauthorized);
  // Access can be lost after the page loaded, so a 403 hides the list even
  // with members already read.
  const denied = !expired && list.error?.status === forbidden;
  const account = current.data?.data;
  const members = list.data?.data.members;

  useEffect(() => {
    headingRef.current?.focus();
  }, []);
  useEffect(() => {
    if (!expired) return;
    clearAccountCache(client);
    void navigate({ to: "/sign-in", replace: true });
  }, [expired, client, navigate]);

  // Ends the page on a lost session and rereads access on a 403 from a write.
  function handleAccessError(err: ApiError): void {
    if (err.status === unauthorized) setWriteExpired(true);
    // The table rereads the list after every write, which shows the 403 here.
    if (err.status === forbidden)
      void client.invalidateQueries({ queryKey: getGetCurrentSessionQueryKey() });
  }

  // A failed read without data hides the list; a failed refetch keeps it.
  const readError =
    expired || denied
      ? null
      : (!current.data && current.error) || (!list.data && list.error) || null;
  // A kept list may be older than a change, so say it could not be reread.
  const staleError = !expired && !denied && list.data && list.error;

  return (
    <>
      <AccountNav account={account} wide />
      <main className="mx-auto flex w-full max-w-4xl flex-col gap-8 px-5 py-8 wrap-anywhere sm:px-8 sm:py-12">
        <h1 ref={headingRef} tabIndex={-1} className="text-3xl font-bold tracking-tight">
          Members
        </h1>

        {(current.isPending || list.isPending) && !readError && !denied && !expired && (
          <output>Loading members...</output>
        )}
        {expired && <output>Opening sign-in...</output>}

        {denied && (
          <p>
            The member list is available only to administrators with two-factor authentication.
            {account?.role === "admin" && (
              <>
                {" "}
                <Link
                  to="/security"
                  className="underline underline-offset-4 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid"
                >
                  Set up two-factor authentication
                </Link>
                .
              </>
            )}
          </p>
        )}
        {readError && (
          <div className="flex flex-col gap-3" role="alert">
            <p>Could not load members. {userMessage(readError)}</p>
            {readError.requestId && (
              <p className="text-sm text-muted-foreground">
                Request reference: <code className="break-all">{readError.requestId}</code>
              </p>
            )}
            <Button
              type="button"
              variant="outline"
              className="min-h-11 w-fit border-muted-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid"
              disabled={current.isFetching || list.isFetching}
              onClick={() => {
                void current.refetch();
                void list.refetch();
              }}
            >
              Try again
            </Button>
          </div>
        )}

        {staleError && (
          <div className="flex flex-col gap-3" role="alert">
            <p>
              Could not reload members, so the list may be out of date. {userMessage(staleError)}
            </p>
            <Button
              type="button"
              variant="outline"
              className="min-h-11 w-fit border-muted-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid"
              disabled={list.isFetching}
              onClick={() => void list.refetch()}
            >
              Try again
            </Button>
          </div>
        )}
        {account && members && !expired && !denied && (
          <MemberTable
            members={members}
            yourEmail={account.email}
            fetching={list.isFetching}
            onAccessError={handleAccessError}
          />
        )}
      </main>
    </>
  );
}
