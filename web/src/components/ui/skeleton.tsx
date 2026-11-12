import type * as React from "react";
import { cn } from "cn";

// Skeleton is a placeholder shaped like the content it stands for.
//
// DESIGN: unlike shadcn's it does not pulse, since DESIGN.md lets nothing
// loop but a spinner; and it has the hairline colour, not muted, which is
// the sidebar's own surface.
function Skeleton({ className, ...props }: React.ComponentProps<"div">) {
  return <div data-slot="skeleton" className={cn("rounded-md bg-border", className)} {...props} />;
}

export { Skeleton };
