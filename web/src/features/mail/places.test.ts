import { describe, expect, it } from "vite-plus/test";
import { isViewPlace } from "./places";

describe("isViewPlace", () => {
  it("knows the sidebar's six views", () => {
    for (const place of ["inbox", "starred", "sent", "all", "spam", "trash"]) {
      expect(isViewPlace(place)).toBe(true);
    }
  });

  it("refuses anything else", () => {
    expect(isViewPlace("outbox")).toBe(false);
    expect(isViewPlace("Inbox")).toBe(false);
  });
});
