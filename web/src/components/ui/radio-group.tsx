import type * as React from "react";
import { cn } from "@/lib/cn";
import {
  RadioButton,
  RadioField,
  RadioGroup as RadioGroupPrimitive,
  type RadioFieldProps,
  type RadioGroupProps,
} from "react-aria-components";

function RadioGroup({
  className,
  ...props
}: Omit<RadioGroupProps, "className"> & { className?: string }) {
  return (
    <RadioGroupPrimitive
      data-slot="radio-group"
      className={cn("flex w-full flex-col gap-1", className)}
      {...props}
    />
  );
}

// RadioGroupItem is a dot and its label: children are the label, inside the
// clickable row; className styles that row. Props go to React Aria's
// RadioField, whose hidden input takes id and the aria-* props.
//
// DESIGN: aria-nova still ships React Aria's deprecated Radio, so this
// builds on RadioField and RadioButton, which replace it
// (react-aria-components 1.22).
function RadioGroupItem({
  className,
  children,
  ...props
}: Omit<RadioFieldProps, "className" | "children"> & {
  className?: string;
  children?: React.ReactNode;
}) {
  return (
    <RadioField data-slot="radio-group-item" className="flex" {...props}>
      <RadioButton
        className={cn(
          "group/radio relative flex min-h-9 items-center gap-2 text-sm pointer-coarse:min-h-11 data-disabled:cursor-not-allowed data-disabled:opacity-50",
          className,
        )}
      >
        <span
          data-slot="radio-group-indicator"
          aria-hidden="true"
          className="flex size-4 shrink-0 items-center justify-center rounded-full border border-input bg-card transition-colors group-data-focus-visible/radio:focus-ring group-data-invalid/radio:border-destructive group-data-selected/radio:border-primary group-data-selected/radio:bg-primary"
        >
          <span className="hidden size-1.5 rounded-full bg-primary-foreground group-data-selected/radio:block" />
        </span>
        {children}
      </RadioButton>
    </RadioField>
  );
}

export { RadioGroup, RadioGroupItem };
