import { describe, expect, it } from "vite-plus/test";
import { addressName, formatReceived, formatSize, splitName, subjectText } from "./text";

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

describe("formatSize", () => {
  it("shows a size in the largest unit below it", () => {
    expect(formatSize(0)).toBe("0 bytes");
    expect(formatSize(1)).toBe("1 byte");
    expect(formatSize(1023)).toBe("1,023 bytes");
    expect(formatSize(1024)).toBe("1 KB");
    expect(formatSize(2_516_582)).toBe("2.4 MB");
    expect(formatSize(36_864)).toBe("36 KB");
    expect(formatSize(5 * 1024 ** 4)).toBe("5,120 GB");
  });

  it("moves up a unit when rounding reaches it", () => {
    expect(formatSize(1_048_575)).toBe("1 MB");
    expect(formatSize(1023 * 1024)).toBe("1,023 KB");
    expect(formatSize(10_239)).toBe("10 KB");
  });
});

describe("splitName", () => {
  it("keeps the extension apart, and what comes just before it", () => {
    expect(splitName("Q3-report-northwind-final-v12.xlsx")).toEqual({
      start: "Q3-report-northwind",
      beforeExtension: "-final-v12",
      extension: ".xlsx",
    });
  });

  it("keeps the end of a name with no extension", () => {
    expect(splitName("Minutes of the garden committee")).toEqual({
      start: "Minutes of the garden",
      beforeExtension: " committee",
      extension: "",
    });
  });

  it("leaves a short name with no start", () => {
    expect(splitName("invite.ics")).toEqual({
      start: "",
      beforeExtension: "invite",
      extension: ".ics",
    });
    expect(splitName("a")).toEqual({ start: "", beforeExtension: "a", extension: "" });
  });

  it("takes a leading dot or a long tail after a dot as part of the name", () => {
    expect(splitName(".bash_history_backup")).toEqual({
      start: ".bash_hist",
      beforeExtension: "ory_backup",
      extension: "",
    });
    expect(splitName("Notes. Then everything after it")).toEqual({
      start: "Notes. Then everythin",
      beforeExtension: "g after it",
      extension: "",
    });
  });

  it("never splits inside an emoji", () => {
    // Each family is one character on screen, made of seven code points.
    const family = "👩‍👩‍👧‍👦";

    expect(splitName(`party${family.repeat(12)}`)).toEqual({
      start: `party${family.repeat(2)}`,
      beforeExtension: family.repeat(10),
      extension: "",
    });
  });
});
