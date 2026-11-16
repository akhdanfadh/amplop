import type { ComponentType, ReactElement } from "react";
import { Badge, type badgeVariants } from "@/components/ui/badge";
import { cn } from "@/lib/cn";
import type { VariantProps } from "class-variance-authority";

// Tone is what a status asks of the member (DESIGN.md "Status badges"):
// nothing, wait, nothing more, a look, or a fix.
export type Tone = "neutral" | "info" | "success" | "warning" | "destructive";

type Variant = NonNullable<VariantProps<typeof badgeVariants>["variant"]>;

// Neutral uses shadcn's secondary look: a fill and a hairline.
const variants: Record<Tone, Variant> = {
  neutral: "secondary",
  info: "info",
  success: "success",
  warning: "warning",
  destructive: "destructive",
};

// StatusIcon is an icon a status badge can show: a lucide icon or Spinner.
export type StatusIcon = ComponentType<{ className?: string }>;

// StatusBadge shows a status as a colour, an icon, and a word: tone picks
// the colour, icon is hidden from screen readers, and children is the word
// they read.
export function StatusBadge({
  tone,
  icon: Icon,
  children,
}: {
  tone: Tone;
  icon: StatusIcon;
  children: string;
}): ReactElement {
  return (
    <Badge variant={variants[tone]}>
      <Icon data-icon="inline-start" />
      {children}
    </Badge>
  );
}

// The tint of each tone's mark, as its badge's (badge.tsx).
const markTints: Record<Tone, string> = {
  neutral: "bg-secondary text-secondary-foreground",
  info: "bg-info/8 text-info dark:bg-info/12",
  success: "bg-success/8 text-success dark:bg-success/12",
  warning: "bg-warning/8 text-warning dark:bg-warning/12",
  destructive: "bg-destructive/8 text-destructive dark:bg-destructive/12",
};

// StatusMark shows a status as its tone's tint in a 32 px circle around
// icon, beside words that name it, e.g. the compose window's summary of a
// sent message. It is hidden from screen readers; the words carry it.
export function StatusMark({ tone, icon: Icon }: { tone: Tone; icon: StatusIcon }): ReactElement {
  return (
    <span
      aria-hidden="true"
      className={cn("grid size-8 shrink-0 place-items-center rounded-full", markTints[tone])}
    >
      <Icon className="size-4" />
    </span>
  );
}
