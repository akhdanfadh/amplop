import { describe, expect, it } from "vite-plus/test";
import type { PlaceCount } from "./conversations";
import { labelPath, labelTree, unreadIn } from "./place-counts";

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

describe("labelTree", () => {
  it("nests each label under its parent, each level in name order", () => {
    const counts = [
      label(1, "w", "Work"),
      label(1, "z", "zeta", "w"),
      label(1, "p", "Projects", "w"),
      label(1, "q4", "2026 Q4", "p"),
      label(1, "a", "Archive"),
      label(2, "other", "Other"),
    ];

    const tree = labelTree(counts, 1);

    expect(tree.map((node) => node.name)).toEqual(["Archive", "Work"]);
    expect(tree[1].children.map((node) => node.name)).toEqual(["Projects", "zeta"]);
    expect(tree[1].children[0].children.map((node) => node.place)).toEqual(["q4"]);
  });

  it("puts a label whose parent is missing at the top", () => {
    expect(labelTree([label(1, "b", "Bills", "gone")], 1).map((node) => node.name)).toEqual([
      "Bills",
    ]);
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
