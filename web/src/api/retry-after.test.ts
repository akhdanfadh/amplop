import { afterEach, beforeEach, describe, expect, it, vi } from "vite-plus/test";
import { retryAfterDelay, retryAfterTime } from "./retry-after";

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(new Date("2026-10-23T00:00:00Z"));
});

afterEach(() => {
  vi.useRealTimers();
});

describe("retryAfterDelay", () => {
  it.each([
    { header: "0", delay: 0 },
    { header: "3", delay: 3_000 },
    { header: " 3 ", delay: 3_000 },
  ])("converts integer seconds $header to milliseconds", ({ header, delay }) => {
    expect(retryAfterDelay(header)).toBe(delay);
  });

  it.each([undefined, "", " ", "-1", "1.5", "0x10", "not a date"])(
    "returns no delay for an absent or unusable header %s",
    (header) => {
      expect(retryAfterDelay(header)).toBeUndefined();
    },
  );

  it.each([
    "Fri, 23 Oct 2026 00:00:03 GMT",
    "Friday, 23-Oct-26 00:00:03 GMT",
    "Fri Oct 23 00:00:03 2026",
  ])("reads HTTP date %s in UTC", (header) => {
    expect(retryAfterDelay(header)).toBe(3_000);
  });

  it("returns zero for a valid date that has already passed", () => {
    expect(retryAfterDelay("Thu, 22 Oct 2026 23:59:59 GMT")).toBe(0);
  });

  it("rejects a date whose day cannot be parsed", () => {
    expect(retryAfterDelay("Fri, 99 Oct 2026 00:00:03 GMT")).toBeUndefined();
  });

  it("treats a legacy year exactly fifty years ahead as a future date", () => {
    expect(retryAfterDelay("Thursday, 23-Oct-76 00:00:00 GMT")).toBe(1_577_923_200_000);
  });

  it("moves a legacy date more than fifty years ahead into the previous century", () => {
    expect(retryAfterDelay("Thursday, 23-Oct-76 00:00:01 GMT")).toBe(0);
  });
});

describe("retryAfterTime", () => {
  const receivedAt = Date.UTC(2026, 9, 22, 12, 0, 0);

  it("adds integer seconds to when the response arrived", () => {
    expect(retryAfterTime("90", receivedAt)).toBe(receivedAt + 90_000);
  });

  it("returns an HTTP date as is, even if it has passed", () => {
    expect(retryAfterTime("Thu, 22 Oct 2026 11:00:00 GMT", receivedAt)).toBe(
      Date.UTC(2026, 9, 22, 11, 0, 0),
    );
  });

  it.each([undefined, "", "-1", "soon"])("returns nothing for an unusable header %s", (header) => {
    expect(retryAfterTime(header, receivedAt)).toBeUndefined();
  });
});
