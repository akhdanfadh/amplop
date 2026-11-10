import { find } from "linkifyjs";
import { describe, expect, it } from "vite-plus/test";
import { linkOptions } from "./links";

// links returns the hrefs a plain-text body's links get, as linkify-react
// finds them with the same options.
function links(text: string): string[] {
  return find(text, linkOptions).map((link) => link.href);
}

describe("plain-text links", () => {
  it("links web and email addresses", () => {
    expect(links("See https://example.com/a?b=1. Or write to ana@example.com.")).toEqual([
      "https://example.com/a?b=1",
      "mailto:ana@example.com",
    ]);
    expect(links("mailto:ana@example.com http://example.com")).toEqual([
      "mailto:ana@example.com",
      "http://example.com",
    ]);
  });

  it("opens an address without a scheme over https", () => {
    expect(links("example.com/news")).toEqual(["https://example.com/news"]);
  });

  it("leaves a link with a direction control as text", () => {
    expect(links("https://example.com/\u202Egnp.exe")).toEqual([]);
    expect(links("https://example.com/a\u2067b")).toEqual([]);
  });

  it("leaves other schemes as text", () => {
    expect(links("file:///etc/passwd ftp://example.com/a ftps://example.com/b")).toEqual([]);
    expect(links("javascript:alert(1) data:text/html,x")).toEqual([]);
  });
});
