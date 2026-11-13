import type { ComponentType, ReactElement } from "react";
import { Badge, type badgeVariants } from "@/components/ui/badge";
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
