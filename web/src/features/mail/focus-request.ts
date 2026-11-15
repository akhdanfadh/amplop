import { useSyncExternalStore } from "react";
import { onAccountCacheClear } from "@/features/auth/session-cache";
import type { Place } from "./places";

// FocusRequest is a row an action in the reading pane sent the member
// back to its list from: the list focuses that row itself when it stays,
// e.g. after Mark unread, else the row that took its place, by where this
// one sat in the list's order.
export type FocusRequest = { id: string; dateMs: number; stays: boolean };

// The open request, by its list's key.
const requests = new Map<string, FocusRequest>();
const listeners = new Set<() => void>();

function emit(): void {
  for (const listener of listeners) listener();
}

// One member's requests end with their session (session-cache.ts), so the
// next member's list never moves focus by them.
// The module lives as long as the page, so it never stops listening.
void onAccountCacheClear(() => {
  requests.clear();
  emit();
});

// listKey returns the key of place's list, of account alone when given.
export function listKey(place: Place, account: number | undefined): string {
  return `${place} ${account ?? ""}`;
}

// requestRowFocus asks key's list to focus row, or the row that takes its
// place.
export function requestRowFocus(key: string, row: FocusRequest): void {
  requests.set(key, row);
  emit();
}

// takeRowFocus ends key's request, once its list has carried it out.
export function takeRowFocus(key: string): void {
  if (requests.delete(key)) emit();
}

// useRowFocusRequest returns the open request for key's list, if any.
export function useRowFocusRequest(key: string): FocusRequest | undefined {
  return useSyncExternalStore(
    (changed) => {
      listeners.add(changed);
      return () => listeners.delete(changed);
    },
    () => requests.get(key),
  );
}

// nextRow returns the index in rows, newest first then by ID descending,
// of the row request asks to focus: its own while it stays and is listed;
// else the next older, else the one above; undefined when no other row is
// left. A leaving row may still be among rows, before its guess reaches
// the list.
export function nextRow(
  rows: readonly { id: string; dateMs: number }[],
  request: FocusRequest,
): number | undefined {
  if (request.stays) {
    const own = rows.findIndex((row) => row.id === request.id);
    if (own !== -1) return own;
  }
  const older = rows.findIndex(
    (row) => row.dateMs < request.dateMs || (row.dateMs === request.dateMs && row.id < request.id),
  );
  if (older !== -1) return older;
  let above = rows.length - 1;
  if (rows[above]?.id === request.id) above--;
  return above >= 0 ? above : undefined;
}
