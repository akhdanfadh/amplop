import { useMemo } from "react";
import { createCollection, localStorageCollectionOptions } from "@tanstack/db";
import { useLiveQuery } from "@tanstack/react-db";
import { onAccountCacheClear } from "@/features/auth/session-cache";
import { localStore } from "@/lib/local-store";

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

// The sessionStorage key of this tab's folds, and the localStorage key of
// the folds the member last chose in any tab.
const tabKey = "amplop-sidebar-folds";
const lastKey = "amplop-sidebar-last-folds";

// tabStorage returns this tab's sessionStorage, kept in memory as well.
// Once the browser refuses it (a private window, blocked site data, a full
// quota), reads and writes go to memory alone, and folds last as long as
// the page.
//
// NOTE: the collection throws when a write fails and puts the rows back,
// so a member's press would undo itself.
function tabStorage(): Pick<Storage, "getItem" | "setItem" | "removeItem"> {
  const memory = new Map<string, string>();
  let session: Storage | undefined;
  try {
    session = window.sessionStorage;
  } catch {
    // Refused: memory alone.
  }
  // run calls step with the session's storage while the browser allows it.
  const run = <T>(step: (storage: Storage) => T): T | undefined => {
    if (!session) return undefined;
    try {
      return step(session);
    } catch {
      session = undefined;
      return undefined;
    }
  };
  return {
    getItem(key) {
      const value = run((storage) => storage.getItem(key));
      if (value !== undefined && value !== null) memory.set(key, value);
      return memory.get(key) ?? null;
    },
    setItem(key, value) {
      memory.set(key, value);
      run((storage) => storage.setItem(key, value));
    },
    removeItem(key) {
      memory.delete(key);
      run((storage) => storage.removeItem(key));
    },
  };
}

// isFoldKey reports whether value names an account or a label as FoldKey
// does.
function isFoldKey(value: unknown): value is FoldKey {
  return typeof value === "string" && /^(account:\d+|label:.+)$/.test(value);
}

// readLastFolds returns the folds the member last chose in any tab, or
// none when the browser kept none or something else.
function readLastFolds(): FoldKey[] {
  let parsed: unknown;
  try {
    parsed = JSON.parse(localStore.getItem(lastKey) ?? "[]");
  } catch {
    return [];
  }
  return Array.isArray(parsed) ? parsed.filter(isFoldKey) : [];
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
// anything else, e.g. text an older amplop left: the collection refuses a
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

const storage = tabStorage();

// 1. Whether this tab has no folds of its own, read before the collection
// writes: a tab reloaded or restored, or opened with a copy of another's
// sessionStorage, already holds its own.
const isNewTab = storage.getItem(tabKey) === null;

// The sidebar items unfolded in this tab, one row each, kept through its
// reloads; everything else is folded. A key of an account or label the
// member no longer has is never asked for, so it stays harmlessly until
// the next sign-out.
//
// DESIGN: the rows are this tab's alone, not the browser's, so two tabs
// can show two accounts' labels and a press in one never moves the
// other's sidebar.
const unfolded = createCollection(
  localStorageCollectionOptions({
    id: "sidebar-unfolded",
    storageKey: tabKey,
    storage,
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

// setUnfolded unfolds key when open is true, else folds it, as the member
// chose, and keeps this tab's folds for a tab without folds of its own.
export function setUnfolded(key: FoldKey, open: boolean): void {
  if (unfolded.has(key) === open) return;
  if (open) unfolded.insert({ key });
  else unfolded.delete(key);
  localStore.setItem(lastKey, JSON.stringify([...unfolded.keys()]));
}

// unfold unfolds every key in keys, e.g. the path to the open place, and
// leaves the rest as they are. It keeps nothing for other tabs, so moving
// between places never changes where a tab without folds starts.
export function unfold(keys: readonly FoldKey[]): void {
  const missing = [...new Set(keys)].filter((key) => !unfolded.has(key));
  if (missing.length > 0) unfolded.insert(missing.map((key) => ({ key })));
}

// 2. A tab without folds of its own starts from the folds the member last
// chose in any tab, and keeps them, even none, as its own: the collection
// writes only on a change, and a tab left without its key would take
// another tab's choice when it reloads.
if (isNewTab) {
  unfold(readLastFolds());
  if (storage.getItem(tabKey) === null) storage.setItem(tabKey, "{}");
}

// One member's folds end with their session, so the next member on this
// browser starts with everything folded.
// The module lives as long as the page, so it never stops listening.
void onAccountCacheClear(() => {
  const keys = [...unfolded.keys()];
  if (keys.length > 0) unfolded.delete(keys);
  localStore.removeItem(lastKey);
});
