import { useEffect, useRef, useState, type ReactElement } from "react";
import { flushSync } from "react-dom";
import { PencilIcon, TriangleAlertIcon } from "lucide-react";
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
  AccountSync,
  ListedAccount,
  ResponseAuthorization,
  ResponseConnectedAccounts,
} from "@/api/generated/model";
import type { ApiError } from "@/api/transport";
import { accountDot, accountName } from "@/components/account-mark";
import { Address } from "@/components/address";
import { ConfirmDialog, type Failure } from "@/components/confirm-dialog";
import { StatusBadge } from "@/components/status-badge";
import { Button } from "@/components/ui/button";
import { Empty, EmptyContent, EmptyDescription, EmptyTitle } from "@/components/ui/empty";
import { Spinner } from "@/components/ui/spinner";
import { Tooltip, TooltipTrigger } from "@/components/ui/tooltip";
import { useSessionEnd } from "@/features/auth/session-end";
import { SettingsSection } from "@/features/settings/page";
import { cn } from "@/lib/cn";
import { NameForm } from "./name-form";
import { checkLanded, syncNowRefresh, syncView, type SyncView } from "./sync";
import {
  alreadyDisconnectedNotice,
  checkedAtText,
  checkingText,
  checkNowText,
  connectText,
  disconnectedNotice,
  disconnectFailure,
  disconnectText,
  disconnectTitle,
  disconnectWords,
  emptyText,
  emptyTitle,
  failureWords,
  formatDate,
  formatMoment,
  isAccountGone,
  notOfferedStep,
  notOfferedText,
  notOfferedTitle,
  openingGoogleText,
  reconnectText,
  renameText,
  runningText,
  sectionAbout,
  sectionTitle,
  startErrorMessage,
  syncNotice,
  syncWaitingText,
  tryAgainText,
  upToDateAsOfText,
  upToDateText,
  type PageNotice,
} from "./text";

const conflict = 409;
const buttonClass = "w-fit whitespace-normal";
const connectId = "connect-gmail";

// AccountList is the page's one section: the member's connected accounts,
// oldest first as the server lists them, each with where its sync stands
// and the actions its allowedActions offer, then Connect Gmail while
// offered, i.e. the instance has a Google client; admin says whether the
// member is an administrator, who can set one up. onNotice sets the page's
// notice, undefined clearing it as an action starts; onSyncStarted runs
// when the server accepts a Check now, so the page reads the list often.
export function AccountList({
  accounts,
  offered,
  admin,
  onNotice,
  onSyncStarted,
}: {
  accounts: ListedAccount[];
  offered: boolean;
  admin: boolean;
  onNotice: (notice: PageNotice | undefined) => void;
  onSyncStarted: () => void;
}): ReactElement {
  const client = useQueryClient();
  const sessionEnd = useSessionEnd();
  const headingRef = useRef<HTMLHeadingElement>(null);
  // The account the dialog asks about, kept while it closes so its words
  // stay.
  const [confirming, setConfirming] = useState<ListedAccount>();
  const [dialogOpen, setDialogOpen] = useState(false);
  // Where focus goes once a disconnect took the row away: the next row's
  // pencil, else the previous row's, else Connect Gmail.
  const focusAfter = useRef<string>(undefined);
  // openGoogle goes with each mutate call rather than here: the hook's own
  // callbacks still run after the page has closed, and would take the member
  // to Google from wherever they went.
  const startOptions = { mutation: { gcTime: 0, onError: startFailed } };
  const connect = useConnectGmail(startOptions);
  const reconnect = useReconnectConnectedAccount(startOptions);
  const disconnect = useDisconnectConnectedAccount({
    mutation: {
      gcTime: 0,
      // The row goes at once, here rather than with the call, so it does
      // before onSettled's reread starts, which removing it would cancel
      // (TanStack Query runs the hook's callbacks before the call's).
      onSuccess: (_, { id }) => removeAccount(id),
      onError: async (err, { id }) => {
        // Gone before the member's disconnect reached it, e.g. from
        // another tab: what they asked for holds.
        if (isAccountGone(err)) await removeAccount(id);
      },
      // Every outcome, a lost answer included, is settled by the server's
      // list. Here, not with the call, so it runs even if the page closed.
      onSettled: () =>
        void client.invalidateQueries({ queryKey: getListConnectedAccountsQueryKey() }),
    },
  });
  // A successful start keeps the page locked while the browser leaves for
  // Google.
  const starting =
    connect.isPending || reconnect.isPending || connect.isSuccess || reconnect.isSuccess;
  const disconnectError =
    disconnect.error && !isAccountGone(disconnect.error) ? disconnect.error : null;
  const failure: Failure | undefined =
    disconnectError && confirming
      ? {
          message: disconnectFailure(disconnectError, accountName(confirming)),
          requestId: disconnectError.requestId,
        }
      : undefined;
  const role = admin ? "admin" : "member";
  const words = confirming
    ? disconnectWords(accountName(confirming), confirming.status === "token-unreadable")
    : undefined;

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

  // startFailed ends the page on a 401, says why a start failed, and
  // rereads what a refusal shows has changed.
  function startFailed(err: ApiError): void {
    if (sessionEnd(err)) return;
    onNotice({ tone: "problem", text: startErrorMessage(err) });
    // Gmail was offered when the page loaded; an administrator has removed
    // it since, which takes Connect and Reconnect away.
    if (err.status === conflict)
      void client.invalidateQueries({ queryKey: getListAccountProvidersQueryKey() });
    // The account was disconnected elsewhere.
    if (isAccountGone(err))
      void client.invalidateQueries({ queryKey: getListConnectedAccountsQueryKey() });
  }

  // begin clears the last action's result as another starts.
  function begin(): void {
    connect.reset();
    reconnect.reset();
    onNotice(undefined);
  }

  function startConnect(): void {
    if (starting) return;
    begin();
    connect.mutate(undefined, { onSuccess: openGoogle });
  }

  function startReconnect(account: ListedAccount): void {
    if (starting) return;
    begin();
    reconnect.mutate({ id: account.id }, { onSuccess: openGoogle });
  }

  function askDisconnect(account: ListedAccount): void {
    begin();
    disconnect.reset();
    setConfirming(account);
    setDialogOpen(true);
  }

  function confirmDisconnect(): void {
    if (!confirming || disconnect.isPending) return;
    const account = confirming;
    const at = accounts.findIndex((a) => a.id === account.id);
    const next = accounts[at + 1] ?? accounts[at - 1];
    focusAfter.current = next ? renameId(next.id) : connectId;
    disconnect.mutate(
      { id: account.id },
      {
        onSuccess: () => {
          onNotice(disconnectedNotice(accountName(account)));
          setDialogOpen(false);
        },
        onError: (err) => {
          if (sessionEnd(err) || !isAccountGone(err)) return;
          onNotice(alreadyDisconnectedNotice(accountName(account)));
          setDialogOpen(false);
        },
      },
    );
  }

  // removeAccount takes the account with id out of the cached list at once,
  // even if the reread after it fails. A read already under way would put
  // it back as it lands, so it stops first.
  async function removeAccount(id: number): Promise<void> {
    await client.cancelQueries({ queryKey: getListConnectedAccountsQueryKey(), exact: true });
    client.setQueryData<ResponseConnectedAccounts>(getListConnectedAccountsQueryKey(), (list) =>
      list
        ? {
            ...list,
            data: { ...list.data, accounts: list.data.accounts.filter((a) => a.id !== id) },
          }
        : list,
    );
  }

  const connectButton = offered && (
    <Button
      id={connectId}
      type="button"
      className={buttonClass}
      isDisabled={starting && !connect.isPending && !connect.isSuccess}
      isPending={connect.isPending || connect.isSuccess}
      onPress={startConnect}
    >
      {connect.isPending || connect.isSuccess ? (
        <>
          {/* Turns while the request runs, as a sending form's button does. */}
          <Spinner />
          {openingGoogleText}
        </>
      ) : (
        connectText
      )}
    </Button>
  );

  return (
    <SettingsSection
      title={sectionTitle}
      about={sectionAbout}
      layout="stacked"
      headingRef={headingRef}
    >
      {accounts.length === 0 ? (
        <Empty className="p-0">
          {/* No larger than the section's own heading above it. */}
          <EmptyTitle elementType="h3" className="text-base">
            {offered ? emptyTitle : notOfferedTitle}
          </EmptyTitle>
          <EmptyDescription>{offered ? emptyText : notOfferedStep[role]}</EmptyDescription>
          {connectButton && <EmptyContent>{connectButton}</EmptyContent>}
        </Empty>
      ) : (
        <>
          <ul className="flex w-full flex-col divide-y border-y">
            {accounts.map((account) => (
              <AccountRow
                key={account.id}
                account={account}
                locked={starting}
                reconnecting={
                  (reconnect.isPending || reconnect.isSuccess) &&
                  reconnect.variables?.id === account.id
                }
                onAction={begin}
                onNotice={onNotice}
                onSyncStarted={onSyncStarted}
                onReconnect={() => startReconnect(account)}
                onDisconnect={() => askDisconnect(account)}
              />
            ))}
          </ul>
          {offered ? (
            connectButton
          ) : (
            <p className="text-muted-foreground">{notOfferedText[role]}</p>
          )}
        </>
      )}
      <ConfirmDialog
        isOpen={dialogOpen}
        onOpenChange={setDialogOpen}
        title={
          confirming && (
            <>
              {disconnectTitle.before}
              <AccountNameText account={confirming} after={disconnectTitle.after} />
            </>
          )
        }
        description={words?.description ?? ""}
        action={words?.action ?? disconnectText}
        working={words?.working ?? ""}
        tone="destructive"
        pending={disconnect.isPending}
        failure={failure}
        onConfirm={confirmDisconnect}
        fallbackFocus={() =>
          (focusAfter.current && document.getElementById(focusAfter.current)) || headingRef.current
        }
      />
    </SettingsSection>
  );
}

// AccountRow is one account: its name, which the pencil opens a form for in
// place, and one line on where it stands, then its actions, from its
// allowedActions: Reconnect when inbok can't use it, else Check now unless
// a sync is already moving, and Disconnect. locked disables them while the
// page leaves for Google, reconnecting marking this row's Reconnect as the
// cause. onAction runs as an action starts, onNotice reports a Check now's
// refusal, onSyncStarted its acceptance, and onReconnect and onDisconnect
// start those.
function AccountRow({
  account,
  locked,
  reconnecting,
  onAction,
  onNotice,
  onSyncStarted,
  onReconnect,
  onDisconnect,
}: {
  account: ListedAccount;
  locked: boolean;
  reconnecting: boolean;
  onAction: () => void;
  onNotice: (notice: PageNotice) => void;
  onSyncStarted: () => void;
  onReconnect: () => void;
  onDisconnect: () => void;
}): ReactElement {
  const client = useQueryClient();
  const sessionEnd = useSessionEnd();
  const [editing, setEditing] = useState(false);
  // Whether Check now has focus, so focus has a place when it goes, e.g. as
  // a full sync starts.
  const syncFocused = useRef(false);
  const sync = useSyncConnectedAccount({
    mutation: {
      gcTime: 0,
      // Every outcome, a refusal included, is settled by the server's list.
      // Here, not with the call, so it runs even if the page closed.
      onSettled: () =>
        void client.invalidateQueries({ queryKey: getListConnectedAccountsQueryKey() }),
    },
  });
  // The sync a Check now was accepted against, until the list shows a
  // newer outcome or syncNowRefresh passes.
  const [waitingFor, setWaitingFor] = useState<AccountSync>();
  // The account as that check's outcome landed, which the row says once.
  const [landedAs, setLandedAs] = useState<ListedAccount>();
  const timer = useRef<number>(undefined);
  const allowed = account.allowedActions;
  const usable = account.status === "usable";
  const view = syncView(account.sync);
  // Whether the list shows the outcome of the Check now it waits for.
  const landed = waitingFor !== undefined && checkLanded(account.sync, waitingFor);
  const waiting = waitingFor !== undefined && !landed;
  // Kept as it landed: a later read, e.g. a full sync's count every 2 s,
  // would otherwise be said again (react.dev, "Storing information from
  // previous renders").
  if (landed && landedAs === undefined) setLandedAs(account);
  const checking = sync.isPending || waiting;
  // An account inbok can't use needs its permission again, which Reconnect
  // gives; a sync waiting or copying needs no start.
  const showSync =
    allowed.includes("sync") &&
    usable &&
    (checking || (view.kind !== "waiting" && view.kind !== "running"));
  const showReconnect = allowed.includes("reconnect") && !usable;
  const name = accountName(account);

  useEffect(() => () => window.clearTimeout(timer.current), []);

  useEffect(() => {
    // Check now went while it had focus, e.g. as a full sync started, and
    // focus fell to the page.
    if (syncFocused.current && !showSync && document.activeElement === document.body) {
      syncFocused.current = false;
      document.getElementById(renameId(account.id))?.focus();
    }
  }, [showSync, account.id]);

  // closeName closes the name form and gives focus to the pencil, which
  // comes back in its place.
  function closeName(): void {
    // The pencil must be in the page before it can take focus (react.dev,
    // "Flushing state updates synchronously with flushSync").
    flushSync(() => setEditing(false));
    document.getElementById(renameId(account.id))?.focus();
  }

  function check(): void {
    if (checking) return;
    onAction();
    // The last check's wait ends with it: still landed, it would at once
    // hold its outcome again as this check's.
    window.clearTimeout(timer.current);
    setWaitingFor(undefined);
    setLandedAs(undefined);
    const before = account.sync;
    sync.mutate(
      { id: account.id },
      {
        // The answer's sync state is not written into the list: the server
        // read it before the request was saved, so it can be older than a
        // list read that lands first. The list read after it shows it.
        onSuccess: () => {
          onSyncStarted();
          setWaitingFor(before);
          window.clearTimeout(timer.current);
          // As long as the page reads the list often after it.
          timer.current = window.setTimeout(() => {
            setWaitingFor(undefined);
            setLandedAs(undefined);
          }, syncNowRefresh);
        },
        onError: (err) => {
          if (!sessionEnd(err)) onNotice(syncNotice(err, name));
        },
      },
    );
  }

  return (
    <li className="flex flex-wrap items-start justify-between gap-x-6 gap-y-3 py-5">
      <div className="flex min-w-0 flex-1 basis-64 flex-col gap-1">
        {editing && allowed.includes("rename") ? (
          <div className="flex flex-col gap-3">
            {/* Which account this is, while its name is in the field: the
                address, which a rename never changes. */}
            <p className="flex min-w-0 items-start gap-1.5 font-medium">
              <AccountDot account={account} />
              <span className="min-w-0 wrap-anywhere">
                <Address email={account.email} />
              </span>
            </p>
            <NameForm account={account} onClose={closeName} />
          </div>
        ) : (
          <NameLine
            account={account}
            renamable={allowed.includes("rename")}
            locked={locked}
            onRename={() => {
              onAction();
              setEditing(true);
            }}
          />
        )}
        <p className="text-sm text-muted-foreground">
          <StatusLine account={account} view={view} reconnectable={showReconnect} />
        </p>
        {/* Says the line above once a Check now's outcome lands, which
            only the spinner's end shows the eye: the focused button reads
            just its own name again. Always present, so the words are
            announced as they fill it; empty again when the wait's 30 s
            end or another check starts. */}
        <output className="sr-only">
          {landedAs && (
            <>
              {accountName(landedAs)}:{" "}
              <StatusLine
                account={landedAs}
                view={syncView(landedAs.sync)}
                reconnectable={showReconnect}
              />
            </>
          )}
        </output>
      </div>
      <div className="flex flex-wrap gap-2">
        {showSync && (
          <Button
            type="button"
            variant="outline"
            className={buttonClass}
            isDisabled={locked}
            isPending={checking}
            onFocus={() => (syncFocused.current = true)}
            onBlur={() => (syncFocused.current = false)}
            onPress={check}
          >
            {/* Both labels share one cell, so the button keeps the wider
                one's width while it waits and Disconnect stays put under
                the pointer; the hidden one is out of the accessible name. */}
            <span className="grid *:col-start-1 *:row-start-1">
              <span className={cn(checking && "invisible")}>
                {view.kind === "failed" ? tryAgainText : checkNowText}
              </span>
              {/* Turns until the check's outcome shows, as a sending
                  form's button does. */}
              <span className={cn("flex items-center gap-1.5", !checking && "invisible")}>
                <Spinner />
                {checkingText}
              </span>
            </span>
            <span className="sr-only"> {account.email}</span>
          </Button>
        )}
        {showReconnect && (
          <Button
            type="button"
            className={buttonClass}
            isDisabled={locked && !reconnecting}
            isPending={reconnecting}
            onPress={onReconnect}
          >
            {reconnecting ? (
              <>
                <Spinner />
                {openingGoogleText}
              </>
            ) : (
              reconnectText
            )}
            <span className="sr-only"> {account.email}</span>
          </Button>
        )}
        {allowed.includes("disconnect") && (
          <Button
            type="button"
            variant="outline"
            className={buttonClass}
            isDisabled={locked}
            onPress={onDisconnect}
          >
            {disconnectText}
            <span className="sr-only"> {account.email}</span>
          </Button>
        )}
      </div>
    </li>
  );
}

// NameLine is account's name, its address until it has one, wrapping,
// followed by the pencil that renames it while renamable, disabled while
// locked, then its badge, and its address under a chosen name.
function NameLine({
  account,
  renamable,
  locked,
  onRename,
}: {
  account: ListedAccount;
  renamable: boolean;
  locked: boolean;
  onRename: () => void;
}): ReactElement {
  const nameId = `account-name-${account.id}`;
  return (
    <div className="flex min-w-0 flex-col gap-0.5">
      <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
        <p className="flex min-w-0 items-start gap-1.5 font-medium">
          <AccountDot account={account} />
          {/* The whole name, wrapping: this page is where the member tells
              the accounts apart. The pencil follows its last word, so it
              stays beside a name of several lines. */}
          <span className="min-w-0 wrap-anywhere">
            <span id={nameId}>{account.name ?? <Address email={account.email} />}</span>
            {renamable && (
              <>
                {" "}
                <TooltipTrigger>
                  <Button
                    id={renameId(account.id)}
                    type="button"
                    variant="ghost"
                    size="icon-sm"
                    aria-label={`${renameText} ${account.email}`}
                    // Read with a chosen name, so a rename is heard on
                    // return; the address is already in its name.
                    aria-describedby={account.name === null ? undefined : nameId}
                    // Its line keeps the text's height, 32 px on a mouse
                    // and 44 px on touch.
                    className="-my-1.5 ml-0.5 align-middle text-muted-foreground pointer-coarse:-my-3"
                    isDisabled={locked}
                    onPress={onRename}
                  >
                    <PencilIcon aria-hidden="true" />
                  </Button>
                  <Tooltip>{renameText}</Tooltip>
                </TooltipTrigger>
              </>
            )}
          </span>
        </p>
        {account.status !== "usable" && (
          <StatusBadge tone="warning" icon={TriangleAlertIcon}>
            Needs reconnecting
          </StatusBadge>
        )}
      </div>
      {account.name !== null && (
        <p className="text-sm text-muted-foreground">
          <Address email={account.email} />
        </p>
      )}
    </div>
  );
}

// AccountDot is account's colour dot at the start of a line of text, on
// its first line's centre however many lines the text takes.
function AccountDot({ account }: { account: ListedAccount }): ReactElement {
  return (
    <span
      aria-hidden="true"
      className={cn("mt-1.5 size-2 shrink-0 rounded-full", accountDot(account.color))}
    />
  );
}

// AccountNameText is account's name in a sentence, e.g. a dialog's title,
// followed by after, e.g. "?": its chosen name, else its address, wrapping
// at its seams with after kept on its last line; never translated.
function AccountNameText({
  account,
  after,
}: {
  account: ListedAccount;
  after: string;
}): ReactElement {
  return account.name === null ? (
    <Address email={account.email} after={after} />
  ) : (
    <>
      <span translate="no">{account.name}</span>
      {after}
    </>
  );
}

// StatusLine says where account stands: why inbok can't use it, with the
// fix while reconnectable, else where its sync, view, stands (ADR001,
// "Polling").
function StatusLine({
  account,
  view,
  reconnectable,
}: {
  account: ListedAccount;
  view: SyncView;
  reconnectable: boolean;
}): ReactElement {
  const reconnect = reconnectable && " Reconnect and choose this same Google account.";
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
  if (view.kind === "waiting") return <>{syncWaitingText}</>;
  if (view.kind === "running") return <>{runningText(view.messagesSoFar)}</>;
  if (view.kind === "failed") {
    const words = failureWords(view.failure, view.status);
    return (
      <>
        {words.before} <Moment iso={view.failedAt} />
        {words.after}
        {view.upToDateAt !== null && (
          <>
            {" "}
            {upToDateAsOfText} <Moment iso={view.upToDateAt} />.
          </>
        )}
      </>
    );
  }
  if (view.checkedAt === null) return <>{upToDateText}</>;
  return (
    <>
      {checkedAtText} <Moment iso={view.checkedAt} />.
    </>
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
//
// DESIGN: the same tab, not a popup (ADR031): a popup must open within the
// click or a blocker stops it, so it needs this as a fallback anyway; the
// opener can't rely on window.opener under Google's cross-origin opener
// policy, nor tell a closed popup; and a phone turns a popup into a tab.
// The same tab already comes back to this page with the outcome and a
// fresh list.
function openGoogle(result: ResponseAuthorization): void {
  // The address comes from inbok's own server, which also serves this app,
  // so it is trusted as the app's code is.
  window.location.assign(result.data.url);
}

// renameId returns the id of the pencil that renames accountId.
function renameId(accountId: number): string {
  return `rename-${accountId}`;
}
