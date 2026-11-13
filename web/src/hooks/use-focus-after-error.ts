import { useEffect, type RefObject } from "react";

// An invalid control: a field marked aria-invalid, or the button of a
// React Aria select, which marks only its root (data-invalid) and drops
// aria-invalid given to its button.
const invalidControl =
  '[aria-invalid="true"], [data-slot="select"][data-invalid] [data-slot="select-trigger"]';

// useFocusAfterError moves focus, once a request that ended in error is no
// longer pending, to the first field in form marked invalid, else to
// fallback, e.g. the submit button. Pass the request's error and whether it
// is pending; focus moves again for each new error.
export function useFocusAfterError(
  error: unknown,
  pending: boolean,
  form: RefObject<HTMLFormElement | null>,
  fallback: RefObject<HTMLElement | null>,
): void {
  useEffect(() => {
    if (!error || pending) return;
    // The button lost focus while disabled. A refused value takes the user
    // to its field, whose error is read with it; any other failure, to the
    // button beside its message.
    const invalid = form.current?.querySelector<HTMLElement>(invalidControl);
    (invalid ?? fallback.current)?.focus();
  }, [error, pending, form, fallback]);
}
