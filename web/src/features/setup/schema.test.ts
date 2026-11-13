import { describe, expect, it } from "vite-plus/test";
import { validationMessage } from "@/api/validation";
import { SetupSchema } from "./schema";

const valid = {
  token: "test-only-browser-setup-token-1234567890",
  email: "Admin@example.com",
  password: "a long setup password",
  confirmPassword: "a long setup password",
};

describe("SetupSchema", () => {
  it.each([
    "p".repeat(15),
    "p".repeat(128),
    "\u{1f512}".repeat(15),
    "\u{1f512}".repeat(128),
    " ".repeat(15),
  ])("accepts password bounds without changing the value", (password) => {
    expect(SetupSchema.parse({ ...valid, password, confirmPassword: password }).password).toBe(
      password,
    );
  });

  it.each(["p".repeat(14), "p".repeat(129), "\u{1f512}".repeat(14), "\u{1f512}".repeat(129)])(
    "rejects passwords outside the code-point bounds",
    (password) => {
      const result = SetupSchema.safeParse(
        {
          ...valid,
          password,
          confirmPassword: password,
        },
        { error: validationMessage },
      );

      expect(result.success).toBe(false);
      expect(result.error?.issues).toMatchObject([
        { path: ["password"], message: "Must be 15 to 128 characters." },
      ]);
    },
  );

  it("preserves email case while validating its syntax", () => {
    const email = "Admin@EXAMPLE.COM";

    expect(SetupSchema.parse({ ...valid, email }).email).toBe(email);
  });

  it("rejects text that is not an email address", () => {
    const result = SetupSchema.safeParse({ ...valid, email: "hello" });

    expect(result.error?.issues).toMatchObject([{ path: ["email"], format: "regex" }]);
  });

  it.each(["admin@localhost", "admin@example.c", "user!name@example.com", "admin@münchen.de"])(
    "accepts an address the server accepts: %s",
    (email) => {
      expect(SetupSchema.safeParse({ ...valid, email }).success).toBe(true);
    },
  );

  it("keeps the API email length cap", () => {
    const email = `${"a".repeat(64)}@${"b".repeat(61)}.${"c".repeat(61)}.${"d".repeat(61)}.com`;

    expect(SetupSchema.parse({ ...valid, email }).email).toBe(email);
    expect(SetupSchema.safeParse({ ...valid, email: `a${email}` }).success).toBe(false);
  });

  it.each(["", "ab", "a".repeat(255)])("rejects email outside the API length bounds", (email) => {
    expect(SetupSchema.safeParse({ ...valid, email }).success).toBe(false);
  });

  it("requires a token without changing its contents", () => {
    expect(SetupSchema.safeParse({ ...valid, token: "" }).success).toBe(false);
    expect(SetupSchema.parse({ ...valid, token: " spaced token " }).token).toBe(" spaced token ");
  });

  it.each(["", "a different password", "a long setup password ", "A long setup password"])(
    "rejects missing or mismatched confirmation on its own field",
    (confirmPassword) => {
      const result = SetupSchema.safeParse({ ...valid, confirmPassword });

      expect(result.success).toBe(false);
      expect(result.error?.issues.every((issue) => issue.path[0] === "confirmPassword")).toBe(true);
    },
  );
});
