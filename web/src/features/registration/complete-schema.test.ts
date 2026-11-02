import { describe, expect, it } from "vite-plus/test";
import { validationMessage } from "@/api/validation";
import { CompleteSchema } from "./complete-schema";

const password = "a long member password";

describe("CompleteSchema", () => {
  it("accepts a matching password", () => {
    expect(CompleteSchema.parse({ password, confirmPassword: password })).toEqual({
      password,
      confirmPassword: password,
    });
  });

  // The API's rule, as setup uses it.
  it.each(["p".repeat(14), "p".repeat(129)])("rejects a password of the wrong length", (value) => {
    const result = CompleteSchema.safeParse(
      { password: value, confirmPassword: value },
      { error: validationMessage },
    );

    expect(result.error?.issues).toMatchObject([
      { path: ["password"], message: "Must be 15 to 128 characters." },
    ]);
  });

  it("asks for the confirmation", () => {
    const result = CompleteSchema.safeParse({ password, confirmPassword: "" });

    expect(result.error?.issues).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ path: ["confirmPassword"], message: "Confirm your password." }),
      ]),
    );
  });

  it("rejects a confirmation that differs", () => {
    const result = CompleteSchema.safeParse({ password, confirmPassword: `${password}!` });

    expect(result.error?.issues).toMatchObject([
      { path: ["confirmPassword"], message: "Passwords must match." },
    ]);
  });
});
