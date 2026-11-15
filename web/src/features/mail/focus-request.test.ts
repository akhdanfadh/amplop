import { describe, expect, it } from "vite-plus/test";
import { nextRow } from "./focus-request";

describe("nextRow", () => {
  const rows = [
    { id: "c", dateMs: 3 },
    { id: "b", dateMs: 2 },
    { id: "a", dateMs: 1 },
  ];

  it("is the next older row, whether or not the left row is still listed", () => {
    expect(nextRow(rows, { id: "b", dateMs: 2, stays: false })).toBe(2);
    expect(nextRow([rows[0], rows[2]], { id: "b", dateMs: 2, stays: false })).toBe(1);
  });

  it("is the row above when the left row was the oldest", () => {
    expect(nextRow(rows, { id: "a", dateMs: 1, stays: false })).toBe(1);
    expect(nextRow(rows.slice(0, 2), { id: "a", dateMs: 1, stays: false })).toBe(1);
  });

  it("is none when no other row is left", () => {
    expect(nextRow([], { id: "a", dateMs: 1, stays: false })).toBeUndefined();
    expect(nextRow([rows[2]], { id: "a", dateMs: 1, stays: false })).toBeUndefined();
  });

  it("is the row itself while it stays in the list, e.g. after Mark unread", () => {
    expect(nextRow(rows, { id: "b", dateMs: 2, stays: true })).toBe(1);
    // Gone after all, e.g. by a list's refresh: the one in its place.
    expect(nextRow([rows[0], rows[2]], { id: "b", dateMs: 2, stays: true })).toBe(1);
  });
});
