import { useRef } from "react";
import type { FormValidateFn, StandardSchemaV1 } from "@tanstack/react-form";
import { formErrors, formSummary, uncertainOutcome } from "@/api/problem";
import { ApiError } from "@/api/transport";
import { useMounted } from "@/hooks/use-mounted";

// One field's error from the server: its message and the value it refused.
type Refusal = { name: string; value: unknown; message: string };

// A form's values, by field name.
type Values = Record<string, unknown>;

// The part of a form that send checks again.
type Checkable = { validate: (cause: "change") => unknown };

// The part of a generated mutation hook's result a form request uses, e.g.
// useCreateSession()'s.
type Mutation<TData, TVariables> = {
  mutateAsync: (variables: TVariables) => Promise<TData>;
  error: ApiError | null;
};

// How a request a form sent ended: answered with data, refused with an
// error (its field errors already on the form), or left behind, because
// the form was gone when the answer came.
type Sent<TData> =
  | { outcome: "done"; data: TData }
  | { outcome: "refused"; error: ApiError }
  | { outcome: "left" };

// What a form uses to send its values through one request.
type FormRequest<TData, TVariables> = {
  // check returns a form check for useAppForm's onDynamic: schema's issues
  // plus each server refusal while its field still holds the refused value.
  check: <TValues extends Values>(
    schema: StandardSchemaV1<TValues, unknown>,
  ) => FormValidateFn<TValues>;
  // send sends variables, made from the form's values value, and waits for
  // the answer. A refusal's field errors go on form's fields in place of
  // earlier ones; an answer clears them. A failure that is not the
  // request's, i.e. a fault in the code, is thrown, for the crash screen.
  send: (form: Checkable, value: Values, variables: TVariables) => Promise<Sent<TData>>;
  // summary returns what RequestError shows for the last failure: message
  // words its cause, then the refusals no field shows. Nothing while the
  // request has not failed.
  summary: (message: (err: ApiError) => string) => { message: string }[];
  // reference is the last failure's request reference when the failure is
  // a fault, a 5xx or a lost answer, for reporting it; a refusal, e.g. a
  // wrong password, has none (DESIGN.md "Forms").
  reference: string | undefined;
};

// useFormRequest returns how a form sends its values through mutation, the
// result of a generated mutation hook, e.g. useCreateSession(). names lists
// the form's fields: a refusal of another input stays out of them and goes
// in the summary.
//
// DESIGN: a refusal is part of validation, not of TanStack Form's submit
// errors, which a passing blur check clears. As a check it stays while the
// field holds the value, returns if the member types that value again, and
// blocks sending a value the server is known to refuse. A rule that spans
// fields, e.g. a username that needs TLS, must also be in the form's schema,
// which then refuses it first; a refusal of it clears only when its own field
// changes.
export function useFormRequest<TData, TVariables>(
  mutation: Mutation<TData, TVariables>,
  names: readonly string[],
): FormRequest<TData, TVariables> {
  const refusals = useRef<Refusal[]>([]);
  const mounted = useMounted();
  const { mutateAsync, error } = mutation;

  // record keeps the refusals err holds, as value sent them, and checks form
  // again to show them; without err it keeps none.
  function record(form: Checkable, value: Values, err?: ApiError): void {
    const fields = err ? formErrors(err, names).fields : {};
    refusals.current = Object.entries(fields).flatMap(([name, messages]) =>
      messages.map(({ message }) => ({ name, value: value[name], message })),
    );
    // A check after the first submit runs on "change" (revalidateLogic).
    form.validate("change");
  }

  return {
    check:
      (schema) =>
      ({ value, formApi }) => {
        const fields: Record<string, { message: string }[]> = {
          ...formApi.parseValuesWithSchema(schema)?.fields,
        };
        for (const { name, value: refused, message } of refusals.current) {
          if (!Object.is(value[name], refused)) continue;
          fields[name] = [...(fields[name] ?? []), { message }];
        }
        return Object.keys(fields).length > 0 ? { fields } : undefined;
      },
    send: async (form, value, variables) => {
      try {
        const data = await mutateAsync(variables);
        // A member who left while the request ran, e.g. for "Forgot
        // password?", stays where they went: the form must not act on it.
        if (!mounted()) return { outcome: "left" };
        record(form, value);
        return { outcome: "done", data };
      } catch (err) {
        if (!(err instanceof ApiError)) throw err;
        if (!mounted()) return { outcome: "left" };
        record(form, value, err);
        return { outcome: "refused", error: err };
      }
    },
    summary: (message) => (error ? formSummary(message(error), formErrors(error, names)) : []),
    reference: error && uncertainOutcome(error) ? error.requestId : undefined,
  };
}
