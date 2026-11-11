import { describe, expect, it } from "vite-plus/test";
import { pollWhile } from "./poll";

const every = 2000;
const busy = (data: { moving: boolean }) => data.moving;

describe("pollWhile", () => {
  it("reads again while the data still changes by itself", () => {
    expect(pollWhile(every, busy)({ state: { status: "success", data: { moving: true } } })).toBe(
      every,
    );
  });

  it("stops once nothing changes by itself", () => {
    expect(pollWhile(every, busy)({ state: { status: "success", data: { moving: false } } })).toBe(
      false,
    );
  });

  // The data kept from before the failure may still look busy; repeating
  // a failing read would only repeat its error, so Try again restarts it.
  it("stops after a failed read, even with busy data from before", () => {
    expect(pollWhile(every, busy)({ state: { status: "error", data: { moving: true } } })).toBe(
      false,
    );
  });

  it("waits for a first answer", () => {
    expect(pollWhile(every, busy)({ state: { status: "pending", data: undefined } })).toBe(false);
  });
});
