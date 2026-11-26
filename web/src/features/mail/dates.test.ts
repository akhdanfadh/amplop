import { describe, expect, it } from "vite-plus/test";
import { fullDate, headerDate, listDate } from "./dates";

// A Thursday afternoon, in the browser's time zone.
const now = new Date(2026, 10, 12, 15, 30);

describe("listDate", () => {
  it("shows today's time", () => {
    const morning = new Date(2026, 10, 12, 9, 5);
    expect(listDate(morning, now)).toBe(
      new Intl.DateTimeFormat("en-GB", {
        hour: "numeric",
        minute: "2-digit",
        hourCycle: new Intl.DateTimeFormat(undefined, { hour: "numeric" }).resolvedOptions()
          .hourCycle,
      }).format(morning),
    );
  });

  it("says Yesterday for the day before", () => {
    expect(listDate(new Date(2026, 10, 11, 23, 59), now)).toBe("Yesterday");
  });

  it("names the weekday within a week", () => {
    expect(listDate(new Date(2026, 10, 9, 8, 0), now)).toBe("Mon");
    expect(listDate(new Date(2026, 10, 6, 8, 0), now)).toBe("Fri");
  });

  it("shows the day and month this year", () => {
    expect(listDate(new Date(2026, 10, 5, 8, 0), now)).toBe("5 Nov");
    expect(listDate(new Date(2026, 0, 2, 8, 0), now)).toBe("2 Jan");
  });

  it("adds the year before this one", () => {
    expect(listDate(new Date(2025, 10, 2, 8, 0), now)).toBe("2 Nov 2025");
  });

  it("dates a later day, as from a sender whose clock runs ahead", () => {
    expect(listDate(new Date(2026, 10, 13, 1, 0), now)).toBe("13 Nov");
    expect(listDate(new Date(2027, 0, 2, 1, 0), now)).toBe("2 Jan 2027");
  });
});

describe("fullDate", () => {
  it("writes the time on the browser's clock, as a row does", () => {
    const date = new Date(2026, 10, 12, 15, 9);
    const time = new Intl.DateTimeFormat(undefined, { hour: "numeric", minute: "2-digit" });
    const hourCycle = time.resolvedOptions().hourCycle;
    expect(fullDate(date)).toContain(hourCycle === "h23" || hourCycle === "h24" ? "15:09" : "3:09");
    expect(fullDate(date)).toContain("12 November 2026");
  });
});

describe("headerDate", () => {
  it("shows the day in amplop's words and the time on the browser's clock", () => {
    const date = new Date(2026, 10, 12, 15, 9);
    const hourCycle = new Intl.DateTimeFormat(undefined, { hour: "numeric" }).resolvedOptions()
      .hourCycle;

    expect(headerDate(date)).toContain("12 Nov 2026");
    expect(headerDate(date)).toContain(
      hourCycle === "h23" || hourCycle === "h24" ? "15:09" : "3:09",
    );
  });
});
