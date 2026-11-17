import {
  useCallback,
  useEffect,
  useId,
  useRef,
  useState,
  type KeyboardEvent,
  type ReactElement,
  type ReactNode,
  type RefObject,
} from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Link, useBlocker, useRouter, useRouterState } from "@tanstack/react-router";
import { FocusScope } from "react-aria/FocusScope";
import { useLandmark } from "react-aria/useLandmark";
import { Maximize2Icon, Minimize2Icon, MinusIcon, XIcon } from "lucide-react";
import { getGetOutgoingMessageQueryKey, useListConnectedAccounts } from "@/api/generated/inbok";
import type { ListedAccount } from "@/api/generated/model";
import { userMessage } from "@/api/problem";
import type { ApiError } from "@/api/transport";
import { textLink } from "@/components/link-style";
import { ReadError } from "@/components/read-error";
import { ChoiceDialog } from "@/components/choice-dialog";
import { Button } from "@/components/ui/button";
import { Tooltip, TooltipTrigger } from "@/components/ui/tooltip";
import { isSessionEnded } from "@/features/auth/guard";
import { useHeldWork } from "@/features/auth/session-end";
import { SessionEndedBanner } from "@/features/auth/session-ended-banner";
import { useLayout } from "@/hooks/use-layout";
import { cn } from "@/lib/cn";
import { ComposeForm } from "./compose-form";
import { composeValues } from "./compose-schema";
import { useCompose, type ComposeMessage } from "./compose-store";
import { useOutgoingMessage } from "./outbox";
import { MessageStatus } from "./status";
import { MessageStatusMark } from "./status-badge";
import {
  composeAccountsUnreadable,
  closeText,
  messageStatusStale,
  newMessageTitle,
  noAccountText,
  readErrorMessage,
  replaceText,
} from "./text";

// How long a sent message's status stays before the window closes itself.
export const closesAfterSentMs = 3_000;

// The window's size while docked, where nothing covers the page.
type Size = "docked" | "minimised" | "full";

// ComposeWindow is the compose window (DESIGN.md "Compose and reply") while
// the member's compose store holds a message: docked bottom right on a
// wide screen, a full-screen editor on a phone. Each window starts afresh;
// a message replacing another, e.g. by Edit and send, keeps the window.
export function ComposeWindow(): ReactElement | null {
  const { state } = useCompose();
  if (!state.message) return null;
  return <OpenWindow key={state.message.window} message={state.message} />;
}

// How the window's close moves focus: back to what opened the window, or
// nowhere, e.g. when it closes itself while focus is on the page.
type CloseFocus = "focus-opener" | "leave-focus";

// OpenWindow is one window, holding message: its layout, size, history
// entry on a phone, focus, and the questions before something is lost.
// What it holds, the editor or the sent message's status, is
// MessageContent's, which starts afresh for each message.
function OpenWindow({ message }: { message: ComposeMessage }): ReactElement {
  const router = useRouter();
  const { store, state } = useCompose();
  const layout = useLayout();
  // DESIGN: the layout when the window opened holds until it closes, so
  // turning a phone or resizing never moves what is being written; a
  // docked window narrowed to a phone's width stays docked, never wider
  // than the screen.
  const [phone] = useState(layout === "phone");
  const [size, setSize] = useState<Size>("docked");
  const covering = phone || size === "full";
  const minimised = !phone && size === "minimised";

  const sectionRef = useRef<HTMLElement>(null);
  const titleId = useId();
  // The field that had focus when the window was minimised.
  const lastFocus = useRef<HTMLElement | null>(null);
  const [asking, setAsking] = useState(false);

  // close's choice, for the history step it waits on (finish).
  const closeFocus = useRef<CloseFocus>("focus-opener");
  // finish closes the window and moves focus as closeFocus says: to what
  // opened it, or the page's heading when that is gone.
  const finish = useCallback(() => {
    const opener = state.opener;
    store.close();
    if (closeFocus.current === "leave-focus") return;
    // After the page is no longer inert behind the window.
    requestAnimationFrame(() => {
      if (opener?.isConnected && !opener.closest("[inert]")) opener.focus();
      else document.querySelector<HTMLElement>("main h1, h1")?.focus();
    });
  }, [state.opener, store]);

  // On a phone the window is a history entry of its own, so Back closes it.
  const pushed = useRef(false);
  // Set once the window's entry is the current one.
  const arrived = useRef(false);
  // Set by the window's own close, which steps back through history.
  const closing = useRef(false);
  const entry = useRouterState({ select: (s) => s.location.state.composeWindow });
  useEffect(() => {
    if (!phone || pushed.current) return;
    pushed.current = true;
    const { location } = router.history;
    // Its own step, past any page's leave warning: it keeps the address.
    router.history.push(
      location.href,
      { ...location.state, composeWindow: message.window },
      { ignoreBlocker: true },
    );
  }, [phone, router, message.window]);
  useEffect(() => {
    if (!phone) return;
    if (entry === message.window) arrived.current = true;
    // Leaving the entry, by Back, a link, or Close, closes the window; the
    // blocker below asks first when that would lose something.
    else if (arrived.current) finish();
  }, [phone, entry, message.window, finish]);

  const loses = state.loses;
  // A message being written is kept when the session ends, to copy.
  useHeldWork(loses !== null);
  const blocker = useBlocker({
    // The store's state now, not this render's: a Back right after typing
    // comes before the blocker is set again with the render's. Leaving the
    // signed-in pages for sign-in, e.g. from the signed-out banner, would
    // close the window, so it asks too.
    shouldBlockFn: ({ next }) =>
      store.snapshot().loses !== null &&
      ((arrived.current && !closing.current) || next.pathname === "/sign-in"),
    // Closing or reloading the tab asks with the browser's own words.
    enableBeforeUnload: () => store.snapshot().loses !== null,
    withResolver: true,
  });

  useEffect(() => {
    store.setCovering(covering);
  }, [store, covering]);
  useEffect(() => () => store.setCovering(false), [store]);
  useEffect(() => () => store.setLoses(null), [store]);

  // Compose pressed again: the window comes back, focused. Set while
  // rendering, as React advises for state that follows another's change.
  const [firstShown] = useState(state.shown);
  const [shown, setShown] = useState(state.shown);
  if (state.shown !== shown) {
    setShown(state.shown);
    if (size === "minimised") setSize("docked");
  }
  useEffect(() => {
    if (shown === firstShown) return;
    requestAnimationFrame(() => focusInside(sectionRef.current, lastFocus.current));
  }, [shown, firstShown]);

  // close closes the window, moving focus as moveFocus says; on a phone it
  // steps back out of the window's history entry, which then finishes.
  const close = useCallback(
    (moveFocus: CloseFocus) => {
      closeFocus.current = moveFocus;
      if (!arrived.current) {
        finish();
        return;
      }
      closing.current = true;
      // Back to the same address: no page's leave warning has to ask.
      router.history.back({ ignoreBlocker: true });
    },
    [finish, router],
  );

  // The pointer on the window or focus on one of its controls holds a sent
  // message's window open, as the Undo bar does.
  const [holding, setHolding] = useState({ pointer: false, focus: false });

  const { landmarkProps } = useLandmark({ role: "region", "aria-labelledby": titleId }, sectionRef);
  const role = covering ? { role: "dialog", "aria-modal": true } : {};

  function minimise(): void {
    setSize("minimised");
    requestAnimationFrame(() =>
      sectionRef.current?.querySelector<HTMLElement>("[data-window-title]")?.focus(),
    );
  }
  function restore(): void {
    setSize("docked");
    requestAnimationFrame(() => focusInside(sectionRef.current, lastFocus.current));
  }
  function askToClose(): void {
    if (loses === null) close("focus-opener");
    else setAsking(true);
  }
  function onKeyDown(event: KeyboardEvent<HTMLElement>): void {
    // Escape leaves full screen, never closes: there are no drafts to keep
    // a message closed by a stray key (ADR031).
    if (event.key !== "Escape" || size !== "full" || phone) return;
    event.preventDefault();
    setSize("docked");
  }

  const header = ({ title, send, sizes, mark }: HeaderParts): ReactNode => (
    <TitleBar
      titleId={titleId}
      title={title}
      mark={minimised ? mark : null}
      look={phone ? "full-screen" : "window"}
      size={size}
      sizes={sizes}
      onMinimise={minimise}
      onRestore={restore}
      onFull={() => setSize(size === "full" ? "docked" : "full")}
      onClose={askToClose}
      send={send}
    />
  );

  return (
    <>
      {size === "full" && !phone && (
        // Pressing the dimmed page goes back to the docked window, never
        // closing it.
        <div
          aria-hidden="true"
          className="fixed inset-0 z-40 bg-scrim"
          onClick={() => setSize("docked")}
        />
      )}
      {/* oxlint-disable-next-line jsx-a11y/no-noninteractive-element-interactions -- Escape from its fields leaves full screen. */}
      <section
        ref={sectionRef}
        {...landmarkProps}
        {...role}
        aria-labelledby={titleId}
        onKeyDown={onKeyDown}
        onPointerEnter={() => setHolding((h) => ({ ...h, pointer: true }))}
        onPointerLeave={() => setHolding((h) => ({ ...h, pointer: false }))}
        onFocus={(event) => {
          // The field to come back to after the window is restored.
          if (event.target.matches('input, textarea, [data-slot="select-trigger"]'))
            lastFocus.current = event.target;
          // The status itself takes focus after Send, which holds nothing;
          // the member moving to one of its controls does.
          setHolding((h) => ({
            ...h,
            focus: event.target.getAttribute("aria-label") !== "Status",
          }));
        }}
        onBlur={(event) => {
          if (!event.currentTarget.contains(event.relatedTarget))
            setHolding((h) => ({ ...h, focus: false }));
        }}
        className={cn(
          "fixed z-40 flex flex-col overflow-hidden bg-card",
          phone
            ? "inset-0 bg-background"
            : size === "full"
              ? "inset-x-0 top-[6vh] mx-auto h-[88vh] w-[min(calc(100%-3rem),60rem)] rounded-2xl border shadow-dialog dark:border-foreground/20"
              : "right-6 bottom-0 rounded-t-xl border border-b-0 shadow-dialog dark:border-foreground/20",
          // Its edge, not each field's ring, marks the editor in use, as the
          // reply's card does: a text field's caret shows where typing goes.
          !phone && "transition-colors focus-within:border-input dark:focus-within:border-input",
          // Never wider than the screen leaves, e.g. with text at 200% or a
          // window narrowed after it opened.
          size === "docked" && !phone && "w-[min(35rem,calc(100vw-3rem))]",
          minimised && "w-[min(18rem,calc(100vw-3rem))]",
          // It rises into place as it opens (DESIGN.md "Motion").
          "animate-in duration-250 ease-standard fade-in-0 slide-in-from-bottom-4 motion-reduce:slide-in-from-bottom-0",
        )}
      >
        {/* Covering the page, focus stays in the window, as in any modal
            dialog; Tab goes round its controls. */}
        <FocusScope contain={covering}>
          <MessageContent
            key={message.id}
            message={message}
            look={phone ? "full-screen" : "window"}
            size={size}
            holding={holding.pointer || holding.focus}
            section={sectionRef}
            header={header}
            onSent={() => setSize((current) => (current === "full" ? "docked" : current))}
            onDiscard={askToClose}
            onClose={close}
          />
        </FocusScope>
      </section>
      <ChoiceDialog
        isOpen={asking}
        text={closeText(loses ?? "typed")}
        onStay={() => setAsking(false)}
        onLeave={() => {
          setAsking(false);
          close("focus-opener");
        }}
      />
      <ChoiceDialog
        isOpen={blocker.status === "blocked"}
        text={closeText(loses ?? "typed")}
        onStay={() => blocker.reset?.()}
        onLeave={() => {
          closing.current = true;
          blocker.proceed?.();
        }}
      />
      <ChoiceDialog
        isOpen={state.replacing !== null}
        text={replaceText}
        onStay={store.keep}
        onLeave={store.replace}
      />
    </>
  );
}

// The parts of the window's top bar that what it holds decides: title, the
// window's name; send, the Send button for a full-screen editor's bar;
// sizes, whether Full screen is offered; mark, the sent message's status,
// shown while minimised.
type HeaderParts = {
  title: string;
  send: ReactNode;
  sizes: "all" | "no-full-screen";
  mark?: ReactNode;
};

// MessageContent is what the window holds for message: the editor, then
// the sent message's status, which closes the window 3 s after Gmail
// takes it unless holding; or, until the accounts load or when none can
// send, where things stand. look and size are the window's; section is its
// element; header draws its top bar. onSent runs once the message is
// saved, onDiscard on Discard, onClose to close the window.
function MessageContent({
  message,
  look,
  size,
  holding,
  section,
  header,
  onSent,
  onDiscard,
  onClose,
}: {
  message: ComposeMessage;
  look: "window" | "full-screen";
  size: Size;
  holding: boolean;
  section: RefObject<HTMLElement | null>;
  header: (parts: HeaderParts) => ReactNode;
  onSent: () => void;
  onDiscard: () => void;
  onClose: (moveFocus: CloseFocus) => void;
}): ReactElement {
  const client = useQueryClient();
  const { store } = useCompose();
  const minimised = look === "window" && size === "minimised";
  const [sentId, setSentId] = useState<string | null>(null);
  const accounts = useListConnectedAccounts();
  const list = accounts.data?.data.accounts;
  const sent = useOutgoingMessage(sentId ?? "", { enabled: sentId !== null });
  const sentMessage = sent.message;
  // The session ended before the accounts were read: nothing to show, and
  // the session-end rule opens sign-in.
  const accountsEnded = isSessionEnded(accounts.error);
  const senders = list?.filter((account) => account.allowedActions.includes("send")) ?? [];
  // Once shown, the editor stays, whatever a later read of the accounts
  // says, so typed text is never dropped; its From then marks an account
  // that can no longer send, and the server refuses it. Set while
  // rendering, as React advises for state that follows a render's result.
  const [formShown, setFormShown] = useState(false);
  const showForm =
    list !== undefined && (formShown || senders.length > 0 || message.edit !== undefined);
  if (showForm && !formShown) setFormShown(true);

  // A sent message closes the window 3 s after Gmail takes it, waiting
  // while holding.
  const done = sentMessage?.status === "sent";
  useEffect(() => {
    if (!done || holding) return undefined;
    const timer = setTimeout(() => {
      const inside = section.current?.contains(document.activeElement) ?? false;
      onClose(inside ? "focus-opener" : "leave-focus");
    }, closesAfterSentMs);
    return () => clearTimeout(timer);
  }, [done, holding, onClose, section]);

  // The editor that held focus is gone after Send; its status takes it.
  const statusShown = sentMessage !== undefined;
  useEffect(() => {
    if (!statusShown) return;
    const active = document.activeElement;
    if (active !== document.body && !section.current?.contains(active)) return;
    section.current?.querySelector<HTMLElement>('[aria-label="Status"]')?.focus();
  }, [statusShown, section]);

  // Where the frame's signed-out banner is hidden, under the window
  // covering the page.
  const signedOut = <SessionEndedBanner place="window" />;
  // The editor's height while docked, which the status, shorter, doesn't take.
  const dockedHeight = look === "window" && size === "docked" && "h-[min(36rem,calc(100dvh-4rem))]";

  if (sentId !== null) {
    return (
      <>
        {header({
          title: sentMessage?.subject || newMessageTitle,
          send: null,
          sizes: "no-full-screen",
          mark: sentMessage && <MessageStatusMark message={sentMessage} />,
        })}
        {signedOut}
        <div hidden={minimised} className="min-h-0 overflow-y-auto">
          {sentMessage && (
            <MessageStatus
              look="window"
              message={sentMessage}
              accounts={list}
              onSentAgain={(copy) => setSentId(copy.id)}
              onEdit={() => store.open(sentMessage)}
            />
          )}
          {/* A failed reread keeps the status, saying it may have moved on, as
              the reply's does; polling stops on a failure, and Try again
              restarts it. */}
          {sent.error && !isSessionEnded(sent.error) && (
            <div className="px-4 pb-4">
              <ReadError
                message={
                  sentMessage
                    ? `${messageStatusStale} ${userMessage(sent.error)}`
                    : readErrorMessage(sent.error)
                }
                error={sent.error}
                retrying={sent.retrying}
                onRetry={sent.retry}
              />
            </div>
          )}
        </div>
      </>
    );
  }

  if (showForm)
    return (
      <div className={cn("flex min-h-0 flex-col", dockedHeight || "flex-1")}>
        <ComposeForm
          accounts={list}
          edit={message.edit}
          initial={composeValues(message.edit)}
          note={signedOut}
          look={look}
          header={({ title, send }) => header({ title, send, sizes: "all" })}
          collapsed={minimised}
          onSent={(saved) => {
            onSent();
            // The status shows at once, without waiting for a read.
            client.setQueryData(getGetOutgoingMessageQueryKey(saved.id), { data: saved });
            store.setLoses(null);
            setSentId(saved.id);
          }}
          onDiscard={onDiscard}
          onLoses={store.setLoses}
        />
      </div>
    );

  return (
    <>
      {header({ title: newMessageTitle, send: null, sizes: "all" })}
      {signedOut}
      {!(accountsEnded && !list) && (
        <div hidden={minimised} className="p-4">
          <WindowState
            accounts={list}
            error={accounts.error}
            retrying={accounts.isFetching}
            onRetry={() => void accounts.refetch()}
          />
        </div>
      )}
    </>
  );
}

// focusInside moves focus to last, the field that had it, when it is
// still in section, else to the section's first field.
function focusInside(section: HTMLElement | null, last: HTMLElement | null): void {
  if (last?.isConnected && section?.contains(last)) {
    last.focus();
    return;
  }
  section
    ?.querySelector<HTMLElement>('[data-slot="select-trigger"], input, textarea, [tabindex="-1"]')
    ?.focus();
}

// TitleBar is the top of the window: its title, named titleId, and its
// buttons. look "window" is the docked window's muted strip, whose title
// minimises and restores it; "full-screen" is the full-screen editor's
// bar, Close first and send, the Send button, last. size is the window's
// now; sizes "no-full-screen" leaves out Full screen, e.g. for a sent
// message's status. mark shows that status while minimised.
function TitleBar({
  titleId,
  title,
  mark,
  look,
  size,
  sizes,
  onMinimise,
  onRestore,
  onFull,
  onClose,
  send,
}: {
  titleId: string;
  title: string;
  mark: ReactNode;
  look: "window" | "full-screen";
  size: Size;
  sizes: "all" | "no-full-screen";
  onMinimise: () => void;
  onRestore: () => void;
  onFull: () => void;
  onClose: () => void;
  send: ReactNode;
}): ReactElement {
  if (look === "full-screen")
    return (
      <div className="flex h-14 shrink-0 items-center gap-2 border-b px-2">
        <WindowButton label="Close" onPress={onClose}>
          <XIcon aria-hidden="true" />
        </WindowButton>
        <h2 id={titleId} className="min-w-0 flex-1 truncate text-sm font-semibold">
          {title}
        </h2>
        {send}
      </div>
    );

  const minimised = size === "minimised";
  return (
    // A touch screen's 44 px buttons fit the strip, which grows to them.
    <div className="flex h-10 shrink-0 items-center gap-1 bg-muted pr-1 pl-4 pointer-coarse:h-11">
      <button
        type="button"
        data-window-title
        aria-expanded={!minimised}
        onClick={minimised ? onRestore : onMinimise}
        className="flex min-w-0 flex-1 items-center gap-2 self-stretch rounded-md text-left outline-none focus-visible:focus-ring-inset"
      >
        {mark && <span className="[&_svg]:size-3 [&>span]:size-5">{mark}</span>}
        <h2 id={titleId} className="min-w-0 flex-1 truncate text-sm font-semibold">
          {title}
        </h2>
      </button>
      <WindowButton
        label={minimised ? "Restore" : "Minimise"}
        onPress={minimised ? onRestore : onMinimise}
      >
        <MinusIcon aria-hidden="true" />
      </WindowButton>
      {!minimised && sizes === "all" && (
        <WindowButton label={size === "full" ? "Exit full screen" : "Full screen"} onPress={onFull}>
          {size === "full" ? (
            <Minimize2Icon aria-hidden="true" />
          ) : (
            <Maximize2Icon aria-hidden="true" />
          )}
        </WindowButton>
      )}
      <WindowButton label="Close" onPress={onClose}>
        <XIcon aria-hidden="true" />
      </WindowButton>
    </div>
  );
}

// WindowButton is an icon button of the title bar, named label, which its
// tooltip shows.
function WindowButton({
  label,
  onPress,
  children,
}: {
  label: string;
  onPress: () => void;
  children: ReactNode;
}): ReactElement {
  return (
    <TooltipTrigger>
      <Button type="button" variant="ghost" size="icon-sm" aria-label={label} onPress={onPress}>
        {children}
      </Button>
      <Tooltip>{label}</Tooltip>
    </TooltipTrigger>
  );
}

// WindowState is what the window shows instead of the editor: accounts
// loading, failing to load, or none able to send. onRetry reads them again.
function WindowState({
  accounts,
  error,
  retrying,
  onRetry,
}: {
  accounts: ListedAccount[] | undefined;
  error: ApiError | null;
  retrying: boolean;
  onRetry: () => void;
}): ReactElement {
  if (!accounts && error)
    return (
      <ReadError
        message={`${composeAccountsUnreadable} ${userMessage(error)}`}
        error={error}
        retrying={retrying}
        onRetry={onRetry}
      />
    );
  if (!accounts) return <output>Loading...</output>;
  return (
    <p>
      {noAccountText(accounts)}{" "}
      <Link to="/settings/connected-accounts" className={textLink}>
        Open Connected accounts
      </Link>
    </p>
  );
}
