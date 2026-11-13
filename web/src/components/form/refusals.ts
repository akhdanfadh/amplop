import { useMemo, useRef } from "react";
import type { FormValidateFn, StandardSchemaV1 } from "@tanstack/react-form";
import { formErrors } from "@/api/problem";
import type { ApiError } from "@/api/transport";

// One field's error from the server: its message and the value it refused.
type Refusal = { name: string; value: unknown; message: string };

// The part of a form that record checks again.
type Checkable = { validate: (cause: "change") => unknown };

// What a form keeps of the server's refusals of its values.
export type Refusals<TValues> = {
  // check returns a form check for useAppForm's onDynamic: schema's issues
  // plus each refusal while its field still holds the refused value.
  check: (schema: StandardSchemaV1<TValues, unknown>) => FormValidateFn<TValues>;
  // record keeps the field errors err holds for the fields in names, as the
  // values sent refused them, in place of earlier ones, and checks form
  // again to show them. Without err, e.g. after a success, it keeps none.
  record: (form: Checkable, sent: TValues, err?: ApiError) => void;
};

// useRefusals returns the server's refusals of a form whose fields are
// names, as a check that holds each refusal while its field holds the
// refused value. A refusal outside names stays out of the form's fields, e.g.
// for the request summary.
//
// DESIGN: a refusal is part of validation, not of TanStack Form's submit
// errors, which a passing blur check clears. As a check it stays while the
// field holds the value, returns if the member types that value again, and
// blocks sending a value the server is known to refuse. A rule that spans
// fields, e.g. a username that needs TLS, must also be in the form's schema,
// which then refuses it first; a refusal of it clears only when its own field
// changes.
export function useRefusals<TValues extends Record<string, unknown>>(
  names: readonly string[],
): Refusals<TValues> {
  const refusals = useRef<Refusal[]>([]);
  // names is a list of literals, the same on every render.
  const fieldNames = useRef(names).current;

  return useMemo(
    () => ({
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
      record: (form, sent, err) => {
        const fields = err ? formErrors(err, fieldNames).fields : {};
        refusals.current = Object.entries(fields).flatMap(([name, messages]) =>
          messages.map(({ message }) => ({ name, value: sent[name], message })),
        );
        // A check after the first submit runs on "change" (revalidateLogic).
        form.validate("change");
      },
    }),
    [fieldNames],
  );
}
