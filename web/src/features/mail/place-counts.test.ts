import { describe, expect, it } from "vite-plus/test";
import type { PlaceCount } from "./conversations";
import { labelPath, unreadIn } from "./place-counts";

// label returns account's label place, named name under parent.
function label(accountId: number, place: string, name: string, parent: string | null = null) {
  return { accountId, place, name, parent, unread: 0 } satisfies PlaceCount;
}

describe("unreadIn", () => {
  const counts: PlaceCount[] = [
    { accountId: 1, place: "inbox", name: null, parent: null, unread: 3 },
    { accountId: 2, place: "inbox", name: null, parent: null, unread: 2 },
    { accountId: 1, place: "spam", name: null, parent: null, unread: 9 },
  ];

  it("adds every account's count, or takes one account's", () => {
    expect(unreadIn(counts, "inbox", undefined)).toBe(5);
    expect(unreadIn(counts, "inbox", 2)).toBe(2);
    expect(unreadIn(counts, "starred", undefined)).toBe(0);
  });
});

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
