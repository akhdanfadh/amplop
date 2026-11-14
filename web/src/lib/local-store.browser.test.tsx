import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { localStore } from "./local-store";

afterEach(() => {
  vi.restoreAllMocks();
  localStorage.clear();
});

describe("localStore", () => {
  it("keeps a value", () => {
    localStore.setItem("k", "v");
    expect(localStore.getItem("k")).toBe("v");
    expect(localStore.getItem("missing")).toBeNull();
    localStore.removeItem("k");
    expect(localStore.getItem("k")).toBeNull();
  });

  it("finds nothing and drops writes when storage refuses", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new DOMException("Blocked", "SecurityError");
    });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new DOMException("Full", "QuotaExceededError");
    });
    vi.spyOn(Storage.prototype, "removeItem").mockImplementation(() => {
      throw new DOMException("Blocked", "SecurityError");
    });
    expect(localStore.getItem("k")).toBeNull();
    expect(() => localStore.setItem("k", "v")).not.toThrow();
    expect(() => localStore.removeItem("k")).not.toThrow();
  });
});
