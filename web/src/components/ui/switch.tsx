import type * as React from "react";
import { cn } from "cn";
import { SwitchButton, SwitchField, type SwitchFieldProps } from "react-aria-components";

// Switch is a track and its label: children are the label, inside the
// clickable row. Props go to React Aria's SwitchField, whose hidden input
// takes id and the aria-* props.
//
// DESIGN: aria-nova still ships React Aria's deprecated Switch, so this
// builds on SwitchField and SwitchButton, which replace it
// (react-aria-components 1.22).
function Switch({
  className,
  children,
  ...props
}: Omit<SwitchFieldProps, "className" | "children"> & {
  className?: string;
  children?: React.ReactNode;
}) {
  return (
    <SwitchField data-slot="switch" className={cn("flex", className)} {...props}>
      <SwitchButton className="group/switch relative flex min-h-9 items-center gap-2 text-sm data-readonly:cursor-default pointer-coarse:min-h-11 data-disabled:cursor-not-allowed data-disabled:opacity-50">
        <span
          data-slot="switch-track"
          aria-hidden="true"
          className="inline-flex h-5 w-9 shrink-0 items-center rounded-full border border-input bg-muted p-0.5 transition-colors group-data-focus-visible/switch:focus-ring group-data-invalid/switch:border-destructive group-data-selected/switch:border-primary group-data-selected/switch:bg-primary"
        >
          <span
            data-slot="switch-thumb"
            className="block size-3.5 rounded-full bg-muted-foreground transition-transform group-data-selected/switch:translate-x-4 group-data-selected/switch:bg-primary-foreground motion-reduce:transition-none"
          />
        </span>
        {children}
      </SwitchButton>
    </SwitchField>
  );
}

export { Switch };
