import { describe, expect, it } from "vite-plus/test";
import { codePaste, codeSchema } from "./code-schema";

describe("sign-in code input", () => {
  it("preserves an authenticator code's leading zero", () => {
    expect(codeSchema("authenticator").parse({ code: "012345" })).toEqual({ code: "012345" });
  });

  it.each(["", "12345", "1234567", "abcdef"])(
    "rejects malformed authenticator input %s",
    (code) => {
      expect(codeSchema("authenticator").safeParse({ code }).success).toBe(false);
    },
  );

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
