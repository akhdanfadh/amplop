import * as React from "react";
import { cn } from "cn";
import {
  Dialog,
  DialogTrigger,
  Heading,
  Modal,
  ModalOverlay,
  OverlayTriggerStateContext,
  Text,
  type DialogTriggerProps,
  type ModalOverlayProps,
  type PressEvent,
} from "react-aria-components";

import { Button, type ButtonProps } from "@/components/ui/button";

// The longest double-click a pointer press may belong to, in milliseconds:
// Windows' default double-click time, which macOS's default does not exceed.
const doubleClickTime = 500;

// When the open dialog appeared, as performance.now(); read by its action.
const OpenedAtContext = React.createContext<React.RefObject<number> | null>(null);

// AlertDialogTrigger opens the AlertDialog among its children when the
// Button among them is pressed, and returns focus to that button on close.
function AlertDialogTrigger(props: DialogTriggerProps) {
  return <DialogTrigger {...props} />;
}

// AlertDialog is a confirmation over a dimmed page. Control it with isOpen
// and onOpenChange, or place it in an AlertDialogTrigger. Escape cancels; a
// press outside does not. On close, focus returns to where it was before.
function AlertDialog({
  className,
  children,
  isDismissable = false,
  ...props
}: Omit<ModalOverlayProps, "className" | "children"> & {
  className?: string;
  children: React.ReactNode;
}) {
  return (
    <ModalOverlay
      data-slot="alert-dialog-overlay"
      isDismissable={isDismissable}
      // A tall dialog scrolls here; past the end the page behind stays still,
      // as React Aria's iOS scroll lock would have it (web/README.md).
      className="fixed inset-0 isolate z-50 flex overflow-y-auto overscroll-contain bg-black/30 p-4 duration-250 ease-standard data-entering:animate-in data-entering:fade-in-0 data-exiting:animate-out data-exiting:fade-out-0 dark:bg-black/60"
      {...props}
    >
      <Modal
        data-slot="alert-dialog-content"
        className={cn(
          "m-auto w-full max-w-sm rounded-2xl bg-popover text-popover-foreground shadow-dialog ring-1 ring-border duration-250 ease-standard outline-none data-entering:animate-in data-entering:fade-in-0 data-entering:zoom-in-95 data-exiting:animate-out data-exiting:fade-out-0 data-exiting:zoom-out-95 motion-reduce:data-entering:zoom-in-100 motion-reduce:data-exiting:zoom-out-100",
          className,
        )}
      >
        <Dialog
          data-slot="alert-dialog"
          role="alertdialog"
          className="flex flex-col gap-6 p-6 outline-none"
        >
          <OpenedAt>{children}</OpenedAt>
        </Dialog>
      </Modal>
    </ModalOverlay>
  );
}

// OpenedAt records when the dialog last opened for its AlertDialogAction,
// including a reopen while it was still closing.
function OpenedAt({ children }: { children: React.ReactNode }) {
  const isOpen = React.use(OverlayTriggerStateContext)?.isOpen;
  const openedAt = React.useRef(0);
  React.useEffect(() => {
    if (isOpen) openedAt.current = performance.now();
  }, [isOpen]);
  return <OpenedAtContext value={openedAt}>{children}</OpenedAtContext>;
}

function AlertDialogHeader({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="alert-dialog-header"
      className={cn("flex flex-col gap-2", className)}
      {...props}
    />
  );
}

function AlertDialogFooter({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="alert-dialog-footer"
      className={cn("flex flex-col-reverse gap-2 sm:flex-row sm:justify-end", className)}
      {...props}
    />
  );
}

// AlertDialogTitle names the dialog; React Aria points the dialog's
// aria-labelledby at it.
function AlertDialogTitle({
  className,
  ...props
}: Omit<React.ComponentProps<typeof Heading>, "slot">) {
  return (
    <Heading
      slot="title"
      data-slot="alert-dialog-title"
      className={cn("text-lg font-semibold", className)}
      {...props}
    />
  );
}

// AlertDialogDescription states the consequence; React Aria points the
// dialog's aria-describedby at it, so it is read when the dialog opens.
function AlertDialogDescription({
  className,
  ...props
}: Omit<React.ComponentProps<typeof Text>, "slot">) {
  return (
    <Text
      slot="description"
      elementType="p"
      data-slot="alert-dialog-description"
      className={cn("text-sm text-muted-foreground", className)}
      {...props}
    />
  );
}

// AlertDialogAction is the confirming button; it closes the dialog after
// onPress. It ignores a mouse or touch press that starts within a
// double-click of the dialog opening, and the dialog stays open: the second
// click of a double-click on the button that opened the dialog can land on
// it. A keyboard press is kept.
function AlertDialogAction({ className, onPressStart, onPress, ...props }: ButtonProps) {
  const openedAt = React.use(OpenedAtContext);
  const dialog = React.use(OverlayTriggerStateContext);
  const early = React.useRef(false);
  return (
    <Button
      data-slot="alert-dialog-action"
      className={className}
      onPressStart={(event) => {
        early.current = isEarly(event, openedAt);
        onPressStart?.(event);
      }}
      onPress={(event) => {
        if (early.current) return;
        onPress?.(event);
        dialog?.close();
      }}
      {...props}
    />
  );
}

// isEarly reports whether a mouse or touch press started within
// doubleClickTime of the dialog opening at openedAt.
function isEarly(event: PressEvent, openedAt: React.RefObject<number> | null): boolean {
  if (event.pointerType === "keyboard" || event.pointerType === "virtual") return false;
  if (!openedAt) return false;
  return performance.now() - openedAt.current < doubleClickTime;
}

// AlertDialogCancel is the safe choice; it closes the dialog and takes focus
// when the dialog opens.
function AlertDialogCancel({ className, variant = "outline", ...props }: ButtonProps) {
  return (
    <Button
      slot="close"
      // The WAI-ARIA alert dialog pattern focuses the least destructive
      // action when the dialog opens; the dialog itself asks for it.
      // oxlint-disable-next-line jsx-a11y/no-autofocus
      autoFocus
      data-slot="alert-dialog-cancel"
      className={className}
      variant={variant}
      {...props}
    />
  );
}

export {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
};
