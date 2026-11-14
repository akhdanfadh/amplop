import { describe, expect, it } from "vite-plus/test";
import type { PlaceCount } from "./conversations";
import { labelPath } from "./place-counts";

// label returns account's label place, named name under parent.
function label(accountId: number, place: string, name: string, parent: string | null = null) {
  return { accountId, place, name, parent, unread: 0 } satisfies PlaceCount;
}

describe("labelPath", () => {
  const counts = [label(1, "w", "Work"), label(1, "p", "Projects", "w")];

  it("names a label with its parents, the top one first", () => {
    expect(labelPath(counts, "p")).toEqual(["Work", "Projects"]);
    expect(labelPath(counts, "missing")).toEqual([]);
  });

  it("ends at a loop, which only a server fault could make", () => {
    const looped = [label(1, "a", "A", "b"), label(1, "b", "B", "a")];
    expect(labelPath(looped, "a")).toEqual(["B", "A"]);
  });
});
