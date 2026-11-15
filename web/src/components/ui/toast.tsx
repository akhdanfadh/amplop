import { useCallback, type ReactElement } from "react";
import {
  Text,
  UNSTABLE_Toast as ToastPrimitive,
  UNSTABLE_ToastContent as ToastContentPrimitive,
  UNSTABLE_ToastQueue as ToastQueue,
  UNSTABLE_ToastRegion as ToastRegionPrimitive,
} from "react-aria-components";
import { CircleAlertIcon, XIcon } from "lucide-react";
import { Button } from "@/components/ui/button";

// NOTE: React Aria's toast is still alpha, hence UNSTABLE_; its API may
// change in any upgrade, so recheck this file and its tests after one.

// ToastMessage is what one toast says: title, what failed, e.g. "Couldn't
// archive the conversation"; and description, an optional sentence, the
// fix. An action's Undo is not a toast: it has its own bar (DESIGN.md
// "Feedback").
type ToastMessage = {
  title: string;
  description?: string;
};

// The app's one queue. React Aria shows the newest toast first.
// No limit, so every toast shows at once: an error toast stays until
// closed, and a limit would hide later ones.
const queue = new ToastQueue<ToastMessage>();

// showError shows title, what failed, and description, the fix. It stays
// until the member closes it. An error already showing is not shown again.
export function showError(title: string, description?: string): void {
  // With no limit, a failure repeated, e.g. every action while offline,
  // would stack copies the member must close one by one.
  const shown = queue.visibleToasts.some(
    ({ content }) => content.title === title && content.description === description,
  );
  if (shown) return;
  // No timeout, so the fix can't vanish before it is read.
  queue.add({ title, description });
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
          className="dark flex shrink-0 animate-in items-center gap-3 rounded-xl bg-popover p-3 text-sm text-popover-foreground ring-1 ring-border transition-none duration-200 ease-standard fade-in-0 outline-none data-focus-visible:focus-ring-inset forced-colors:outline-1 forced-colors:outline-solid"
        >
          <CircleAlertIcon
            aria-hidden="true"
            className="size-4 shrink-0 text-destructive forced-colors:text-[CanvasText]"
          />
          <ToastContentPrimitive className="flex min-w-0 flex-1 flex-col gap-0.5 py-0.5">
            <Text slot="title" className="font-semibold">
              {toast.content.title}
            </Text>
            {toast.content.description && (
              <Text slot="description">{toast.content.description}</Text>
            )}
          </ToastContentPrimitive>
          {/* React Aria names it "Close", in the locale the root sets. */}
          <Button slot="close" variant="ghost" size="icon-sm">
            <XIcon aria-hidden="true" />
          </Button>
        </ToastPrimitive>
      )}
    </ToastRegionPrimitive>
  );
}
