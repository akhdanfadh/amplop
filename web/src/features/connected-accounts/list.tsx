import { useEffect, useRef, useState, type ReactElement } from "react";
import { useQueryClient } from "@tanstack/react-query";
import {
  getListAccountProvidersQueryKey,
  getListConnectedAccountsQueryKey,
  useConnectGmail,
  useDisconnectConnectedAccount,
  useReconnectConnectedAccount,
} from "@/api/generated/inbok";
import type {
  ConnectedAccount,
  ResponseAuthorization,
  ResponseConnectedAccounts,
  ResponseDisconnectedAccount,
} from "@/api/generated/model";
import type { ApiError } from "@/api/transport";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { disconnectedMessage, disconnectErrorMessage, formatDate, startErrorMessage } from "./text";

const unauthorized = 401;
const notFound = 404;
const conflict = 409;
const focusClass =
  "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid";
const buttonClass = `h-auto min-h-11 w-fit whitespace-normal ${focusClass}`;
const outlineClass = `${buttonClass} border-muted-foreground`;
const connectId = "connect-gmail";

// AccountList lists the member's connected accounts, connects Gmail,
// reconnects an account that needs it, and disconnects one. accounts is the
// server's list, oldest first; gmailOffered says whether the instance has a
// Google client, without which only disconnecting is offered. fetching marks
// the list as being reread. onWriteStart runs as a write begins, so the page
// can drop an older message; onUnauthorized runs on a write's 401.
export function AccountList({
  accounts,
  gmailOffered,
  fetching,
  onWriteStart,
  onUnauthorized,
}: {
  accounts: ConnectedAccount[];
  gmailOffered: boolean;
  fetching: boolean;
  onWriteStart: () => void;
  onUnauthorized: () => void;
}): ReactElement {
  const client = useQueryClient();
  const headingRef = useRef<HTMLHeadingElement>(null);
  // The element a write started from; focus returns to it, or to the
  // heading when the write removed it.
  const restoreFocus = useRef<string | null>(null);
  // The account the dialog asks about, kept while it closes so its text stays.
  const [confirming, setConfirming] = useState<ConnectedAccount | null>(null);
  const [dialogOpen, setDialogOpen] = useState(false);
  // Set by the dialog's confirm button. The disconnect starts once the dialog
  // has closed: started earlier, the dialog would give focus back to a button
  // the write has disabled or the reread has removed.
  const confirmed = useRef(false);
  const [disconnected, setDisconnected] = useState("");
  // openGoogle goes with each mutate call rather than here: the hook's own
  // callbacks still run after the page has closed, and would take the member
  // to Google from wherever they went.
  const startOptions = { mutation: { gcTime: 0, onError: handleStartError } };
  const connect = useConnectGmail(startOptions);
  const reconnect = useReconnectConnectedAccount(startOptions);
  const disconnect = useDisconnectConnectedAccount({
    mutation: {
      gcTime: 0,
      onMutate: prepareDisconnect,
      onSuccess: removeDisconnected,
      onError: (err: ApiError) => {
        if (err.status === unauthorized) onUnauthorized();
      },
      onSettled: rereadAccounts,
    },
  });
  // A successful start keeps the page locked while the browser leaves for Google.
  const leaving = connect.isSuccess || reconnect.isSuccess;
  const pending = connect.isPending || reconnect.isPending || disconnect.isPending || leaving;
  const startError = connect.error ?? reconnect.error;
  const notice = disconnect.isSuccess ? disconnectedMessage(disconnected) : "";
  const failure =
    startError && startError.status !== unauthorized
      ? startErrorMessage(startError)
      : disconnect.error && disconnect.error.status !== unauthorized
        ? disconnectErrorMessage(disconnect.error, disconnected)
        : "";
  const failedRequest = startError ?? disconnect.error;

  useEffect(() => {
    // A disabled button loses focus, so give it back once the list is reread.
    const id = restoreFocus.current;
    if (id === null || pending || fetching) return;
    restoreFocus.current = null;
    if (document.activeElement !== document.body) return;
    (document.getElementById(id) ?? headingRef.current)?.focus();
  }, [pending, fetching]);

  const resetConnect = connect.reset;
  const resetReconnect = reconnect.reset;
  useEffect(() => {
    // Back from Google can restore this page from the browser's memory as it
    // was left: locked, waiting for Google's page, with the list it had then.
    function unlock(event: PageTransitionEvent): void {
      if (!event.persisted) return;
      resetConnect();
      resetReconnect();
      void client.invalidateQueries({ queryKey: getListConnectedAccountsQueryKey() });
    }
    window.addEventListener("pageshow", unlock);
    return () => window.removeEventListener("pageshow", unlock);
  }, [resetConnect, resetReconnect, client]);

  // handleStartError ends the page on a 401 and rereads what a refusal
  // shows has changed.
  function handleStartError(err: ApiError): void {
    if (err.status === unauthorized) onUnauthorized();
    // Gmail was offered when the page loaded; an administrator has removed it since.
    if (err.status === conflict)
      void client.invalidateQueries({ queryKey: getListAccountProvidersQueryKey() });
    // The account was disconnected elsewhere.
    if (err.status === notFound)
      void client.invalidateQueries({ queryKey: getListConnectedAccountsQueryKey() });
  }

  // prepareDisconnect stops a list read, so it cannot overwrite the list
  // removeDisconnected writes.
  async function prepareDisconnect(): Promise<void> {
    await client.cancelQueries({ queryKey: getListConnectedAccountsQueryKey(), exact: true });
  }

  // removeDisconnected takes the account in result out of the cached list.
  function removeDisconnected(result: ResponseDisconnectedAccount): void {
    const removed = result.data.account.id;
    // The row goes at once, even if the reread below fails.
    client.setQueryData<ResponseConnectedAccounts>(getListConnectedAccountsQueryKey(), (list) =>
      list
        ? {
            ...list,
            data: { ...list.data, accounts: list.data.accounts.filter((a) => a.id !== removed) },
          }
        : list,
    );
  }

  // rereadAccounts rereads the list after any disconnect outcome.
  function rereadAccounts(): void {
    // Every outcome, including a lost response, is settled by the server's list.
    void client.invalidateQueries({ queryKey: getListConnectedAccountsQueryKey() });
  }

  // begin clears every earlier write's result before a write from the
  // element with focusId.
  function begin(focusId: string): void {
    connect.reset();
    reconnect.reset();
    disconnect.reset();
    restoreFocus.current = focusId;
    onWriteStart();
  }

  function startConnect(): void {
    if (pending) return;
    begin(connectId);
    connect.mutate(undefined, { onSuccess: openGoogle });
  }

  function startReconnect(account: ConnectedAccount): void {
    if (pending) return;
    begin(reconnectId(account.id));
    reconnect.mutate({ id: account.id }, { onSuccess: openGoogle });
  }

  function startDisconnect(account: ConnectedAccount): void {
    if (pending) return;
    begin(disconnectId(account.id));
    setDisconnected(account.email);
    disconnect.mutate({ id: account.id });
  }

  return (
    <section className="flex flex-col gap-6" aria-labelledby="accounts-heading">
      <h2 id="accounts-heading" ref={headingRef} tabIndex={-1} className="text-xl font-semibold">
        Gmail accounts
      </h2>
      {/* Stay mounted so screen readers announce changed text. */}
      <output className={notice ? undefined : "sr-only"}>{notice}</output>
      {failure && <p role="alert">{failure}</p>}
      {failure && failedRequest?.requestId && (
        <p className="text-sm text-muted-foreground">
          Request reference: <code className="break-all">{failedRequest.requestId}</code>
        </p>
      )}
      {!gmailOffered && (
        <p>
          Gmail isn&apos;t set up on this inbok, so accounts can&apos;t be connected or reconnected.
          Ask an administrator to set up a Google client.
        </p>
      )}
      {accounts.length === 0 ? (
        <p>No Gmail account is connected yet.</p>
      ) : (
        <ul className="flex flex-col divide-y border-y" aria-busy={fetching || pending}>
          {accounts.map((account) => (
            <AccountRow
              key={account.id}
              account={account}
              gmailOffered={gmailOffered}
              reconnecting={
                (reconnect.isPending || reconnect.isSuccess) &&
                reconnect.variables?.id === account.id
              }
              disconnecting={disconnect.isPending && disconnect.variables?.id === account.id}
              disabled={pending}
              onReconnect={() => startReconnect(account)}
              onDisconnect={() => {
                confirmed.current = false;
                setConfirming(account);
                setDialogOpen(true);
              }}
            />
          ))}
        </ul>
      )}
      {gmailOffered && (
        <Button
          id={connectId}
          type="button"
          className={buttonClass}
          disabled={pending}
          onClick={startConnect}
        >
          {connect.isPending || connect.isSuccess ? "Opening Google..." : "Connect Gmail"}
        </Button>
      )}
      <AlertDialog
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        onOpenChangeComplete={(open) => {
          if (open || !confirmed.current || !confirming) return;
          confirmed.current = false;
          startDisconnect(confirming);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle className="wrap-anywhere">
              Disconnect <span translate="no">{confirming?.email}</span>?
            </AlertDialogTitle>
            <AlertDialogDescription>
              This removes the account from inbok, with everything inbok keeps for it. inbok also
              asks Google to end its access, unless another member here connected the same Google
              account. Your mail in Gmail isn&apos;t changed.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel className={`min-h-11 border-muted-foreground ${focusClass}`}>
              Cancel
            </AlertDialogCancel>
            <AlertDialogAction
              type="button"
              variant="destructive"
              className={`min-h-11 ${focusClass}`}
              disabled={pending}
              onClick={() => {
                confirmed.current = true;
                setDialogOpen(false);
              }}
            >
              Disconnect
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </section>
  );
}

function AccountRow({
  account,
  gmailOffered,
  reconnecting,
  disconnecting,
  disabled,
  onReconnect,
  onDisconnect,
}: {
  account: ConnectedAccount;
  gmailOffered: boolean;
  reconnecting: boolean;
  disconnecting: boolean;
  disabled: boolean;
  onReconnect: () => void;
  onDisconnect: () => void;
}): ReactElement {
  const detailsId = `account-${account.id}`;
  const needsReconnect = account.needsReconnectSince !== null;

  return (
    <li className="flex flex-wrap items-center justify-between gap-x-4 gap-y-3 py-4">
      <div className="flex min-w-0 flex-col gap-1">
        <p className="flex flex-wrap items-center gap-2 font-medium">
          <span translate="no">{account.email}</span>
          {needsReconnect && <Badge variant="destructive">Needs reconnecting</Badge>}
        </p>
        <p id={detailsId} className="text-sm text-muted-foreground">
          {account.needsReconnectSince !== null ? (
            <>
              Google stopped accepting inbok&apos;s access on{" "}
              <time dateTime={account.needsReconnectSince} className="whitespace-nowrap">
                {formatDate(account.needsReconnectSince)}
              </time>
              .{gmailOffered && " Reconnect and choose this same Google account."}
            </>
          ) : (
            <>
              Connected on{" "}
              <time dateTime={account.createdAt} className="whitespace-nowrap">
                {formatDate(account.createdAt)}
              </time>
              .
            </>
          )}
        </p>
      </div>
      <div className="flex flex-wrap gap-3">
        {/* Only Google's refusal marks an account. One whose saved
            permission inbok can't read is not marked, since restoring the
            key fixes it without the member. */}
        {gmailOffered && needsReconnect && (
          <Button
            id={reconnectId(account.id)}
            type="button"
            className={buttonClass}
            aria-describedby={detailsId}
            disabled={disabled}
            onClick={onReconnect}
          >
            {reconnecting ? "Opening Google..." : "Reconnect"}
            <span className="sr-only"> {account.email}</span>
          </Button>
        )}
        <Button
          id={disconnectId(account.id)}
          type="button"
          variant="outline"
          className={outlineClass}
          aria-describedby={detailsId}
          disabled={disabled}
          onClick={onDisconnect}
        >
          {disconnecting ? "Disconnecting..." : "Disconnect"}
          <span className="sr-only"> {account.email}</span>
        </Button>
      </div>
    </li>
  );
}

// openGoogle sends this tab to Google's consent page from result, which
// returns to this page with the outcome.
function openGoogle(result: ResponseAuthorization): void {
  // The address comes from inbok's own server, which also serves this app,
  // so it is trusted as the app's code is.
  window.location.assign(result.data.url);
}

// reconnectId returns the id of the Reconnect button for accountId.
function reconnectId(accountId: number): string {
  return `reconnect-${accountId}`;
}

// disconnectId returns the id of the Disconnect button for accountId.
function disconnectId(accountId: number): string {
  return `disconnect-${accountId}`;
}
