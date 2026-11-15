import { useEffect, useRef, useState, type ReactElement, type ReactNode } from "react";
import { useSelector } from "@tanstack/react-form";
import { cn } from "@/lib/cn";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { useFormContext } from "./form-context";

// Form is the current form's <form> element, named label for screen
// readers. It submits through TanStack Form and ignores a submit while one
// is sending. A submit started from a field moves focus to the submit
// button; after each submit, focus moves to the first invalid field. A
// submit that throws shows the app's crash screen.
export function Form({
  label,
  className,
  children,
}: {
  label: string;
  className?: string;
  children: ReactNode;
}): ReactElement {
  const form = useFormContext();
  const ref = useRef<HTMLFormElement>(null);
  const sending = useSelector(form.store, (state) => state.isSubmitting);
  // Counts finished submits; each moves focus once its errors render.
  const [finished, setFinished] = useState(0);
  const [crash, setCrash] = useState<{ error: unknown }>();

  useEffect(() => {
    if (finished > 0 && ref.current) focusFirstInvalid(ref.current);
  }, [finished]);

  // A form catches the failures of its own request, so a submit that throws
  // is a fault in the code. Error boundaries see only what a render throws
  // (react.dev, "Catching rendering errors with an error boundary"), so the
  // error comes back through one.
  if (crash) throw crash.error;

  return (
    <form
      ref={ref}
      className={cn("flex flex-col gap-6", className)}
      // The form's own checks speak in the app's words, not the browser's.
      noValidate
      aria-label={label}
      aria-busy={sending}
      onSubmit={(event) => {
        event.preventDefault();
        // Enter in a field submits even while the button is pending.
        if (form.state.isSubmitting) return;
        focusSubmitButton(event.currentTarget);
        form.handleSubmit().then(
          () => setFinished((count) => count + 1),
          (error: unknown) => setCrash({ error }),
        );
      }}
    >
      {children}
    </form>
  );
}

// focusSubmitButton moves focus to form's submit button when it is on
// another control in form, e.g. a field where Enter submitted it.
function focusSubmitButton(form: HTMLFormElement): void {
  const button = form.querySelector<HTMLElement>('[type="submit"]');
  const focused = document.activeElement;
  // The submit locks the fields, which would drop focus to the page. The
  // pending button keeps it and tells a screen reader that it is working.
  if (button && focused !== button && form.contains(focused)) button.focus();
}

// focusFirstInvalid moves focus to form's first invalid field, which reads
// its error with it. Without one, focus stays where the submit left it.
function focusFirstInvalid(form: HTMLFormElement): void {
  const invalid = form.querySelector<HTMLElement>('[aria-invalid="true"]');
  if (invalid?.getAttribute("role") !== "radiogroup") {
    invalid?.focus();
    return;
  }
  // A radio group is invalid as a whole, but takes no focus itself: its
  // checked radio does, as Tab would reach it, else its first.
  const radio =
    invalid.querySelector<HTMLElement>('input[type="radio"]:checked') ??
    invalid.querySelector<HTMLElement>('input[type="radio"]');
  radio?.focus();
}

// SubmitButton submits the current form: children name what it does, e.g.
// "Sign in", and working what it is doing while the form sends, e.g.
// "Signing in...". isDisabled also blocks it, e.g. while the form checks
// an earlier request's outcome. variant "destructive" marks a submit that
// removes something, e.g. turning off two-factor authentication.
export function SubmitButton({
  children,
  working,
  isDisabled,
  variant = "default",
}: {
  children: ReactNode;
  working: string;
  isDisabled?: boolean;
  variant?: "default" | "destructive";
}): ReactElement {
  const form = useFormContext();
  const sending = useSelector(form.store, (state) => state.isSubmitting);

  return (
    // Pending, unlike disabled, keeps focus on the button and tells a screen
    // reader that it is working, while presses do nothing.
    <Button
      type="submit"
      variant={variant}
      className="whitespace-normal"
      isPending={sending}
      isDisabled={isDisabled}
    >
      {sending ? (
        <>
          {/* Turns while the request runs; the words say what runs. */}
          <Spinner />
          {working}
        </>
      ) : (
        children
      )}
    </Button>
  );
}
