// The part of a TanStack Form field that blurKeepingSubmitErrors uses.
// setErrorMap is a method so any field's typed error map fits.
type BlurField = {
  state: { meta: { errorMap: { onSubmit?: unknown } } };
  handleBlur: () => void;
  setErrorMap(errorMap: { onSubmit?: unknown }): void;
};

// blurKeepingSubmitErrors marks field blurred and validates it, as
// field.handleBlur does, then puts back the errors the last submit set on
// it, e.g. a server's refusal of its value. An edit still clears them.
export function blurKeepingSubmitErrors(field: BlurField): void {
  const submitErrors = field.state.meta.errorMap.onSubmit;
  field.handleBlur();
  // TanStack Form drops a field's submit errors whenever a blur validation
  // passes, which would hide why the server refused the value.
  if (submitErrors !== undefined) field.setErrorMap({ onSubmit: submitErrors });
}
