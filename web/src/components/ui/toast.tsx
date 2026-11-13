import type { ReactElement } from "react";
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

// DESIGN.md "Feedback": an Undo toast closes itself after 10 s; React Aria
// pauses the timer while the pointer is over a toast or focus is in one.
const undoTimeout = 10_000;

// ToastMessage is what one toast says: title, the result or the failure,
// e.g. "Conversation archived"; description, an optional second sentence,
// e.g. the fix; and an optional action, e.g. Undo.
type ToastMessage = {
  tone: "result" | "error";
  title: string;
  description?: string;
  action?: { label: string; onAction: () => void };
};

// The app's one queue. React Aria shows the newest toast first.
// No limit, so every toast shows at once: an error toast stays until
// closed, and a limit would hide later ones.
const queue = new ToastQueue<ToastMessage>();

// showUndo shows title, the result of an action, with an Undo button that
// runs onUndo and closes the toast. It closes itself after 10 s.
export function showUndo(title: string, onUndo: () => void): void {
  queue.add(
    { tone: "result", title, action: { label: "Undo", onAction: onUndo } },
    { timeout: undoTimeout },
  );
}

// showError shows title, what failed, and description, the fix. It stays
// until the member closes it. An error already showing is not shown again.
export function showError(title: string, description?: string): void {
  // With no limit, a failure repeated, e.g. every action while offline,
  // would stack copies the member must close one by one.
  const shown = queue.visibleToasts.some(
    ({ content }) =>
      content.tone === "error" && content.title === title && content.description === description,
  );
  if (shown) return;
  // No timeout, so the fix can't vanish before it is read.
  queue.add({ tone: "error", title, description });
}

// closeToasts closes every toast, e.g. on sign-out, when no toast should
// outlive the member it was for.
export function closeToasts(): void {
  queue.clear();
}

// Toaster is the region every toast appears in, bottom right (bottom on a
// phone), above any open dialog or drawer. F6 moves focus into it and
// back. Render it once, in the root.
export function Toaster(): ReactElement {
  return (
    // z-60, above a dialog's or drawer's scrim (z-50), so a toast shown
    // before one opened stays in reach.
    <ToastRegionPrimitive
      queue={queue}
      className="fixed right-4 bottom-4 left-4 z-60 flex flex-col gap-2 rounded-xl outline-none data-focus-visible:focus-ring sm:left-auto sm:w-sm"
    >
      {({ toast }) => (
        <ToastPrimitive
          toast={toast}
          // A dark surface in both themes (DESIGN.md "Components"), so the
          // toast stands apart from the page: the dark class gives it, and
          // everything in it, the dark theme's tokens. Its focus ring sits
          // inside, on that surface: outside, the dark theme's ring colour
          // would be too light on a light page.
          className="dark flex items-start gap-3 rounded-xl bg-popover p-3 text-sm text-popover-foreground ring-1 ring-border outline-none data-focus-visible:focus-ring-inset forced-colors:outline-1 forced-colors:outline-solid"
        >
          {toast.content.tone === "error" && (
            <CircleAlertIcon
              aria-hidden="true"
              className="mt-0.5 size-4 shrink-0 text-destructive forced-colors:text-[CanvasText]"
            />
          )}
          <ToastContentPrimitive className="flex min-w-0 flex-1 flex-col gap-0.5 py-0.5">
            <Text slot="title" className="font-semibold">
              {toast.content.title}
            </Text>
            {toast.content.description && (
              <Text slot="description">{toast.content.description}</Text>
            )}
          </ToastContentPrimitive>
          {toast.content.action && (
            <Button
              variant="ghost"
              size="sm"
              onPress={() => {
                toast.content.action?.onAction();
                queue.close(toast.key);
              }}
            >
              {toast.content.action.label}
            </Button>
          )}
          {/* React Aria names it "Close", in the locale the root sets. */}
          <Button slot="close" variant="ghost" size="icon-sm">
            <XIcon aria-hidden="true" />
          </Button>
        </ToastPrimitive>
      )}
    </ToastRegionPrimitive>
  );
}
