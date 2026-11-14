import { afterEach, describe, expect, it } from "vite-plus/test";
import { renderHook } from "vitest-browser-react";

const storeKey = "inbok-sidebar-folds";

let loads = 0;

// fresh returns the folds module as a new page load gets it, so it reads
// this browser's storage anew.
async function fresh(): Promise<typeof import("./folds")> {
  loads++;
  // NOTE: browser mode keeps a module through vi.resetModules; another
  // address is another module.
  return import(/* @vite-ignore */ `./folds.ts?load=${loads}`);
}

// snapshot returns storage's text holding keys, as a tab writes it.
function snapshot(keys: readonly string[]): string {
  return JSON.stringify(
    Object.fromEntries(keys.map((key) => [`s:${key}`, { versionKey: key, data: { key } }])),
  );
}

// stored returns the keys kept in this browser's storage.
function stored(): Set<string> {
  const text = localStorage.getItem(storeKey) ?? "";
  return new Set([...text.matchAll(/"data":\{"key":"([^"]+)"\}/g)].map((match) => match[1]));
}

afterEach(() => {
  localStorage.removeItem(storeKey);
});

describe("the sidebar's folds", () => {
  it("start from what this browser kept", async () => {
    localStorage.setItem(storeKey, snapshot(["account:1", "label:x"]));
    const folds = await fresh();

    const { result } = await renderHook(() => folds.useUnfolded());

    expect(result.current).toEqual(new Set(["account:1", "label:x"]));
  });

  it("are kept for the next load as the member unfolds and folds", async () => {
    const folds = await fresh();

    folds.setUnfolded(folds.accountFold(2), true);
    folds.unfold([folds.labelFold("y"), folds.accountFold(2)]);
    expect(stored()).toEqual(new Set(["account:2", "label:y"]));
    folds.setUnfolded(folds.accountFold(2), false);
    expect(stored()).toEqual(new Set(["label:y"]));
  });

  it("follow what another tab of this browser unfolds", async () => {
    const folds = await fresh();
    const { result } = await renderHook(() => folds.useUnfolded());

    // The other tab writes; this one hears the browser's storage event.
    const written = snapshot(["account:3"]);
    localStorage.setItem(storeKey, written);
    window.dispatchEvent(
      new StorageEvent("storage", { key: storeKey, newValue: written, storageArea: localStorage }),
    );

    await expect.poll(() => [...result.current]).toEqual(["account:3"]);
  });

  it("start folded when storage holds something else", async () => {
    localStorage.setItem(storeKey, "{not a list");
    const folds = await fresh();

    const { result } = await renderHook(() => folds.useUnfolded());

    expect(result.current.size).toBe(0);
    folds.setUnfolded(folds.accountFold(4), true);
    expect(stored()).toEqual(new Set(["account:4"]));
  });

  it("keep only the rows they wrote", async () => {
    localStorage.setItem(
      storeKey,
      JSON.stringify({ ...JSON.parse(snapshot(["account:5"])), "s:odd": { data: 7 } }),
    );
    const folds = await fresh();

    const { result } = await renderHook(() => folds.useUnfolded());

    expect(result.current).toEqual(new Set(["account:5"]));
  });
});
