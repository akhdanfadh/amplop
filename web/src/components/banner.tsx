import type { ReactElement, ReactNode } from "react";
import type { LucideIcon } from "lucide-react";
import { cn } from "@/lib/cn";

// Banner is one line across the top of the content for a condition that
// lasts (DESIGN.md "Feedback"): icon, then children, a sentence and, where
// something fixes it, a link to it, tinted by tone, "warning" for what stops
// working now or "info" for what the member still owes.
export function Banner({
  tone,
  icon: Icon,
  children,
}: {
  tone: "warning" | "info";
  icon: LucideIcon;
  children: ReactNode;
}): ReactElement {
  return (
    <p
      className={cn(
        // The line under it, in its tone, keeps stacked banners of one tone
        // apart.
        "flex shrink-0 items-start gap-2 border-b px-4 py-2 text-sm",
        tone === "warning"
          ? "border-warning/35 bg-warning/8 text-warning dark:bg-warning/12"
          : "border-info/35 bg-info/8 text-info dark:bg-info/12",
      )}
    >
      {/* On the first line's centre, however many lines the text takes. */}
      <Icon aria-hidden="true" className="mt-0.5 size-4 shrink-0" />
      <span>{children}</span>
    </p>
  );
}
