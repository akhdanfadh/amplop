import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { useRouter } from "@tanstack/react-router";
import type { Key } from "react-aria-components";

// Picking is the conversations picked in one list, by ID, and what
// changes them: pick replaces them, and an empty set stops picking;
// follow unpicks those no longer among rows, the list's, unless held;
// hold holds some, e.g. an action's, whose guess took them out of the
// list until it answers, and returns release, which lets them go: each
// one out of the list is unpicked, but those kept, e.g. the refused,
// which the list is drawing again.
export type Picking = {
  ids: ReadonlySet<Key>;
  pick: (ids: ReadonlySet<Key>) => void;
  follow: (rows: readonly { id: string }[]) => void;
  hold: (ids: readonly string[]) => (kept: Iterable<string>) => void;
};

const none: ReadonlySet<Key> = new Set();

// How many history entries picking has added, which numbers each.
let entries = 0;

// usePicking returns the picked conversations of the list key, none when
// the list changes. Starting to pick adds a history entry, so Back, the
// phone's or the browser's, stops picking before it leaves the page;
// stopping another way takes that entry back while it is the current one
// (DESIGN.md "Message list").
//
// DESIGN: an entry, not a URL: the address holds where the member is,
// never picked rows (DESIGN.md "Addresses"). An entry still under a
// conversation opened since, e.g. by a click while picking, stays when
// picking stops, and Back passes it on the way: accepted for the demo.
export function usePicking(key: string): Picking {
  const router = useRouter();
  const [picked, setPicked] = useState<{ key: string; ids: ReadonlySet<Key> }>({
    key,
    ids: none,
  });
  // A new list starts with none picked: what another list had must not
  // come back on returning to it.
  if (picked.key !== key) setPicked({ key, ids: none });
  const ids = picked.key === key ? picked.ids : none;
  // The picked IDs as last set, ahead of the next draw, so changes made
  // together build on each other.
  const latest = useRef(ids);
  useLayoutEffect(() => {
    latest.current = ids;
  }, [ids]);
  // The history entry picking added, by its key, while it may be current.
  const entry = useRef<string | undefined>(undefined);
  // The number of the entry this picking added, which the entries of the
  // conversations opened while picking carry too.
  const session = useRef<number | undefined>(undefined);
  // The number of the entry being added, until history reports its push.
  const pushing = useRef<number | undefined>(undefined);
  // How many holds each held ID has.
  const held = useRef(new Map<Key, number>());
  // The list's rows as follow last had them.
  const listed = useRef(new Set<Key>());

  // Back, or Forward, onto an entry this picking did not add, nor a
  // conversation opened while picking, stops picking.
  useEffect(
    () =>
      router.history.subscribe(({ action, location }) => {
        // The entry's key is taken as it is pushed: by the time the
        // navigation settles, the member may have moved on to another.
        if (action.type === "PUSH" && pushing.current !== undefined) {
          if (location.state.picking === pushing.current) {
            entry.current = keyOf(location.state);
            pushing.current = undefined;
          }
          return;
        }
        if (action.type === "PUSH" || action.type === "REPLACE") return;
        if (session.current !== undefined && location.state.picking === session.current) return;
        entry.current = undefined;
        session.current = undefined;
        latest.current = none;
        setPicked((was) => (was.ids.size === 0 ? was : { key: was.key, ids: none }));
      }),
    [router],
  );
  // A new list starts with none picked, and its entry is not this one's.
  useEffect(() => {
    entry.current = undefined;
    session.current = undefined;
  }, [key]);

  const pick = useCallback(
    (next: ReadonlySet<Key>) => {
      const was = latest.current.size > 0;
      latest.current = next;
      setPicked({ key, ids: next });
      if (!was && next.size > 0) {
        entries++;
        const number = entries;
        session.current = number;
        // After the pick has drawn: a navigation re-renders the whole app at
        // once, which kept the first tick from showing for about 300 ms on
        // a mid-range phone.
        requestAnimationFrame(() =>
          setTimeout(() => {
            // Stopped, or stopped and started again, before the entry was
            // added: this one is not needed.
            if (latest.current.size === 0 || session.current !== number) return;
            pushing.current = number;
            void router
              .navigate({
                to: ".",
                search: true,
                params: true,
                state: (s) => ({ ...s, picking: number }),
              })
              .then(() => {
                // Stopped while it was added: take it back.
                if (latest.current.size === 0) leave(router, entry);
              });
          }),
        );
      } else if (was && next.size === 0) {
        session.current = undefined;
        leave(router, entry);
      }
    },
    [key, router],
  );

  // keep keeps the picks test passes, as one change.
  const keep = useCallback(
    (test: (id: Key) => boolean) => {
      const kept = [...latest.current].filter(test);
      if (kept.length !== latest.current.size) pick(new Set(kept));
    },
    [pick],
  );
  const follow = useCallback(
    (rows: readonly { id: string }[]) => {
      listed.current = new Set(rows.map((row) => row.id));
      keep((id) => listed.current.has(id) || held.current.has(id));
    },
    [keep],
  );
  const hold = useCallback(
    (some: readonly string[]) => {
      for (const id of some) held.current.set(id, (held.current.get(id) ?? 0) + 1);
      return (kept: Iterable<string>) => {
        for (const id of some) {
          const n = (held.current.get(id) ?? 1) - 1;
          if (n > 0) held.current.set(id, n);
          else held.current.delete(id);
        }
        const back = new Set<Key>(kept);
        keep((id) => listed.current.has(id) || held.current.has(id) || back.has(id));
      };
    },
    [keep],
  );
  return useMemo(() => ({ ids, pick, follow, hold }), [ids, pick, follow, hold]);
}

// leave goes back from the history entry picking added, entry, while it is
// the current one.
function leave(router: ReturnType<typeof useRouter>, entry: { current: string | undefined }): void {
  if (entry.current === undefined || entryKey(router) !== entry.current) return;
  entry.current = undefined;
  router.history.back();
}

// entryKey returns the key TanStack's history gives its current entry.
function entryKey(router: ReturnType<typeof useRouter>): string | undefined {
  return keyOf(router.history.location.state);
}

// keyOf returns the key TanStack's history gives the entry of state.
function keyOf(state: { __TSR_key?: string }): string | undefined {
  // oxlint-disable-next-line no-underscore-dangle -- TanStack's own name for it.
  return state.__TSR_key;
}
