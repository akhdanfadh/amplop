import {
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
  useSyncExternalStore,
  type FocusEvent,
  type ReactElement,
  type RefObject,
} from "react";
import { useLandmark } from "react-aria/useLandmark";
import { XIcon } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Tooltip, TooltipTrigger } from "@/components/ui/tooltip";
import { onAccountCacheClear } from "@/features/auth/session-cache";
import { cn } from "@/lib/cn";
import { isShortcut, shortcutName } from "@/lib/shortcut";
import { closeName, findItIn, undoBarName, undone, undoName, undoTooltip } from "./text";

// The shortcut that runs the showing Undo: Z with Ctrl, or with Cmd on a
// Mac, as everywhere else (isShortcut).
const undoLetter = "z";
const undoCode = "KeyZ";

// PendingUndo is the last action's Undo: what it did, e.g. "Archived"; the
// conversation's subject; where it went, e.g. "Trash"; and what Undo does.
type PendingUndo = {
  key: number;
  title: string;
  subject: string;
  where: string;
  onUndo: () => void;
};

// Said is what the live region says once an Undo has run, keyed so the
// same words twice are still a change.
type Said = { key: number; text: string };

// The Undo showing, if any, what an Undo that ran says, and who listens.
let state: { pending?: PendingUndo; said?: Said } = {};
let keys = 0;
const listeners = new Set<() => void>();

function set(next: typeof state): void {
  state = next;
  for (const listener of listeners) listener();
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

// showUndo shows undo in the Undo bar in place of any other, and returns a
// function that closes it, e.g. once its action is refused.
export function showUndo(undo: Omit<PendingUndo, "key">): () => void {
  keys++;
  const key = keys;
  set({ pending: { ...undo, key } });
  return () => closeUndo(key);
}

// closeUndo closes the Undo key, or any Undo and what one said when key is
// not given, e.g. as the member signs out.
export function closeUndo(key?: number): void {
  if (key === undefined) {
    if (state.pending || state.said) set({});
    return;
  }
  if (state.pending?.key !== key) return;
  set({ said: state.said });
}

// sayUndone tells screen readers that Undo put the conversation of subject
// back, until the next action's Undo shows.
export function sayUndone(subject: string): void {
  keys++;
  set({ pending: state.pending, said: { key: keys, text: undone(subject) } });
}

// One member's Undo ends with their session (session-cache.ts).
// The module lives as long as the page, so it never stops listening.
void onAccountCacheClear(() => closeUndo());

// runUndo closes undo's bar and runs it.
function runUndo(undo: PendingUndo): void {
  closeUndo(undo.key);
  undo.onUndo();
}

// UndoBar is the last action's Undo, docked at the bottom of the list, or
// of the mail area on a phone (DESIGN.md "Feedback"): what it did and to which conversation, Undo,
// and Close. It shows for 10 s, a line draining as they pass, paused while
// the pointer or focus is on it and while the tab is hidden; the next
// action's Undo takes its place. It is part of the layout, so it covers
// nothing, and it follows the panes in the page, so Tab reaches it from
// the list; F6 reaches it as a landmark, and Ctrl or Cmd+Z runs its Undo
// from anywhere but a text field or an open menu or dialog. As it closes,
// by Undo, Close, its countdown, or its action's refusal, focus inside it
// goes back to where it came from, else to fallbackFocus, e.g. the list's
// heading. Screen readers hear it as it appears, and where the
// conversation went, and that an Undo ran. While it shows, its height is
// --undo-space, which error toasts stand above.
//
// DESIGN: the line's animation is the bar's only clock, so what it shows
// and when the bar closes cannot drift apart; paused, the animation pauses
// too.
export function UndoBar({
  fallbackFocus,
}: {
  fallbackFocus: RefObject<HTMLElement | null>;
}): ReactElement {
  const { pending, said } = useSyncExternalStore(subscribe, () => state);
  const [shown, leaving] = useLeaving(pending);
  const hidden = useTabHidden();
  const bar = useRef<HTMLDivElement>(null);
  // Where focus was before it entered the bar.
  const cameFrom = useRef<HTMLElement | null>(null);

  // Before the closing bar's buttons go, while it fades, so focus never
  // drops to the page.
  useLayoutEffect(() => {
    if (pending || !bar.current?.contains(document.activeElement)) return;
    const back = cameFrom.current?.isConnected ? cameFrom.current : fallbackFocus.current;
    back?.focus();
  }, [pending, fallbackFocus]);

  useEffect(() => {
    if (!pending) return undefined;
    const onKeyDown = (event: KeyboardEvent) => {
      // A key that composes text, e.g. in a Japanese input method, is the
      // input method's.
      if (event.isComposing || !isShortcut(event, undoLetter, undoCode)) return;
      event.preventDefault();
      runUndo(pending);
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [pending]);

  const message = pending
    ? { key: pending.key, text: `${pending.title}: ${pending.subject}. ${findItIn(pending.where)}` }
    : said;
  return (
    <>
      {/* Always in the page, empty between Undos: screen readers announce
          what changes inside a live region, and many say nothing for one
          added with its text already in it. Polite and atomic stated, as
          not every browser makes an output live by itself; each message
          its own node, so the same words twice are said twice. */}
      <output aria-live="polite" aria-atomic="true" className="sr-only">
        {message && <span key={message.key}>{message.text}</span>}
      </output>
      {shown && (
        <Bar
          // A new Undo is a new bar: its own line, from full, and its own
          // pointer and focus.
          key={shown.key}
          ref={bar}
          undo={shown}
          leaving={leaving}
          hidden={hidden}
          onFocusFrom={(from) => {
            // Out of the list, focus goes back to the list itself, which
            // focuses the row it was on: Tab leaves a grid from its last
            // drawn row's star, React Aria's doing.
            cameFrom.current = from.closest<HTMLElement>("[role=grid]") ?? from;
          }}
        />
      )}
    </>
  );
}

// Bar is undo's bar, leaving as it fades out, its countdown paused while
// hidden or while the pointer or focus is on it. onFocusFrom gets where
// focus was before it came in.
function Bar({
  ref,
  undo,
  leaving,
  hidden,
  onFocusFrom,
}: {
  ref: RefObject<HTMLDivElement | null>;
  undo: PendingUndo;
  leaving: boolean;
  hidden: boolean;
  onFocusFrom: (from: HTMLElement) => void;
}): ReactElement {
  const [pointer, setPointer] = useState(false);
  const [focus, setFocus] = useState(false);
  const paused = pointer || focus || hidden || leaving;
  const id = useId();
  const { landmarkProps } = useLandmark({ role: "region", "aria-label": undoBarName }, ref);
  const shortcut = shortcutName(undoLetter);
  useEffect(() => publishSpace(ref.current), [ref]);

  return (
    // oxlint-disable-next-line jsx-a11y/no-static-element-interactions -- the pointer and focus only pause the countdown; the bar's buttons are what act.
    <div
      {...landmarkProps}
      ref={ref}
      data-slot="undo-bar"
      onPointerEnter={() => setPointer(true)}
      onPointerLeave={() => setPointer(false)}
      onFocus={(event: FocusEvent<HTMLElement>) => {
        const from = event.relatedTarget;
        if (from instanceof HTMLElement && !event.currentTarget.contains(from)) onFocusFrom(from);
        setFocus(true);
      }}
      onBlur={(event: FocusEvent<HTMLElement>) => {
        if (!event.currentTarget.contains(event.relatedTarget)) setFocus(false);
      }}
      className={cn(
        "@container relative shrink-0 overflow-hidden border-t bg-muted py-2 pr-2 pl-4 text-sm duration-200 ease-standard outline-none focus-visible:focus-ring-inset",
        leaving
          ? "animate-out fade-out-0 fill-mode-forwards slide-out-to-bottom-2"
          : "animate-in fade-in-0 slide-in-from-bottom-2",
      )}
    >
      {/* Under 18em, e.g. a phone at large text, though not the 300 px
          list beside the reading pane, the words take the whole width and
          the buttons wrap below them, so the subject stays readable and
          nothing runs under a button. */}
      <div className="flex items-center gap-3 @max-[18em]:flex-wrap">
        {/* The live region says these; Undo names them as its
            description, so a screen reader that Tabs here hears which
            conversation it puts back. */}
        <span aria-hidden="true" className="flex min-w-0 flex-1 flex-col @max-[18em]:basis-full">
          <span id={`${id}-title`} className="truncate font-semibold">
            {undo.title}
          </span>
          <span id={`${id}-subject`} dir="auto" className="truncate text-muted-foreground">
            {undo.subject}
          </span>
        </span>
        <TooltipTrigger>
          <Button
            variant="ghost"
            size="sm"
            className="text-primary @max-[18em]:ml-auto"
            // NOTE: this replaces the description React Aria's tooltip
            // gives, so the shortcut joins it here: React Aria's Button
            // drops aria-keyshortcuts.
            aria-describedby={`${id}-title ${id}-subject ${id}-keys`}
            onPress={() => runUndo(undo)}
          >
            {undoName}
          </Button>
          <Tooltip>{undoTooltip(shortcut)}</Tooltip>
        </TooltipTrigger>
        <span id={`${id}-keys`} hidden>
          {shortcut}
        </span>
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label={closeName}
          onPress={() => closeUndo(undo.key)}
        >
          <XIcon aria-hidden="true" />
        </Button>
      </div>
      <span
        aria-hidden="true"
        // The countdown keeps running through a theme switch, which ends
        // every other animation at once (styles.css); in forced colours a
        // background is the page's, so the line takes the text's colour.
        data-countdown=""
        className="pointer-events-none absolute inset-x-0 top-0 h-0.5 origin-left animate-[undo-drain_10s_linear_forwards] bg-primary forced-colors:bg-[CanvasText]"
        style={{ animationPlayState: paused ? "paused" : "running" }}
        onAnimationEnd={() => closeUndo(undo.key)}
      />
    </div>
  );
}

// The bar's height from the bottom of the window, as a CSS variable,
// which the error toasts stand above, so none covers Undo (WCAG 2.4.11).
const undoSpace = "--undo-space";

// publishSpace keeps --undo-space while bar is in the page, and returns
// what stops it.
function publishSpace(bar: HTMLElement | null): (() => void) | undefined {
  if (!bar) return undefined;
  const root = document.documentElement;
  const observer = new ResizeObserver(() => {
    root.style.setProperty(undoSpace, `${window.innerHeight - bar.getBoundingClientRect().top}px`);
  });
  observer.observe(bar);
  return () => {
    observer.disconnect();
    root.style.removeProperty(undoSpace);
  };
}

// useLeaving returns the Undo to draw: pending, or the one that just closed,
// kept 200 ms for its fade, and whether it is leaving.
function useLeaving(pending: PendingUndo | undefined): [PendingUndo | undefined, boolean] {
  const [last, setLast] = useState(pending);
  // A new Undo is kept as it arrives, while drawing.
  if (pending !== undefined && pending !== last) setLast(pending);
  useEffect(() => {
    if (pending) return undefined;
    const timer = setTimeout(() => setLast(undefined), 200);
    return () => clearTimeout(timer);
  }, [pending]);
  const shown = pending ?? last;
  return [shown, pending === undefined && shown !== undefined];
}

// useTabHidden reports whether the page's tab is hidden.
function useTabHidden(): boolean {
  return useSyncExternalStore(
    (changed) => {
      document.addEventListener("visibilitychange", changed);
      return () => document.removeEventListener("visibilitychange", changed);
    },
    () => document.visibilityState === "hidden",
  );
}
