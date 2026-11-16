import type { ReactElement } from "react";
import { cn } from "@/lib/cn";

// AccountColor is one of the eight account colours (DESIGN.md "Colour"),
// as the API numbers them, 1 to 8.
export type AccountColor = number;

// Each colour as a background and as an avatar's ring, written out whole
// so Tailwind finds the class names in the source.
const backgrounds = [
  "bg-account-1",
  "bg-account-2",
  "bg-account-3",
  "bg-account-4",
  "bg-account-5",
  "bg-account-6",
  "bg-account-7",
  "bg-account-8",
];
const rings = [
  "after:border-account-1",
  "after:border-account-2",
  "after:border-account-3",
  "after:border-account-4",
  "after:border-account-5",
  "after:border-account-6",
  "after:border-account-7",
  "after:border-account-8",
];

// Each colour as a plain border, for a ring drawn as its own element.
const borders = [
  "border-account-1",
  "border-account-2",
  "border-account-3",
  "border-account-4",
  "border-account-5",
  "border-account-6",
  "border-account-7",
  "border-account-8",
];

// accountBorder returns the class that colours a border in color, e.g. the
// ring a picked row's box keeps in the avatar's place.
export function accountBorder(color: AccountColor): string {
  return borders[colorIndex(color)];
}

// accountDot returns the class that fills an account's dot in color.
export function accountDot(color: AccountColor): string {
  return backgrounds[colorIndex(color)];
}

// accountRing returns the class that colours an avatar's ring, the border
// it draws over its own edge, in color.
export function accountRing(color: AccountColor): string {
  return rings[colorIndex(color)];
}

// AccountMark names an account the way DESIGN.md marks it everywhere: a
// dot in its color, never alone, and name, its member-chosen name or
// address, cut short with an ellipsis when the space ends.
export function AccountMark({
  color,
  name,
  className,
}: {
  color: AccountColor;
  name: string;
  className?: string;
}): ReactElement {
  // The name gives the mark its baseline, so beside other text, e.g. a
  // heading, it sits on the same line; the dot, which has none, is
  // centred on the name.
  return (
    <span className={cn("inline-flex min-w-0 items-baseline gap-1.5", className)}>
      <span
        aria-hidden="true"
        className={cn("size-2 shrink-0 self-center rounded-full", accountDot(color))}
      />
      <span className="truncate">{name}</span>
    </span>
  );
}

// accountName returns what the app calls an account: its member-chosen
// name, or its address until it has one.
export function accountName(account: { name: string | null; email: string }): string {
  return account.name ?? account.email;
}

// colorIndex returns color's place in the lists above; a colour the app
// does not know, e.g. from a newer server, takes the first.
function colorIndex(color: AccountColor): number {
  return Number.isInteger(color) && color >= 1 && color <= backgrounds.length ? color - 1 : 0;
}
