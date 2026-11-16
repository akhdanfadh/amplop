import { describe, expect, it } from "vite-plus/test";
import type { PlaceCount } from "./conversations";
import { canMoveFrom, labelsOf, rowLabelsOf } from "./labels";
import { labelWords, moveWords } from "./words";

// label returns account's label place, named name under parent.
function label(accountId: number, place: string, name: string, parent: string | null = null) {
  return { accountId, place, name, parent, unread: 0 } satisfies PlaceCount;
}

describe("labelsOf", () => {
  it("lists each account's labels as the sidebar's tree, with their paths and depths", () => {
    const counts: PlaceCount[] = [
      { accountId: 1, place: "inbox", name: null, parent: null, unread: 0 },
      label(1, "w", "Work"),
      label(1, "w10", "Week 10", "w"),
      label(1, "w2", "Week 2", "w"),
      label(1, "a", "Archive"),
      label(2, "f", "Family"),
    ];

    expect(labelsOf(counts).map((l) => [l.accountId, l.path, l.depth])).toEqual([
      [1, "Archive", 0],
      [1, "Work", 0],
      [1, "Work/Week 2", 1],
      [1, "Work/Week 10", 1],
      [2, "Family", 0],
    ]);
  });
});

describe("canMoveFrom", () => {
  it("is true where a change can take a conversation out: the contract's views and labels", () => {
    expect(canMoveFrom("inbox")).toBe(true);
    expect(canMoveFrom("spam")).toBe(true);
    expect(canMoveFrom("trash")).toBe(true);
    expect(canMoveFrom("0b1c3f5e-6a7d-4e1f-9a2b-3c4d5e6f7a8b")).toBe(true);
  });

  it("is false for the views that follow from others", () => {
    expect(canMoveFrom("all")).toBe(false);
    expect(canMoveFrom("starred")).toBe(false);
    expect(canMoveFrom("sent")).toBe(false);
  });
});

describe("label words", () => {
  it("names a move by the label's path, where screen readers find it", () => {
    const words = moveWords("Clients/Acme");
    expect(words.done).toBe("Moved to Clients/Acme");
    expect(words.where).toBe("Clients/Acme");
    expect(words.failed(3)).toBe("Couldn't move 3 conversations to Clients/Acme");
  });

  it("names one label's change by its path, any other as labels changed, always with Undo", () => {
    expect(labelWords(["Hiring"], []).done).toBe("Added to Hiring");
    expect(labelWords([], ["Finance"]).done).toBe("Removed from Finance");
    expect(labelWords(["Hiring"], ["Finance"]).done).toBe("Labels changed");
    expect(labelWords(["Hiring", "Q4"], []).done).toBe("Labels changed");
    expect(labelWords(["Hiring"], []).alwaysUndo).toBe(true);
    expect(labelWords([], ["Finance"]).failed()).toBe("Couldn't change labels on the conversation");
  });
});

describe("rowLabelsOf", () => {
  it("names a label by its own name, and then its parent's path when another of the account's shares it", () => {
    const labels = labelsOf([
      label(1, "c", "Clients"),
      label(1, "ca", "Acme", "c"),
      label(1, "v", "Vendors"),
      label(1, "va", "Acme", "v"),
      label(1, "t", "Travel"),
      label(1, "tj", "2026 Jakarta", "t"),
      label(2, "a", "Acme"),
    ]);

    const rows = rowLabelsOf(labels);

    expect(rows.get("ca")?.name).toBe("Acme · Clients");
    expect(rows.get("va")?.name).toBe("Acme · Vendors");
    expect(rows.get("ca")?.path).toBe("Clients/Acme");
    expect(rows.get("tj")?.name).toBe("2026 Jakarta");
    // Another account's Acme is its own.
    expect(rows.get("a")?.name).toBe("Acme");
    expect(rows.get("c")!.order).toBeLessThan(rows.get("tj")!.order);
  });
});
