import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { captureLinkSecret, forgetLinkSecret, linkSecret } from "./link-secret";

const paths = ["/confirm", "/finish"];

// openPage stubs window at url, with a history that records replacements.
function openPage(url: string) {
  const { pathname, search, hash } = new URL(url, "https://inbok.example");
  const history = { state: { key: "entry" }, replaceState: vi.fn<History["replaceState"]>() };
  vi.stubGlobal("window", { location: { pathname, search, hash }, history });
  return history;
}

afterEach(() => {
  forgetLinkSecret();
});

describe("captureLinkSecret", () => {
  it.each(paths)("keeps the secret of %s and removes it from the address", (path) => {
    const history = openPage(`${path}#abc_123`);

    captureLinkSecret(paths);

    expect(linkSecret(path)).toBe("abc_123");
    expect(history.replaceState).toHaveBeenCalledExactlyOnceWith(history.state, "", path);
  });

  it("keeps the query string", () => {
    const history = openPage("/confirm?from=mail#abc_123");

    captureLinkSecret(paths);

    expect(history.replaceState).toHaveBeenCalledWith(history.state, "", "/confirm?from=mail");
  });

  it.each(["/confirm", "/confirm#", "/security#abc_123"])("leaves %s alone", (url) => {
    const history = openPage(url);

    captureLinkSecret(paths);

    expect(linkSecret("/confirm")).toBe("");
    expect(linkSecret("/security")).toBe("");
    expect(history.replaceState).not.toHaveBeenCalled();
  });

  // Tests run in node, which has no window, like the build's prerender.
  it("does nothing during the prerender", () => {
    captureLinkSecret(paths);

    expect(linkSecret("/confirm")).toBe("");
  });
});

describe("linkSecret", () => {
  // One kind of link's secret must never be sent as another kind's.
  it("keeps one page's secret from another", () => {
    openPage("/finish#abc_123");
    captureLinkSecret(paths);

    expect(linkSecret("/confirm")).toBe("");
  });
});

describe("forgetLinkSecret", () => {
  it("drops the kept secret", () => {
    openPage("/confirm#abc_123");
    captureLinkSecret(paths);

    forgetLinkSecret();

    expect(linkSecret("/confirm")).toBe("");
  });
});
