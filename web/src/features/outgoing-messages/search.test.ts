import { describe, expect, it } from "vite-plus/test";
import { composeSearch } from "./search";

describe("compose search", () => {
  it("keeps a message ID to edit", () => {
    const edit = "0b9f3a52-5f1d-4a8e-9a43-5f0c2b6b1c11";
    expect(composeSearch.parse({ edit })).toEqual({ edit });
  });

  it("drops any other value, so the form starts empty", () => {
    for (const edit of ["../sessions", "", 1, ["0b9f3a52-5f1d-4a8e-9a43-5f0c2b6b1c11"]]) {
      expect(composeSearch.parse({ edit })).toEqual({ edit: undefined });
    }
  });

  it("reads an address without one", () => {
    expect(composeSearch.parse({})).toEqual({ edit: undefined });
  });
});
