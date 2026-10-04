import { useForm, useSelector, type AnyFieldMeta } from "@tanstack/react-form";
import { useCreateProbeEvent } from "@/api/generated/inbok";
import { NewProbeEvent } from "@/api/generated/model";
import { fieldErrors, userMessage } from "@/api/problem";
import type { ApiError } from "@/api/transport";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Field, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";

// The form holds exactly the request body, so it is sent as is.
const emptyEvent: NewProbeEvent = { note: "", amount: 1, highlighted: false, tag: null };

// Go's answer when the event would push the counter past its limit.
const conflict = 409;

// Adds an event to the probe counter: an example write.
export function ProbeForm() {
  const create = useCreateProbeEvent();

  const form = useForm({
    defaultValues: emptyEvent,
    // Each edit is checked against the contract's schema; nothing is sent
    // while a value breaks it.
    validators: { onChange: NewProbeEvent },
    onSubmit: ({ value, formApi }) => {
      create.mutate(
        { data: value },
        {
          onSuccess: () => formApi.reset(),
          // A value Go rejects shows on its field, e.g. after Go changed a
          // rule before `pnpm generate`. Editing that field clears it.
          onError: (err) => formApi.setErrorMap({ onSubmit: { fields: fieldErrors(err) } }),
        },
      );
    },
    // After an edit, the last request's message no longer describes the
    // form, e.g. "The counter is full." once the amount is lowered.
    listeners: { onChange: () => create.reset() },
  });
  const submitted = useSelector(form.store, (state) => state.submissionAttempts > 0);

  return (
    <form
      className="flex w-full max-w-sm flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault();
        void form.handleSubmit();
      }}
    >
      {/* Inputs lock while saving, so an edit cannot clear the running
          request's state or be lost when success clears the form. */}
      <FieldGroup>
        <form.Field name="note">
          {(field) => {
            const errors = shown(field.state.meta, submitted);
            return (
              <Field data-invalid={errors.length > 0}>
                <FieldLabel htmlFor={field.name}>Note</FieldLabel>
                <Input
                  id={field.name}
                  disabled={create.isPending}
                  aria-invalid={errors.length > 0}
                  value={field.state.value}
                  onBlur={field.handleBlur}
                  onChange={(e) => field.handleChange(e.target.value)}
                />
                <FieldError errors={errors} />
              </Field>
            );
          }}
        </form.Field>

        <form.Field name="amount">
          {(field) => {
            const errors = shown(field.state.meta, submitted);
            return (
              <Field data-invalid={errors.length > 0}>
                <FieldLabel htmlFor={field.name}>Amount</FieldLabel>
                <Input
                  id={field.name}
                  disabled={create.isPending}
                  aria-invalid={errors.length > 0}
                  type="number"
                  // An emptied box gives NaN; show it as empty, not "NaN".
                  value={Number.isNaN(field.state.value) ? "" : field.state.value}
                  onBlur={field.handleBlur}
                  onChange={(e) => field.handleChange(e.target.valueAsNumber)}
                />
                <FieldError errors={errors} />
              </Field>
            );
          }}
        </form.Field>

        <form.Field name="tag">
          {(field) => {
            const errors = shown(field.state.meta, submitted);
            return (
              <Field data-invalid={errors.length > 0}>
                <FieldLabel htmlFor={field.name}>Tag (optional)</FieldLabel>
                <Input
                  id={field.name}
                  disabled={create.isPending}
                  aria-invalid={errors.length > 0}
                  value={field.state.value ?? ""}
                  onBlur={field.handleBlur}
                  // An empty box means no tag (null); Go rejects an empty tag.
                  onChange={(e) => field.handleChange(e.target.value || null)}
                />
                <FieldError errors={errors} />
              </Field>
            );
          }}
        </form.Field>

        <form.Field name="highlighted">
          {(field) => {
            const errors = shown(field.state.meta, submitted);
            return (
              <Field orientation="horizontal" data-invalid={errors.length > 0}>
                <Checkbox
                  id={field.name}
                  disabled={create.isPending}
                  aria-invalid={errors.length > 0}
                  checked={field.state.value}
                  onCheckedChange={(checked) => field.handleChange(checked)}
                />
                <FieldLabel htmlFor={field.name}>Highlighted</FieldLabel>
                <FieldError errors={errors} />
              </Field>
            );
          }}
        </form.Field>
      </FieldGroup>

      {/* Disabled while saving, so a second click or Enter sends nothing. */}
      <Button type="submit" disabled={create.isPending}>
        {create.isPending ? "Adding..." : "Add event"}
      </Button>

      {/* The last attempt's error. Kept out of the form's own errors,
          which mean "these values are wrong" and block submitting until an
          edit: a retry after Go restarts would send nothing. */}
      {create.error && <FieldError>{failureText(create.error)}</FieldError>}
      {create.data && (
        <output>
          Added {create.data.data.event.amount}. Total: {create.data.data.total}.
        </output>
      )}
    </form>
  );
}

// "The counter is full." for Go's 409; the app's general wording otherwise.
function failureText(err: ApiError) {
  if (err.problem.status === conflict) return "The counter is full.";
  return userMessage(err);
}

// A field's errors once the user has left it or tried to submit, so a
// value is not flagged while first typed, e.g. Amount cleared to type 5.
function shown(meta: AnyFieldMeta, submitted: boolean): AnyFieldMeta["errors"] {
  return meta.isBlurred || submitted ? meta.errors : [];
}
