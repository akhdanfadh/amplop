import { describe, expect, it } from "vite-plus/test";
import { codePaste, codeSchema } from "./code-schema";

describe("sign-in code input", () => {
  it("preserves an authenticator code's leading zero", () => {
    expect(codeSchema("authenticator").parse({ code: "012345" })).toEqual({ code: "012345" });
  });

  it.each(["", "12345", "1234567", "abcdef", "abcde-fghjk"])(
    "rejects malformed authenticator input %s",
    (code) => {
      expect(codeSchema("authenticator").safeParse({ code }).success).toBe(false);
    },
  );

  // What the six-slot field lets a member type: one message, not one per
  // check. Longer input also meets the contract's own length limit.
  it.each(["", "12345", "abcdef"])("words authenticator input %s with one message", (code) => {
    const issues = codeSchema("authenticator").safeParse({ code }).error?.issues ?? [];
    expect(new Set(issues.map((issue) => issue.message))).toEqual(new Set(["Enter all 6 digits."]));
  });

  it("preserves backup letter case for server verification", () => {
    expect(codeSchema("backup").parse({ code: "aBcDefGhJk" })).toEqual({ code: "aBcDefGhJk" });
  });

  it.each(["", "abcde", "abcdefghjkm", "abcde!ghjk"])(
    "rejects malformed backup input %s",
    (code) => {
      expect(codeSchema("backup").safeParse({ code }).success).toBe(false);
    },
  );

  it.each([" abcde-fghjk ", "abcde\u0085fghjk", "abcde\u2003fghjk"])(
    "removes pasted separators from %s",
    (value) => {
      expect(codePaste(value)).toBe("abcdefghjk");
    },
  );

  it("preserves unsupported punctuation for validation to reject", () => {
    expect(codePaste("abcde!ghjk")).toBe("abcde!ghjk");
  });
});
