import { useCallback, useId, type ReactElement } from "react";
import {
  Disclosure,
  DisclosurePanel,
  Text,
  UNSTABLE_Toast as ToastPrimitive,
  UNSTABLE_ToastContent as ToastContentPrimitive,
  UNSTABLE_ToastQueue as ToastQueue,
  UNSTABLE_ToastRegion as ToastRegionPrimitive,
} from "react-aria-components";
import { ChevronRightIcon, CircleAlertIcon, XIcon } from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/cn";

// NOTE: React Aria's toast is still alpha, hence UNSTABLE_; its API may
// change in any upgrade, so recheck this file and its tests after one.

// ToastDetail is one group of a toast's details: its heading, e.g. why,
// and the items it holds, e.g. the conversations refused for that reason.
export type ToastDetail = { heading: string; items: readonly string[] };

// ToastMessage is what one toast says: title, what failed, e.g. "Couldn't
// archive the conversation"; description, an optional sentence, the fix;
// details, groups of items behind Show details, e.g. which conversations
// failed and why; and about, the keys of what it is about, e.g. a label
// on a conversation, which closeAbout closes it by. An action's Undo is
// not a toast: it has its own bar (DESIGN.md "Feedback").
type ToastMessage = {
  title: string;
  description?: string;
  details?: readonly ToastDetail[];
  about?: string[];
};

// The app's one queue. React Aria shows the newest toast first.
// No limit, so every toast shows at once: an error toast stays until
// closed, and a limit would hide later ones.
const queue = new ToastQueue<ToastMessage>();

// showError shows title, what failed, and description, the fix, with
// details, which Show details opens, and about, what closeAbout closes it
// by. It stays until the member closes it. An error already showing is not
// shown again: the one showing is then about both.
export function showError(
  title: string,
  description?: string,
  { details, about }: { details?: readonly ToastDetail[]; about?: readonly string[] } = {},
): void {
  // With no limit, a failure repeated, e.g. every action while offline,
  // would stack copies the member must close one by one. The copy shown
  // takes on what the new one is about too, so closeAbout closes it only
  // once a newer action changed all of it.
  const shown = queue.visibleToasts.find(
    ({ content }) => content.title === title && content.description === description,
  );
  if (shown) {
    if (shown.content.about && about) {
      shown.content.about = [...new Set([...shown.content.about, ...about])];
    }
    return;
  }
  // No timeout, so the fix can't vanish before it is read.
  queue.add({ title, description, details, about: about && [...about] });
}

// closeAbout marks keys as no longer failing, e.g. what a newer action
// changed again: each toast about them is then about the rest only, and
// closes once nothing it was about is left. Keys match exactly; which keys
// a change covers is the caller's to say.
export function closeAbout(keys: Iterable<string>): void {
  const closed = new Set(keys);
  for (const toast of queue.visibleToasts) {
    const about = toast.content.about;
    if (!about || about.length === 0) continue;
    const left = about.filter((key) => !closed.has(key));
    if (left.length === 0) queue.close(toast.key);
    else toast.content.about = left;
  }
}

// shownAbout returns the keys the toasts still showing are about, so a
// caller that tracks them can let go of the rest, e.g. one the member
// closed.
export function shownAbout(): Set<string> {
  return new Set(queue.visibleToasts.flatMap((toast) => toast.content.about ?? []));
}

// closeToasts closes every toast, e.g. on sign-out, when no toast should
// outlive the member it was for.
export function closeToasts(): void {
  queue.clear();
}

// The space the toasts take from the bottom of the window, as a CSS
// variable, which a scrolling box sets as its scroll-padding-bottom
// (`scroll-pb-(--toast-space)`), so what scrolls into view, e.g. a focused
// row, stops above a toast rather than under it (WCAG 2.4.11).
const toastSpace = "--toast-space";

// Toaster is the region every toast appears in, bottom right (bottom on a
// phone), above any open dialog or drawer, and above the Undo bar while it
// shows (--undo-space, features/mail/undo.tsx); toasts that do not fit
// between there and the window's top scroll. F6 moves focus into it and
// back. Render it once, in the root.
export function Toaster(): ReactElement {
  // React Aria draws the region only while a toast shows.
  const region = useCallback((element: HTMLElement | null) => {
    const root = document.documentElement;
    if (!element) {
      root.style.removeProperty(toastSpace);
      return undefined;
    }
    const observer = new ResizeObserver(() => {
      root.style.setProperty(
        toastSpace,
        `${window.innerHeight - element.getBoundingClientRect().top}px`,
      );
    });
    observer.observe(element);
    return () => {
      observer.disconnect();
      root.style.removeProperty(toastSpace);
    };
  }, []);
  return (
    // z-60, above a dialog's or drawer's scrim (z-50), so a toast shown
    // before one opened stays in reach. At most the window's height less
    // its 16 px edges and the Undo bar, scrolling past that, so a stack of
    // errors never reaches above the window, where no Close could be
    // pressed; 1 px inside its edge, so the scrolling box keeps the toasts'
    // outline.
    <ToastRegionPrimitive
      ref={region}
      queue={queue}
      className="fixed right-4 bottom-[calc(--spacing(4)+var(--undo-space,0px))] left-4 z-60 flex max-h-[calc(100dvh-var(--undo-space,0px)-2*--spacing(4))] flex-col gap-2 overflow-y-auto overscroll-contain rounded-xl p-px outline-none data-focus-visible:focus-ring sm:left-auto sm:w-sm"
    >
      {({ toast }) => (
        <ToastPrimitive
          toast={toast}
          // A dark surface in both themes (DESIGN.md "Components"), so the
          // toast stands apart from the page: the dark class gives it, and
          // everything in it, the dark theme's tokens. Its focus ring sits
          // inside, on that surface: outside, the dark theme's ring colour
          // would be too light on a light page.
          // Fades in, so one toast replacing another is seen (DESIGN.md
          // "Motion"); the duration is the fade's only: with no
          // transition, the focus ring shows at once.
          // With details, the icon and Close keep to the title's line as
          // the details open below it.
          className={cn(
            "dark flex shrink-0 animate-in gap-3 rounded-xl bg-popover p-3 text-sm text-popover-foreground ring-1 ring-border transition-none duration-200 ease-standard fade-in-0 outline-none data-focus-visible:focus-ring-inset forced-colors:outline-1 forced-colors:outline-solid",
            toast.content.details ? "items-start" : "items-center",
          )}
        >
          <CircleAlertIcon
            aria-hidden="true"
            className={cn(
              "size-4 shrink-0 text-destructive forced-colors:text-[CanvasText]",
              // Centred on the title's 20 px line, the close button's
              // 32 px less its 6 px of space above the words.
              toast.content.details && "mt-2",
            )}
          />
          {/* Show details sits under the words but outside them: they are
              the alert, read at once and whole, and stay as they are. */}
          <div className="flex min-w-0 flex-1 flex-col">
            <ToastContentPrimitive className="flex flex-col gap-0.5 py-0.5">
              <Text slot="title" className="font-semibold">
                {toast.content.title}
              </Text>
              {toast.content.description && (
                <Text slot="description">{toast.content.description}</Text>
              )}
            </ToastContentPrimitive>
            {toast.content.details && <Details details={toast.content.details} />}
          </div>
          {/* React Aria names it "Close", in the locale the root sets. */}
          <Button slot="close" variant="ghost" size="icon-sm">
            <XIcon aria-hidden="true" />
          </Button>
        </ToastPrimitive>
      )}
    </ToastRegionPrimitive>
  );
}

// The words that open a toast's details.
const showDetails = "Show details";

// Details is a toast's details, folded under Show details: each group's
// heading over its items. The toasts' region scrolls when they run long.
function Details({ details }: { details: readonly ToastDetail[] }): ReactElement {
  const id = useId();
  return (
    <Disclosure className="group/details mt-1">
      {/* -ml-3: the chevron near where the words above start, the fill
          stopping at the icon's column. Open, it keeps no fill: the turned
          chevron says it is open, and a fill reads as a stuck press. */}
      <Button
        slot="trigger"
        variant="ghost"
        size="sm"
        className="-ml-3 aria-expanded:not-hover:bg-transparent"
      >
        <ChevronRightIcon
          aria-hidden="true"
          className="transition-transform duration-150 ease-standard group-data-expanded/details:rotate-90 motion-reduce:transition-none"
        />
        {showDetails}
      </Button>
      <DisclosurePanel className="flex flex-col gap-2 pt-1">
        {details.map((group, at) => (
          <div key={group.heading} className="flex flex-col gap-0.5">
            {/* Body weight under the toast's 600 title, its items muted
                under it (DESIGN.md "Type"); it names its list. */}
            {/* Heard once, as its list's name. */}
            <p id={`${id}-${at}`} aria-hidden="true">
              {group.heading}
            </p>
            <ul aria-labelledby={`${id}-${at}`} className="flex flex-col text-muted-foreground">
              {group.items.map((item, index) => (
                // oxlint-disable-next-line react/no-array-index-key -- two conversations can share a subject and account; the list never reorders.
                <li key={`${index} ${item}`} dir="auto" className="break-words">
                  {item}
                </li>
              ))}
            </ul>
          </div>
        ))}
      </DisclosurePanel>
    </Disclosure>
  );
}
