import { parseDate } from "@internationalized/date";
import { describe, expect, it } from "vite-plus/test";
import { searchFilters, searchParams, searches, words } from "./search";
import { resultsCount } from "./text";

const today = parseDate("2026-11-16");

// midnight returns local midnight of day as the API takes it.
function midnight(day: string): string {
  const [year, month, date] = day.split("-").map(Number);
  return new Date(year, month - 1, date).toISOString();
}

describe("searchParams", () => {
  it("sends the words only from two characters, trimmed", () => {
    expect(words(" a ")).toBeUndefined();
    expect(words(" ab ")).toBe("ab");
    expect(searchParams({ q: "x" }, undefined, today).q).toBeUndefined();
  });

  it("counts a date choice from today's local midnight", () => {
    expect(searchParams({ date: "7d" }, undefined, today)).toMatchObject({
      since: midnight("2026-11-09"),
      before: undefined,
    });
    expect(searchParams({ date: "older" }, undefined, today)).toMatchObject({
      since: undefined,
      before: midnight("2025-11-16"),
    });
  });

  it("sends a range's days as their local midnights, Before excluded", () => {
    expect(searchParams({ since: "2026-11-02", before: "2026-11-10" }, 3, today)).toMatchObject({
      account: 3,
      since: midnight("2026-11-02"),
      before: midnight("2026-11-10"),
    });
  });

  it("leaves places out for All mail", () => {
    expect(searchParams({ place: [] }, undefined, today).place).toBeUndefined();
    expect(searchParams({ place: ["inbox", "spam"] }, undefined, today).place).toEqual([
      "inbox",
      "spam",
    ]);
  });
});

describe("searchFilters", () => {
  it("drops what an edited link spoils, keeping the rest", () => {
    expect(
      searchFilters.parse({
        q: "invoice",
        place: ["inbox", "nowhere"],
        date: "forever",
        since: "2026-13-40",
        has: "pictures",
      }),
    ).toEqual({ q: "invoice", place: ["inbox"] });
  });
});

describe("searches", () => {
  it("asks for words or a filter, not an account alone", () => {
    expect(searches({})).toBe(false);
    expect(searches({ q: "ab" })).toBe(true);
    expect(searches({ has: "attachment" })).toBe(true);
  });
});

describe("search wording", () => {
  it("counts up to the API's most", () => {
    expect(resultsCount(1, 1000)).toBe("1 conversation");
    expect(resultsCount(1000, 1000)).toBe("1,000 or more conversations");
  });
});
