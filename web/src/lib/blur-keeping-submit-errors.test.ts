import { FieldApi, FormApi } from "@tanstack/react-form";
import { describe, expect, it } from "vite-plus/test";
import * as z from "zod";
import { blurKeepingSubmitErrors } from "./blur-keeping-submit-errors";

const refusal = { message: "Enter the email address this link was sent to." };

// emailField returns a mounted field whose submit errors hold refusal, as a
// form's onError sets them after the server refuses the value.
function emailField() {
  const form = new FormApi({
    defaultValues: { email: "member@example.org" },
    validators: { onChange: z.object({ email: z.string().min(1) }) },
  });
  form.mount();
  const field = new FieldApi({ form, name: "email" });
  field.mount();
  form.setErrorMap({ onSubmit: { fields: { email: [refusal] } } });
  return field;
}

describe("blurKeepingSubmitErrors", () => {
  // Pins the library behavior the helper works around.
  it("is needed: a plain blur drops the refusal", () => {
    const field = emailField();

    field.handleBlur();

    expect(field.state.meta.errors).toEqual([]);
  });

  it("keeps the refusal through a blur", () => {
    const field = emailField();

    blurKeepingSubmitErrors(field);

    expect(field.state.meta.errors).toEqual([refusal]);
    expect(field.state.meta.isBlurred).toBe(true);
  });

  it("lets an edit clear the refusal", () => {
    const field = emailField();
    blurKeepingSubmitErrors(field);

    field.handleChange("other@example.org");

    expect(field.state.meta.errors).toEqual([]);
  });
});
