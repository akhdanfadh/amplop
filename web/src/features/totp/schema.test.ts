import { describe, expect, it } from "vite-plus/test";
import {
  EnrollmentCodeSchema,
  PasswordSchema,
  ReplacementCodeSchema,
  turnOffSchema,
} from "./schema";

describe.each([
  ["the enrollment code", EnrollmentCodeSchema],
  ["the replacement code", ReplacementCodeSchema],
])("%s", (_, schema) => {
  it("keeps leading zeros", () => {
    expect(schema.parse({ code: "012345" })).toEqual({ code: "012345" });
  });

  it.each(["", "12345", "1234567", "abcdef", "abcde-fghjk"])(
    "rejects incomplete or non-authenticator input %s",
    (code) => {
      expect(schema.safeParse({ code }).success).toBe(false);
    },
  );

  // What the six-slot field lets a member type: one message, not one per
  // check. Longer input also meets the contract's own length limit.
  it.each(["", "12345", "abcdef"])("words input %s with one message", (code) => {
    const issues = schema.safeParse({ code }).error?.issues ?? [];
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

  it.each([
    ["authenticator", "abcdef"],
    ["authenticator", "12345"],
    ["backup", "012345"],
    ["backup", "abcde!ghjk"],
  ] as const)("rejects %s input %s", (kind, code) => {
    expect(turnOffSchema(kind).safeParse({ code }).success).toBe(false);
  });
});
