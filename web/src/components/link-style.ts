// The look of a link (DESIGN.md "Links"): primary, underlined, the
// underline thickening on hover, with the focus ring.

// textLink is a link inside a sentence.
export const textLink =
  "text-primary underline underline-offset-4 hover:decoration-2 focus-visible:focus-ring";

// lineLink is a link on a line of its own, e.g. "Back to sign in", 44 px
// tall so a finger reaches it (DESIGN.md "Control height").
export const lineLink = `inline-flex min-h-11 w-fit items-center ${textLink}`;

// smallTarget is the size of a control in small text beside a label, link
// or button: 24 px tall, the least WCAG 2.2 allows for a target that is not
// in a sentence (2.5.8, Target Size (Minimum)).
export const smallTarget = "min-h-6 w-fit text-sm pointer-coarse:min-h-6";

// inlineLinkButton sizes a `Button variant="link"` to its text inside a
// line, e.g. "Show all" after a list cut short; ::before gives touch its
// 44 px (DESIGN.md "Control height").
export const inlineLinkButton =
  "relative min-h-0 p-0 align-baseline pointer-coarse:min-h-0 pointer-coarse:before:absolute pointer-coarse:before:inset-x-0 pointer-coarse:before:-inset-y-3";

// smallLink is a link in small text beside a label, e.g. "Forgot password?".
export const smallLink = `inline-flex items-center ${smallTarget} ${textLink}`;
