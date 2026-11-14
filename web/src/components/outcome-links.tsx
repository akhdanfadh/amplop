import type { ReactElement } from "react";
import { Link, type LinkProps } from "@tanstack/react-router";
import { cn } from "@/lib/cn";
import { buttonVariants } from "@/components/ui/button";

// An outcome screen's way on: where it leads and its words.
export type OutcomeLink = { to: LinkProps["to"]; label: string };

// OutcomeLinks shows an outcome screen's ways on as full-width buttons, the
// first, most useful, primary and the rest outlined (DESIGN.md "Outcome
// screens").
export function OutcomeLinks({ links }: { links: readonly OutcomeLink[] }): ReactElement {
  return (
    <div className="flex flex-col gap-3">
      {links.map(({ to, label }, index) => (
        <Link
          key={label}
          to={to}
          // Exact, or the router marks /register current on
          // /register/complete and a screen reader says so.
          activeOptions={{ exact: true }}
          className={cn(
            buttonVariants({ variant: index === 0 ? "default" : "outline" }),
            "whitespace-normal",
          )}
        >
          {label}
        </Link>
      ))}
    </div>
  );
}
