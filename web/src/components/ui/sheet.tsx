import * as React from "react";
import { cn } from "@/lib/cn";
import {
  Heading,
  ModalOverlay as ModalOverlayPrimitive,
  Modal as ModalPrimitive,
  Dialog as SheetPrimitive,
  DialogTrigger as SheetTriggerPrimitive,
  type ModalOverlayProps as ModalOverlayPrimitiveProps,
  type DialogProps as SheetPrimitiveProps,
  type DialogTriggerProps as SheetTriggerPrimitiveProps,
} from "react-aria-components";

import { Button } from "@/components/ui/button";
import { XIcon } from "lucide-react";

function SheetTrigger({ ...props }: SheetTriggerPrimitiveProps) {
  return <SheetTriggerPrimitive {...props} />;
}

function SheetClose({
  className,
  variant = "outline",
  size = "default",
  ...props
}: React.ComponentProps<typeof Button>) {
  return (
    <Button
      slot="close"
      data-slot="sheet-close"
      variant={variant}
      size={size}
      className={className}
      {...props}
    />
  );
}

function SheetOverlay({
  className,
  children,
  ...props
}: Omit<ModalOverlayPrimitiveProps, "className" | "children"> & {
  className?: string;
  children: React.ReactNode;
}) {
  return (
    <ModalOverlayPrimitive
      data-slot="sheet-overlay"
      isDismissable
      className={cn(
        "fixed inset-0 z-50 bg-scrim transition-opacity duration-250 ease-standard data-entering:opacity-0 data-exiting:opacity-0",
        className,
      )}
      {...props}
    >
      {children}
    </ModalOverlayPrimitive>
  );
}

// Sheet is a dialog that slides in from a side of the window, e.g. the
// sidebar's drawer. Its width on the left or right comes from
// --sheet-width, else 75% of the window up to 24rem.
//
// DESIGN: inbok's copy differs from shadcn's: the scrim, shadow, easing,
// and duration are DESIGN.md's dialog ones; it slides only where motion is
// welcome; the title is a dialog title (18/24, 600). It stays on React
// Aria's ModalOverlay: React Aria's own Sheet (react-aria-components 1.22)
// adds swiping but is new and built on scroll-driven animations, which not
// every browser has yet (ADR031 follow-ups).
function Sheet({
  className,
  children,
  side = "right",
  showCloseButton = true,
  ...props
}: Omit<ModalOverlayPrimitiveProps, "className" | "children"> &
  Pick<React.ComponentProps<typeof ModalPrimitive>, "isDismissable"> & {
    className?: string;
    children: React.ReactNode;
    side?: "top" | "right" | "bottom" | "left";
    showCloseButton?: boolean;
  }) {
  return (
    <SheetOverlay {...props}>
      <ModalPrimitive
        data-slot="sheet-content"
        data-side={side}
        className={cn(
          "fixed z-50 flex flex-col gap-4 bg-popover bg-clip-padding text-sm text-popover-foreground shadow-dialog transition-[opacity,translate] duration-250 ease-standard data-entering:opacity-0 data-exiting:opacity-0",
          "data-[side=bottom]:inset-x-0 data-[side=bottom]:bottom-0 data-[side=bottom]:h-auto data-[side=bottom]:border-t data-[side=top]:inset-x-0 data-[side=top]:top-0 data-[side=top]:h-auto data-[side=top]:border-b",
          "data-[side=left]:inset-y-0 data-[side=left]:left-0 data-[side=left]:h-full data-[side=left]:w-(--sheet-width,min(75%,24rem)) data-[side=left]:border-r data-[side=right]:inset-y-0 data-[side=right]:right-0 data-[side=right]:h-full data-[side=right]:w-(--sheet-width,min(75%,24rem)) data-[side=right]:border-l",
          // It slides in only where motion is welcome; otherwise it fades.
          "motion-safe:data-[side=bottom]:data-entering:translate-y-10 motion-safe:data-[side=bottom]:data-exiting:translate-y-10 motion-safe:data-[side=left]:data-entering:-translate-x-10 motion-safe:data-[side=left]:data-exiting:-translate-x-10 motion-safe:data-[side=right]:data-entering:translate-x-10 motion-safe:data-[side=right]:data-exiting:translate-x-10 motion-safe:data-[side=top]:data-entering:-translate-y-10 motion-safe:data-[side=top]:data-exiting:-translate-y-10",
          className,
        )}
      >
        <SheetPrimitive
          data-slot="sheet"
          className="[display:inherit] h-full max-h-[inherit] [flex-direction:inherit] gap-[inherit] outline-none"
        >
          {children}
          {showCloseButton && (
            <SheetClose variant="ghost" className="absolute top-3 right-3" size="icon-sm">
              <XIcon aria-hidden="true" />
              <span className="sr-only">Close</span>
            </SheetClose>
          )}
        </SheetPrimitive>
      </ModalPrimitive>
    </SheetOverlay>
  );
}

// SheetContent is Sheet under the name shadcn's Radix sheets use, so code
// written for those reads the same.
function SheetContent(props: React.ComponentProps<typeof Sheet>) {
  return <Sheet {...props} />;
}

function SheetHeader({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="sheet-header"
      className={cn("flex flex-col gap-0.5 p-4", className)}
      {...props}
    />
  );
}

function SheetFooter({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="sheet-footer"
      className={cn("mt-auto flex flex-col gap-2 p-4", className)}
      {...props}
    />
  );
}

function SheetTitle({ className, ...props }: Omit<React.ComponentProps<typeof Heading>, "slot">) {
  return (
    <Heading
      slot="title"
      data-slot="sheet-title"
      className={cn("text-lg font-semibold text-foreground", className)}
      {...props}
    />
  );
}

function SheetDescription({ className, ...props }: Omit<React.ComponentProps<"div">, "slot">) {
  return (
    <div
      data-slot="sheet-description"
      className={cn("text-sm text-muted-foreground", className)}
      {...props}
    />
  );
}

export {
  type SheetPrimitiveProps,
  type SheetTriggerPrimitiveProps,
  Sheet,
  SheetTrigger,
  SheetClose,
  SheetContent,
  SheetHeader,
  SheetFooter,
  SheetTitle,
  SheetDescription,
};
