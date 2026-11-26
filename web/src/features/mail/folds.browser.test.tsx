import { testQueryClient } from "@/test/query-client";
import { describe, expect, it, vi } from "vite-plus/test";
import { renderHook } from "vitest-browser-react";
import { clearAccountCache } from "@/features/auth/session-cache";

// This tab's folds, in sessionStorage, and the member's last choice in any
// tab, in localStorage.
const tabKey = "amplop-sidebar-folds";
const lastKey = "amplop-sidebar-last-folds";

let loads = 0;

// fresh returns the folds module as a new page load gets it, so it reads
// this tab's and this browser's storage anew.
async function fresh(): Promise<typeof import("./folds")> {
  loads++;
  // NOTE: browser mode keeps a module through vi.resetModules; another
  // address is another module.
  return import(/* @vite-ignore */ `./folds.ts?load=${loads}`);
}

// TabStorage is what the folds module uses of sessionStorage.
type TabStorage = Pick<Storage, "getItem" | "setItem" | "removeItem">;

// tabStore returns a tab storage holding saved as this tab's folds, and
// refuse, which makes one of its methods throw from then on, as a browser
// that refuses storage does.
function tabStore(saved: readonly string[] = []): {
  storage: TabStorage;
  refuse: (method: keyof TabStorage) => void;
} {
  const store = new Map<string, string>();
  if (saved.length > 0) store.set(tabKey, snapshot(saved));
  const refused = new Set<keyof TabStorage>();
  const check = (method: keyof TabStorage) => {
    if (refused.has(method)) throw new DOMException("Refused", "SecurityError");
  };
  return {
    storage: {
      getItem: (key) => {
        check("getItem");
        return store.get(key) ?? null;
      },
      setItem: (key, value) => {
        check("setItem");
        store.set(key, value);
      },
      removeItem: (key) => {
        check("removeItem");
        store.delete(key);
      },
    },
    refuse: (method) => void refused.add(method),
  };
}

// freshWith returns the folds module as a new page load gets it, with
// storage as this tab's sessionStorage.
//
// NOTE: the page gets its real sessionStorage back before the test goes
// on: replacing Storage's methods for the whole page across a test's
// awaits froze the browser test run.
async function freshWith(storage: TabStorage): Promise<typeof import("./folds")> {
  vi.stubGlobal("sessionStorage", storage);
  try {
    return await fresh();
  } finally {
    vi.unstubAllGlobals();
  }
}

// snapshot returns storage's text holding keys, as a tab writes it.
function snapshot(keys: readonly string[]): string {
  return JSON.stringify(
    Object.fromEntries(keys.map((key) => [`s:${key}`, { versionKey: key, data: { key } }])),
  );
}

// stored returns the keys kept for this tab.
function stored(): Set<string> {
  const text = sessionStorage.getItem(tabKey) ?? "";
  return new Set([...text.matchAll(/"data":\{"key":"([^"]+)"\}/g)].map((match) => match[1]));
}

// last returns the keys kept for the next new tab, or null when none are.
function last(): Set<string> | null {
  const text = localStorage.getItem(lastKey);
  if (text === null) return null;
  const parsed: unknown = JSON.parse(text);
  return new Set(Array.isArray(parsed) ? parsed.map(String) : []);
}

describe("the sidebar's folds", () => {
  it("start from what this tab kept, whatever another tab chose", async () => {
    sessionStorage.setItem(tabKey, snapshot(["account:1", "label:x"]));
    localStorage.setItem(lastKey, JSON.stringify(["account:9"]));
    const folds = await fresh();

    const { result } = await renderHook(() => folds.useUnfolded());

    expect(result.current).toEqual(new Set(["account:1", "label:x"]));
  });

  it("start a new tab from the member's last choice in any tab", async () => {
    localStorage.setItem(lastKey, JSON.stringify(["account:1", "label:x"]));
    const folds = await fresh();

    const { result } = await renderHook(() => folds.useUnfolded());

    expect(result.current).toEqual(new Set(["account:1", "label:x"]));
    expect(stored()).toEqual(new Set(["account:1", "label:x"]));
  });

  it("are kept for this tab's next load as the member unfolds and folds", async () => {
    const folds = await fresh();

    folds.setUnfolded(folds.accountFold(2), true);
    folds.unfold([folds.labelFold("y"), folds.accountFold(2)]);
    expect(stored()).toEqual(new Set(["account:2", "label:y"]));
    folds.setUnfolded(folds.accountFold(2), false);
    expect(stored()).toEqual(new Set(["label:y"]));
  });

  it("keep the tab's folds for a new tab only as the member chooses", async () => {
    const folds = await fresh();

    folds.unfold([folds.accountFold(2), folds.labelFold("y")]);
    expect(last()).toBeNull();
    folds.setUnfolded(folds.accountFold(3), true);
    expect(last()).toEqual(new Set(["account:2", "label:y", "account:3"]));
  });

  it("stay as they are when another tab chooses", async () => {
    const folds = await fresh();
    folds.setUnfolded(folds.accountFold(1), true);
    const { result } = await renderHook(() => folds.useUnfolded());

    // The other tab writes; this one hears the browser's storage event.
    const written = JSON.stringify(["account:3"]);
    localStorage.setItem(lastKey, written);
    window.dispatchEvent(
      new StorageEvent("storage", { key: lastKey, newValue: written, storageArea: localStorage }),
    );

    expect(result.current).toEqual(new Set(["account:1"]));
  });

  it("start folded when storage holds something else", async () => {
    sessionStorage.setItem(tabKey, "{not a list");
    const folds = await fresh();

    const { result } = await renderHook(() => folds.useUnfolded());

    expect(result.current.size).toBe(0);
    folds.setUnfolded(folds.accountFold(4), true);
    expect(stored()).toEqual(new Set(["account:4"]));
  });

  it("keep only the rows they wrote", async () => {
    sessionStorage.setItem(
      tabKey,
      JSON.stringify({ ...JSON.parse(snapshot(["account:5"])), "s:odd": { data: 7 } }),
    );
    const folds = await fresh();

    const { result } = await renderHook(() => folds.useUnfolded());

    expect(result.current).toEqual(new Set(["account:5"]));
  });

  it("take only fold keys from the last choice", async () => {
    localStorage.setItem(lastKey, JSON.stringify(["account:6", 7, "account:x", "label:z"]));
    const folds = await fresh();

    const { result } = await renderHook(() => folds.useUnfolded());

    expect(result.current).toEqual(new Set(["account:6", "label:z"]));
  });

  it("keep a tab's empty folds over another tab's later choice", async () => {
    await fresh();
    expect(sessionStorage.getItem(tabKey)).not.toBeNull();

    // Another tab presses; then this one reloads.
    localStorage.setItem(lastKey, JSON.stringify(["account:9"]));
    const folds = await fresh();
    const { result } = await renderHook(() => folds.useUnfolded());

    expect(result.current.size).toBe(0);
  });

  it.each(["getItem", "setItem"] as const)(
    "keep a press for the page when storage refuses %s from the start",
    async (method) => {
      const tab = tabStore();
      tab.refuse(method);
      const folds = await freshWith(tab.storage);
      const { result } = await renderHook(() => folds.useUnfolded());
      expect(result.current.size).toBe(0);

      folds.setUnfolded(folds.accountFold(1), true);

      await expect.poll(() => [...result.current]).toEqual(["account:1"]);
    },
  );

  it.each(["getItem", "setItem"] as const)(
    "keep saved folds and later presses when storage starts refusing %s",
    async (method) => {
      const tab = tabStore(["account:3", "label:x"]);
      const folds = await freshWith(tab.storage);
      const { result } = await renderHook(() => folds.useUnfolded());
      expect(result.current).toEqual(new Set(["account:3", "label:x"]));

      tab.refuse(method);
      folds.setUnfolded(folds.accountFold(4), true);
      folds.setUnfolded(folds.accountFold(5), true);

      await expect
        .poll(() => result.current)
        .toEqual(new Set(["account:3", "label:x", "account:4", "account:5"]));
    },
  );

  it("clear the page's folds at sign-out after storage refused", async () => {
    const tab = tabStore(["account:3"]);
    const folds = await freshWith(tab.storage);
    const { result } = await renderHook(() => folds.useUnfolded());
    tab.refuse("setItem");
    folds.setUnfolded(folds.accountFold(4), true);
    await expect.poll(() => result.current.size).toBe(2);

    // Only this load's own clearing reaches its rows: once refused, they
    // live in its memory alone.
    clearAccountCache(testQueryClient());

    await expect.poll(() => result.current.size).toBe(0);
  });
});
