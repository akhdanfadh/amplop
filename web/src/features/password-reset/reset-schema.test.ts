import { describe, expect, it } from "vite-plus/test";
import { validationMessage } from "@/api/validation";
import { ResetSchema } from "./reset-schema";

const email = "member@example.org";
const password = "a long new password";

describe("ResetSchema", () => {
  it("accepts an email and a matching password", () => {
    expect(ResetSchema.parse({ email, password, confirmPassword: password })).toEqual({
      email,
      password,
      confirmPassword: password,
    });
  });

  it("asks for a valid email", () => {
    const result = ResetSchema.safeParse(
      { email: "not an email", password, confirmPassword: password },
      { error: validationMessage },
    );

    expect(result.error?.issues).toMatchObject([{ path: ["email"] }]);
  });

  // The API's rule, as setup and registration use it.
  it.each(["p".repeat(14), "p".repeat(129)])("rejects a password of the wrong length", (value) => {
    const result = ResetSchema.safeParse(
      { email, password: value, confirmPassword: value },
      { error: validationMessage },
    );

    expect(result.error?.issues).toMatchObject([
      { path: ["password"], message: "Must be 15 to 128 characters." },
    ]);
  });

  it("asks for the confirmation", () => {
    const result = ResetSchema.safeParse({ email, password, confirmPassword: "" });

    expect(result.error?.issues).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          path: ["confirmPassword"],
          message: "Confirm your new password.",
        }),
      ]),
    );
  });

  it("rejects a confirmation that differs", () => {
    const result = ResetSchema.safeParse({ email, password, confirmPassword: `${password}!` });

    expect(result.error?.issues).toMatchObject([
      { path: ["confirmPassword"], message: "Passwords must match." },
    ]);
  });
});
