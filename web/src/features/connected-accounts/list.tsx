import { useEffect, useRef, useState, type ReactElement } from "react";
import { TriangleAlertIcon } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import {
  getListAccountProvidersQueryKey,
  getListConnectedAccountsQueryKey,
  useConnectGmail,
  useDisconnectConnectedAccount,
  useReconnectConnectedAccount,
  useSyncConnectedAccount,
} from "@/api/generated/inbok";
import type {
  ListedAccount,
  ResponseAccountSync,
  ResponseAuthorization,
  ResponseConnectedAccounts,
  ResponseDisconnectedAccount,
} from "@/api/generated/model";
import type { ApiError } from "@/api/transport";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { StatusBadge } from "@/components/status-badge";
import { Button } from "@/components/ui/button";
import { syncView, type SyncView } from "./sync";
import {
  checkedAtText,
  checkingText,
  checkNowText,
  disconnectedMessage,
  disconnectErrorMessage,
  failureWords,
  formatDate,
  formatMoment,
  runningText,
  startErrorMessage,
  syncErrorMessage,
  syncStartedMessage,
  syncStoppedText,
  syncWaitingText,
  tryAgainText,
  upToDateAsOfText,
  upToDateText,
} from "./text";

const unauthorized = 401;
const notFound = 404;
const conflict = 409;
const buttonClass = "w-fit whitespace-normal";
const connectId = "connect-gmail";

// AccountList lists the member's connected accounts with where each one's
// sync stands, connects Gmail, reconnects an account that needs it, starts
// an account's sync, and disconnects one. accounts is the server's list,
// oldest first; gmailOffered says whether the instance has a Google client,
// without which only disconnecting is offered, and syncStopped whether
// syncing has stopped on the instance, which a banner says.
// fetching marks the list as being reread. onWriteStart runs as a write
// begins, so the page can drop an older message; onSyncStarted runs when
// the server accepts a Check now; onUnauthorized runs on a write's 401.
export function AccountList({
  accounts,
  gmailOffered,
  syncStopped,
  fetching,
  onWriteStart,
  onSyncStarted,
  onUnauthorized,
}: {
  accounts: ListedAccount[];
  gmailOffered: boolean;
  syncStopped: boolean;
  fetching: boolean;
  onWriteStart: () => void;
  onSyncStarted: () => void;
  onUnauthorized: () => void;
}): ReactElement {
  const client = useQueryClient();
  const headingRef = useRef<HTMLHeadingElement>(null);
  // The element a write started from; focus returns to it, or to the
  // heading when the write removed it.
  const restoreFocus = useRef<string | null>(null);
  // The account the dialog asks about, kept while it closes so its text stays.
  const [confirming, setConfirming] = useState<ListedAccount | null>(null);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [disconnected, setDisconnected] = useState("");
  // The email of the account the last Check now named.
  const [syncEmail, setSyncEmail] = useState("");
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
  const sync = useSyncConnectedAccount({
    mutation: {
      gcTime: 0,
      onSuccess: saveSync,
      onError: (err: ApiError) => {
        if (err.status === unauthorized) onUnauthorized();
      },
      onSettled: rereadAccounts,
    },
  });
  // A successful start keeps the page locked while the browser leaves for Google.
  const leaving = connect.isSuccess || reconnect.isSuccess;
  const pending =
    connect.isPending || reconnect.isPending || disconnect.isPending || sync.isPending || leaving;
  const startError = connect.error ?? reconnect.error;
  const notice = disconnect.isSuccess
    ? disconnectedMessage(disconnected)
    : sync.isSuccess
      ? syncStartedMessage(syncEmail)
      : "";
  const failure =
    startError && startError.status !== unauthorized
      ? startErrorMessage(startError)
      : disconnect.error && disconnect.error.status !== unauthorized
        ? disconnectErrorMessage(disconnect.error, disconnected)
        : sync.error && sync.error.status !== unauthorized
          ? syncErrorMessage(sync.error, syncEmail)
          : "";
  const failedRequest = startError ?? disconnect.error ?? sync.error;

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

  // saveSync puts the sync state in result into the cached list, and has
  // the page read the list often while the check runs.
  function saveSync(result: ResponseAccountSync, { id }: { id: number }): void {
    onSyncStarted();
    client.setQueryData<ResponseConnectedAccounts>(getListConnectedAccountsQueryKey(), (list) =>
      list
        ? {
            ...list,
            data: {
              ...list.data,
              accounts: list.data.accounts.map((a) =>
                a.id === id ? { ...a, sync: result.data } : a,
              ),
            },
          }
        : list,
    );
  }

  // rereadAccounts rereads the list after any disconnect or sync outcome.
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
    sync.reset();
    restoreFocus.current = focusId;
    onWriteStart();
  }

  function startConnect(): void {
    if (pending) return;
    begin(connectId);
    connect.mutate(undefined, { onSuccess: openGoogle });
  }

  function startSync(account: ListedAccount): void {
    if (pending) return;
    begin(syncId(account.id));
    setSyncEmail(account.email);
    sync.mutate({ id: account.id });
  }

  function startReconnect(account: ListedAccount): void {
    if (pending) return;
    begin(reconnectId(account.id));
    reconnect.mutate({ id: account.id }, { onSuccess: openGoogle });
  }

  function startDisconnect(account: ListedAccount): void {
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
      {/* Stays mounted so a stop found by a later read is announced. */}
      <output className={syncStopped ? "rounded-md border border-destructive p-4" : "sr-only"}>
        {syncStopped ? syncStoppedText : ""}
      </output>
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
              syncable={gmailOffered}
              syncing={sync.isPending && sync.variables?.id === account.id}
              disabled={pending}
              onSync={() => startSync(account)}
              onReconnect={() => startReconnect(account)}
              onDisconnect={() => {
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
          isDisabled={pending}
          onPress={startConnect}
        >
          {connect.isPending || connect.isSuccess ? "Opening Google..." : "Connect Gmail"}
        </Button>
      )}
      <AlertDialog isOpen={dialogOpen} onOpenChange={setDialogOpen}>
        <AlertDialogHeader>
          <AlertDialogTitle className="wrap-anywhere">
            Disconnect <span translate="no">{confirming?.email}</span>?
          </AlertDialogTitle>
          <AlertDialogDescription>
            {confirming?.status === "token-unreadable"
              ? "inbok removes this account and everything it saved for it. It can't ask Google to end its access, so you can remove inbok in your Google Account."
              : "inbok removes this account and everything it saved for it, and asks Google to end its access."}{" "}
            Your mail in Gmail stays as it is.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            type="button"
            variant="destructive"
            onPress={() => {
              if (confirming) startDisconnect(confirming);
            }}
          >
            Disconnect
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialog>
    </section>
  );
}

// AccountRow shows account, where its sync stands, and its actions.
// syncable says whether a sync can be started, which needs a Google client.
// While disabled, the button whose write runs stays focusable as pending,
// so focus has a place when the disconnect dialog closes; the rest are
// disabled.
function AccountRow({
  account,
  gmailOffered,
  reconnecting,
  disconnecting,
  syncable,
  syncing,
  disabled,
  onSync,
  onReconnect,
  onDisconnect,
}: {
  account: ListedAccount;
  gmailOffered: boolean;
  reconnecting: boolean;
  disconnecting: boolean;
  syncable: boolean;
  syncing: boolean;
  disabled: boolean;
  onSync: () => void;
  onReconnect: () => void;
  onDisconnect: () => void;
}): ReactElement {
  const detailsId = `account-${account.id}`;
  const needsReconnect = account.status !== "usable";
  const view = syncView(account.sync);
  // A sync that is waiting or copying needs no start.
  const showSync =
    syncable && !needsReconnect && view.kind !== "waiting" && view.kind !== "running";

  return (
    <li className="flex flex-wrap items-center justify-between gap-x-4 gap-y-3 py-4">
      <div className="flex min-w-0 flex-col gap-1">
        <p className="flex flex-wrap items-center gap-2 font-medium">
          <span translate="no">{account.email}</span>
          {needsReconnect && (
            <StatusBadge tone="warning" icon={TriangleAlertIcon}>
              Needs reconnecting
            </StatusBadge>
          )}
        </p>
        <p id={detailsId} className="text-sm text-muted-foreground">
          <AccountNote account={account} gmailOffered={gmailOffered} />
        </p>
        {/* An account inbok cannot use cannot sync; the line above says
            what to do. */}
        {!needsReconnect && <SyncStatus view={view} />}
      </div>
      <div className="flex flex-wrap gap-3">
        {showSync && (
          <Button
            id={syncId(account.id)}
            type="button"
            variant="outline"
            className={buttonClass}
            aria-describedby={detailsId}
            isDisabled={disabled && !syncing}
            isPending={syncing}
            onPress={onSync}
          >
            {syncing ? checkingText : view.kind === "failed" ? tryAgainText : checkNowText}
            <span className="sr-only"> {account.email}</span>
          </Button>
        )}
        {/* An unreadable token is read anew each time, so a restored key
            takes the button away again without the member. */}
        {gmailOffered && needsReconnect && (
          <Button
            id={reconnectId(account.id)}
            type="button"
            className={buttonClass}
            aria-describedby={detailsId}
            isDisabled={disabled && !reconnecting}
            isPending={reconnecting}
            onPress={onReconnect}
          >
            {reconnecting ? "Opening Google..." : "Reconnect"}
            <span className="sr-only"> {account.email}</span>
          </Button>
        )}
        <Button
          id={disconnectId(account.id)}
          type="button"
          variant="outline"
          className={buttonClass}
          aria-describedby={detailsId}
          isDisabled={disabled && !disconnecting}
          isPending={disconnecting}
          onPress={onDisconnect}
        >
          {disconnecting ? "Disconnecting..." : "Disconnect"}
          <span className="sr-only"> {account.email}</span>
        </Button>
      </div>
    </li>
  );
}

// AccountNote says how account stands: since when Google refused it, that
// inbok can no longer use its saved access, or when it was connected. The
// first two say to reconnect while gmailOffered.
function AccountNote({
  account,
  gmailOffered,
}: {
  account: ListedAccount;
  gmailOffered: boolean;
}): ReactElement {
  const reconnect = gmailOffered && " Reconnect and choose this same Google account.";
  if (account.status === "token-unreadable")
    return <>inbok can no longer use the access it saved for this account.{reconnect}</>;
  if (account.status === "needs-reconnect") {
    if (account.needsReconnectSince === null)
      return <>Google stopped accepting inbok&apos;s access.{reconnect}</>;
    return (
      <>
        Google stopped accepting inbok&apos;s access on{" "}
        <time dateTime={account.needsReconnectSince} className="whitespace-nowrap">
          {formatDate(account.needsReconnectSince)}
        </time>
        .{reconnect}
      </>
    );
  }
  return (
    <>
      Connected on{" "}
      <time dateTime={account.createdAt} className="whitespace-nowrap">
        {formatDate(account.createdAt)}
      </time>
      .
    </>
  );
}

// SyncStatus words view, where an account's sync stands (ADR001, "Polling").
function SyncStatus({ view }: { view: SyncView }): ReactElement {
  const className = "text-sm text-muted-foreground";
  if (view.kind === "waiting") return <p className={className}>{syncWaitingText}</p>;
  if (view.kind === "running")
    return <p className={className}>{runningText(view.messagesSoFar)}</p>;
  if (view.kind === "failed") {
    const words = failureWords(view.failure, view.status);
    return (
      <p className={className}>
        {words.before} <Moment iso={view.failedAt} />
        {words.after}
        {view.upToDateAt !== null && (
          <>
            {" "}
            {upToDateAsOfText} <Moment iso={view.upToDateAt} />.
          </>
        )}
      </p>
    );
  }
  if (view.checkedAt === null) return <p className={className}>{upToDateText}</p>;
  return (
    <p className={className}>
      {checkedAtText} <Moment iso={view.checkedAt} />.
    </p>
  );
}

// Moment shows iso as a time, with its date unless it is today.
function Moment({ iso }: { iso: string }): ReactElement {
  return (
    <time dateTime={iso} className="whitespace-nowrap">
      {formatMoment(iso)}
    </time>
  );
}

// openGoogle sends this tab to Google's consent page from result, which
// returns to this page with the outcome.
function openGoogle(result: ResponseAuthorization): void {
  // The address comes from inbok's own server, which also serves this app,
  // so it is trusted as the app's code is.
  window.location.assign(result.data.url);
}

// syncId returns the id of the Check now button for accountId.
function syncId(accountId: number): string {
  return `sync-${accountId}`;
}

// reconnectId returns the id of the Reconnect button for accountId.
function reconnectId(accountId: number): string {
  return `reconnect-${accountId}`;
}

// disconnectId returns the id of the Disconnect button for accountId.
function disconnectId(accountId: number): string {
  return `disconnect-${accountId}`;
}
