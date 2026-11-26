import type * as React from "react";
import { cn } from "@/lib/cn";
import {
  type Key,
  ToggleButton as ToggleButtonPrimitive,
  ToggleButtonGroup as ToggleButtonGroupPrimitive,
  type ToggleButtonGroupProps,
  type ToggleButtonProps,
} from "react-aria-components";

// ToggleGroup is a row of choices, one or several picked, e.g. Light, Dark,
// and System. aria-label names the choice.
//
// DESIGN: amplop's copy differs from shadcn's: one look, a segmented
// control (the theme choice), rather than variants and sizes; items are
// 32 px high (44 px coarse); the chosen item has the selection tint and a
// field-coloured outline, not a fill alone, as the current sidebar item
// has its tint and a bar; in forced colours it takes the system's
// highlight, which keeps the app's ring colour too, so the focus ring
// takes the system's text colour there; DESIGN.md's focus ring; no
// separate toggle. Its keys are
// React Aria's toolbar keys, though one-of-several is announced as radios:
// the group is one Tab stop, and the arrows move between items without
// choosing; Space or Enter chooses. A RadioGroup has the radio keys, but
// its items take no tooltip, which an item with only an icon needs.
function ToggleGroup({
  className,
  ...props
}: Omit<ToggleButtonGroupProps, "className"> & { className?: string }) {
  return (
    <ToggleButtonGroupPrimitive
      className={cn("inline-flex w-fit gap-0.5 rounded-lg bg-muted p-0.5", className)}
      {...props}
    />
  );
}

// ToggleGroupItem is one choice; id is the key the group reports. One
// with only an icon needs aria-label, and a tooltip with the same name.
function ToggleGroupItem({
  className,
  ...props
}: Omit<ToggleButtonProps, "className" | "id"> & {
  // Without an id React Aria treats the item as a toggle of its own,
  // outside the group's choice.
  id: Key;
  className?: string;
  children?: React.ReactNode;
}) {
  return (
    <ToggleButtonPrimitive
      className={cn(
        "inline-flex h-8 min-w-8 items-center justify-center gap-1.5 rounded-md px-2 text-sm font-medium text-muted-foreground transition-colors outline-none hover:text-foreground data-focus-visible:focus-ring pointer-coarse:h-11 pointer-coarse:min-w-11 data-selected:bg-selection data-selected:text-foreground data-selected:ring-1 data-selected:ring-input forced-colors:data-selected:bg-[Highlight] forced-colors:data-selected:text-[HighlightText] forced-colors:data-selected:forced-color-adjust-none forced-colors:data-selected:[--ring:CanvasText] [&_svg]:size-4 [&_svg]:shrink-0",
        className,
      )}
      {...props}
    />
  );
}

export { ToggleGroup, ToggleGroupItem };
