import { describe, expect, it } from "vite-plus/test";
import { ConfirmationSchema, pasteCode } from "./schema";

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
