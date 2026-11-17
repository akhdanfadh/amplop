import { describe, expect, it, vi } from "vite-plus/test";
import { renderHook } from "vitest-browser-react";
import { fakeClock } from "@/test/clock";
import { useRestIntent } from "./list-state";

describe("useRestIntent", () => {
  it("reads only the row the pointer rests on, after 100 ms", async () => {
    const clock = fakeClock();
    const read = vi.fn<(id: string) => void>();
    const { result } = await renderHook(() => useRestIntent(read));
    result.current.rest("a");
    await clock.advance(50);
    result.current.rest("b");
    await clock.advance(99);
    expect(read).not.toHaveBeenCalled();
    await clock.advance(1);
    expect(read).toHaveBeenCalledExactlyOnceWith("b");
  });

  it("reads nothing once the pointer leaves first", async () => {
    const clock = fakeClock();
    const read = vi.fn<(id: string) => void>();
    const { result } = await renderHook(() => useRestIntent(read));
    result.current.rest("a");
    result.current.leave();
    await clock.advance(200);
    expect(read).not.toHaveBeenCalled();
  });
});
