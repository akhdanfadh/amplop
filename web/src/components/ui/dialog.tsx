import * as React from "react";
import { cn } from "@/lib/cn";
import {
  Dialog as DialogPrimitive,
  DialogTrigger as DialogTriggerPrimitive,
  Heading,
  ModalOverlay as ModalOverlayPrimitive,
  Modal as ModalPrimitive,
  type DialogProps as DialogPrimitiveProps,
  type DialogTriggerProps as DialogTriggerPrimitiveProps,
  type ModalOverlayProps as ModalOverlayPrimitiveProps,
} from "react-aria-components";

import { Button } from "@/components/ui/button";
import { XIcon } from "lucide-react";

function DialogTrigger({ ...props }: DialogTriggerPrimitiveProps) {
  return <DialogTriggerPrimitive data-slot="dialog-trigger" {...props} />;
}

function DialogClose({
  className,
  variant = "outline",
  size = "default",
  ...props
}: React.ComponentProps<typeof Button>) {
  return (
    <Button
      slot="close"
      data-slot="dialog-close"
      variant={variant}
      size={size}
      className={className}
      {...props}
    />
  );
}

function DialogOverlay({
  className,
  children,
  ...props
}: Omit<ModalOverlayPrimitiveProps, "className" | "children"> & {
  className?: string;
  children: React.ReactNode;
}) {
  return (
    <ModalOverlayPrimitive
      data-slot="dialog-overlay"
      // A tall dialog scrolls here; past the end the page behind stays still,
      // as React Aria's iOS scroll lock would have it (web/README.md).
      className={cn(
        "fixed inset-0 isolate z-50 flex overflow-y-auto overscroll-contain bg-scrim p-4 duration-250 ease-standard data-entering:animate-in data-entering:fade-in-0 data-exiting:animate-out data-exiting:fade-out-0",
        className,
      )}
      {...props}
    >
      {children}
    </ModalOverlayPrimitive>
  );
}

// Dialog is a short form over a dimmed page, e.g. New label (DESIGN.md
// "Components"). Control it with isOpen and onOpenChange, or place it in a
// DialogTrigger. Escape closes it; a press outside does not, unless
// isDismissable, so a stray press keeps what the member typed. On close,
// focus returns to where it was before.
//
// DESIGN: inbok's copy differs from shadcn's: the scrim, shadow, radius,
// padding, easing, and duration are the alert dialog's (alert-dialog.tsx),
// so both read as one kind of surface; it centres in a scrolling overlay
// rather than by a transform, so a tall one scrolls; no close button by
// default, as its form has Cancel.
function Dialog({
  className,
  children,
  showCloseButton = false,
  isDismissable = false,
  ...props
}: Omit<ModalOverlayPrimitiveProps, "className" | "children"> &
  Pick<React.ComponentProps<typeof ModalPrimitive>, "isDismissable"> & {
    className?: string;
    children: React.ReactNode;
    showCloseButton?: boolean;
  }) {
  return (
    <DialogOverlay isDismissable={isDismissable} {...props}>
      <ModalPrimitive
        data-slot="dialog-content"
        className={cn(
          "relative m-auto w-full max-w-sm rounded-2xl bg-popover text-popover-foreground shadow-dialog ring-1 ring-border duration-250 ease-standard outline-none data-entering:animate-in data-entering:fade-in-0 data-entering:zoom-in-95 data-exiting:animate-out data-exiting:fade-out-0 data-exiting:zoom-out-95 motion-reduce:data-entering:zoom-in-100 motion-reduce:data-exiting:zoom-out-100",
          className,
        )}
      >
        <DialogPrimitive data-slot="dialog" className="flex flex-col gap-6 p-6 outline-none">
          {children}
          {showCloseButton && (
            <DialogClose variant="ghost" className="absolute top-2 right-2" size="icon-sm">
              <XIcon aria-hidden="true" />
              <span className="sr-only">Close</span>
            </DialogClose>
          )}
        </DialogPrimitive>
      </ModalPrimitive>
    </DialogOverlay>
  );
}

function DialogHeader({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div data-slot="dialog-header" className={cn("flex flex-col gap-2", className)} {...props} />
  );
}

function DialogFooter({
  className,
  showCloseButton = false,
  children,
  ...props
}: React.ComponentProps<"div"> & {
  showCloseButton?: boolean;
}) {
  return (
    <div
      data-slot="dialog-footer"
      className={cn("flex flex-col-reverse gap-2 sm:flex-row sm:justify-end", className)}
      {...props}
    >
      {children}
      {showCloseButton && <DialogClose variant="outline">Close</DialogClose>}
    </div>
  );
}

// DialogTitle names the dialog, as a dialog title (18/24, 600); React Aria
// points the dialog's aria-labelledby at it.
function DialogTitle({ className, ...props }: Omit<React.ComponentProps<typeof Heading>, "slot">) {
  return (
    <Heading
      slot="title"
      data-slot="dialog-title"
      className={cn("text-lg font-semibold", className)}
      {...props}
    />
  );
}

function DialogDescription({ className, ...props }: Omit<React.ComponentProps<"div">, "slot">) {
  return (
    <div
      data-slot="dialog-description"
      className={cn(
        "text-sm text-muted-foreground *:[a]:underline *:[a]:underline-offset-3 *:[a]:hover:text-foreground",
        className,
      )}
      {...props}
    />
  );
}

export {
  type DialogPrimitiveProps,
  type DialogTriggerPrimitiveProps,
  Dialog,
  DialogClose,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogOverlay,
  DialogTitle,
  DialogTrigger,
};
