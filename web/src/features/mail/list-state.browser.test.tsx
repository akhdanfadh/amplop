import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { renderHook } from "vitest-browser-react";
import { useRestIntent } from "./list-state";

afterEach(() => {
  vi.useRealTimers();
});

describe("useRestIntent", () => {
  it("reads only the row the pointer rests on, after 100 ms", async () => {
    vi.useFakeTimers();
    const read = vi.fn<(id: string) => void>();
    const { result } = await renderHook(() => useRestIntent(read));
    result.current.rest("a");
    vi.advanceTimersByTime(50);
    result.current.rest("b");
    vi.advanceTimersByTime(99);
    expect(read).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1);
    expect(read).toHaveBeenCalledExactlyOnceWith("b");
  });

  it("reads nothing once the pointer leaves first", async () => {
    vi.useFakeTimers();
    const read = vi.fn<(id: string) => void>();
    const { result } = await renderHook(() => useRestIntent(read));
    result.current.rest("a");
    result.current.leave();
    vi.advanceTimersByTime(200);
    expect(read).not.toHaveBeenCalled();
  });
});
