import { describe, expect, it } from "vite-plus/test";
import {
  EnrollmentCodeSchema,
  PasswordSchema,
  ReplacementCodeSchema,
  turnOffSchema,
} from "./schema";

// features/auth/code-schema.test.ts holds the rule for a complete code, which
// every schema here takes from completeCode. Each schema's tests prove only
// that it applies the rule, for the kind of code it asks for.
describe.each([
  ["the enrollment code", EnrollmentCodeSchema],
  ["the replacement code", ReplacementCodeSchema],
])("%s", (_, schema) => {
  it("keeps a complete authenticator code as typed", () => {
    expect(schema.parse({ code: "012345" })).toEqual({ code: "012345" });
  });

  // Five digits pass the contract's own limit, so only the shared rule
  // refuses them, with its words for an authenticator code.
  it("refuses an incomplete code with the authenticator code's message", () => {
    const issues = schema.safeParse({ code: "12345" }).error?.issues ?? [];
    expect(new Set(issues.map((issue) => issue.message))).toEqual(new Set(["Enter all 6 digits."]));
  });
});

describe("the password check", () => {
  it("keeps the password as typed, spaces included", () => {
    expect(PasswordSchema.parse({ password: " pass word " })).toEqual({ password: " pass word " });
  });

  it("asks for a password", () => {
    const result = PasswordSchema.safeParse({ password: "" });
    expect(result.error?.issues[0]?.message).toBe("Enter your password.");
  });

  it("refuses more than the server reads", () => {
    expect(PasswordSchema.safeParse({ password: "p".repeat(129) }).success).toBe(false);
  });
});

describe("turning off two-factor authentication", () => {
  it("accepts an authenticator code or a backup code as entered", () => {
    expect(turnOffSchema("authenticator").parse({ code: "012345" })).toEqual({ code: "012345" });
    expect(turnOffSchema("backup").parse({ code: "aBcDefGhJk" })).toEqual({ code: "aBcDefGhJk" });
  });

  // One refusal a kind: letters where the authenticator's digits go, and an
  // authenticator code where a backup code goes.
  it.each([
    ["authenticator", "abcdef"],
    ["backup", "012345"],
  ] as const)("rejects %s input %s", (kind, code) => {
    expect(turnOffSchema(kind).safeParse({ code }).success).toBe(false);
  });
});
