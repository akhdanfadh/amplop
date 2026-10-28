import { describe, expect, it } from "vite-plus/test";
import { newSessionPasswordMax } from "@/api/generated/model";
import { SignInSchema } from "./sign-in-schema";

describe("sign-in input", () => {
  it("preserves email casing and password whitespace", () => {
    expect(SignInSchema.parse({ email: "Admin@Example.COM", password: " password " })).toEqual({
      email: "Admin@Example.COM",
      password: " password ",
    });
  });

  it("uses the generated email validation without trimming", () => {
    expect(
      SignInSchema.safeParse({ email: " Admin@Example.COM ", password: "password" }).success,
    ).toBe(false);
  });

  it("allows short passwords without applying account-creation rules", () => {
    expect(SignInSchema.safeParse({ email: "admin@example.com", password: "short" }).success).toBe(
      true,
    );
  });

  it("counts Unicode code points for the password limit", () => {
    const email = "admin@example.com";
    expect(
      SignInSchema.safeParse({ email, password: "\u{1f512}".repeat(newSessionPasswordMax) })
        .success,
    ).toBe(true);
    const result = SignInSchema.safeParse({
      email,
      password: "\u{1f512}".repeat(newSessionPasswordMax + 1),
    });
    expect(result.success).toBe(false);
    expect(result.error?.issues).toMatchObject([
      { path: ["password"], message: `Use at most ${newSessionPasswordMax} characters.` },
    ]);
  });

  it("requires an email address and a password", () => {
    expect(SignInSchema.safeParse({ email: "", password: "password" }).success).toBe(false);
    const result = SignInSchema.safeParse({ email: "admin@example.com", password: "" });
    expect(result.success).toBe(false);
    expect(result.error?.issues).toMatchObject([
      { path: ["password"], message: "Enter your password." },
    ]);
  });
});
