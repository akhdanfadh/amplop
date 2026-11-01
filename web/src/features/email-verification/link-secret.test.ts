import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { captureLinkSecret, forgetLinkSecret, linkSecret } from "./link-secret";

// openPage stubs window at url, with a history that records replacements.
function openPage(url: string) {
  const { pathname, search, hash } = new URL(url, "https://inbok.example");
  const history = { state: { key: "entry" }, replaceState: vi.fn<History["replaceState"]>() };
  vi.stubGlobal("window", { location: { pathname, search, hash }, history });
  return history;
}

afterEach(() => {
  forgetLinkSecret();
  vi.unstubAllGlobals();
});

describe("captureLinkSecret", () => {
  it("keeps the secret and removes it from the address", () => {
    const history = openPage("/verify-email#abc_123");

    captureLinkSecret();

    expect(linkSecret()).toBe("abc_123");
    expect(history.replaceState).toHaveBeenCalledExactlyOnceWith(
      history.state,
      "",
      "/verify-email",
    );
  });

  it("keeps the query string", () => {
    const history = openPage("/verify-email?from=mail#abc_123");

    captureLinkSecret();

    expect(history.replaceState).toHaveBeenCalledWith(history.state, "", "/verify-email?from=mail");
  });

  it.each(["/verify-email", "/verify-email#", "/security#abc_123"])("leaves %s alone", (url) => {
    const history = openPage(url);

    captureLinkSecret();

    expect(linkSecret()).toBe("");
    expect(history.replaceState).not.toHaveBeenCalled();
  });

  // Tests run in node, which has no window, like the build's prerender.
  it("does nothing during the prerender", () => {
    captureLinkSecret();

    expect(linkSecret()).toBe("");
  });
});

describe("forgetLinkSecret", () => {
  it("drops the kept secret", () => {
    openPage("/verify-email#abc_123");
    captureLinkSecret();

    forgetLinkSecret();

    expect(linkSecret()).toBe("");
  });
});
