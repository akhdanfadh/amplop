import { describe, expect, it } from "vite-plus/test";
import { signInSearch } from "./search";

describe("signInSearch", () => {
  it("keeps a path on this site", () => {
    expect(signInSearch.parse({ redirect: "/mail/inbox?account=a#top" })).toEqual({
      redirect: "/mail/inbox?account=a#top",
    });
  });

  it.each([
    ["another host", "//evil.example/mail"],
    ["a backslash host", "/\\evil.example"],
    ["an absolute address", "https://evil.example/"],
    ["a script address", "javascript:alert(1)"],
    ["a relative path", "mail/inbox"],
    ["a number", 5],
  ])("drops %s", (_name, redirect) => {
    expect(signInSearch.parse({ redirect })).toEqual({ redirect: undefined });
  });

  it("allows no redirect", () => {
    expect(signInSearch.parse({})).toEqual({ redirect: undefined });
  });
});
