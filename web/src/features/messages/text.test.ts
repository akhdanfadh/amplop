import { describe, expect, it } from "vite-plus/test";
import { addressName, formatReceived, subjectText } from "./text";

describe("message header", () => {
  it("names an address by its display name when it has one", () => {
    expect(addressName({ name: " Ana Lima ", email: "ana@example.com" })).toBe("Ana Lima");
    expect(addressName({ email: "ana@example.com" })).toBeUndefined();
    expect(addressName({ name: " ", email: "ana@example.com" })).toBeUndefined();
    // A name that only repeats the address says nothing more.
    expect(addressName({ name: "ana@example.com", email: "ana@example.com" })).toBeUndefined();
  });

  it("says when a message has no subject", () => {
    expect(subjectText("Lunch?")).toBe("Lunch?");
    expect(subjectText("  ")).toBe("(no subject)");
  });

  it("shows the time short, and in full for its tooltip", () => {
    const received = formatReceived("2026-11-09T07:05:00Z");

    // The exact wording follows the reader's locale and time zone.
    expect(received.short).toMatch(/\S/);
    expect(received.full.length).toBeGreaterThan(received.short.length);
  });

  it("shows a value that is not a time as given", () => {
    expect(formatReceived("Mon, 32 Foo")).toEqual({ short: "Mon, 32 Foo", full: "Mon, 32 Foo" });
  });
});
