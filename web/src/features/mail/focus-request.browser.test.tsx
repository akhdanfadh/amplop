import { QueryClient } from "@tanstack/react-query";
import { expect, it } from "vite-plus/test";
import { renderHook } from "vitest-browser-react";
import { clearAccountCache } from "@/features/auth/session-cache";
import { requestRowFocus, useRowFocusRequest } from "./focus-request";

it("ends an open request with the member's session", async () => {
  const { result } = await renderHook(() => useRowFocusRequest("inbox "));
  requestRowFocus("inbox ", { id: "b", dateMs: 2, stays: false });
  await expect.poll(() => result.current?.id).toBe("b");

  clearAccountCache(new QueryClient());

  await expect.poll(() => result.current).toBeUndefined();
});
