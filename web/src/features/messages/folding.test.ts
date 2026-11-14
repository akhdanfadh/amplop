import { describe, expect, it } from "vite-plus/test";
import { foldRows, initiallyOpen, type FoldedRow } from "./folding";

// thread returns count read messages, m1 oldest, with those in unread
// unread.
function thread(count: number, unread: string[] = []) {
  return Array.from({ length: count }, (_, i) => ({
    id: `m${i + 1}`,
    unread: unread.includes(`m${i + 1}`),
  }));
}

// shape returns rows as message IDs, and "more:N" for a fold of N.
function shape(rows: FoldedRow<{ id: string }>[]): string[] {
  return rows.map((row) =>
    row.kind === "message" ? row.message.id : `more:${row.messages.length}`,
  );
}

describe("initiallyOpen", () => {
  it("opens the newest message and every unread one", () => {
    expect(initiallyOpen(thread(5, ["m2"]))).toEqual(new Set(["m2", "m5"]));
  });

  it("opens nothing in a conversation with no messages", () => {
    expect(initiallyOpen([])).toEqual(new Set());
  });
});

describe("foldRows", () => {
  it("counts the folded middle of a long thread as more messages", () => {
    const messages = thread(12);

    // The first, then 8 folded, then the newest three.
    expect(shape(foldRows(messages, initiallyOpen(messages), "grouped"))).toEqual([
      "m1",
      "more:8",
      "m10",
      "m11",
      "m12",
    ]);
  });

  it("shows a single folded message as itself", () => {
    const messages = thread(5);

    expect(shape(foldRows(messages, initiallyOpen(messages), "grouped"))).toEqual([
      "m1",
      "m2",
      "m3",
      "m4",
      "m5",
    ]);
  });

  it("keeps an open message in the middle, folding the runs on either side", () => {
    const messages = thread(12, ["m6"]);

    expect(shape(foldRows(messages, initiallyOpen(messages), "grouped"))).toEqual([
      "m1",
      "more:4",
      "m6",
      "more:3",
      "m10",
      "m11",
      "m12",
    ]);
  });

  it("shows every message once asked to", () => {
    const messages = thread(12);

    expect(foldRows(messages, initiallyOpen(messages), "all")).toHaveLength(12);
  });

  it("folds nothing in a short thread", () => {
    for (const count of [1, 2, 3, 4]) {
      const messages = thread(count);
      expect(shape(foldRows(messages, new Set(), "grouped"))).toEqual(messages.map(({ id }) => id));
    }
  });
});
