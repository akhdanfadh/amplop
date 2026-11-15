import { describe, expect, it } from "vite-plus/test";
import { ConfirmationSchema, ReplacementCodeSchema, turnOffSchema } from "./schema";

describe("enrollment confirmation", () => {
  it("keeps leading zeros", () => {
    expect(ConfirmationSchema.parse({ code: "012345" })).toEqual({ code: "012345" });
  });

  it.each(["", "12345", "1234567", "abcdef", "abcde-fghjk"])(
    "rejects incomplete or non-authenticator input %s",
    (code) => {
      expect(ConfirmationSchema.safeParse({ code }).success).toBe(false);
    },
  );
});

describe("the replacement code", () => {
  it("keeps leading zeros", () => {
    expect(ReplacementCodeSchema.parse({ code: "012345" })).toEqual({ code: "012345" });
  });

  it.each(["", "12345", "1234567", "abcdef", "abcde-fghjk"])(
    "rejects incomplete or non-authenticator input %s",
    (code) => {
      expect(ReplacementCodeSchema.safeParse({ code }).success).toBe(false);
    },
  );

  // What the six-slot field lets a member type: one message, not one per
  // check. Longer input also meets the contract's own length limit.
  it.each(["", "12345", "abcdef"])("words input %s with one message", (code) => {
    const issues = ReplacementCodeSchema.safeParse({ code }).error?.issues ?? [];
    expect(new Set(issues.map((issue) => issue.message))).toEqual(new Set(["Enter all 6 digits."]));
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
