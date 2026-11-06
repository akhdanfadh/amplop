import { useEffect, type RefObject } from "react";

// useFocusAfterError moves focus, once a request that ended in error is no
// longer pending, to the first field in form marked aria-invalid, else to
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
    const invalid = form.current?.querySelector<HTMLElement>('[aria-invalid="true"]');
    (invalid ?? fallback.current)?.focus();
  }, [error, pending, form, fallback]);
}
