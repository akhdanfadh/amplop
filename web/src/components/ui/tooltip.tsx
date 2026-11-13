import * as React from "react";
import { cn } from "@/lib/cn";
import {
  OverlayArrow,
  Tooltip as TooltipPrimitive,
  TooltipTrigger as TooltipTriggerPrimitive,
} from "react-aria-components";

// Where the arrow sits for each side of its trigger the tooltip opens on:
// moved half out over the tooltip's edge, turned into a diamond's point.
const arrowTransform = {
  bottom: "translate(-50%, calc(50% + 2px)) rotate(45deg)",
  top: "translate(-50%, calc(-50% - 2px)) rotate(45deg)",
  left: "translate(calc(-50% - 2px), -50%) rotate(45deg)",
  right: "translate(calc(50% + 2px), -50%) rotate(45deg)",
};

// restMs is how long the pointer rests on a trigger before the first
// tooltip shows (DESIGN.md "Motion").
const restMs = 500;

// TooltipTrigger pairs a React Aria button or link with its Tooltip. The
// first tooltip shows after the pointer rests on its trigger for delay ms,
// restMs by default; while one is showing, and for 500 ms after it closes,
// the next shows at once. Keyboard focus shows it at once.
//
// DESIGN: inbok's copy differs from shadcn's: the trigger is a React Aria
// button or link as is, without Focusable, since those take the trigger's
// props themselves; React Aria's warm-up instead of shadcn's no delay, so
// a pointer crossing the rail does not flash every icon's name; it opens
// in 200 ms on DESIGN.md's easing; the arrow is placed by one transform;
// no styling for a kbd inside, since a shortcut is written in words
// (DESIGN.md "Wording"). A tooltip holds no words found nowhere else:
// inside a dialog or drawer it is outside the modal, so assistive
// technology never reads it.
function TooltipTrigger({
  delay = restMs,
  children,
  ...props
}: React.ComponentProps<typeof TooltipTriggerPrimitive>) {
  return (
    <TooltipTriggerPrimitive delay={delay} {...props}>
      {children}
    </TooltipTriggerPrimitive>
  );
}

// Tooltip is the label a TooltipTrigger shows, e.g. an icon button's name.
function Tooltip({
  className,
  placement = "top",
  offset = 4,
  crossOffset = 0,
  children,
  ...props
}: Omit<React.ComponentProps<typeof TooltipPrimitive>, "children" | "className"> & {
  className?: string;
  children?: React.ReactNode;
}) {
  return (
    <TooltipPrimitive
      data-slot="tooltip-content"
      placement={placement}
      offset={offset}
      crossOffset={crossOffset}
      // The duration is for opening and closing only: with no transition, the
      // position React Aria sets as it follows the trigger moves at once.
      className={cn(
        "inline-flex w-fit max-w-xs origin-(--trigger-anchor-point) items-center gap-1.5 rounded-md bg-foreground px-3 py-1.5 text-xs text-background transition-none duration-200 ease-standard data-entering:animate-in data-entering:fade-in-0 data-entering:zoom-in-95 data-exiting:animate-out data-exiting:fade-out-0 data-exiting:zoom-out-95 data-[placement=bottom]:slide-in-from-top-2 data-[placement=left]:slide-in-from-right-2 data-[placement=right]:slide-in-from-left-2 data-[placement=top]:slide-in-from-bottom-2 forced-colors:outline-1 forced-colors:outline-solid",
        className,
      )}
      {...props}
    >
      {children}
      <OverlayArrow
        className="size-2.5 rounded-xs bg-foreground fill-foreground"
        style={({ placement: side, defaultStyle }) => ({
          ...defaultStyle,
          // NOTE: set after React Aria's own style, which has no transform
          // today; one in an upgrade would be replaced here.
          transform: side && side !== "center" ? arrowTransform[side] : undefined,
        })}
      />
    </TooltipPrimitive>
  );
}

export { Tooltip, TooltipTrigger };
