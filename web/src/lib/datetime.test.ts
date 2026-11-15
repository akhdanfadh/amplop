import { describe, expect, it } from "vite-plus/test";
import { clockTime, dayAndTime, dayDate, hourCycle } from "./datetime";

const twelve = hourCycle === "h12" || hourCycle === "h11";
const afternoon = new Date(2026, 10, 14, 14, 32);

describe("inbok's dates and times", () => {
  it("writes a time in English on the browser's clock", () => {
    expect(clockTime.format(afternoon)).toBe(twelve ? "2:32 pm" : "14:32");
    expect(clockTime.format(new Date(2026, 10, 14, 9, 5))).toBe(twelve ? "9:05 am" : "9:05");
  });

  it("writes a day, and a day with its time, in English", () => {
    expect(dayDate.format(afternoon)).toBe("14 Nov 2026");
    expect(dayAndTime.format(afternoon)).toBe(
      twelve ? "14 Nov 2026, 2:32 pm" : "14 Nov 2026, 14:32",
    );
  });
});
