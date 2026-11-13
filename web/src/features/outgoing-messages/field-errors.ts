import type { AnyFieldMeta } from "@tanstack/react-form";

// Whether the member has tried to send a form: from then on its fields
// show their errors, even a field not yet left.
export type Submission = "untried" | "tried";

// describedBy returns what describes the input with id: the element with
// descriptionId, if given, and its error when errors has one.
export function describedBy(
  id: string,
  errors: AnyFieldMeta["errors"],
  descriptionId?: string,
): string | undefined {
  const ids = [descriptionId, errors.length > 0 ? `${id}-error` : undefined];
  return ids.filter(Boolean).join(" ") || undefined;
}

// shownErrors returns a field's errors once the member has left it or
// tried to send, so an address is not flagged while it is being typed.
export function shownErrors(meta: AnyFieldMeta, submission: Submission): AnyFieldMeta["errors"] {
  return meta.isBlurred || submission === "tried" ? meta.errors : [];
}
