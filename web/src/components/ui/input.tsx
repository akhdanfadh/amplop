import * as React from "react";
import { cn } from "cn";
import { composeRenderProps, Input as InputPrimitive } from "react-aria-components";

// The focus ring shows on any focus, a click included, since it marks where
// typing goes; React Aria's data-focus-visible follows only the keyboard.
// Inside an InputGroup, the group draws it.
function Input({ className, type, ...props }: React.ComponentProps<typeof InputPrimitive>) {
  return (
    <InputPrimitive
      type={type}
      data-slot="input"
      className={composeRenderProps(className, (stateClassName) =>
        cn(
          "h-9 w-full min-w-0 rounded-lg border border-input bg-card px-3 py-1 text-base transition-colors outline-none file:inline-flex file:h-6 file:border-0 file:bg-transparent file:text-sm file:font-medium file:text-foreground placeholder:text-muted-foreground read-only:bg-muted aria-invalid:border-destructive not-in-data-[slot=input-group]:data-focused:focus-ring data-invalid:border-destructive md:text-sm dark:bg-input/10 pointer-coarse:h-11 data-disabled:cursor-not-allowed data-disabled:opacity-50",
          stateClassName,
        ),
      )}
      {...props}
    />
  );
}

export { Input };
