import * as React from "react";
import { cn } from "@/lib/cn";
import { composeRenderProps, TextArea as TextareaPrimitive } from "react-aria-components";

// The focus ring shows on any focus, a click included, since it marks where
// typing goes; React Aria's data-focus-visible follows only the keyboard.
// Inside an InputGroup, the group draws it.
function Textarea({ className, ...props }: React.ComponentProps<typeof TextareaPrimitive>) {
  return (
    <TextareaPrimitive
      data-slot="textarea"
      className={composeRenderProps(className, (stateClassName) =>
        cn(
          "flex field-sizing-content min-h-16 w-full rounded-lg border border-input bg-card px-3 py-2 text-base transition-colors outline-none placeholder:text-muted-foreground read-only:bg-muted aria-invalid:border-destructive not-in-data-[slot=input-group]:data-focused:focus-ring data-invalid:border-destructive md:text-sm dark:bg-input/10 data-disabled:cursor-not-allowed data-disabled:opacity-50",
          stateClassName,
        ),
      )}
      {...props}
    />
  );
}

export { Textarea };
