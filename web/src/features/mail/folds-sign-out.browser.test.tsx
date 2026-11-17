import { testQueryClient } from "@/test/query-client";
import { expect, it } from "vite-plus/test";
import { renderHook } from "vitest-browser-react";
import { clearAccountCache } from "@/features/auth/session-cache";
import { accountFold, setUnfolded, useUnfolded } from "./folds";

// This file loads the folds module once, as a page does: in a file with
// other loads, an earlier load's clearing would clear the rows too.

const tabKey = "inbok-sidebar-folds";
const lastKey = "inbok-sidebar-last-folds";

it("ends the folds with the member's session, in this tab and for new ones", async () => {
  const { result } = await renderHook(() => useUnfolded());
  setUnfolded(accountFold(7), true);
  await expect.poll(() => [...result.current]).toEqual(["account:7"]);

  clearAccountCache(testQueryClient());

  await expect.poll(() => result.current.size).toBe(0);
  expect(sessionStorage.getItem(tabKey)).toBe("{}");
  expect(localStorage.getItem(lastKey)).toBeNull();
});
