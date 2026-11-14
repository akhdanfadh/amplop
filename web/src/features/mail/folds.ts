import { useMemo } from "react";
import { createCollection, localStorageCollectionOptions } from "@tanstack/db";
import { useLiveQuery } from "@tanstack/react-db";
import { onAccountCacheClear } from "@/features/auth/session-cache";

// FoldKey names a sidebar item that folds: an account, by ID, or a parent
// label, by its place.
export type FoldKey = `account:${number}` | `label:${string}`;

// accountFold and labelFold return the fold key of an account and a label.
export function accountFold(id: number): FoldKey {
  return `account:${id}`;
}
export function labelFold(place: string): FoldKey {
  return `label:${place}`;
}

// browserStorage returns this browser's localStorage itself, or a store in
// memory when the browser refuses it (a private window, blocked site
// data): folds then last as long as the page.
//
// NOTE: the collection takes another tab's storage event only when its
// storageArea is this very object, so a wrapper around localStorage would
// silently end the tabs' sync.
function browserStorage(): Pick<Storage, "getItem" | "setItem" | "removeItem"> {
  try {
    return window.localStorage;
  } catch {
    const memory = new Map<string, string>();
    return {
      getItem: (key) => memory.get(key) ?? null,
      setItem: (key, value) => void memory.set(key, value),
      removeItem: (key) => void memory.delete(key),
    };
  }
}

// isRow reports whether row, kept under encoded, is one the collection
// wrote: a version and the row, under its encoded key.
//
// NOTE: TanStack DB encodes a text key as "s:" and the key, undocumented
// (local-storage.js, encodeStorageKey).
function isRow(encoded: string, row: unknown): boolean {
  if (typeof row !== "object" || row === null) return false;
  if (!("versionKey" in row) || typeof row.versionKey !== "string") return false;
  if (!("data" in row) || typeof row.data !== "object" || row.data === null) return false;
  return "key" in row.data && encoded === `s:${String(row.data.key)}`;
}

// storageParser reads and writes the kept rows as JSON. Reading drops
// anything else, e.g. text an older inbok left: the collection refuses a
// value it cannot read, which would fail the sidebar, so a bad one folds
// everything instead.
const storageParser = {
  stringify: (data: unknown): string => JSON.stringify(data),
  parse: (text: string): unknown => {
    let parsed: unknown;
    try {
      parsed = JSON.parse(text);
    } catch {
      return {};
    }
    if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) return {};
    return Object.fromEntries(Object.entries(parsed).filter(([key, row]) => isRow(key, row)));
  },
};

// The sidebar items the member unfolded, one row each; everything else is
// folded. They are kept in this browser, the same in each of its tabs. A
// key of an account or label the member no longer has is never asked
// for, so it stays harmlessly until the next sign-out.
const unfolded = createCollection(
  localStorageCollectionOptions({
    id: "sidebar-unfolded",
    storageKey: "inbok-sidebar-folds",
    storage: browserStorage(),
    parser: storageParser,
    getKey: (row: { key: FoldKey }) => row.key,
    // Read at once, so what is unfolded is known before the sidebar
    // first asks.
    startSync: true,
  }),
);

// useUnfolded returns the sidebar items the member unfolded.
export function useUnfolded(): ReadonlySet<string> {
  const { data } = useLiveQuery(unfolded);
  return useMemo(() => new Set(data.map((row) => row.key)), [data]);
}

// setUnfolded unfolds key when open is true, else folds it.
export function setUnfolded(key: FoldKey, open: boolean): void {
  if (unfolded.has(key) === open) return;
  if (open) unfolded.insert({ key });
  else unfolded.delete(key);
}

// unfold unfolds every key in keys, e.g. the path to the open place, and
// leaves the rest as they are.
export function unfold(keys: readonly FoldKey[]): void {
  const missing = [...new Set(keys)].filter((key) => !unfolded.has(key));
  if (missing.length > 0) unfolded.insert(missing.map((key) => ({ key })));
}

// One member's folds end with their session, so the next member on this
// browser starts with everything folded.
// The module lives as long as the page, so it never stops listening.
void onAccountCacheClear(() => {
  const keys = [...unfolded.keys()];
  if (keys.length > 0) unfolded.delete(keys);
});
