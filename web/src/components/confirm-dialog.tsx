import { useEffect, useRef, type ReactElement, type ReactNode } from "react";
import { cn } from "@/lib/cn";
import { RequestError } from "@/components/request-error";
import { Spinner } from "@/components/ui/spinner";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";

// Failure is why a confirmed change failed: the message, and a request
// reference for a fault.
export type Failure = { message: string; requestId?: string };

// ConfirmDialog asks before a change, then holds it until the server answers
// (DESIGN.md "Feedback"): action names the confirming button, working
// its label while pending, and tone "destructive" colours it for a change
// that takes something away. children follow the description, e.g. a list
// of what else the change takes. onConfirm starts the change; the caller closes
// the dialog through onOpenChange once it is saved, or passes failure, shown
// inside, where pressing again retries. While pending, Cancel and Escape
// wait. fallbackFocus returns where focus goes when the dialog has closed and
// the button that opened it went away with the change, e.g. Close on a
// paused registration.
export function ConfirmDialog({
  isOpen,
  onOpenChange,
  title,
  description,
  action,
  working,
  tone = "default",
  pending,
  failure,
  onConfirm,
  fallbackFocus,
  children,
}: {
  isOpen: boolean;
  onOpenChange: (isOpen: boolean) => void;
  title: ReactNode;
  description: string;
  action: string;
  working: string;
  tone?: "default" | "destructive";
  pending: boolean;
  failure: Failure | undefined;
  onConfirm: () => void;
  fallbackFocus?: () => HTMLElement | null;
  children?: ReactNode;
}): ReactElement {
  return (
    <AlertDialog
      isOpen={isOpen}
      // A change already sent can't be called back, so the dialog stays
      // until its answer says what happened.
      isKeyboardDismissDisabled={pending}
      onOpenChange={(open) => {
        if (!open && pending) return;
        onOpenChange(open);
      }}
    >
      <FocusFallback target={fallbackFocus} />
      <AlertDialogHeader>
        <AlertDialogTitle className="wrap-anywhere">{title}</AlertDialogTitle>
        {/* It can name what it acts on, e.g. a label, which wraps. */}
        <AlertDialogDescription className="wrap-anywhere">{description}</AlertDialogDescription>
      </AlertDialogHeader>
      {children}
      <RequestError
        errors={failure ? [{ message: failure.message }] : []}
        requestId={failure?.requestId}
      />
      {/* Side by side, a short Cancel keeps its height beside an action
          whose long label wraps. */}
      <AlertDialogFooter className="sm:items-center">
        <AlertDialogCancel isDisabled={pending}>Cancel</AlertDialogCancel>
        <AlertDialogAction
          type="button"
          closes="by-caller"
          variant={tone}
          // Its label can name what it acts on, e.g. a long account name,
          // which wraps rather than leave the dialog: a button keeps its
          // width and one line by default.
          className="min-w-0 shrink wrap-anywhere whitespace-normal"
          isPending={pending}
          onPress={onConfirm}
        >
          {/* Both labels share one cell, so the button keeps the wider
              one's width and nothing moves under the pointer; the hidden
              one is out of the accessible name. */}
          <span className="grid *:col-start-1 *:row-start-1">
            <span className={cn(pending && "invisible")}>{action}</span>
            {/* Turns while the request runs, as a sending form's button does. */}
            <span className={cn("flex items-center gap-1.5", !pending && "invisible")}>
              <Spinner />
              {working}
            </span>
          </span>
        </AlertDialogAction>
      </AlertDialogFooter>
    </AlertDialog>
  );
}

// FocusFallback focuses what target returns once the dialog it sits in has
// gone, when focus went with it: React Aria returns focus to the element
// that had it before the dialog opened, and to nothing when that element
// was removed meanwhile (FocusScope, restoreFocus).
export function FocusFallback({ target }: { target?: () => HTMLElement | null }): null {
  const latest = useRef(target);

  useEffect(() => {
    latest.current = target;
  });

  useEffect(
    () => () => {
      // NOTE: React Aria restores focus in a layout effect's cleanup and one
      // animation frame after it (react-aria FocusScope, useRestoreFocus),
      // which its docs do not promise; two frames later its outcome is
      // settled.
      requestAnimationFrame(() =>
        requestAnimationFrame(() => {
          const active = document.activeElement;
          if (active && active !== document.body) return;
          latest.current?.()?.focus();
        }),
      );
    },
    [],
  );

  return null;
}
