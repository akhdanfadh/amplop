import { describe, expect, it } from "vite-plus/test";
import { connectSearch } from "./search";

describe("connect search", () => {
  it("keeps an outcome the contract lists", () => {
    expect(connectSearch.parse({ outcome: "connected" })).toEqual({ outcome: "connected" });
    expect(connectSearch.parse({ outcome: "no-gmail" })).toEqual({ outcome: "no-gmail" });
  });

  it("drops any other value", () => {
    for (const outcome of ["Connected", "<b>connected</b>", "", 1, ["connected"]]) {
      expect(connectSearch.parse({ outcome })).toEqual({ outcome: undefined });
    }
  });

  it("reads an address without an outcome", () => {
    expect(connectSearch.parse({})).toEqual({ outcome: undefined });
  });
});
