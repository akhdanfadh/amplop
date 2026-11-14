import { describe, expect, it } from "vite-plus/test";
import { syncingDetail } from "./text";

describe("syncingDetail", () => {
  it("says how many messages a sync has copied", () => {
    expect(syncingDetail(1)).toBe("Copying the last 90 days: 1 message so far.");
    expect(syncingDetail(1200)).toBe("Copying the last 90 days: 1,200 messages so far.");
  });
});
