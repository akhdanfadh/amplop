import type * as React from "react";
import { cn } from "@/lib/cn";

// Empty says why a list or pane has nothing to show, with one next step.
//
// DESIGN: inbok's copy differs from shadcn's: words only (DESIGN.md
// "States"), so no EmptyHeader or EmptyMedia; the text starts at the left edge as the
// list's rows do; the title is a heading (18/24, 600) and the description
// a paragraph, whose links show DESIGN.md's focus ring.
function Empty({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="empty"
      className={cn("flex w-full min-w-0 flex-col items-start gap-3 p-6", className)}
      {...props}
    />
  );
}

// EmptyTitle states the cause, e.g. "Nothing in Inbox"; elementType sets
// its heading level, h2 by default.
function EmptyTitle({
  className,
  elementType: Element = "h2",
  ...props
}: React.HTMLAttributes<HTMLHeadingElement> & { elementType?: "h2" | "h3" }) {
  return (
    <Element
      data-slot="empty-title"
      className={cn("text-lg font-semibold", className)}
      {...props}
    />
  );
}

function EmptyDescription({ className, ...props }: React.ComponentProps<"p">) {
  return (
    <p
      data-slot="empty-description"
      className={cn(
        "max-w-prose text-sm text-muted-foreground [&>a]:text-primary [&>a]:underline [&>a]:underline-offset-4 [&>a:focus-visible]:focus-ring [&>a:hover]:decoration-2 [&>a[data-focus-visible]]:focus-ring",
        className,
      )}
      {...props}
    />
  );
}

// EmptyContent holds the next step, e.g. a Connect Gmail button.
function EmptyContent({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="empty-content"
      className={cn("flex flex-wrap items-center gap-2", className)}
      {...props}
    />
  );
}

export { Empty, EmptyContent, EmptyDescription, EmptyTitle };
