import type * as React from "react";
import { cn } from "cn";
import { CheckboxButton, CheckboxField, type CheckboxFieldProps } from "react-aria-components";
import { CheckIcon, MinusIcon } from "lucide-react";

// Checkbox is a box and its label: children are the label, inside the
// clickable row. Props go to React Aria's CheckboxField, whose hidden input
// takes id and the aria-* props.
//
// DESIGN: aria-nova still ships React Aria's deprecated Checkbox, so this
// builds on CheckboxField and CheckboxButton, which replace it
// (react-aria-components 1.22).
function Checkbox({
  className,
  children,
  ...props
}: Omit<CheckboxFieldProps, "className" | "children"> & {
  className?: string;
  children?: React.ReactNode;
}) {
  return (
    <CheckboxField data-slot="checkbox" className={cn("flex", className)} {...props}>
      <CheckboxButton className="group/checkbox relative flex min-h-9 items-center gap-2 text-sm pointer-coarse:min-h-11 data-disabled:cursor-not-allowed data-disabled:opacity-50">
        {({ isSelected, isIndeterminate }) => (
          <>
            <span
              data-slot="checkbox-box"
              aria-hidden="true"
              className="flex size-4 shrink-0 items-center justify-center rounded-sm border border-input bg-card transition-colors group-data-focus-visible/checkbox:focus-ring group-data-indeterminate/checkbox:border-primary group-data-indeterminate/checkbox:bg-primary group-data-indeterminate/checkbox:text-primary-foreground group-data-invalid/checkbox:border-destructive group-data-selected/checkbox:border-primary group-data-selected/checkbox:bg-primary group-data-selected/checkbox:text-primary-foreground [&>svg]:size-3.5"
            >
              {isIndeterminate ? <MinusIcon /> : isSelected && <CheckIcon />}
            </span>
            {children}
          </>
        )}
      </CheckboxButton>
    </CheckboxField>
  );
}

export { Checkbox };
