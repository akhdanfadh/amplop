import { describe, expect, it } from "vite-plus/test";
import { ConfirmationSchema, RegenerationSchema, pasteCode, turnOffSchema } from "./schema";

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

  it("accepts pasted separators without discarding unrelated characters", () => {
    expect(pasteCode(" 012-345\u0085")).toBe("012345");
    expect(pasteCode("012!345")).toBe("012!345");
  });
});

describe("backup-code regeneration", () => {
  it("keeps an authenticator code's leading zero", () => {
    expect(RegenerationSchema.parse({ code: "012345" })).toEqual({ code: "012345" });
  });

  it.each(["", "12345", "1234567", "abcdef", "abcde-fghjk"])(
    "rejects incomplete or non-authenticator proof %s",
    (code) => {
      expect(RegenerationSchema.safeParse({ code }).success).toBe(false);
    },
  );
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
