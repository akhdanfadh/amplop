import { describe, expect, it } from "vite-plus/test";
import { validationMessage } from "@/api/validation";
import { CompleteSchema } from "./complete-schema";

const email = "visitor@example.org";
const password = "a long member password";

describe("CompleteSchema", () => {
  it("accepts an email and a matching password", () => {
    expect(CompleteSchema.parse({ email, password, confirmPassword: password })).toEqual({
      email,
      password,
      confirmPassword: password,
    });
  });

  it("asks for a valid email", () => {
    const result = CompleteSchema.safeParse(
      { email: "not an email", password, confirmPassword: password },
      { error: validationMessage },
    );

    expect(result.error?.issues).toMatchObject([{ path: ["email"] }]);
  });

  // The API's rule, as setup uses it.
  it.each(["p".repeat(14), "p".repeat(129)])("rejects a password of the wrong length", (value) => {
    const result = CompleteSchema.safeParse(
      { email, password: value, confirmPassword: value },
      { error: validationMessage },
    );

    expect(result.error?.issues).toMatchObject([
      { path: ["password"], message: "Must be 15 to 128 characters." },
    ]);
  });

  it("asks for the confirmation", () => {
    const result = CompleteSchema.safeParse({ email, password, confirmPassword: "" });

    expect(result.error?.issues).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ path: ["confirmPassword"], message: "Confirm your password." }),
      ]),
    );
  });

  it("rejects a confirmation that differs", () => {
    const result = CompleteSchema.safeParse({ email, password, confirmPassword: `${password}!` });

    expect(result.error?.issues).toMatchObject([
      { path: ["confirmPassword"], message: "Passwords must match." },
    ]);
  });
});
