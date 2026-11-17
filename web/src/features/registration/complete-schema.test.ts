import { describe, expect, it } from "vite-plus/test";
import { validationMessage } from "@/api/validation";
import { CompleteSchema } from "./complete-schema";

const valid = {
  email: "visitor@example.org",
  password: "a long member password",
  confirmPassword: "a long member password",
};
const tooShort = "p".repeat(14);
const tooLong = "p".repeat(129);

describe("CompleteSchema", () => {
  it("accepts an email and a matching password", () => {
    expect(CompleteSchema.parse(valid)).toEqual(valid);
  });

  it.each([
    {
      name: "text that is not an email address",
      change: { email: "not an email" },
      issues: [{ path: ["email"], message: "Enter an email address, like name@example.com." }],
    },
    // The API's rule, as setup uses it.
    {
      name: "a password one character short",
      change: { password: tooShort, confirmPassword: tooShort },
      issues: [{ path: ["password"], message: "Must be 15 to 128 characters." }],
    },
    {
      name: "a password one character too long",
      change: { password: tooLong, confirmPassword: tooLong },
      issues: [{ path: ["password"], message: "Must be 15 to 128 characters." }],
    },
    // An empty confirmation also differs from the password: the request for
    // it comes first, and both are on its field.
    {
      name: "no confirmation",
      change: { confirmPassword: "" },
      issues: [
        { path: ["confirmPassword"], message: "Confirm your password." },
        { path: ["confirmPassword"] },
      ],
    },
    {
      name: "a confirmation that differs",
      change: { confirmPassword: `${valid.password}!` },
      issues: [{ path: ["confirmPassword"], message: "Passwords must match." }],
    },
  ])("refuses $name", ({ change, issues }) => {
    const result = CompleteSchema.safeParse({ ...valid, ...change }, { error: validationMessage });

    expect(result.error?.issues).toMatchObject(issues);
  });
});
