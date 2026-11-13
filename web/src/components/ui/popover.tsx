import { cn } from "@/lib/cn";
import {
  Dialog,
  DialogTrigger,
  Heading,
  Popover as PopoverPrimitive,
  type DialogProps,
  type DialogTriggerProps,
  type PopoverProps as PopoverPrimitiveProps,
} from "react-aria-components";

// PopoverTrigger pairs a React Aria button with the Popover it opens.
function PopoverTrigger(props: DialogTriggerProps) {
  return <DialogTrigger {...props} />;
}

// Popover is a small dialog beside its trigger, named by aria-label, e.g.
// "Member menu". children may be a function of close, which a link in it
// calls so the popover closes as the page changes; close works only inside
// a PopoverTrigger, which holds the open state.
//
// DESIGN: inbok's copy differs from shadcn's: the popover holds a dialog,
// so it can mix links, buttons, and a toggle group, which a menu cannot;
// it uses DESIGN.md's menu surface (popover shadow, hairline, about 10 px
// radius), outlined in forced colours, which drop shadows, and opens in
// 200 ms on its easing.
function Popover({
  className,
  placement = "bottom",
  offset = 4,
  crossOffset = 0,
  children,
  "aria-label": label,
  ...props
}: Omit<PopoverPrimitiveProps, "className" | "children"> & {
  className?: string;
  children: DialogProps["children"];
  "aria-label": string;
}) {
  return (
    <PopoverPrimitive
      placement={placement}
      offset={offset}
      crossOffset={crossOffset}
      // The duration is for opening and closing only: with no transition, the
      // position React Aria sets as it follows the trigger moves at once.
      className={cn(
        "w-64 origin-(--trigger-anchor-point) rounded-xl bg-popover p-1 text-sm text-popover-foreground shadow-popover ring-1 ring-border transition-none duration-200 ease-standard outline-none data-entering:animate-in data-entering:fade-in-0 data-entering:zoom-in-95 data-exiting:animate-out data-exiting:fade-out-0 data-exiting:zoom-out-95 data-[placement=bottom]:slide-in-from-top-2 data-[placement=left]:slide-in-from-right-2 data-[placement=right]:slide-in-from-left-2 data-[placement=top]:slide-in-from-bottom-2 forced-colors:outline-1 forced-colors:outline-solid",
        className,
      )}
      {...props}
    >
      <Dialog aria-label={label} className="flex flex-col gap-0.5 outline-none">
        {children}
      </Dialog>
    </PopoverPrimitive>
  );
}

// PopoverHeader holds a popover's title and description at its top, on
// the rows' inset.
function PopoverHeader({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="popover-header"
      className={cn("flex flex-col gap-0.5 px-2 py-1.5 text-sm", className)}
      {...props}
    />
  );
}

// PopoverTitle is a popover's heading. The popover keeps its aria-label,
// which names it for screen readers.
function PopoverTitle({ className, ...props }: React.ComponentProps<typeof Heading>) {
  return <Heading data-slot="popover-title" className={cn("font-medium", className)} {...props} />;
}

function PopoverDescription({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="popover-description"
      className={cn("text-muted-foreground", className)}
      {...props}
    />
  );
}

export { Popover, PopoverDescription, PopoverHeader, PopoverTitle, PopoverTrigger };
