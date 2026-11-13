// The look of a link (DESIGN.md "Links"): primary, underlined, the
// underline thickening on hover, with the focus ring.

// textLink is a link inside a sentence.
export const textLink =
  "text-primary underline underline-offset-4 hover:decoration-2 focus-visible:focus-ring";

// lineLink is a link on a line of its own, e.g. "Back to sign in", 44 px
// tall so a finger reaches it (DESIGN.md "Control height").
export const lineLink = `inline-flex min-h-11 w-fit items-center ${textLink}`;
